package desk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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

const (
	// maxAttachmentBytes caps one stored attachment; larger ones are listed
	// in the thread text but not kept.
	maxAttachmentBytes = 20 << 20
	// maxAttachmentsBytes caps all attachments of one message together.
	maxAttachmentsBytes = 40 << 20
	// maxMessageBytes caps a whole message; larger ones are recorded as
	// import failures and not downloaded.
	maxMessageBytes = 50 << 20
	// maxParts caps the MIME parts read from one message; the rest is
	// ignored and noted.
	maxParts = 100
	// maxAttachments caps the attachments kept from one message.
	maxAttachments = 50
)

var (
	htmlPolicy  = newHTMLPolicy()
	stripPolicy = bluemonday.StrictPolicy()
	// "[#123]" in a subject: Desk adds it to every outgoing subject.
	subjectNumber = regexp.MustCompile(`\[#(\d+)\]`)
	// cid: references in HTML (src="cid:...", also unquoted).
	cidRef = regexp.MustCompile(`(?i)\bcid:([^"'\s>)]+)`)
)

// newHTMLPolicy sanitises inbound HTML. Links open in a new tab without
// access to Desk (noopener) and without sending Desk's address as the
// referrer.
func newHTMLPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.RequireNoFollowOnLinks(true)
	p.RequireNoReferrerOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	return p
}

// ParsedAttachment is one attachment or inline part.
type ParsedAttachment struct {
	Filename    string
	ContentType string
	ContentID   string
	Inline      bool
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
	DeliveredTo []string // Delivered-To and X-Original-To, for routing
	Subject     string
	Text        string
	HTML        string
	Attachments []ParsedAttachment
	Skipped     []string // attachments too large to keep
	// AutoReply is set for out-of-office and other automatic answers.
	AutoReply bool
	// Bounce is set for delivery failure reports.
	Bounce bool
	// List is set for mailing list traffic (List-Id).
	List bool
	// Truncated is set when the message had more parts than Desk reads.
	Truncated bool
	// ProviderID is the message's id at the mail provider (Gmail), set by
	// the fetcher.
	ProviderID string
}

// Automatic reports whether the message was sent by a machine (auto-reply
// or bounce): it may join a conversation but never opens or reopens one,
// and Desk never auto-replies to it.
func (p *ParsedMessage) Automatic() bool { return p.AutoReply || p.Bounce }

// ParseMessage reads a raw RFC 5322 message.
func ParseMessage(raw []byte) (*ParsedMessage, error) {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		return nil, err
	}
	h := mr.Header
	p := &ParsedMessage{}
	subject, _ := h.Subject()
	p.Subject = clean(subject)
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
		p.FromName = clean(from[0].Name)
	}
	p.To = addresses(h, "To")
	p.CC = addresses(h, "Cc")
	for _, k := range []string{"Delivered-To", "X-Original-To", "X-Forwarded-To"} {
		for _, v := range h.Values(k) {
			if v = strings.ToLower(strings.Trim(strings.TrimSpace(v), "<>")); v != "" {
				p.DeliveredTo = append(p.DeliveredTo, v)
			}
		}
	}
	classify(p, h)

	parts := 0
	attachBytes := 0
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
		if parts++; parts > maxParts {
			p.Truncated = true
			break
		}
		switch ph := part.Header.(type) {
		case *mail.InlineHeader:
			ct, params, _ := ph.ContentType()
			body, _ := io.ReadAll(io.LimitReader(part.Body, maxAttachmentBytes+1))
			switch {
			case ct == "text/plain" && p.Text == "":
				p.Text = clean(string(body))
			case ct == "text/html" && p.HTML == "":
				p.HTML = clean(string(body))
			case ct == "message/delivery-status":
				p.Bounce = true
				p.Text += "\n\n" + clean(string(body))
			case strings.HasPrefix(ct, "text/"):
				// Further text parts (e.g. forwarded bodies) are appended.
				p.Text += "\n\n" + clean(string(body))
			default:
				name := params["name"]
				if name == "" {
					name = "inline"
				}
				p.addAttachment(&attachBytes, ParsedAttachment{
					Filename: clean(name), ContentType: ct, Inline: true,
					ContentID: contentID(ph.Get("Content-Id")), Data: body,
				})
			}
		case *mail.AttachmentHeader:
			name, _ := ph.Filename()
			ct, _, _ := ph.ContentType()
			if name == "" {
				name = "attachment"
			}
			body, _ := io.ReadAll(io.LimitReader(part.Body, maxAttachmentBytes+1))
			p.addAttachment(&attachBytes, ParsedAttachment{
				Filename: clean(name), ContentType: ct, ContentID: contentID(ph.Get("Content-Id")), Data: body,
			})
		}
	}
	return p, nil
}

// classify spots automatic mail (RFC 3834 Auto-Submitted and the common
// vendor headers) and delivery failure reports (RFC 3464).
func classify(p *ParsedMessage, h mail.Header) {
	if v := strings.ToLower(strings.TrimSpace(h.Get("Auto-Submitted"))); v != "" && v != "no" {
		p.AutoReply = true
	}
	switch strings.ToLower(strings.TrimSpace(h.Get("Precedence"))) {
	case "bulk", "junk", "auto_reply":
		p.AutoReply = true
	case "list":
		p.List = true
	}
	for _, k := range []string{"X-Autoreply", "X-Autorespond", "X-Auto-Response-Suppress"} {
		if h.Get(k) != "" {
			p.AutoReply = true
		}
	}
	if h.Get("List-Id") != "" || h.Get("List-Unsubscribe") != "" {
		p.List = true
	}
	if ct, params, _ := h.ContentType(); ct == "multipart/report" && strings.EqualFold(params["report-type"], "delivery-status") {
		p.Bounce = true
	}
	local := p.FromEmail
	if i := strings.IndexByte(local, '@'); i >= 0 {
		local = local[:i]
	}
	switch local {
	case "mailer-daemon", "postmaster":
		p.Bounce = true
	}
	if strings.TrimSpace(h.Get("Return-Path")) == "<>" {
		p.Bounce = true
	}
}

func addresses(h mail.Header, key string) []string {
	list, _ := h.AddressList(key)
	var out []string
	for _, a := range list {
		out = append(out, strings.ToLower(a.Address))
	}
	return out
}

func contentID(v string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(v), "<>"))
}

// clean makes text storable: valid UTF-8 without NUL bytes (PostgreSQL
// rejects both, which used to stall a mailbox on one bad message).
func clean(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	return strings.ReplaceAll(s, "\x00", "")
}

func (p *ParsedMessage) addAttachment(total *int, a ParsedAttachment) {
	switch {
	case len(a.Data) > maxAttachmentBytes:
		p.Skipped = append(p.Skipped, a.Filename+" (over 20 MB)")
		return
	case *total+len(a.Data) > maxAttachmentsBytes:
		p.Skipped = append(p.Skipped, a.Filename+" (attachments over 40 MB in total)")
		return
	case len(p.Attachments) >= maxAttachments:
		p.Skipped = append(p.Skipped, a.Filename+" (more than 50 attachments)")
		return
	}
	if a.ContentType == "" {
		a.ContentType = "application/octet-stream"
	}
	*total += len(a.Data)
	p.Attachments = append(p.Attachments, a)
}

// bodies returns sanitised HTML and plain text for storage. cids maps
// Content-IDs to stored attachment ids; matching cid: references become
// attachment URLs, which the web app loads with the user's token.
func (p *ParsedMessage) bodies(cids map[string]uuid.UUID) (string, string) {
	text := strings.TrimSpace(p.Text)
	var htmlBody string
	if strings.TrimSpace(p.HTML) != "" {
		src := cidRef.ReplaceAllStringFunc(p.HTML, func(m string) string {
			id, ok := cids[strings.ToLower(m[4:])]
			if !ok {
				return m
			}
			return AttachmentURL(id)
		})
		htmlBody = htmlPolicy.Sanitize(src)
		if text == "" {
			text = strings.TrimSpace(html.UnescapeString(stripPolicy.Sanitize(p.HTML)))
		}
	} else {
		htmlBody = textToHTML(text)
	}
	var notes []string
	if len(p.Skipped) > 0 {
		notes = append(notes, "attachments not kept: "+strings.Join(p.Skipped, ", "))
	}
	if p.Truncated {
		notes = append(notes, fmt.Sprintf("the message has more than %d parts; the rest was not read", maxParts))
	}
	for _, n := range notes {
		note := "[Desk: " + n + "]"
		text += "\n\n" + note
		htmlBody += "<p><em>" + html.EscapeString(note) + "</em></p>"
	}
	return htmlBody, text
}

// AttachmentURL is the API path of one attachment.
func AttachmentURL(id uuid.UUID) string { return "/api/v1/attachments/" + id.String() }

// ImportResult says what happened to one inbound message.
type ImportResult struct {
	ConversationID uuid.UUID
	ThreadID       uuid.UUID
	Created        bool
	Duplicate      bool
	Skipped        string
}

// Import stores one inbound message in mb: threads it into an existing
// conversation (In-Reply-To / References, then "[#123]" in the subject when
// the sender is that conversation's customer) or opens a new one.
// Re-importing the same Message-ID does nothing. Automatic mail (bounces,
// auto-replies) only joins an existing conversation, without reopening it.
// raw is kept in object storage next to the attachments.
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
	var dup bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM message_ids WHERE message_id = $1)`, p.MessageID).Scan(&dup); err != nil {
		return ImportResult{}, err
	}
	if dup {
		return ImportResult{Duplicate: true}, nil
	}

	// Files first: a failed upload stops the run (and the message is tried
	// again next time) instead of leaving rows without content.
	threadID := uuid.New()
	type stored struct {
		id  uuid.UUID
		key string
		a   ParsedAttachment
	}
	var files []stored
	var keys []string
	cleanup := func() {
		for _, k := range keys {
			_ = s.blobs.Delete(context.WithoutCancel(ctx), k)
		}
	}
	cids := map[string]uuid.UUID{}
	for _, a := range p.Attachments {
		f := stored{id: uuid.New(), a: a}
		f.key = fmt.Sprintf("attachments/%s/%s/%s", mb.ID, threadID, f.id)
		if err := s.blobs.Put(ctx, f.key, a.Data, a.ContentType); err != nil {
			cleanup()
			return ImportResult{}, fmt.Errorf("store attachment: %w", err)
		}
		keys = append(keys, f.key)
		files = append(files, f)
		if a.ContentID != "" {
			cids[strings.ToLower(a.ContentID)] = f.id
		}
	}
	rawKey := ""
	if len(raw) > 0 {
		rawKey = fmt.Sprintf("raw/%s/%s.eml", mb.ID, threadID)
		if err := s.blobs.Put(ctx, rawKey, raw, "message/rfc822"); err != nil {
			cleanup()
			return ImportResult{}, fmt.Errorf("store raw message: %w", err)
		}
		keys = append(keys, rawKey)
	}

	res, err := s.importTx(ctx, mb, p, threadID, rawKey, int64(len(raw)), cids, func(tx pgx.Tx) error {
		for _, f := range files {
			if _, err := tx.Exec(ctx, `
				INSERT INTO attachments (id, thread_id, filename, content_type, size_bytes, storage_key, content_id, inline)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				f.id, threadID, f.a.Filename, f.a.ContentType, len(f.a.Data), f.key, f.a.ContentID, f.a.Inline); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil || res.Skipped != "" || res.Duplicate {
		cleanup()
	}
	return res, err
}

func (s *Service) importTx(ctx context.Context, mb Mailbox, p *ParsedMessage, threadID uuid.UUID, rawKey string, rawSize int64,
	cids map[string]uuid.UUID, attach func(pgx.Tx) error) (ImportResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	convID, err := findConversation(ctx, tx, mb.ID, p)
	if err != nil {
		return ImportResult{}, err
	}
	res := ImportResult{ThreadID: threadID}
	auto := p.Automatic()
	switch {
	case convID == uuid.Nil && auto:
		return ImportResult{Skipped: "automatic message (auto-reply or bounce) without a conversation"}, nil
	case convID == uuid.Nil:
		customerID, err := upsertCustomer(ctx, tx, mb.OrganizationID, p.FromEmail, p.FromName)
		if err != nil {
			return ImportResult{}, err
		}
		subject := strings.TrimSpace(p.Subject)
		if subject == "" {
			subject = "(no subject)"
		}
		convID, err = insertConversation(ctx, tx, mb.ID, customerID, subject, domain.StatusActive)
		if err != nil {
			return ImportResult{}, err
		}
		res.Created = true
	case !auto:
		// A customer answer reopens closed and pending conversations.
		if _, err := tx.Exec(ctx, `
			UPDATE conversations SET status = 'active', closed_at = NULL
			 WHERE id = $1 AND status IN ('closed', 'pending')`, convID); err != nil {
			return ImportResult{}, err
		}
	}
	res.ConversationID = convID

	htmlBody, text := p.bodies(cids)
	if _, err := tx.Exec(ctx, `
		INSERT INTO threads (id, conversation_id, kind, from_email, to_emails, cc_emails, body_html, body_text,
		                     message_id, raw_key, raw_size_bytes, auto_submitted, provider_message_id)
		VALUES ($1, $2, 'customer', $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		threadID, convID, p.FromEmail, nonNil(p.To), nonNil(p.CC), htmlBody, text, p.MessageID,
		rawKey, rawSize, auto, p.ProviderID); err != nil {
		return ImportResult{}, err
	}
	if err := attach(tx); err != nil {
		return ImportResult{}, err
	}
	if res.Created {
		var number int
		if err := tx.QueryRow(ctx, `SELECT number FROM conversations WHERE id = $1`, convID).Scan(&number); err != nil {
			return ImportResult{}, err
		}
		reply, err := shouldAutoReply(ctx, tx, mb, p)
		if err != nil {
			return ImportResult{}, err
		}
		if reply {
			if err := queueAutoReply(ctx, tx, mb, convID, p.FromEmail); err != nil {
				return ImportResult{}, err
			}
		}
		if err := s.notifyNewConversation(ctx, tx, mb, convID, number, p.Subject, p.FromEmail); err != nil {
			return ImportResult{}, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO message_ids (message_id, conversation_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		p.MessageID, convID); err != nil {
		return ImportResult{}, err
	}
	if !auto {
		if _, err := tx.Exec(ctx, `UPDATE conversations SET last_activity_at = now(), preview = $2 WHERE id = $1`,
			convID, previewOf(text)); err != nil {
			return ImportResult{}, err
		}
	}
	return res, tx.Commit(ctx)
}

// findConversation threads by Message-ID first; a "[#n]" subject tag only
// counts when the sender is that conversation's customer, because the
// numbers are sequential and anyone could guess one.
func findConversation(ctx context.Context, tx pgx.Tx, mailboxID uuid.UUID, p *ParsedMessage) (uuid.UUID, error) {
	if len(p.References) > 0 {
		var id uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT m.conversation_id FROM message_ids m JOIN conversations c ON c.id = m.conversation_id
			 WHERE m.message_id = ANY($1) AND c.mailbox_id = $2 AND c.deleted_at IS NULL LIMIT 1`, p.References, mailboxID).Scan(&id)
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
		err := tx.QueryRow(ctx, `
			SELECT c.id FROM conversations c
			 WHERE c.mailbox_id = $1 AND c.number = $2 AND c.deleted_at IS NULL
			   AND EXISTS (SELECT 1 FROM customer_emails ce WHERE ce.customer_id = c.customer_id AND ce.email = $3)`,
			mailboxID, n, p.FromEmail).Scan(&id)
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
