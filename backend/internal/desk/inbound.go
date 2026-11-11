package desk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // non-UTF-8 bodies and headers
	"github.com/emersion/go-message/mail"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/microcosm-cc/bluemonday"

	"github.com/AGlElectronics/desk/backend/internal/domain"
)

// maxAttachmentBytes caps one stored attachment; larger ones are listed
// in the thread text but not kept.
const maxAttachmentBytes = 20 << 20

// maxMessageBytes caps a whole message; larger ones are recorded as import
// failures and not downloaded.
const maxMessageBytes = 50 << 20

var (
	htmlPolicy  = bluemonday.UGCPolicy()
	stripPolicy = bluemonday.StrictPolicy()
	// "[#123]" in a subject: Desk adds it to every outgoing subject.
	subjectNumber = regexp.MustCompile(`\[#(\d+)\]`)
)

// ParsedAttachment is one attachment or inline part.
type ParsedAttachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// ParsedMessage is the subset of an email Desk stores.
type ParsedMessage struct {
	MessageID   string
	References  []string // In-Reply-To first, then References
	FromEmail   string
	FromName    string
	To          []string
	CC          []string
	Subject     string
	Text        string
	HTML        string
	Attachments []ParsedAttachment
	Skipped     []string // attachments too large to keep
}

// ParseMessage reads a raw RFC 5322 message.
func ParseMessage(raw []byte) (*ParsedMessage, error) {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		return nil, err
	}
	h := mr.Header
	p := &ParsedMessage{}
	p.Subject, _ = h.Subject()
	p.MessageID, _ = h.MessageID()
	if p.MessageID == "" {
		sum := sha256.Sum256(raw)
		p.MessageID = "desk.import." + hex.EncodeToString(sum[:16])
	}
	inReplyTo, _ := h.MsgIDList("In-Reply-To")
	refs, _ := h.MsgIDList("References")
	p.References = append(inReplyTo, refs...)
	if from, _ := h.AddressList("From"); len(from) > 0 {
		p.FromEmail = strings.ToLower(from[0].Address)
		p.FromName = from[0].Name
	}
	if to, _ := h.AddressList("To"); len(to) > 0 {
		for _, a := range to {
			p.To = append(p.To, strings.ToLower(a.Address))
		}
	}
	if cc, _ := h.AddressList("Cc"); len(cc) > 0 {
		for _, a := range cc {
			p.CC = append(p.CC, strings.ToLower(a.Address))
		}
	}

	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if message.IsUnknownCharset(err) {
				continue
			}
			break
		}
		switch ph := part.Header.(type) {
		case *mail.InlineHeader:
			ct, params, _ := ph.ContentType()
			body, _ := io.ReadAll(io.LimitReader(part.Body, maxAttachmentBytes+1))
			switch {
			case ct == "text/plain" && p.Text == "":
				p.Text = string(body)
			case ct == "text/html" && p.HTML == "":
				p.HTML = string(body)
			case strings.HasPrefix(ct, "text/"):
				// Further text parts (e.g. forwarded bodies) are appended.
				p.Text += "\n\n" + string(body)
			default:
				name := params["name"]
				if name == "" {
					name = "inline"
				}
				p.addAttachment(name, ct, body)
			}
		case *mail.AttachmentHeader:
			name, _ := ph.Filename()
			ct, _, _ := ph.ContentType()
			if name == "" {
				name = "attachment"
			}
			body, _ := io.ReadAll(io.LimitReader(part.Body, maxAttachmentBytes+1))
			p.addAttachment(name, ct, body)
		}
	}
	return p, nil
}

func (p *ParsedMessage) addAttachment(name, ct string, body []byte) {
	if len(body) > maxAttachmentBytes {
		p.Skipped = append(p.Skipped, name)
		return
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	p.Attachments = append(p.Attachments, ParsedAttachment{Filename: name, ContentType: ct, Data: body})
}

// bodies returns sanitised HTML and plain text for storage.
func (p *ParsedMessage) bodies() (string, string) {
	text := strings.TrimSpace(p.Text)
	var htmlBody string
	if strings.TrimSpace(p.HTML) != "" {
		htmlBody = htmlPolicy.Sanitize(p.HTML)
		if text == "" {
			text = strings.TrimSpace(html.UnescapeString(stripPolicy.Sanitize(p.HTML)))
		}
	} else {
		htmlBody = textToHTML(text)
	}
	if len(p.Skipped) > 0 {
		note := "[Desk: attachments too large to keep: " + strings.Join(p.Skipped, ", ") + "]"
		text += "\n\n" + note
		htmlBody += "<p><em>" + html.EscapeString(note) + "</em></p>"
	}
	return htmlBody, text
}

// ImportResult says what happened to one inbound message.
type ImportResult struct {
	ConversationID uuid.UUID
	Created        bool
	Duplicate      bool
	Skipped        string
}

// Import stores one inbound message in mb: threads it into an existing
// conversation (In-Reply-To / References, then "[#123]" in the subject) or
// opens a new one. Re-importing the same Message-ID does nothing.
func (s *Service) Import(ctx context.Context, mb Mailbox, p *ParsedMessage, raw []byte) (ImportResult, error) {
	if p.FromEmail == "" {
		return ImportResult{Skipped: "no sender"}, nil
	}
	var own bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM mailboxes WHERE email = $1)`, p.FromEmail).Scan(&own); err != nil {
		return ImportResult{}, err
	}
	if own {
		return ImportResult{Skipped: "sent by a Desk mailbox"}, nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var dup bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM message_ids WHERE message_id = $1)`, p.MessageID).Scan(&dup); err != nil {
		return ImportResult{}, err
	}
	if dup {
		return ImportResult{Duplicate: true}, nil
	}

	convID, err := findConversation(ctx, tx, mb.ID, p)
	if err != nil {
		return ImportResult{}, err
	}
	customerID, err := upsertCustomer(ctx, tx, mb.OrganizationID, p.FromEmail, p.FromName)
	if err != nil {
		return ImportResult{}, err
	}
	res := ImportResult{}
	if convID == uuid.Nil {
		subject := strings.TrimSpace(p.Subject)
		if subject == "" {
			subject = "(no subject)"
		}
		convID, err = insertConversation(ctx, tx, mb.ID, customerID, subject, domain.StatusActive)
		if err != nil {
			return ImportResult{}, err
		}
		res.Created = true
	} else {
		// A customer answer reopens closed and pending conversations.
		if _, err := tx.Exec(ctx, `
			UPDATE conversations SET status = 'active', closed_at = NULL
			 WHERE id = $1 AND status IN ('closed', 'pending')`, convID); err != nil {
			return ImportResult{}, err
		}
	}
	res.ConversationID = convID

	htmlBody, text := p.bodies()
	threadID := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO threads (id, conversation_id, kind, from_email, to_emails, cc_emails, body_html, body_text, message_id)
		VALUES ($1, $2, 'customer', $3, $4, $5, $6, $7, $8)`,
		threadID, convID, p.FromEmail, nonNil(p.To), nonNil(p.CC), htmlBody, text, p.MessageID); err != nil {
		return ImportResult{}, err
	}
	for _, a := range p.Attachments {
		if _, err := tx.Exec(ctx, `
			INSERT INTO attachments (thread_id, filename, content_type, size_bytes, content)
			VALUES ($1, $2, $3, $4, $5)`, threadID, a.Filename, a.ContentType, len(a.Data), a.Data); err != nil {
			return ImportResult{}, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO message_ids (message_id, conversation_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		p.MessageID, convID); err != nil {
		return ImportResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE conversations SET last_activity_at = now(), preview = $2 WHERE id = $1`,
		convID, previewOf(text)); err != nil {
		return ImportResult{}, err
	}
	return res, tx.Commit(ctx)
}

func findConversation(ctx context.Context, tx pgx.Tx, mailboxID uuid.UUID, p *ParsedMessage) (uuid.UUID, error) {
	if len(p.References) > 0 {
		var id uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT m.conversation_id FROM message_ids m JOIN conversations c ON c.id = m.conversation_id
			 WHERE m.message_id = ANY($1) AND c.mailbox_id = $2 LIMIT 1`, p.References, mailboxID).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, err
		}
	}
	if m := subjectNumber.FindStringSubmatch(p.Subject); m != nil {
		n, _ := strconv.Atoi(m[1])
		var id uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM conversations WHERE mailbox_id = $1 AND number = $2`, mailboxID, n).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, err
		}
	}
	return uuid.Nil, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
