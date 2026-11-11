package desk

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"strconv"
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

const (
	// defaultPageSize and maxPageSize bound the conversation list.
	defaultPageSize = 50
	maxPageSize     = 100
	// maxThreads bounds the threads returned with a conversation (newest).
	maxThreads = 500
	// maxTags per conversation.
	maxTags = 20
)

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
	Tags           []string   `json:"tags"`
	Starred        bool       `json:"starred"`
	LastActivityAt time.Time  `json:"last_activity_at"`
	CreatedAt      time.Time  `json:"created_at"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
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
	AutoSubmitted bool             `json:"auto_submitted"`
	DeletedAt     *time.Time       `json:"deleted_at,omitempty"`
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
	// ThreadsTruncated: only the newest maxThreads threads are included.
	ThreadsTruncated bool `json:"threads_truncated"`
	// History is the customer's other conversations, newest first.
	History []Conversation `json:"history"`
	Viewers []Viewer       `json:"viewers"`
	Access  MailboxRights  `json:"access"`
}

// convSelect: $1 is always the viewer's user id (for "starred").
const convSelect = `
	SELECT c.id, c.mailbox_id, m.name, c.number, c.subject, c.status, c.preview,
	       cu.id, cu.email::text, cu.name, cu.notes,
	       c.assignee_id, COALESCE(u.display_name, ''),
	       (SELECT count(*) FROM threads t WHERE t.conversation_id = c.id AND t.kind <> 'system' AND t.deleted_at IS NULL),
	       ARRAY(SELECT ct.tag FROM conversation_tags ct WHERE ct.conversation_id = c.id ORDER BY ct.tag),
	       EXISTS (SELECT 1 FROM conversation_stars st WHERE st.conversation_id = c.id AND st.user_id = $1),
	       c.last_activity_at, c.created_at, c.closed_at, c.deleted_at
	  FROM conversations c
	  JOIN mailboxes m ON m.id = c.mailbox_id
	  JOIN customers cu ON cu.id = c.customer_id
	  LEFT JOIN users u ON u.id = c.assignee_id`

func scanConversation(row pgx.Row) (Conversation, error) {
	var c Conversation
	err := row.Scan(&c.ID, &c.MailboxID, &c.MailboxName, &c.Number, &c.Subject, &c.Status, &c.Preview,
		&c.Customer.ID, &c.Customer.Email, &c.Customer.Name, &c.Customer.Notes,
		&c.AssigneeID, &c.AssigneeName, &c.ThreadCount, &c.Tags, &c.Starred,
		&c.LastActivityAt, &c.CreatedAt, &c.ClosedAt, &c.DeletedAt)
	if c.Tags == nil {
		c.Tags = []string{}
	}
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
	// MailboxIDs are the mailboxes the caller may view (required).
	MailboxIDs []uuid.UUID
	// MailboxID narrows to one of them; nil searches all of them.
	MailboxID *uuid.UUID
	// Folder: unassigned | mine | assigned | pending | closed | spam | all |
	// starred | trash.
	Folder string
	Query  string
	Tag    string
	UserID uuid.UUID
	// Cursor continues after the last conversation of the previous page.
	Cursor string
	Limit  int
}

// ListPage is one page of conversations.
type ListPage struct {
	Conversations []Conversation `json:"conversations"`
	// NextCursor is "" on the last page.
	NextCursor string `json:"next_cursor"`
}

func encodeCursor(c Conversation) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(c.LastActivityAt.UnixMicro(), 10) + "|" + c.ID.String()))
}

func decodeCursor(s string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.Nil, errors.New("bad cursor")
	}
	micros, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	u, err := uuid.Parse(id)
	return time.UnixMicro(micros), u, err
}

// ListConversations returns one page, newest activity first. A query
// searches subjects and message text (full-text), customer names and
// addresses, and "#123" numbers, across every visible mailbox unless one
// is chosen.
func (s *Service) ListConversations(ctx context.Context, f ListFilter) (ListPage, error) {
	args := []any{f.UserID}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	conds := []string{"c.mailbox_id = ANY(" + arg(f.MailboxIDs) + ")", "m.deleted_at IS NULL"}
	if f.MailboxID != nil {
		conds = append(conds, "c.mailbox_id = "+arg(*f.MailboxID))
	}
	live := "c.deleted_at IS NULL AND "
	switch f.Folder {
	case "", "unassigned":
		conds = append(conds, live+"c.status = 'active' AND c.assignee_id IS NULL")
	case "mine":
		conds = append(conds, live+"c.status IN ('active','pending') AND c.assignee_id = $1")
	case "assigned":
		conds = append(conds, live+"c.status IN ('active','pending') AND c.assignee_id IS NOT NULL")
	case "pending", "closed", "spam":
		conds = append(conds, live+"c.status = "+arg(f.Folder))
	case "all":
		conds = append(conds, live+"c.status <> 'spam'")
	case "starred":
		conds = append(conds, live+"EXISTS (SELECT 1 FROM conversation_stars st WHERE st.conversation_id = c.id AND st.user_id = $1)")
	case "trash":
		conds = append(conds, "c.deleted_at IS NOT NULL")
	default:
		return ListPage{}, httperror.BadRequest("unknown folder: " + f.Folder)
	}
	if t := strings.ToLower(strings.TrimSpace(f.Tag)); t != "" {
		conds = append(conds, "EXISTS (SELECT 1 FROM conversation_tags ct WHERE ct.conversation_id = c.id AND ct.tag = "+arg(t)+")")
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		num := strings.TrimPrefix(q, "#")
		if n, err := strconv.Atoi(num); err == nil && strconv.Itoa(n) == num {
			conds = append(conds, "c.number = "+arg(n))
		} else {
			like := arg("%" + escapeLike(q) + "%")
			tsq := "websearch_to_tsquery('simple', " + arg(q) + ")"
			conds = append(conds, fmt.Sprintf(`(c.search @@ %[2]s OR c.subject ILIKE %[1]s OR cu.name ILIKE %[1]s
				OR EXISTS (SELECT 1 FROM customer_emails ce WHERE ce.customer_id = cu.id AND ce.email ILIKE %[1]s)
				OR EXISTS (SELECT 1 FROM threads t WHERE t.conversation_id = c.id AND t.deleted_at IS NULL AND t.search @@ %[2]s))`, like, tsq))
		}
	}
	if f.Cursor != "" {
		ts, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return ListPage{}, httperror.BadRequest("invalid cursor")
		}
		conds = append(conds, "(c.last_activity_at, c.id) < ("+arg(ts)+", "+arg(id)+")")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}
	limit = min(limit, maxPageSize)
	list, err := s.queryConversations(ctx, "WHERE "+strings.Join(conds, " AND ")+
		" ORDER BY c.last_activity_at DESC, c.id DESC LIMIT "+arg(limit+1), args...)
	if err != nil {
		return ListPage{}, err
	}
	page := ListPage{Conversations: list}
	if len(list) > limit {
		page.Conversations = list[:limit]
		page.NextCursor = encodeCursor(list[limit-1])
	}
	return page, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// GetConversation loads one conversation or a typed 404.
func (s *Service) GetConversation(ctx context.Context, id uuid.UUID) (Conversation, error) {
	return s.getConversationAs(ctx, id, uuid.Nil)
}

func (s *Service) getConversationAs(ctx context.Context, id, viewer uuid.UUID) (Conversation, error) {
	c, err := scanConversation(s.pool.QueryRow(ctx, convSelect+" WHERE c.id = $2", viewer, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Conversation{}, httperror.NotFound("conversation")
	}
	return c, err
}

// Detail loads threads, attachments, customer history and other viewers.
// Emails in Desk's trash are included only for callers who may restore
// them.
func (s *Service) Detail(ctx context.Context, a Access, c Conversation, mb Mailbox) (ConversationDetail, error) {
	if fresh, err := s.getConversationAs(ctx, c.ID, a.UserID); err == nil {
		c = fresh
	}
	d := ConversationDetail{Conversation: c, Threads: []Thread{}, Viewers: []Viewer{}, Access: a.Rights(mb)}
	showDeleted := a.CanDelete(mb) || c.DeletedAt != nil
	rows, err := s.pool.Query(ctx, `
		SELECT * FROM (
		  SELECT t.id, t.kind, t.author_id, COALESCE(u.display_name, ''), t.from_email, t.to_emails, t.cc_emails,
		         t.body_html, t.body_text, t.send_status, t.send_error, t.next_attempt_at, t.auto_submitted, t.deleted_at, t.created_at
		    FROM threads t LEFT JOIN users u ON u.id = t.author_id
		   WHERE t.conversation_id = $1 AND ($2 OR t.deleted_at IS NULL)
		   ORDER BY t.created_at DESC, t.id DESC LIMIT $3) newest
		 ORDER BY created_at, id`, c.ID, showDeleted, maxThreads+1)
	if err != nil {
		return d, err
	}
	index := map[uuid.UUID]int{}
	for rows.Next() {
		var t Thread
		if err := rows.Scan(&t.ID, &t.Kind, &t.AuthorID, &t.AuthorName, &t.FromEmail, &t.To, &t.CC,
			&t.BodyHTML, &t.BodyText, &t.SendStatus, &t.SendError, &t.NextAttemptAt, &t.AutoSubmitted, &t.DeletedAt, &t.CreatedAt); err != nil {
			rows.Close()
			return d, err
		}
		t.Attachments = []AttachmentMeta{}
		d.Threads = append(d.Threads, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, err
	}
	if len(d.Threads) > maxThreads {
		d.Threads, d.ThreadsTruncated = d.Threads[1:], true
	}
	ids := make([]uuid.UUID, 0, len(d.Threads))
	for i, t := range d.Threads {
		index[t.ID] = i
		ids = append(ids, t.ID)
	}

	rows, err = s.pool.Query(ctx, `
		SELECT thread_id, id, filename, content_type, size_bytes, content_id, inline
		  FROM attachments WHERE thread_id = ANY($1) ORDER BY created_at`, ids)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var threadID uuid.UUID
		var at AttachmentMeta
		if err := rows.Scan(&threadID, &at.ID, &at.Filename, &at.ContentType, &at.SizeBytes, &at.ContentID, &at.Inline); err != nil {
			rows.Close()
			return d, err
		}
		if i, ok := index[threadID]; ok {
			d.Threads[i].Attachments = append(d.Threads[i].Attachments, at)
		}
	}
	rows.Close()

	// History only from mailboxes the caller may see.
	visible, err := s.VisibleMailboxes(ctx, a)
	if err != nil {
		return d, err
	}
	vis := make([]uuid.UUID, 0, len(visible))
	for _, m := range visible {
		vis = append(vis, m.ID)
	}
	d.History, err = s.queryConversations(ctx,
		"WHERE c.customer_id = $2 AND c.id <> $3 AND c.mailbox_id = ANY($4) AND c.deleted_at IS NULL ORDER BY c.last_activity_at DESC LIMIT 20",
		a.UserID, c.Customer.ID, c.ID, vis)
	if err != nil {
		return d, err
	}

	rows, err = s.pool.Query(ctx, `
		SELECT v.user_id, u.display_name, v.mode FROM viewers v JOIN users u ON u.id = v.user_id
		 WHERE v.conversation_id = $1 AND v.user_id <> $2 AND v.seen_at > now() - interval '30 seconds'
		 ORDER BY u.display_name`, c.ID, a.UserID)
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

// UpdateInput changes status, assignee and/or tags. ClearAssignee unassigns.
type UpdateInput struct {
	Status        *string
	AssigneeID    *uuid.UUID
	ClearAssignee bool
	// Tags replaces the conversation's tags.
	Tags *[]string
}

// UpdateConversation applies status / assignee / tag changes and records
// system lines for them.
func (s *Service) UpdateConversation(ctx context.Context, c Conversation, mb Mailbox, actor *Person, in UpdateInput) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.applyUpdate(ctx, tx, c, actor.ID, in); err != nil {
			return err
		}
		if in.AssigneeID != nil && *in.AssigneeID != actor.ID && (c.AssigneeID == nil || *c.AssigneeID != *in.AssigneeID) {
			return s.notifyAssigned(ctx, tx, mb, c, *in.AssigneeID, actor)
		}
		return nil
	})
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
		var name, slug string
		var admin bool
		var groups []string
		err := tx.QueryRow(ctx, `
			SELECT u.display_name, u.desk_admin, u.groups, m.slug
			  FROM users u, conversations c JOIN mailboxes m ON m.id = c.mailbox_id
			 WHERE u.id = $1 AND c.id = $2`, *in.AssigneeID, c.ID).Scan(&name, &admin, &groups, &slug)
		if errors.Is(err, pgx.ErrNoRows) {
			return httperror.Validation("unknown assignee", map[string]string{"assignee_id": "not a Desk user"})
		}
		if err != nil {
			return err
		}
		mayWork := admin || actorID == *in.AssigneeID ||
			HasMailboxRole(s.groups, groups, slug, domain.LevelManager)
		if !mayWork {
			return httperror.Validation("assignee cannot work this mailbox", map[string]string{"assignee_id": "needs the manager role of this mailbox"})
		}
		if _, err := tx.Exec(ctx, `UPDATE conversations SET assignee_id = $2 WHERE id = $1`, c.ID, *in.AssigneeID); err != nil {
			return err
		}
		if err := systemLine(ctx, tx, c.ID, actorID, "Assigned to "+name); err != nil {
			return err
		}
	}
	if in.Tags != nil {
		tags, err := cleanTags(*in.Tags)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM conversation_tags WHERE conversation_id = $1 AND NOT (tag = ANY($2))`, c.ID, tags); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversation_tags (conversation_id, tag) SELECT $1, unnest($2::text[]) ON CONFLICT DO NOTHING`, c.ID, tags); err != nil {
			return err
		}
	}
	return nil
}

func cleanTags(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range in {
		t = strings.ToLower(strings.Join(strings.Fields(t), " "))
		if t == "" || seen[t] {
			continue
		}
		if !tagPattern.MatchString(t) {
			return nil, httperror.Validation("invalid tag", map[string]string{"tags": fmt.Sprintf("%q: letters, digits, spaces, - and _, at most 32 characters", t)})
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) > maxTags {
		return nil, httperror.Validation("too many tags", map[string]string{"tags": fmt.Sprintf("at most %d", maxTags)})
	}
	return out, nil
}

// Tags lists the tags used in the given mailboxes, most used first.
func (s *Service) Tags(ctx context.Context, mailboxIDs []uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ct.tag FROM conversation_tags ct JOIN conversations c ON c.id = ct.conversation_id
		 WHERE c.mailbox_id = ANY($1) AND c.deleted_at IS NULL GROUP BY ct.tag ORDER BY count(*) DESC, ct.tag LIMIT 200`, mailboxIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil {
			out = append(out, t)
		}
	}
	return out, rows.Err()
}

// SetStar stars or unstars a conversation for userID.
func (s *Service) SetStar(ctx context.Context, convID, userID uuid.UUID, on bool) error {
	var err error
	if on {
		_, err = s.pool.Exec(ctx, `INSERT INTO conversation_stars (conversation_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, convID, userID)
	} else {
		_, err = s.pool.Exec(ctx, `DELETE FROM conversation_stars WHERE conversation_id = $1 AND user_id = $2`, convID, userID)
	}
	return err
}

func systemLine(ctx context.Context, tx pgx.Tx, convID, actorID uuid.UUID, text string) error {
	var author *uuid.UUID
	if actorID != uuid.Nil {
		author = &actorID
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO threads (conversation_id, kind, author_id, body_text, body_html)
		VALUES ($1, 'system', $2, $3, $4)`, convID, author, text, html.EscapeString(text))
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

// upsertCustomer finds the customer by any of their addresses or creates
// one.
func upsertCustomer(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, email, name string) (uuid.UUID, error) {
	email = strings.ToLower(email)
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT customer_id FROM customer_emails WHERE organization_id = $1 AND email = $2`, orgID, email).Scan(&id)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE customers SET name = $2 WHERE id = $1 AND name = '' AND $2 <> ''`, id, name)
		return id, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO customers (organization_id, email, name) VALUES ($1, $2, $3)
		ON CONFLICT (organization_id, email) DO UPDATE
		   SET name = CASE WHEN customers.name = '' THEN EXCLUDED.name ELSE customers.name END
		RETURNING id`, orgID, email, name).Scan(&id); err != nil {
		return uuid.Nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_emails (customer_id, organization_id, email) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, id, orgID, email)
	return id, err
}

func insertConversation(ctx context.Context, tx pgx.Tx, mailboxID, customerID uuid.UUID, subject, status string) (uuid.UUID, error) {
	number, err := nextNumber(ctx, tx, mailboxID)
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO conversations (mailbox_id, number, subject, customer_id, status)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, mailboxID, number, subject, customerID, status).Scan(&id)
	return id, err
}

func nextNumber(ctx context.Context, tx pgx.Tx, mailboxID uuid.UUID) (int, error) {
	var number int
	err := tx.QueryRow(ctx, `UPDATE mailboxes SET next_number = next_number + 1 WHERE id = $1 RETURNING next_number - 1`, mailboxID).Scan(&number)
	return number, err
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
