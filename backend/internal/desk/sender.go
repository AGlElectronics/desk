package desk

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/desk/backend/internal/netguard"
)

// retryDelays is the wait after each failed attempt; after the last one the
// reply is marked failed and waits for "Retry" in the UI.
var retryDelays = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}

// staleClaim is how long a 'sending' claim may last before the reply goes
// back to the queue (the process died mid-send).
const staleClaim = 15 * time.Minute

// afterFailure decides what happens to a reply after its n-th failed
// attempt: wait and try again, or give up (failed, retry by hand).
func afterFailure(attempts int) (failed bool, wait time.Duration) {
	if attempts > len(retryDelays) {
		return true, 0
	}
	if attempts < 1 {
		attempts = 1
	}
	return false, retryDelays[attempts-1]
}

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
			s.sendOutbox(ctx, log)
		}
	}
}

// claimDue marks up to limit due replies as 'sending' and returns them.
// SKIP LOCKED lets overlapping runs (or a second replica by mistake) take
// different rows instead of the same ones.
func (s *Service) claimDue(ctx context.Context, limit int) ([]uuid.UUID, error) {
	if _, err := s.pool.Exec(ctx, `
		UPDATE threads SET send_status = 'queued', claimed_at = NULL
		 WHERE send_status = 'sending' AND claimed_at < now() - $1::interval`,
		fmt.Sprintf("%d seconds", int(staleClaim.Seconds()))); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		UPDATE threads SET send_status = 'sending', claimed_at = now()
		 WHERE id IN (SELECT id FROM threads
		               WHERE send_status = 'queued' AND next_attempt_at <= now()
		               ORDER BY next_attempt_at LIMIT $1 FOR UPDATE SKIP LOCKED)
		RETURNING id`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Service) sendDue(ctx context.Context, log zerolog.Logger) {
	ids, err := s.claimDue(ctx, 20)
	if err != nil {
		if ctx.Err() == nil {
			log.Error().Err(err).Msg("claim queued replies")
		}
		return
	}
	for i, id := range ids {
		if ctx.Err() != nil {
			// Not attempted: give the rest back.
			_, _ = s.pool.Exec(context.WithoutCancel(ctx), `
				UPDATE threads SET send_status = 'queued', claimed_at = NULL WHERE id = ANY($1) AND send_status = 'sending'`, ids[i:])
			return
		}
		s.sendOne(ctx, id, log)
	}
}

func (s *Service) sendOne(ctx context.Context, id uuid.UUID, log zerolog.Logger) {
	providerID, err := s.sendReply(ctx, id, log)
	// Record the outcome even when shutdown cancelled ctx meanwhile.
	db := context.WithoutCancel(ctx)
	if err == nil {
		_, _ = s.pool.Exec(db, `
			UPDATE threads SET send_status = 'sent', send_error = '', send_attempts = send_attempts + 1,
			       claimed_at = NULL, provider_message_id = $2 WHERE id = $1`, id, providerID)
		return
	}
	log.Warn().Err(err).Str("thread", id.String()).Msg("send failed")
	var attempts int
	_ = s.pool.QueryRow(db, `UPDATE threads SET send_attempts = send_attempts + 1, send_error = $2 WHERE id = $1 RETURNING send_attempts`,
		id, describeMailError(err)).Scan(&attempts)
	if failed, wait := afterFailure(attempts); failed {
		_, _ = s.pool.Exec(db, `UPDATE threads SET send_status = 'failed', next_attempt_at = NULL, claimed_at = NULL WHERE id = $1`, id)
	} else {
		_, _ = s.pool.Exec(db, `UPDATE threads SET send_status = 'queued', claimed_at = NULL, next_attempt_at = now() + $2::interval WHERE id = $1`,
			id, fmt.Sprintf("%d seconds", int(wait.Seconds())))
	}
}

// sendReply builds and delivers one outgoing thread (reply, forward or
// auto-reply). It returns the provider's message id when there is one.
func (s *Service) sendReply(ctx context.Context, threadID uuid.UUID, log zerolog.Logger) (string, error) {
	var (
		convID            uuid.UUID
		kind              string
		from, text, htmlB string
		msgID, authorName string
		to, cc            []string
		forwardOf         *uuid.UUID
	)
	if err := s.pool.QueryRow(ctx, `
		SELECT t.conversation_id, t.kind, t.from_email, t.to_emails, t.cc_emails, t.body_text, t.body_html, t.message_id,
		       COALESCE(u.display_name, ''), t.forward_of
		  FROM threads t LEFT JOIN users u ON u.id = t.author_id WHERE t.id = $1`, threadID,
	).Scan(&convID, &kind, &from, &to, &cc, &text, &htmlB, &msgID, &authorName, &forwardOf); err != nil {
		return "", err
	}
	c, err := s.GetConversation(ctx, convID)
	if err != nil {
		return "", err
	}
	mb, err := s.GetMailbox(ctx, c.MailboxID)
	if err != nil {
		return "", err
	}
	if mb.DeletedAt != nil {
		return "", displayError{msg: "the mailbox is in the trash"}
	}

	out := outgoing{
		From: mb.Email, To: to, CC: cc, MessageID: "<" + msgID + ">", Text: text, HTML: htmlB,
	}
	out.FromName = mb.Name
	if authorName != "" {
		out.FromName = authorName + " (" + mb.Name + ")"
	}
	switch kind {
	case "forward":
		out.Subject = "Fwd: " + c.Subject
		if forwardOf != nil {
			atts, err := s.loadAttachments(ctx, *forwardOf)
			if err != nil {
				return "", err
			}
			out.Attachments = atts
		}
	default:
		// Every earlier message in the conversation, oldest first, so the
		// customer's client threads the reply and their answer comes back here.
		refs, inReplyTo, err := s.references(ctx, convID, threadID)
		if err != nil {
			return "", err
		}
		out.References, out.InReplyTo = refs, inReplyTo
		out.Subject = replySubject(c.Subject, c.Number, len(refs) > 0)
		if kind == "auto_reply" {
			out.FromName = mb.Name
			out.Extra = map[string]string{"Auto-Submitted": "auto-replied", "X-Auto-Response-Suppress": "All"}
			if mb.autoReply.Subject != "" {
				out.Subject = replySubject(mb.autoReply.Subject, c.Number, false)
			}
		}
	}
	raw, err := buildMessage(out)
	if err != nil {
		return "", err
	}
	return s.deliver(ctx, mb, append(append([]string{}, to...), cc...), raw, log)
}

func (s *Service) references(ctx context.Context, convID, threadID uuid.UUID) ([]string, string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT message_id, kind FROM threads
		 WHERE conversation_id = $1 AND message_id <> '' AND id <> $2
		   AND (kind = 'customer' OR (kind = 'reply' AND send_status = 'sent'))
		 ORDER BY created_at`, convID, threadID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var refs []string
	var inReplyTo string
	for rows.Next() {
		var id, kind string
		if rows.Scan(&id, &kind) == nil {
			refs = append(refs, "<"+id+">")
			if kind == "customer" {
				inReplyTo = "<" + id + ">"
			}
		}
	}
	return refs, inReplyTo, rows.Err()
}

// replySubject adds "Re:" to an answer and the "[#n]" tag Desk threads by.
func replySubject(subject string, number int, isAnswer bool) string {
	if isAnswer && !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	if !subjectNumber.MatchString(subject) {
		subject += fmt.Sprintf(" [#%d]", number)
	}
	return subject
}

func (s *Service) loadAttachments(ctx context.Context, threadID uuid.UUID) ([]outAttachment, error) {
	rows, err := s.pool.Query(ctx, `SELECT filename, content_type, storage_key FROM attachments WHERE thread_id = $1 ORDER BY created_at`, threadID)
	if err != nil {
		return nil, err
	}
	type ref struct{ name, ct, key string }
	var refs []ref
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.name, &r.ct, &r.key); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, r)
	}
	rows.Close()
	var out []outAttachment
	for _, r := range refs {
		body, _, err := s.blobs.Get(ctx, r.key)
		if err != nil {
			return nil, fmt.Errorf("load attachment %s: %w", r.name, err)
		}
		data, err := io.ReadAll(body)
		_ = body.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, outAttachment{Filename: r.name, ContentType: r.ct, Data: data})
	}
	return out, nil
}

// transport sends a finished message for a mailbox and returns the
// provider's message id.
type transport func(ctx context.Context, mb Mailbox, raw []byte) (string, error)

// transportFor returns the provider transport of a mailbox, or nil for SMTP.
func (s *Service) transportFor(mb Mailbox) transport {
	if mb.IsGoogle() {
		return s.googleTransport
	}
	return nil
}

// deliver hands a finished message to the mailbox's transport.
func (s *Service) deliver(ctx context.Context, mb Mailbox, rcpts []string, raw []byte, log zerolog.Logger) (string, error) {
	if t := s.transportFor(mb); t != nil {
		return t(ctx, mb, raw)
	}
	cfg, err := s.smtpFor(mb)
	if err != nil {
		return "", err
	}
	return "", s.sendSMTP(ctx, cfg, mb.Email, rcpts, raw, log)
}

// sendSMTP sends raw over SMTP. Once the server has accepted DATA the
// message is out: a failing QUIT afterwards is logged, not retried, so the
// customer never gets the same reply twice.
func (s *Service) sendSMTP(ctx context.Context, cfg SMTPSettings, from string, rcpts []string, raw []byte, log zerolog.Logger) error {
	cl, err := s.dialSMTP(ctx, cfg)
	if err != nil {
		return err
	}
	defer cl.Close()
	if err := cl.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, rcpt := range rcpts {
		if err := cl.Rcpt(rcpt); err != nil {
			return displayError{msg: "the server refused recipient " + rcpt, err: err}
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
	if err := cl.Quit(); err != nil {
		log.Warn().Err(err).Msg("QUIT failed after the message was accepted; treating it as sent")
	}
	return nil
}

// smtpFor returns the mailbox's SMTP settings, or the module-wide ones.
func (s *Service) smtpFor(mb Mailbox) (SMTPSettings, error) {
	if mb.SMTPHost != "" {
		pw, err := s.decrypt(mb.ID, "smtp_password", mb.smtpPasswordEnc)
		if err != nil {
			return SMTPSettings{}, displayError{msg: err.Error()}
		}
		return SMTPSettings{Host: mb.SMTPHost, Port: mb.SMTPPort, Security: mb.SMTPSecurity, Username: mb.SMTPUsername, Password: pw}, nil
	}
	if s.smtp.Host == "" {
		return SMTPSettings{}, displayError{msg: "no SMTP server: set one on the mailbox or SMTP_HOST for the module"}
	}
	cfg := s.smtp
	cfg.moduleWide = true
	return cfg, nil
}

func (cfg SMTPSettings) label() string {
	if cfg.moduleWide {
		return "module SMTP server (SMTP_HOST)"
	}
	return net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
}

// dialSMTP connects and logs in. Mailbox-specific hosts go through the
// network guard; the module-wide SMTP_HOST is set by the operator and is
// trusted.
func (s *Service) dialSMTP(ctx context.Context, cfg SMTPSettings) (*smtp.Client, error) {
	if cfg.Security == "none" && !s.dev {
		return nil, displayError{msg: "plaintext SMTP is only allowed in development; choose TLS or STARTTLS"}
	}
	port := strconv.Itoa(cfg.Port)
	tlsCfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	guard := s.guard
	if cfg.moduleWide {
		guard = netguard.New([]string{cfg.Host})
	}
	var conn net.Conn
	var err error
	if cfg.Security == "tls" {
		conn, err = guard.DialTLS(dctx, cfg.Host, port, dialTimeout, tlsCfg)
	} else {
		conn, err = guard.Dial(dctx, cfg.Host, port, dialTimeout)
	}
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
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
			return nil, displayError{msg: "the server does not offer STARTTLS"}
		}
		if err := cl.StartTLS(tlsCfg); err != nil {
			cl.Close()
			return nil, fmt.Errorf("starttls: %w", err)
		}
	}
	if cfg.Username != "" {
		if err := cl.Auth(plainAuth{username: cfg.Username, password: cfg.Password, allowPlaintext: cfg.Security == "none"}); err != nil {
			cl.Close()
			return nil, fmt.Errorf("%w (%v)", errLogin, err)
		}
	}
	return cl, nil
}

// plainAuth is SMTP AUTH PLAIN. net/smtp's PlainAuth refuses plaintext
// connections except to localhost; Desk allows them only with
// SMTP_SECURITY=none, which config limits to development.
type plainAuth struct {
	username, password string
	allowPlaintext     bool
}

func (a plainAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !a.allowPlaintext {
		return "", nil, errors.New("refusing to send the password over an unencrypted connection")
	}
	return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
}

func (a plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected server challenge")
	}
	return nil, nil
}

type outAttachment struct {
	Filename, ContentType string
	Data                  []byte
}

type outgoing struct {
	FromName, From string
	To, CC         []string
	Subject        string
	MessageID      string
	InReplyTo      string
	References     []string
	Text, HTML     string
	Attachments    []outAttachment
	// Extra headers (Auto-Submitted on auto-replies).
	Extra map[string]string
}

// buildMessage renders multipart/alternative (text and HTML), wrapped in
// multipart/mixed when there are attachments.
func buildMessage(m outgoing) ([]byte, error) {
	var buf bytes.Buffer
	hdr := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	hdr("From", (&mail.Address{Name: m.FromName, Address: m.From}).String())
	hdr("To", strings.Join(m.To, ", "))
	if len(m.CC) > 0 {
		hdr("Cc", strings.Join(m.CC, ", "))
	}
	hdr("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	hdr("Date", time.Now().Format(time.RFC1123Z))
	hdr("Message-ID", m.MessageID)
	if m.InReplyTo != "" {
		hdr("In-Reply-To", m.InReplyTo)
	}
	if len(m.References) > 0 {
		hdr("References", strings.Join(m.References, " "))
	}
	for _, k := range sortedKeys(m.Extra) {
		hdr(k, m.Extra[k])
	}
	hdr("MIME-Version", "1.0")

	alt := func(w *multipart.Writer) error {
		for _, part := range []struct{ ct, body string }{
			{"text/plain; charset=utf-8", m.Text},
			{"text/html; charset=utf-8", "<!doctype html><html><body>" + m.HTML + "</body></html>"},
		} {
			h := textproto.MIMEHeader{}
			h.Set("Content-Type", part.ct)
			h.Set("Content-Transfer-Encoding", "quoted-printable")
			pw, err := w.CreatePart(h)
			if err != nil {
				return err
			}
			qp := quotedprintable.NewWriter(pw)
			if _, err := qp.Write([]byte(part.body)); err != nil {
				return err
			}
			if err := qp.Close(); err != nil {
				return err
			}
		}
		return w.Close()
	}

	if len(m.Attachments) == 0 {
		mw := multipart.NewWriter(&buf)
		hdr("Content-Type", "multipart/alternative; boundary="+mw.Boundary())
		buf.WriteString("\r\n")
		if err := alt(mw); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	mixed := multipart.NewWriter(&buf)
	hdr("Content-Type", "multipart/mixed; boundary="+mixed.Boundary())
	buf.WriteString("\r\n")
	var inner bytes.Buffer
	aw := multipart.NewWriter(&inner)
	h := textproto.MIMEHeader{}
	h.Set("Content-Type", "multipart/alternative; boundary="+aw.Boundary())
	pw, err := mixed.CreatePart(h)
	if err != nil {
		return nil, err
	}
	if err := alt(aw); err != nil {
		return nil, err
	}
	if _, err := pw.Write(inner.Bytes()); err != nil {
		return nil, err
	}
	for _, a := range m.Attachments {
		h := textproto.MIMEHeader{}
		ct := a.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		h.Set("Content-Type", ct)
		h.Set("Content-Transfer-Encoding", "base64")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Filename}))
		pw, err := mixed.CreatePart(h)
		if err != nil {
			return nil, err
		}
		enc := base64.StdEncoding.EncodeToString(a.Data)
		for len(enc) > 76 {
			fmt.Fprintf(pw, "%s\r\n", enc[:76])
			enc = enc[76:]
		}
		fmt.Fprintf(pw, "%s\r\n", enc)
	}
	if err := mixed.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sortedKeys(m map[string]string) []string {
	return slices.Sorted(maps.Keys(m))
}
