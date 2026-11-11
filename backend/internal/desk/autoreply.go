package desk

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

// AutoReply is a mailbox's automatic answer to the first message of a new
// conversation.
type AutoReply struct {
	Enabled bool   `json:"enabled"`
	Subject string `json:"subject"`
	// BodyHTML from the editor; BodyText is derived when empty.
	BodyHTML string `json:"body_html"`
	BodyText string `json:"body_text"`
}

func (a *AutoReply) normalize() map[string]string {
	issues := map[string]string{}
	a.Subject = strings.TrimSpace(clean(a.Subject))
	a.BodyHTML, a.BodyText = composeBodies(a.BodyText, a.BodyHTML)
	if a.Enabled && a.BodyText == "" {
		issues["auto_reply.body_html"] = "required when the auto-reply is on"
	}
	if len([]rune(a.Subject)) > 200 {
		issues["auto_reply.subject"] = "at most 200 characters"
	}
	return issues
}

func applyAutoReply(ctx context.Context, tx pgx.Tx, id uuid.UUID, a *AutoReply) error {
	if a == nil {
		return nil
	}
	if issues := a.normalize(); len(issues) > 0 {
		return httperror.Validation("invalid auto-reply", issues)
	}
	_, err := tx.Exec(ctx, `
		UPDATE mailboxes SET auto_reply_enabled = $2, auto_reply_subject = $3, auto_reply_html = $4, auto_reply_text = $5,
		       updated_at = now() WHERE id = $1`, id, a.Enabled, a.Subject, a.BodyHTML, a.BodyText)
	return err
}

// noAutoReplyLocal are senders Desk never auto-answers (RFC 3834 §2).
var noAutoReplyLocal = []string{"noreply", "no-reply", "donotreply", "do-not-reply", "mailer-daemon", "postmaster", "bounce", "bounces"}

// shouldAutoReply applies loop protection: no answer to automatic mail,
// mailing lists, Desk's own mailboxes or no-reply senders, and at most one
// auto-reply per customer and mailbox per day.
func shouldAutoReply(ctx context.Context, tx pgx.Tx, mb Mailbox, p *ParsedMessage) (bool, error) {
	if !mb.autoReply.Enabled || mb.autoReply.BodyText == "" || p.Automatic() || p.List {
		return false, nil
	}
	local := p.FromEmail
	if i := strings.IndexByte(local, '@'); i >= 0 {
		local = local[:i]
	}
	for _, n := range noAutoReplyLocal {
		if local == n || strings.HasPrefix(local, n+"+") || strings.HasPrefix(local, n+"-") {
			return false, nil
		}
	}
	var recent bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM threads t JOIN conversations c ON c.id = t.conversation_id
		                WHERE c.mailbox_id = $1 AND t.kind = 'auto_reply' AND $2 = ANY(t.to_emails)
		                  AND t.created_at > now() - interval '24 hours')`, mb.ID, p.FromEmail).Scan(&recent)
	return !recent, err
}

// queueAutoReply adds the mailbox's auto-reply to a new conversation.
func queueAutoReply(ctx context.Context, tx pgx.Tx, mb Mailbox, convID uuid.UUID, to string) error {
	id := uuid.New()
	msgID := fmt.Sprintf("desk.%s@%s", id, domainOf(mb.Email))
	if _, err := tx.Exec(ctx, `
		INSERT INTO threads (id, conversation_id, kind, from_email, to_emails, body_text, body_html, message_id,
		                     send_status, next_attempt_at)
		VALUES ($1, $2, 'auto_reply', $3, $4, $5, $6, $7, 'queued', now())`,
		id, convID, mb.Email, []string{to}, mb.autoReply.BodyText, mb.autoReply.BodyHTML, msgID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO message_ids (message_id, conversation_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, msgID, convID)
	return err
}
