package desk

import (
	"context"
	"fmt"
	"net/mail"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// queueEmail puts one of Desk's own emails (reminders, notifications) in
// the outbox. Without NOTIFY_FROM nothing is queued.
func (s *Service) queueEmail(ctx context.Context, q execer, to []string, subject, body string) error {
	if s.notifySender() == "" {
		return nil
	}
	for _, addr := range to {
		if addr == "" {
			continue
		}
		if _, err := q.Exec(ctx, `INSERT INTO outbox (to_email, subject, body_text) VALUES ($1, $2, $3)`, addr, subject, body); err != nil {
			return err
		}
	}
	return nil
}

// NotificationsEnabled reports whether Desk can send its own emails.
func (s *Service) NotificationsEnabled() bool {
	return s.notifySender() != "" && (s.moduleSMTPHost() != "" || (s.google != nil && s.google.SharedMailbox != ""))
}

// notifySender is the From of Desk's own emails: NOTIFY_FROM, else Den's
// smtp.from (with smtp.from_name), else "" (nothing is sent).
func (s *Service) notifySender() string {
	if s.notifyFrom != "" {
		return s.notifyFrom
	}
	if d, ok := s.moduleSettings().SMTP(); ok && d.From != "" {
		return (&mail.Address{Name: d.FromName, Address: d.From}).String()
	}
	return ""
}

// denSMTP is Den's mail server (module settings smtp.*), without password.
func (s *Service) denSMTP() (SMTPSettings, bool) {
	d, ok := s.moduleSettings().SMTP()
	if !ok {
		return SMTPSettings{}, false
	}
	return SMTPSettings{Host: d.Host, Port: d.Port, Security: d.Security, Username: d.Username, moduleWide: true, fromDen: true}, true
}

func (s *Service) moduleSMTPHost() string {
	if d, ok := s.denSMTP(); ok {
		return d.Host
	}
	return s.smtp.Host
}

// moduleSMTP is the module-wide mail server: Den's when Den sets smtp.host
// (its password fetched from Den when it holds one), else SMTP_*.
func (s *Service) moduleSMTP(ctx context.Context) (SMTPSettings, error) {
	if cfg, ok := s.denSMTP(); ok {
		if pwSet, _ := s.moduleSettings().Bool("smtp.password_set"); pwSet {
			if s.denSMTPPassword == nil {
				return cfg, displayError{msg: "Den holds the SMTP password, but Desk cannot fetch it"}
			}
			pw, err := s.denSMTPPassword(ctx)
			if err != nil {
				return cfg, displayError{msg: "could not fetch the SMTP password from Den", err: err}
			}
			cfg.Password = pw
		}
		return cfg, nil
	}
	if s.smtp.Host == "" {
		return SMTPSettings{}, nil
	}
	cfg := s.smtp
	cfg.moduleWide = true
	return cfg, nil
}

func (s *Service) sendOutbox(ctx context.Context, log zerolog.Logger) {
	rows, err := s.pool.Query(ctx, `
		UPDATE outbox SET status = 'sending', claimed_at = now()
		 WHERE id IN (SELECT id FROM outbox WHERE status = 'queued' AND next_attempt_at <= now()
		               ORDER BY next_attempt_at LIMIT 20 FOR UPDATE SKIP LOCKED)
		RETURNING id, to_email, subject, body_text`)
	if err != nil {
		return
	}
	type item struct {
		id               uuid.UUID
		to, subject, txt string
	}
	var items []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.id, &it.to, &it.subject, &it.txt) == nil {
			items = append(items, it)
		}
	}
	rows.Close()
	db := context.WithoutCancel(ctx)
	for _, it := range items {
		err := s.sendNotification(ctx, it.to, it.subject, it.txt, log)
		if err == nil {
			_, _ = s.pool.Exec(db, `UPDATE outbox SET status = 'sent', sent_at = now(), error = '', claimed_at = NULL, attempts = attempts + 1 WHERE id = $1`, it.id)
			continue
		}
		log.Warn().Err(err).Str("to", it.to).Msg("notification failed")
		var attempts int
		_ = s.pool.QueryRow(db, `UPDATE outbox SET attempts = attempts + 1, error = $2 WHERE id = $1 RETURNING attempts`, it.id, describeMailError(err)).Scan(&attempts)
		if failed, wait := afterFailure(attempts); failed {
			_, _ = s.pool.Exec(db, `UPDATE outbox SET status = 'failed', claimed_at = NULL WHERE id = $1`, it.id)
		} else {
			_, _ = s.pool.Exec(db, `UPDATE outbox SET status = 'queued', claimed_at = NULL, next_attempt_at = now() + $2::interval WHERE id = $1`,
				it.id, fmt.Sprintf("%d seconds", int(wait.Seconds())))
		}
	}
	_, _ = s.pool.Exec(db, `UPDATE outbox SET status = 'queued', claimed_at = NULL WHERE status = 'sending' AND claimed_at < now() - interval '15 minutes'`)
	_, _ = s.pool.Exec(db, `DELETE FROM outbox WHERE status = 'sent' AND sent_at < now() - interval '30 days'`)
}

// sendNotification uses the module SMTP server (Den's or SMTP_HOST), or the
// Google shared account when there is none.
func (s *Service) sendNotification(ctx context.Context, to, subject, text string, log zerolog.Logger) error {
	from, err := mail.ParseAddress(s.notifySender())
	if err != nil {
		return displayError{msg: "NOTIFY_FROM (or Den's smtp.from) is not a valid address"}
	}
	raw, err := buildMessage(outgoing{
		FromName: from.Name, From: from.Address, To: []string{to}, Subject: subject,
		MessageID: fmt.Sprintf("<desk.notify.%s@%s>", uuid.NewString(), domainOf(from.Address)),
		Text:      text, HTML: textToHTML(text),
		Extra: map[string]string{"Auto-Submitted": "auto-generated"},
	})
	if err != nil {
		return err
	}
	cfg, err := s.moduleSMTP(ctx)
	if err != nil {
		return err
	}
	if cfg.Host != "" {
		return s.sendSMTP(ctx, cfg, from.Address, []string{to}, raw, log)
	}
	if s.google != nil && s.google.SharedMailbox != "" {
		_, err := s.google.Provider.Send(ctx, s.google.SharedMailbox, raw)
		return err
	}
	return displayError{msg: "no way to send notifications: set SMTP_HOST or GOOGLE_SHARED_MAILBOX"}
}

// adminEmails are the addresses of Desk admins who have signed in.
func (s *Service) adminEmails(ctx context.Context, orgID uuid.UUID) ([]string, error) {
	_ = orgID // Desk admins are module-wide; kept for when admins become per organization.
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT email FROM users WHERE desk_admin AND COALESCE(email, '') <> '' ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if rows.Scan(&e) == nil {
			out = append(out, e)
		}
	}
	return out, rows.Err()
}

func formatDate(t time.Time) string { return t.UTC().Format("Monday 2 January 2006, 15:04 UTC") }
