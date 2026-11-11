// Code generated from con-services shared/go/gworkspace; DO NOT EDIT. Run scripts/sync-shared.

package gworkspace

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Format is how much of a message GetMessage returns.
type Format string

const (
	FormatMinimal  Format = "minimal"
	FormatMetadata Format = "metadata"
	FormatFull     Format = "full"
	FormatRaw      Format = "raw"
)

// ListQuery filters ListMessages.
type ListQuery struct {
	// Q is a Gmail search such as "in:inbox is:unread newer_than:7d".
	Q        string
	LabelIDs []string
	// MaxResults per page (Google's default 100, maximum 500).
	MaxResults int
	PageToken  string
}

// MessageRef is a message id in a list.
type MessageRef struct {
	ID       string `json:"id"`
	ThreadID string `json:"threadId"`
}

// MessageList is one page of ListMessages.
type MessageList struct {
	Messages           []MessageRef `json:"messages"`
	NextPageToken      string       `json:"nextPageToken,omitempty"`
	ResultSizeEstimate int          `json:"resultSizeEstimate"`
}

// Header is a message header.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// MessagePart is a MIME part (FormatFull).
type MessagePart struct {
	PartID   string   `json:"partId,omitempty"`
	MimeType string   `json:"mimeType"`
	Filename string   `json:"filename,omitempty"`
	Headers  []Header `json:"headers,omitempty"`
	Body     struct {
		AttachmentID string `json:"attachmentId,omitempty"`
		Size         int    `json:"size"`
		// Data is base64url; use DecodeBody.
		Data string `json:"data,omitempty"`
	} `json:"body"`
	Parts []MessagePart `json:"parts,omitempty"`
}

// Header returns the first header called name (case-insensitive).
func (p *MessagePart) Header(name string) string {
	if p == nil {
		return ""
	}
	for _, h := range p.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// DecodeBody decodes Body.Data.
func (p *MessagePart) DecodeBody() ([]byte, error) {
	return decodeB64URL(p.Body.Data)
}

// Message is a Gmail message.
type Message struct {
	ID           string       `json:"id"`
	ThreadID     string       `json:"threadId"`
	LabelIDs     []string     `json:"labelIds,omitempty"`
	Snippet      string       `json:"snippet,omitempty"`
	InternalDate string       `json:"internalDate,omitempty"`
	SizeEstimate int          `json:"sizeEstimate,omitempty"`
	Payload      *MessagePart `json:"payload,omitempty"`
	// Raw is the RFC 5322 message, base64url (FormatRaw); use RawBytes.
	Raw string `json:"raw,omitempty"`
}

// RawBytes decodes Raw.
func (m *Message) RawBytes() ([]byte, error) { return decodeB64URL(m.Raw) }

func decodeB64URL(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "=")); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

// ListMessages lists one page of messages in mailbox.
func (c *Client) ListMessages(ctx context.Context, mailbox string, lq ListQuery) (*MessageList, error) {
	q := url.Values{}
	if lq.Q != "" {
		q.Set("q", lq.Q)
	}
	for _, l := range lq.LabelIDs {
		q.Add("labelIds", l)
	}
	if lq.MaxResults > 0 {
		q.Set("maxResults", strconv.Itoa(lq.MaxResults))
	}
	if lq.PageToken != "" {
		q.Set("pageToken", lq.PageToken)
	}
	var out MessageList
	if err := c.gmail(ctx, mailbox, http.MethodGet, "/messages", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetMessage loads a message; format "" means FormatFull.
func (c *Client) GetMessage(ctx context.Context, mailbox, id string, format Format) (*Message, error) {
	if format == "" {
		format = FormatFull
	}
	var m Message
	if err := c.gmail(ctx, mailbox, http.MethodGet, "/messages/"+esc(id), url.Values{"format": {string(format)}}, nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// SendMessage sends raw (a complete RFC 5322 message, From: mailbox) as
// mailbox. threadID, when set, files it in that thread.
func (c *Client) SendMessage(ctx context.Context, mailbox string, raw []byte, threadID string) (*Message, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("google workspace: empty message")
	}
	body := map[string]string{"raw": base64.RawURLEncoding.EncodeToString(raw)}
	if threadID != "" {
		body["threadId"] = threadID
	}
	var m Message
	if err := c.gmail(ctx, mailbox, http.MethodPost, "/messages/send", nil, body, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ModifyLabels adds and removes label IDs ("UNREAD", "INBOX", "Label_12", …).
func (c *Client) ModifyLabels(ctx context.Context, mailbox, id string, add, remove []string) (*Message, error) {
	body := map[string][]string{"addLabelIds": nonNil(add), "removeLabelIds": nonNil(remove)}
	var m Message
	if err := c.gmail(ctx, mailbox, http.MethodPost, "/messages/"+esc(id)+"/modify", nil, body, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// TrashMessage moves a message to the trash (Gmail deletes it after 30 days).
func (c *Client) TrashMessage(ctx context.Context, mailbox, id string) (*Message, error) {
	var m Message
	if err := c.gmail(ctx, mailbox, http.MethodPost, "/messages/"+esc(id)+"/trash", nil, nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Label is a Gmail label (users.labels). System labels such as INBOX have
// Type "system"; labels this client creates have Type "user".
type Label struct {
	ID                    string `json:"id,omitempty"`
	Name                  string `json:"name"`
	Type                  string `json:"type,omitempty"`
	MessageListVisibility string `json:"messageListVisibility,omitempty"`
	LabelListVisibility   string `json:"labelListVisibility,omitempty"`
}

// ListLabels returns the mailbox's labels, system and user.
func (c *Client) ListLabels(ctx context.Context, mailbox string) ([]Label, error) {
	var out struct {
		Labels []Label `json:"labels"`
	}
	if err := c.gmail(ctx, mailbox, http.MethodGet, "/labels", nil, nil, &out); err != nil {
		return nil, err
	}
	if out.Labels == nil {
		out.Labels = []Label{}
	}
	return out.Labels, nil
}

// CreateLabel creates a user label called name. The label is shown in the
// label list and on messages (labelShow / show), which is what Desk asks
// Google for. An existing name comes back as a conflict (IsConflict).
func (c *Client) CreateLabel(ctx context.Context, mailbox, name string) (*Label, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("google workspace: empty label name")
	}
	body := Label{Name: name, LabelListVisibility: "labelShow", MessageListVisibility: "show"}
	var out Label
	if err := c.gmail(ctx, mailbox, http.MethodPost, "/labels", nil, &body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UntrashMessage moves a message out of the trash.
func (c *Client) UntrashMessage(ctx context.Context, mailbox, id string) (*Message, error) {
	var m Message
	if err := c.gmail(ctx, mailbox, http.MethodPost, "/messages/"+esc(id)+"/untrash", nil, nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// SendAs is a Gmail send-as alias (users.settings.sendAs).
type SendAs struct {
	SendAsEmail        string `json:"sendAsEmail"`
	DisplayName        string `json:"displayName,omitempty"`
	TreatAsAlias       bool   `json:"treatAsAlias"`
	IsPrimary          bool   `json:"isPrimary,omitempty"`
	VerificationStatus string `json:"verificationStatus,omitempty"`
}

// CreateSendAs adds alias as an address mailbox may send as, with the
// display name shown to recipients. TreatAsAlias is always true (a group
// mailbox's address on the shared account, which is how Desk uses it).
// The alias must be in the Workspace domain. An alias that already exists
// is a conflict (IsConflict). This call requests SendAsScopes.
func (c *Client) CreateSendAs(ctx context.Context, mailbox, alias, displayName string) (*SendAs, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil, fmt.Errorf("google workspace: empty send-as address")
	}
	if err := c.checkDomain("send-as", alias); err != nil {
		return nil, err
	}
	body := &SendAs{SendAsEmail: alias, DisplayName: strings.TrimSpace(displayName), TreatAsAlias: true}
	var out SendAs
	if err := c.gmailWith(ctx, mailbox, SendAsScopes(), http.MethodPost, "/settings/sendAs", nil, body, &out); err != nil {
		return nil, err
	}
	if out.SendAsEmail == "" {
		out = *body
	}
	return &out, nil
}

// DeleteSendAs removes a send-as alias from mailbox. A missing alias is
// not found (IsNotFound). This call requests SendAsScopes.
func (c *Client) DeleteSendAs(ctx context.Context, mailbox, alias string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return fmt.Errorf("google workspace: empty send-as address")
	}
	if err := c.checkDomain("send-as", alias); err != nil {
		return err
	}
	return c.gmailWith(ctx, mailbox, SendAsScopes(), http.MethodDelete, "/settings/sendAs/"+esc(alias), nil, nil, nil)
}
