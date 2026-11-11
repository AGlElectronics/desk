package desk

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/desk/backend/internal/blob"
	"github.com/AGlElectronics/desk/backend/internal/domain"
	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

// undoWindow is how long a reply waits in the queue before it is sent, so
// "Undo" can still take it back.
const undoWindow = 10 * time.Second

// Customer is the person on the other side of a conversation.
type Customer struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
	Name  string    `json:"name"`
	Notes string    `json:"notes"`
}

// Conversation is one ticket.
type Conversation struct {
	ID             uuid.UUID  `json:"id"`
	MailboxID      uuid.UUID  `json:"mailbox_id"`
	MailboxName    string     `json:"mailbox_name"`
	Number         int        `json:"number"`
	Subject        string     `json:"subject"`
	Status         string     `json:"status"`
	Preview        string     `json:"preview"`
	Customer       Customer   `json:"customer"`
	AssigneeID     *uuid.UUID `json:"assignee_id,omitempty"`
	AssigneeName   string     `json:"assignee_name,omitempty"`
	ThreadCount    int        `json:"thread_count"`
	LastActivityAt time.Time  `json:"last_activity_at"`
	CreatedAt      time.Time  `json:"created_at"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
}

// AttachmentMeta describes one attachment (content is downloaded separately).
type AttachmentMeta struct {
	ID          uuid.UUID `json:"id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int       `json:"size_bytes"`
	// ContentID and Inline: an inline image the HTML body refers to.
	ContentID string `json:"content_id,omitempty"`
	Inline    bool   `json:"inline"`
}

// Thread is one message, note or system line in a conversation.
type Thread struct {
	ID            uuid.UUID        `json:"id"`
	Kind          string           `json:"kind"`
	AuthorID      *uuid.UUID       `json:"author_id,omitempty"`
	AuthorName    string           `json:"author_name,omitempty"`
	FromEmail     string           `json:"from_email"`
	To            []string         `json:"to"`
	CC            []string         `json:"cc"`
	BodyHTML      string           `json:"body_html"`
	BodyText      string           `json:"body_text"`
	SendStatus    *string          `json:"send_status,omitempty"`
	SendError     string           `json:"send_error,omitempty"`
	NextAttemptAt *time.Time       `json:"next_attempt_at,omitempty"`
	Attachments   []AttachmentMeta `json:"attachments"`
	CreatedAt     time.Time        `json:"created_at"`
}

// Viewer is someone else looking at (or answering) the same conversation.
type Viewer struct {
	UserID      uuid.UUID `json:"user_id"`
	DisplayName string    `json:"display_name"`
	Mode        string    `json:"mode"`
}

// ConversationDetail is the full conversation as the UI shows it.
type ConversationDetail struct {
	Conversation
	Threads []Thread `json:"threads"`
	// History is the customer's other conversations, newest first.
	History []Conversation `json:"history"`
	Viewers []Viewer       `json:"viewers"`
}

const convSelect = `
	SELECT c.id, c.mailbox_id, m.name, c.number, c.subject, c.status, c.preview,
	       cu.id, cu.email::text, cu.name, cu.notes,
	       c.assignee_id, COALESCE(u.display_name, ''),
	       (SELECT count(*) FROM threads t WHERE t.conversation_id = c.id AND t.kind <> 'system'),
	       c.last_activity_at, c.created_at, c.closed_at
	  FROM conversations c
	  JOIN mailboxes m ON m.id = c.mailbox_id
	  JOIN customers cu ON cu.id = c.customer_id
	  LEFT JOIN users u ON u.id = c.assignee_id`

func scanConversation(row pgx.Row) (Conversation, error) {
	var c Conversation
	err := row.Scan(&c.ID, &c.MailboxID, &c.MailboxName, &c.Number, &c.Subject, &c.Status, &c.Preview,
		&c.Customer.ID, &c.Customer.Email, &c.Customer.Name, &c.Customer.Notes,
		&c.AssigneeID, &c.AssigneeName, &c.ThreadCount,
		&c.LastActivityAt, &c.CreatedAt, &c.ClosedAt)
	return c, err
}

func (s *Service) queryConversations(ctx context.Context, where string, args ...any) ([]Conversation, error) {
	rows, err := s.pool.Query(ctx, convSelect+" "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Conversation{}
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListFilter selects conversations for the list view.
type ListFilter struct {
	MailboxID *uuid.UUID
	// Folder: unassigned | mine | assigned | pending | closed | spam | all.
	Folder string
	Query  string
	UserID uuid.UUID
}

// ListConversations returns up to 200 conversations, newest activity first.
func (s *Service) ListConversations(ctx context.Context, f ListFilter) ([]Conversation, error) {
	conds := []string{"TRUE"}
	args := []any{}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.MailboxID != nil {
		conds = append(conds, "c.mailbox_id = "+arg(*f.MailboxID))
	}
	switch f.Folder {
	case "", "unassigned":
		conds = append(conds, "c.status = 'active' AND c.assignee_id IS NULL")
	case "mine":
		conds = append(conds, "c.status IN ('active','pending') AND c.assignee_id = "+arg(f.UserID))
	case "assigned":
		conds = append(conds, "c.status IN ('active','pending') AND c.assignee_id IS NOT NULL")
	case "pending":
		conds = append(conds, "c.status = 'pending'")
	case "closed":
		conds = append(conds, "c.status = 'closed'")
	case "spam":
		conds = append(conds, "c.status = 'spam'")
	case "all":
		conds = append(conds, "c.status <> 'spam'")
	default:
		return nil, httperror.BadRequest("unknown folder: " + f.Folder)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := arg("%" + q + "%")
		num := strings.TrimPrefix(q, "#")
		numCond := "FALSE"
		var n int
		if _, err := fmt.Sscanf(num, "%d", &n); err == nil && fmt.Sprint(n) == num {
			numCond = "c.number = " + arg(n)
		}
		conds = append(conds, fmt.Sprintf(`(c.subject ILIKE %[1]s OR cu.email ILIKE %[1]s OR cu.name ILIKE %[1]s OR %[2]s
			OR EXISTS (SELECT 1 FROM threads t WHERE t.conversation_id = c.id AND t.body_text ILIKE %[1]s))`, like, numCond))
	}
	return s.queryConversations(ctx, "WHERE "+strings.Join(conds, " AND ")+" ORDER BY c.last_activity_at DESC LIMIT 200", args...)
}

// GetConversation loads one conversation or a typed 404.
func (s *Service) GetConversation(ctx context.Context, id uuid.UUID) (Conversation, error) {
	c, err := scanConversation(s.pool.QueryRow(ctx, convSelect+" WHERE c.id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Conversation{}, httperror.NotFound("conversation")
	}
	return c, err
}

// Detail loads threads, attachments, customer history and other viewers.
func (s *Service) Detail(ctx context.Context, c Conversation, viewerID uuid.UUID) (ConversationDetail, error) {
	d := ConversationDetail{Conversation: c, Threads: []Thread{}, Viewers: []Viewer{}}
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.kind, t.author_id, COALESCE(u.display_name, ''), t.from_email, t.to_emails, t.cc_emails,
		       t.body_html, t.body_text, t.send_status, t.send_error, t.next_attempt_at, t.created_at
		  FROM threads t LEFT JOIN users u ON u.id = t.author_id
		 WHERE t.conversation_id = $1
		 ORDER BY t.created_at, t.id`, c.ID)
	if err != nil {
		return d, err
	}
	index := map[uuid.UUID]int{}
	for rows.Next() {
		var t Thread
		if err := rows.Scan(&t.ID, &t.Kind, &t.AuthorID, &t.AuthorName, &t.FromEmail, &t.To, &t.CC,
			&t.BodyHTML, &t.BodyText, &t.SendStatus, &t.SendError, &t.NextAttemptAt, &t.CreatedAt); err != nil {
			rows.Close()
			return d, err
		}
		t.Attachments = []AttachmentMeta{}
		index[t.ID] = len(d.Threads)
		d.Threads = append(d.Threads, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, err
	}

	rows, err = s.pool.Query(ctx, `
		SELECT a.thread_id, a.id, a.filename, a.content_type, a.size_bytes, a.content_id, a.inline
		  FROM attachments a JOIN threads t ON t.id = a.thread_id
		 WHERE t.conversation_id = $1 ORDER BY a.created_at`, c.ID)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var threadID uuid.UUID
		var a AttachmentMeta
		if err := rows.Scan(&threadID, &a.ID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.ContentID, &a.Inline); err != nil {
			rows.Close()
			return d, err
		}
		if i, ok := index[threadID]; ok {
			d.Threads[i].Attachments = append(d.Threads[i].Attachments, a)
		}
	}
	rows.Close()

	d.History, err = s.queryConversations(ctx,
		"WHERE c.customer_id = $1 AND c.id <> $2 ORDER BY c.last_activity_at DESC LIMIT 20", c.Customer.ID, c.ID)
	if err != nil {
		return d, err
	}

	rows, err = s.pool.Query(ctx, `
		SELECT v.user_id, u.display_name, v.mode FROM viewers v JOIN users u ON u.id = v.user_id
		 WHERE v.conversation_id = $1 AND v.user_id <> $2 AND v.seen_at > now() - interval '30 seconds'
		 ORDER BY u.display_name`, c.ID, viewerID)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var v Viewer
		if err := rows.Scan(&v.UserID, &v.DisplayName, &v.Mode); err != nil {
			return d, err
		}
		d.Viewers = append(d.Viewers, v)
	}
	return d, rows.Err()
}

// Heartbeat records that userID is viewing or replying to a conversation.
func (s *Service) Heartbeat(ctx context.Context, convID, userID uuid.UUID, mode string) error {
	if mode != "viewing" && mode != "replying" {
		return httperror.BadRequest("mode must be viewing or replying")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO viewers (conversation_id, user_id, mode, seen_at) VALUES ($1, $2, $3, now())
		ON CONFLICT (conversation_id, user_id) DO UPDATE SET mode = EXCLUDED.mode, seen_at = now()
	`, convID, userID, mode)
	return err
}

// ----------------------------------------------------------------------------
// Changes
// ----------------------------------------------------------------------------

// UpdateInput changes status and/or assignee. ClearAssignee unassigns.
type UpdateInput struct {
	Status        *string
	AssigneeID    *uuid.UUID
	ClearAssignee bool
}

// UpdateConversation applies status / assignee changes and records system
// lines for them.
func (s *Service) UpdateConversation(ctx context.Context, c Conversation, actorID uuid.UUID, in UpdateInput) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := s.applyUpdate(ctx, tx, c, actorID, in); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) applyUpdate(ctx context.Context, tx pgx.Tx, c Conversation, actorID uuid.UUID, in UpdateInput) error {
	if in.Status != nil && *in.Status != c.Status {
		switch *in.Status {
		case domain.StatusActive, domain.StatusPending, domain.StatusClosed, domain.StatusSpam:
		default:
			return httperror.Validation("invalid status", map[string]string{"status": "must be active, pending, closed or spam"})
		}
		if _, err := tx.Exec(ctx, `
			UPDATE conversations SET status = $2,
			       closed_at = CASE WHEN $2 = 'closed' THEN now() ELSE NULL END
			 WHERE id = $1`, c.ID, *in.Status); err != nil {
			return err
		}
		if err := systemLine(ctx, tx, c.ID, actorID, "Status changed to "+*in.Status); err != nil {
			return err
		}
	}
	if in.ClearAssignee && c.AssigneeID != nil {
		if _, err := tx.Exec(ctx, `UPDATE conversations SET assignee_id = NULL WHERE id = $1`, c.ID); err != nil {
			return err
		}
		if err := systemLine(ctx, tx, c.ID, actorID, "Unassigned"); err != nil {
			return err
		}
	} else if in.AssigneeID != nil && (c.AssigneeID == nil || *c.AssigneeID != *in.AssigneeID) {
		var name string
		err := tx.QueryRow(ctx, `SELECT display_name FROM users WHERE id = $1`, *in.AssigneeID).Scan(&name)
		if errors.Is(err, pgx.ErrNoRows) {
			return httperror.Validation("unknown assignee", map[string]string{"assignee_id": "not a Desk user"})
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE conversations SET assignee_id = $2 WHERE id = $1`, c.ID, *in.AssigneeID); err != nil {
			return err
		}
		if err := systemLine(ctx, tx, c.ID, actorID, "Assigned to "+name); err != nil {
			return err
		}
	}
	return nil
}

func systemLine(ctx context.Context, tx pgx.Tx, convID, actorID uuid.UUID, text string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO threads (conversation_id, kind, author_id, body_text, body_html)
		VALUES ($1, 'system', $2, $3, $4)`, convID, actorID, text, html.EscapeString(text))
	return err
}

// ThreadInput is a reply or a note written by an agent.
type ThreadInput struct {
	Kind string // reply | note
	Body string
	// Status after a reply (default pending: waiting on the customer).
	Status *string
}

// AddThread stores a reply (queued for sending) or an internal note.
func (s *Service) AddThread(ctx context.Context, c Conversation, author *Person, in ThreadInput) (uuid.UUID, error) {
	body := strings.TrimSpace(in.Body)
	if body == "" {
		return uuid.Nil, httperror.Validation("empty message", map[string]string{"body": "required"})
	}
	if in.Kind != domain.ThreadReply && in.Kind != domain.ThreadNote {
		return uuid.Nil, httperror.BadRequest("kind must be reply or note")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	threadID := uuid.New()
	if in.Kind == domain.ThreadNote {
		_, err = tx.Exec(ctx, `
			INSERT INTO threads (id, conversation_id, kind, author_id, body_text, body_html)
			VALUES ($1, $2, 'note', $3, $4, $5)`, threadID, c.ID, author.ID, body, textToHTML(body))
		if err != nil {
			return uuid.Nil, err
		}
	} else {
		mb, err := s.GetMailbox(ctx, c.MailboxID)
		if err != nil {
			return uuid.Nil, err
		}
		full := body
		if sig := strings.TrimSpace(strings.ReplaceAll(mb.Signature, "{agent.name}", author.DisplayName)); sig != "" {
			full += "\n\n" + sig
		}
		msgID := fmt.Sprintf("desk.%s@%s", threadID, domainOf(mb.Email))
		_, err = tx.Exec(ctx, `
			INSERT INTO threads (id, conversation_id, kind, author_id, from_email, to_emails, body_text, body_html,
			                     message_id, send_status, next_attempt_at)
			VALUES ($1, $2, 'reply', $3, $4, $5, $6, $7, $8, 'queued', now() + $9::interval)`,
			threadID, c.ID, author.ID, mb.Email, []string{c.Customer.Email}, full, textToHTML(full),
			msgID, fmt.Sprintf("%d seconds", int(undoWindow.Seconds())))
		if err != nil {
			return uuid.Nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO message_ids (message_id, conversation_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, msgID, c.ID); err != nil {
			return uuid.Nil, err
		}
		status := domain.StatusPending
		if in.Status != nil {
			status = *in.Status
		}
		upd := UpdateInput{Status: &status}
		if c.AssigneeID == nil {
			// Answering an unassigned conversation takes it, as in FreeScout.
			upd.AssigneeID = &author.ID
		}
		if err := s.applyUpdate(ctx, tx, c, author.ID, upd); err != nil {
			return uuid.Nil, err
		}
	}
	if in.Kind == domain.ThreadReply {
		_, err = tx.Exec(ctx, `UPDATE conversations SET last_activity_at = now(), preview = $2 WHERE id = $1`, c.ID, previewOf(body))
	} else {
		_, err = tx.Exec(ctx, `UPDATE conversations SET last_activity_at = now() WHERE id = $1`, c.ID)
	}
	if err != nil {
		return uuid.Nil, err
	}
	return threadID, tx.Commit(ctx)
}

// UndoReply deletes a reply that has not been sent yet.
func (s *Service) UndoReply(ctx context.Context, threadID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM threads WHERE id = $1 AND kind = 'reply' AND send_status = 'queued' AND send_attempts = 0
		   AND next_attempt_at > now()`, threadID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httperror.Conflict("too late: the reply is already being sent")
	}
	_, _ = s.pool.Exec(ctx, `DELETE FROM message_ids WHERE message_id LIKE $1`, "desk."+threadID.String()+"@%")
	return nil
}

// RetryReply puts a failed reply back in the queue.
func (s *Service) RetryReply(ctx context.Context, threadID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE threads SET send_status = 'queued', send_attempts = 0, send_error = '', next_attempt_at = now()
		 WHERE id = $1 AND kind = 'reply' AND send_status = 'failed'`, threadID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httperror.Conflict("only failed replies can be retried")
	}
	return nil
}

// ThreadConversation returns the conversation a thread belongs to.
func (s *Service) ThreadConversation(ctx context.Context, threadID uuid.UUID) (Conversation, error) {
	var convID uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT conversation_id FROM threads WHERE id = $1`, threadID).Scan(&convID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Conversation{}, httperror.NotFound("thread")
	}
	if err != nil {
		return Conversation{}, err
	}
	return s.GetConversation(ctx, convID)
}

// NewConversationInput starts a conversation from Desk (agent writes first).
type NewConversationInput struct {
	MailboxID     uuid.UUID
	CustomerEmail string
	CustomerName  string
	Subject       string
	Body          string
}

// CreateConversation opens a conversation and queues its first reply.
func (s *Service) CreateConversation(ctx context.Context, author *Person, in NewConversationInput) (Conversation, error) {
	addr, err := mail.ParseAddress(strings.TrimSpace(in.CustomerEmail))
	issues := map[string]string{}
	if err != nil {
		issues["customer_email"] = "must be an email address"
	}
	if strings.TrimSpace(in.Subject) == "" {
		issues["subject"] = "required"
	}
	if strings.TrimSpace(in.Body) == "" {
		issues["body"] = "required"
	}
	if len(issues) > 0 {
		return Conversation{}, httperror.Validation("invalid conversation", issues)
	}
	mb, err := s.GetMailbox(ctx, in.MailboxID)
	if err != nil {
		return Conversation{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Conversation{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	name := strings.TrimSpace(in.CustomerName)
	if name == "" {
		name = addr.Name
	}
	customerID, err := upsertCustomer(ctx, tx, mb.OrganizationID, addr.Address, name)
	if err != nil {
		return Conversation{}, err
	}
	convID, err := insertConversation(ctx, tx, mb.ID, customerID, strings.TrimSpace(in.Subject), domain.StatusActive)
	if err != nil {
		return Conversation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Conversation{}, err
	}
	c, err := s.GetConversation(ctx, convID)
	if err != nil {
		return Conversation{}, err
	}
	if _, err := s.AddThread(ctx, c, author, ThreadInput{Kind: domain.ThreadReply, Body: in.Body}); err != nil {
		return Conversation{}, err
	}
	return s.GetConversation(ctx, convID)
}

// UpdateCustomer changes a customer's name and notes.
func (s *Service) UpdateCustomer(ctx context.Context, id uuid.UUID, name, notes *string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE customers SET name = COALESCE($2, name), notes = COALESCE($3, notes), updated_at = now()
		 WHERE id = $1`, id, name, notes)
	return err
}

// AttachmentInfo locates one attachment: its metadata and conversation.
func (s *Service) AttachmentInfo(ctx context.Context, id uuid.UUID) (AttachmentMeta, uuid.UUID, string, error) {
	var a AttachmentMeta
	var convID uuid.UUID
	var key string
	err := s.pool.QueryRow(ctx, `
		SELECT a.id, a.filename, a.content_type, a.size_bytes, a.content_id, a.inline, a.storage_key, t.conversation_id
		  FROM attachments a JOIN threads t ON t.id = a.thread_id WHERE a.id = $1`, id,
	).Scan(&a.ID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.ContentID, &a.Inline, &key, &convID)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, uuid.Nil, "", httperror.NotFound("attachment")
	}
	return a, convID, key, err
}

// OpenAttachment streams an attachment's content from object storage.
func (s *Service) OpenAttachment(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	r, size, err := s.blobs.Get(ctx, key)
	if errors.Is(err, blob.ErrNotFound) {
		return nil, 0, httperror.NotFound("attachment content")
	}
	return r, size, err
}

// ----------------------------------------------------------------------------
// Helpers shared with the inbound path
// ----------------------------------------------------------------------------

func upsertCustomer(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, email, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO customers (organization_id, email, name) VALUES ($1, $2, $3)
		ON CONFLICT (organization_id, email) DO UPDATE
		   SET name = CASE WHEN customers.name = '' THEN EXCLUDED.name ELSE customers.name END
		RETURNING id`, orgID, strings.ToLower(email), name).Scan(&id)
	return id, err
}

func insertConversation(ctx context.Context, tx pgx.Tx, mailboxID, customerID uuid.UUID, subject, status string) (uuid.UUID, error) {
	var number int
	if err := tx.QueryRow(ctx, `
		UPDATE mailboxes SET next_number = next_number + 1 WHERE id = $1 RETURNING next_number - 1`, mailboxID,
	).Scan(&number); err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO conversations (mailbox_id, number, subject, customer_id, status)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, mailboxID, number, subject, customerID, status).Scan(&id)
	return id, err
}

func textToHTML(text string) string {
	return strings.ReplaceAll(html.EscapeString(text), "\n", "<br>\n")
}

func previewOf(text string) string {
	t := strings.Join(strings.Fields(text), " ")
	if r := []rune(t); len(r) > 160 {
		return string(r[:160]) + "…"
	}
	return t
}

func domainOf(email string) string {
	if i := strings.LastIndex(email, "@"); i >= 0 {
		return email[i+1:]
	}
	return "desk.local"
}
