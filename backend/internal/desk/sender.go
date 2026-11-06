package desk

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// retryDelays is the wait after each failed attempt; after the last one the
// reply is marked failed and waits for "Retry" in the UI.
var retryDelays = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}

// RunSender sends queued replies until ctx is done.
func (s *Service) RunSender(ctx context.Context, logger zerolog.Logger) {
	log := logger.With().Str("component", "sender").Logger()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sendDue(ctx, log)
		}
	}
}

func (s *Service) sendDue(ctx context.Context, log zerolog.Logger) {
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM threads WHERE send_status = 'queued' AND next_attempt_at <= now()
		 ORDER BY next_attempt_at LIMIT 20`)
	if err != nil {
		log.Error().Err(err).Msg("list queued replies")
		return
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		err := s.sendReply(ctx, id)
		if err == nil {
			_, _ = s.pool.Exec(ctx, `UPDATE threads SET send_status = 'sent', send_error = '', send_attempts = send_attempts + 1 WHERE id = $1`, id)
			continue
		}
		log.Warn().Err(err).Str("thread", id.String()).Msg("send failed")
		var attempts int
		_ = s.pool.QueryRow(ctx, `UPDATE threads SET send_attempts = send_attempts + 1, send_error = $2 WHERE id = $1 RETURNING send_attempts`,
			id, err.Error()).Scan(&attempts)
		if attempts > len(retryDelays) {
			_, _ = s.pool.Exec(ctx, `UPDATE threads SET send_status = 'failed', next_attempt_at = NULL WHERE id = $1`, id)
		} else {
			_, _ = s.pool.Exec(ctx, `UPDATE threads SET next_attempt_at = now() + $2::interval WHERE id = $1`,
				id, fmt.Sprintf("%d seconds", int(retryDelays[attempts-1].Seconds())))
		}
	}
}

func (s *Service) sendReply(ctx context.Context, threadID uuid.UUID) error {
	var (
		convID            uuid.UUID
		from, text, htmlB string
		msgID, authorName string
		to                []string
	)
	if err := s.pool.QueryRow(ctx, `
		SELECT t.conversation_id, t.from_email, t.to_emails, t.body_text, t.body_html, t.message_id, COALESCE(u.display_name, '')
		  FROM threads t LEFT JOIN users u ON u.id = t.author_id WHERE t.id = $1`, threadID,
	).Scan(&convID, &from, &to, &text, &htmlB, &msgID, &authorName); err != nil {
		return err
	}
	c, err := s.GetConversation(ctx, convID)
	if err != nil {
		return err
	}
	mb, err := s.GetMailbox(ctx, c.MailboxID)
	if err != nil {
		return err
	}
	cfg, err := s.smtpFor(mb)
	if err != nil {
		return err
	}

	// Every earlier message in the conversation, oldest first, so the
	// customer's client threads the reply and their answer comes back here.
	var refs []string
	var inReplyTo string
	rows, err := s.pool.Query(ctx, `
		SELECT message_id, kind FROM threads
		 WHERE conversation_id = $1 AND message_id <> '' AND id <> $2 AND (kind = 'customer' OR send_status = 'sent')
		 ORDER BY created_at`, convID, threadID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, kind string
		if rows.Scan(&id, &kind) == nil {
			refs = append(refs, "<"+id+">")
			if kind == "customer" {
				inReplyTo = "<" + id + ">"
			}
		}
	}
	rows.Close()

	subject := c.Subject
	if len(refs) > 0 && !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	if !subjectNumber.MatchString(subject) {
		subject += fmt.Sprintf(" [#%d]", c.Number)
	}

	fromName := mb.Name
	if authorName != "" {
		fromName = authorName + " (" + mb.Name + ")"
	}
	raw, err := buildMessage(outgoing{
		FromName: fromName, From: mb.Email, To: to, Subject: subject,
		MessageID: "<" + msgID + ">", InReplyTo: inReplyTo, References: refs,
		Text: text, HTML: htmlB,
	})
	if err != nil {
		return err
	}
	cl, err := dialSMTP(cfg)
	if err != nil {
		return err
	}
	defer cl.Close()
	if err := cl.Mail(mb.Email); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, rcpt := range to {
		if err := cl.Rcpt(rcpt); err != nil {
			return fmt.Errorf("RCPT TO %s: %w", rcpt, err)
		}
	}
	w, err := cl.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	return cl.Quit()
}

// smtpFor returns the mailbox's SMTP settings, or the module-wide ones.
func (s *Service) smtpFor(mb Mailbox) (SMTPSettings, error) {
	if mb.SMTPHost != "" {
		pw, err := s.decrypt(mb.smtpPasswordEnc)
		if err != nil {
			return SMTPSettings{}, err
		}
		return SMTPSettings{Host: mb.SMTPHost, Port: mb.SMTPPort, Security: mb.SMTPSecurity, Username: mb.SMTPUsername, Password: pw}, nil
	}
	if s.smtp.Host == "" {
		return SMTPSettings{}, errors.New("no SMTP server: set one on the mailbox or SMTP_HOST for the module")
	}
	return s.smtp, nil
}

func dialSMTP(cfg SMTPSettings) (*smtp.Client, error) {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	tlsCfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Timeout: dialTimeout}
	var conn net.Conn
	var err error
	if cfg.Security == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(sessionTimeout))
	cl, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if cfg.Security == "starttls" {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			cl.Close()
			return nil, errors.New("server does not offer STARTTLS")
		}
		if err := cl.StartTLS(tlsCfg); err != nil {
			cl.Close()
			return nil, fmt.Errorf("starttls: %w", err)
		}
	}
	if cfg.Username != "" {
		if err := cl.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			cl.Close()
			return nil, fmt.Errorf("login as %s: %w", cfg.Username, err)
		}
	}
	return cl, nil
}

type outgoing struct {
	FromName, From string
	To             []string
	Subject        string
	MessageID      string
	InReplyTo      string
	References     []string
	Text, HTML     string
}

// buildMessage renders a multipart/alternative message.
func buildMessage(m outgoing) ([]byte, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	hdr := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	hdr("From", mime.QEncoding.Encode("utf-8", m.FromName)+" <"+m.From+">")
	hdr("To", strings.Join(m.To, ", "))
	hdr("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	hdr("Date", time.Now().Format(time.RFC1123Z))
	hdr("Message-ID", m.MessageID)
	if m.InReplyTo != "" {
		hdr("In-Reply-To", m.InReplyTo)
	}
	if len(m.References) > 0 {
		hdr("References", strings.Join(m.References, " "))
	}
	hdr("MIME-Version", "1.0")
	hdr("Content-Type", "multipart/alternative; boundary="+mw.Boundary())
	buf.WriteString("\r\n")

	for _, part := range []struct{ ct, body string }{
		{"text/plain; charset=utf-8", m.Text},
		{"text/html; charset=utf-8", "<!doctype html><html><body>" + m.HTML + "</body></html>"},
	} {
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", part.ct)
		h.Set("Content-Transfer-Encoding", "quoted-printable")
		pw, err := mw.CreatePart(h)
		if err != nil {
			return nil, err
		}
		qp := quotedprintable.NewWriter(pw)
		if _, err := qp.Write([]byte(part.body)); err != nil {
			return nil, err
		}
		if err := qp.Close(); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
