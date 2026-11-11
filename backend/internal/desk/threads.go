package desk

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/desk/backend/internal/domain"
	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

var tagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9 _-]{0,31}$`)

// ThreadInput is a reply or a note written by an agent.
type ThreadInput struct {
	Kind string // reply | note
	// Body is the plain text; BodyHTML the rich text from the editor. At
	// least one is required; a missing plain text is derived from the HTML.
	Body     string
	BodyHTML string
	// CC for a reply. nil: everyone the customer's last message went to,
	// except Desk's own mailboxes and the customer.
	CC *[]string
	// Status after a reply (default pending: waiting on the customer).
	Status *string
}

// composeBodies returns sanitised HTML and plain text for an agent's
// message.
func composeBodies(text, rich string) (string, string) {
	text = strings.TrimSpace(clean(text))
	rich = strings.TrimSpace(clean(rich))
	if rich == "" {
		return textToHTML(text), text
	}
	safe := htmlPolicy.Sanitize(rich)
	if text == "" {
		text = htmlToText(safe)
	}
	return safe, text
}

var (
	blockEnd   = regexp.MustCompile(`(?i)</(p|div|h[1-6]|li|blockquote|tr|pre)>|<br\s*/?>`)
	listItem   = regexp.MustCompile(`(?i)<li[^>]*>`)
	manyBlanks = regexp.MustCompile(`\n{3,}`)
)

// htmlToText is the plain-text alternative of an HTML message.
func htmlToText(h string) string {
	h = blockEnd.ReplaceAllString(h, "$0\n")
	h = listItem.ReplaceAllString(h, "$0- ")
	t := html.UnescapeString(stripPolicy.Sanitize(h))
	lines := strings.Split(t, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(manyBlanks.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// AddThread stores a reply (queued for sending) or an internal note.
func (s *Service) AddThread(ctx context.Context, c Conversation, author *Person, in ThreadInput) (uuid.UUID, error) {
	var id uuid.UUID
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		id, err = s.addThreadTx(ctx, tx, c, author, in)
		return err
	})
	return id, err
}

func (s *Service) addThreadTx(ctx context.Context, tx pgx.Tx, c Conversation, author *Person, in ThreadInput) (uuid.UUID, error) {
	if in.Kind != domain.ThreadReply && in.Kind != domain.ThreadNote {
		return uuid.Nil, httperror.BadRequest("kind must be reply or note")
	}
	if c.DeletedAt != nil {
		return uuid.Nil, httperror.Conflict("the conversation is in the trash; restore it first")
	}
	htmlBody, text := composeBodies(in.Body, in.BodyHTML)
	if text == "" {
		return uuid.Nil, httperror.Validation("empty message", map[string]string{"body": "required"})
	}

	threadID := uuid.New()
	if in.Kind == domain.ThreadNote {
		if _, err := tx.Exec(ctx, `
			INSERT INTO threads (id, conversation_id, kind, author_id, body_text, body_html)
			VALUES ($1, $2, 'note', $3, $4, $5)`, threadID, c.ID, author.ID, text, htmlBody); err != nil {
			return uuid.Nil, err
		}
		_, err := tx.Exec(ctx, `UPDATE conversations SET last_activity_at = now() WHERE id = $1`, c.ID)
		return threadID, err
	}

	mb, err := s.GetMailbox(ctx, c.MailboxID)
	if err != nil {
		return uuid.Nil, err
	}
	var cc []string
	if in.CC != nil {
		cc, err = cleanAddresses(*in.CC, "cc")
		if err != nil {
			return uuid.Nil, err
		}
	} else if cc, err = defaultCC(ctx, tx, c); err != nil {
		return uuid.Nil, err
	}
	if sig := strings.TrimSpace(strings.ReplaceAll(mb.Signature, "{agent.name}", author.DisplayName)); sig != "" {
		text += "\n\n" + sig
		htmlBody += "<br><br>" + textToHTML(sig)
	}
	msgID := fmt.Sprintf("desk.%s@%s", threadID, domainOf(mb.Email))
	if _, err := tx.Exec(ctx, `
		INSERT INTO threads (id, conversation_id, kind, author_id, from_email, to_emails, cc_emails, body_text, body_html,
		                     message_id, send_status, next_attempt_at)
		VALUES ($1, $2, 'reply', $3, $4, $5, $6, $7, $8, $9, 'queued', now() + $10::interval)`,
		threadID, c.ID, author.ID, mb.Email, []string{c.Customer.Email}, cc, text, htmlBody,
		msgID, fmt.Sprintf("%d seconds", int(undoWindow.Seconds()))); err != nil {
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
	_, err = tx.Exec(ctx, `UPDATE conversations SET last_activity_at = now(), preview = $2 WHERE id = $1`, c.ID, previewOf(text))
	return threadID, err
}

// cleanAddresses validates a list of plain addresses.
func cleanAddresses(list []string, field string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range list {
		a, err := mail.ParseAddress(strings.TrimSpace(raw))
		if err != nil {
			return nil, httperror.Validation("invalid address", map[string]string{field: raw + " is not an email address"})
		}
		addr := strings.ToLower(a.Address)
		if !seen[addr] {
			seen[addr] = true
			out = append(out, addr)
		}
	}
	if len(out) > 20 {
		return nil, httperror.Validation("too many recipients", map[string]string{field: "at most 20 addresses"})
	}
	return out, nil
}

// defaultCC is "reply all": the To and Cc of the customer's latest message
// without Desk's own mailboxes and the customer themself.
func defaultCC(ctx context.Context, tx pgx.Tx, c Conversation) ([]string, error) {
	var to, cc []string
	err := tx.QueryRow(ctx, `
		SELECT to_emails, cc_emails FROM threads
		 WHERE conversation_id = $1 AND kind = 'customer' AND NOT auto_submitted AND deleted_at IS NULL
		 ORDER BY created_at DESC LIMIT 1`, c.ID).Scan(&to, &cc)
	if errors.Is(err, pgx.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT lower(email::text) FROM mailboxes
		UNION SELECT lower(email::text) FROM customer_emails WHERE customer_id = $1`, c.Customer.ID)
	if err != nil {
		return nil, err
	}
	own := map[string]bool{strings.ToLower(c.Customer.Email): true}
	for rows.Next() {
		var e string
		if rows.Scan(&e) == nil {
			own[e] = true
		}
	}
	rows.Close()
	out := []string{}
	for _, a := range append(to, cc...) {
		a = strings.ToLower(a)
		if !own[a] {
			own[a] = true
			out = append(out, a)
		}
	}
	return out, nil
}

// threadOwner returns who wrote an outgoing thread.
func (s *Service) threadOwner(ctx context.Context, threadID uuid.UUID) (*uuid.UUID, error) {
	var author *uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT author_id FROM threads WHERE id = $1`, threadID).Scan(&author)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httperror.NotFound("thread")
	}
	return author, err
}

// UndoReply deletes a reply that has not been sent yet. Only its author
// may take it back (or a Desk admin).
func (s *Service) UndoReply(ctx context.Context, a Access, mb Mailbox, threadID uuid.UUID) error {
	author, err := s.threadOwner(ctx, threadID)
	if err != nil {
		return err
	}
	if (author == nil || *author != a.UserID) && !a.IsAdmin(mb.OrganizationID) {
		return httperror.New(httperror.CodeForbidden, "only the author can undo this reply")
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM threads WHERE id = $1 AND kind IN ('reply', 'forward') AND send_status = 'queued' AND send_attempts = 0
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

// RetryReply puts a failed reply back in the queue: its author or a
// manager of the mailbox.
func (s *Service) RetryReply(ctx context.Context, a Access, mb Mailbox, threadID uuid.UUID) error {
	author, err := s.threadOwner(ctx, threadID)
	if err != nil {
		return err
	}
	if (author == nil || *author != a.UserID) && !a.CanWork(mb) {
		return httperror.New(httperror.CodeForbidden, "only the author or a manager of this mailbox can retry this reply")
	}
	return s.retryReply(ctx, threadID)
}

func (s *Service) retryReply(ctx context.Context, threadID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE threads SET send_status = 'queued', send_attempts = 0, send_error = '', next_attempt_at = now()
		 WHERE id = $1 AND kind IN ('reply', 'forward', 'auto_reply') AND send_status = 'failed'`, threadID)
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

// ForwardInput forwards one message of a conversation to other people.
type ForwardInput struct {
	// ThreadID is the message to forward; nil: the customer's latest.
	ThreadID *uuid.UUID
	To       []string
	Body     string
	BodyHTML string
}

// Forward queues a forward of a message (with its attachments) to people
// outside the conversation. The conversation's status does not change.
func (s *Service) Forward(ctx context.Context, c Conversation, author *Person, in ForwardInput) (uuid.UUID, error) {
	to, err := cleanAddresses(in.To, "to")
	if err != nil {
		return uuid.Nil, err
	}
	if len(to) == 0 {
		return uuid.Nil, httperror.Validation("no recipient", map[string]string{"to": "required"})
	}
	if c.DeletedAt != nil {
		return uuid.Nil, httperror.Conflict("the conversation is in the trash; restore it first")
	}
	var src Thread
	q := `SELECT id, from_email, body_text, body_html, created_at FROM threads
	       WHERE conversation_id = $1 AND kind IN ('customer', 'reply') AND deleted_at IS NULL`
	args := []any{c.ID}
	if in.ThreadID != nil {
		q += " AND id = $2"
		args = append(args, *in.ThreadID)
	} else {
		q += " AND kind = 'customer'"
	}
	err = s.pool.QueryRow(ctx, q+" ORDER BY created_at DESC LIMIT 1", args...).Scan(&src.ID, &src.FromEmail, &src.BodyText, &src.BodyHTML, &src.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, httperror.NotFound("message to forward")
	}
	if err != nil {
		return uuid.Nil, err
	}
	mb, err := s.GetMailbox(ctx, c.MailboxID)
	if err != nil {
		return uuid.Nil, err
	}
	noteHTML, noteText := composeBodies(in.Body, in.BodyHTML)
	header := fmt.Sprintf("---------- Forwarded message ----------\nFrom: %s\nDate: %s\nSubject: %s", src.FromEmail, src.CreatedAt.UTC().Format("Mon, 2 Jan 2006 15:04 MST"), c.Subject)
	text := strings.TrimSpace(noteText + "\n\n" + header + "\n\n" + src.BodyText)
	htmlBody := noteHTML + "<br><br><p>" + textToHTML(header) + "</p><blockquote>" + src.BodyHTML + "</blockquote>"
	id := uuid.New()
	msgID := fmt.Sprintf("desk.%s@%s", id, domainOf(mb.Email))
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO threads (id, conversation_id, kind, author_id, from_email, to_emails, body_text, body_html, message_id,
			                     send_status, next_attempt_at, forward_of)
			VALUES ($1, $2, 'forward', $3, $4, $5, $6, $7, $8, 'queued', now() + $9::interval, $10)`,
			id, c.ID, author.ID, mb.Email, to, text, htmlBody, msgID, fmt.Sprintf("%d seconds", int(undoWindow.Seconds())), src.ID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE conversations SET last_activity_at = now() WHERE id = $1`, c.ID)
		return err
	})
	return id, err
}

// NewConversationInput starts a conversation from Desk (agent writes first).
type NewConversationInput struct {
	MailboxID     uuid.UUID
	CustomerEmail string
	CustomerName  string
	Subject       string
	Body          string
	BodyHTML      string
	CC            []string
}

// CreateConversation opens a conversation and queues its first reply, in
// one transaction: a failure leaves nothing behind.
func (s *Service) CreateConversation(ctx context.Context, author *Person, in NewConversationInput) (Conversation, error) {
	addr, err := mail.ParseAddress(strings.TrimSpace(in.CustomerEmail))
	issues := map[string]string{}
	if err != nil {
		issues["customer_email"] = "must be an email address"
	}
	if strings.TrimSpace(in.Subject) == "" {
		issues["subject"] = "required"
	}
	if _, text := composeBodies(in.Body, in.BodyHTML); text == "" {
		issues["body"] = "required"
	}
	if len(issues) > 0 {
		return Conversation{}, httperror.Validation("invalid conversation", issues)
	}
	mb, err := s.GetMailbox(ctx, in.MailboxID)
	if err != nil {
		return Conversation{}, err
	}
	var convID uuid.UUID
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		name := strings.TrimSpace(in.CustomerName)
		if name == "" {
			name = addr.Name
		}
		customerID, err := upsertCustomer(ctx, tx, mb.OrganizationID, addr.Address, name)
		if err != nil {
			return err
		}
		convID, err = insertConversation(ctx, tx, mb.ID, customerID, clean(strings.TrimSpace(in.Subject)), domain.StatusActive)
		if err != nil {
			return err
		}
		c, err := scanConversation(tx.QueryRow(ctx, convSelect+" WHERE c.id = $2", author.ID, convID))
		if err != nil {
			return err
		}
		cc := in.CC
		if cc == nil {
			cc = []string{}
		}
		_, err = s.addThreadTx(ctx, tx, c, author, ThreadInput{Kind: domain.ThreadReply, Body: in.Body, BodyHTML: in.BodyHTML, CC: &cc})
		return err
	})
	if err != nil {
		return Conversation{}, err
	}
	return s.getConversationAs(ctx, convID, author.ID)
}
