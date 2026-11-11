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
