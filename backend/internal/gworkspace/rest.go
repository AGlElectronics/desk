// Code generated from con-services shared/go/gworkspace; DO NOT EDIT. Run scripts/sync-shared.

package gworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// APIError is an error answer from Google.
type APIError struct {
	Status  int
	Message string
	Reason  string
}

func (e *APIError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("google workspace: %d %s (%s)", e.Status, e.Message, e.Reason)
	}
	return fmt.Sprintf("google workspace: %d %s", e.Status, e.Message)
}

// IsNotFound reports a 404 from Google (or the Fake).
func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// IsConflict reports a 409 (for example a user or member that already exists).
func IsConflict(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusConflict
}

func (c *Client) do(ctx context.Context, subject string, scopes []string, method, rawURL string, in, out any) error {
	hc, err := c.clientFor(ctx, subject, scopes)
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("google workspace: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var e struct {
			Error struct {
				Message string `json:"message"`
				Errors  []struct {
					Reason string `json:"reason"`
				} `json:"errors"`
			} `json:"error"`
		}
		ae := &APIError{Status: res.StatusCode, Message: res.Status}
		if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
			ae.Message = e.Error.Message
			if len(e.Error.Errors) > 0 {
				ae.Reason = e.Error.Errors[0].Reason
			}
		}
		return ae
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) dir(ctx context.Context, method, path string, q url.Values, in, out any) error {
	u := c.directoryURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return c.do(ctx, c.cfg.AdminEmail, DirectoryScopes, method, u, in, out)
}

func (c *Client) gmail(ctx context.Context, mailbox, method, path string, q url.Values, in, out any) error {
	if err := c.checkDomain("mailbox", mailbox); err != nil {
		return err
	}
	u := c.gmailURL + "/users/me" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return c.do(ctx, mailbox, GmailScopes, method, u, in, out)
}

func esc(s string) string { return url.PathEscape(s) }
