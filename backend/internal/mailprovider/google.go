// Google is a mailprovider.Provider on top of the shared Workspace client
// (shared/go/gworkspace): a service account with domain-wide delegation.
//
// Calls the shared client does not have are made here, against the same
// Gmail REST API, and reported as shared-package gaps (do not add them in
// Desk's copy of internal/gworkspace):
//
//   - list and create a Gmail label by name (users.labels)
//   - untrash a message (users.messages.untrash)
//   - create and delete a send-as alias (users.settings.sendAs), which
//     needs the scope gmail.settings.sharing in addition to gmail.modify
//
// Directory user-alias APIs are not used. A google_group mailbox is a
// Workspace group whose only member is the shared account, plus a send-as
// alias on that account (docs/adr/0001).
package mailprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/AGlElectronics/desk/backend/internal/gworkspace"
)

// SendAsScope is the extra OAuth scope a send-as alias needs. Authorise it
// for the service account next to gworkspace.GmailScopes.
const SendAsScope = "https://www.googleapis.com/auth/gmail.settings.sharing"

// Google implements Provider with the shared Workspace client.
type Google struct {
	ws     *gworkspace.Client
	gap    *gapAPI
	mu     sync.Mutex
	labels map[string]string // mailbox+"\n"+name -> label id
}

// NewGoogle reads cfg.CredentialsFile and talks to Google. Never call this
// in tests; use NewGoogleForTest with an httptest server.
func NewGoogle(cfg gworkspace.Config) (*Google, error) {
	client, err := gworkspace.New(cfg, gworkspace.Options{})
	if err != nil {
		return nil, err
	}
	key, err := os.ReadFile(cfg.CredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", gworkspace.EnvCredentialsFile, err)
	}
	hc, err := gworkspace.ServiceAccount(key)
	if err != nil {
		return nil, err
	}
	return newGoogle(client, hc, gworkspace.DefaultGmailURL, cfg.Domain), nil
}

// NewGoogleForTest points both the shared client and Desk's extra calls at
// base (an httptest server). hc authorises every request; tests ignore it.
func NewGoogleForTest(domain, admin, base string, hc gworkspace.HTTPClientFunc) (*Google, error) {
	cfg := gworkspace.Config{CredentialsFile: "unused", AdminEmail: admin, Domain: domain}
	client, err := gworkspace.New(cfg, gworkspace.Options{DirectoryURL: base, GmailURL: base, HTTPClient: hc})
	if err != nil {
		return nil, err
	}
	return newGoogle(client, hc, base, domain), nil
}

func newGoogle(client *gworkspace.Client, hc gworkspace.HTTPClientFunc, gmailURL, domain string) *Google {
	return &Google{
		ws: client, labels: map[string]string{},
		gap: &gapAPI{http: hc, gmailURL: strings.TrimRight(gmailURL, "/"), domain: domain},
	}
}

func (g *Google) CreateMailbox(ctx context.Context, m Mailbox) error {
	switch m.Kind {
	case KindUser:
		given, family := splitName(m.DisplayName, m.Address)
		_, err := g.ws.CreateUser(ctx, gworkspace.NewUser{
			PrimaryEmail: m.Address, GivenName: given, FamilyName: family,
		})
		return mapGoogle(err)
	case KindGroup:
		if m.DeliverTo == "" {
			return fmt.Errorf("a group mailbox needs the shared account it delivers to")
		}
		name := strings.TrimSpace(m.DisplayName)
		if name == "" {
			name = m.Address
		}
		if _, err := g.ws.CreateGroup(ctx, gworkspace.Group{
			Email: m.Address, Name: name, Description: "Desk mailbox",
		}); err != nil {
			return mapGoogle(err)
		}
		if _, err := g.ws.AddMember(ctx, m.Address, gworkspace.Member{Email: m.DeliverTo, Role: gworkspace.RoleMember}); err != nil && !gworkspace.IsConflict(err) {
			return mapGoogle(err)
		}
		return g.gap.ensureSendAs(ctx, m.DeliverTo, m.Address, name)
	default:
		return fmt.Errorf("unknown mailbox kind %q", m.Kind)
	}
}

func (g *Google) SuspendMailbox(ctx context.Context, m Mailbox) error {
	if m.Kind == KindGroup {
		if err := g.ws.RemoveMember(ctx, m.Address, m.DeliverTo); err != nil && !gworkspace.IsNotFound(err) {
			return mapGoogle(err)
		}
		return g.gap.deleteSendAs(ctx, m.DeliverTo, m.Address)
	}
	_, err := g.ws.SetSuspended(ctx, m.Address, true)
	return mapGoogle(err)
}

func (g *Google) RestoreMailbox(ctx context.Context, m Mailbox) error {
	if m.Kind == KindGroup {
		if _, err := g.ws.AddMember(ctx, m.Address, gworkspace.Member{Email: m.DeliverTo, Role: gworkspace.RoleMember}); err != nil && !gworkspace.IsConflict(err) {
			return mapGoogle(err)
		}
		return g.gap.ensureSendAs(ctx, m.DeliverTo, m.Address, m.DisplayName)
	}
	_, err := g.ws.SetSuspended(ctx, m.Address, false)
	return mapGoogle(err)
}

func (g *Google) DeleteMailbox(ctx context.Context, m Mailbox) error {
	if m.Kind == KindGroup {
		_ = g.gap.deleteSendAs(ctx, m.DeliverTo, m.Address)
		return mapGoogle(g.ws.DeleteGroup(ctx, m.Address))
	}
	return mapGoogle(g.ws.DeleteUser(ctx, m.Address))
}

func (g *Google) ListUnhandled(ctx context.Context, user, label string, max int) ([]string, error) {
	if max < 1 {
		max = 1
	}
	if max > 500 {
		max = 500
	}
	// Google returns the newest page first. Reverse it so the oldest of
	// that page is imported first.
	list, err := g.ws.ListMessages(ctx, user, gworkspace.ListQuery{
		Q: "in:inbox -label:" + quoteLabel(label), MaxResults: max,
	})
	if err != nil {
		return nil, mapGoogle(err)
	}
	ids := make([]string, 0, len(list.Messages))
	for _, m := range list.Messages {
		ids = append(ids, m.ID)
	}
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	return ids, nil
}

func (g *Google) Get(ctx context.Context, user, id string) ([]byte, error) {
	msg, err := g.ws.GetMessage(ctx, user, id, gworkspace.FormatRaw)
	if err != nil {
		return nil, mapGoogle(err)
	}
	raw, err := msg.RawBytes()
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func (g *Google) AddLabel(ctx context.Context, user, id, label string) error {
	lid, err := g.labelID(ctx, user, label)
	if err != nil {
		return err
	}
	_, err = g.ws.ModifyLabels(ctx, user, id, []string{lid}, nil)
	return mapGoogle(err)
}

func (g *Google) Send(ctx context.Context, user string, raw []byte) (string, error) {
	msg, err := g.ws.SendMessage(ctx, user, raw, "")
	if err != nil {
		if st, ok := statusOf(err); ok && st == http.StatusForbidden {
			return "", ErrSendAsDenied
		}
		return "", mapGoogle(err)
	}
	return msg.ID, nil
}

func (g *Google) Trash(ctx context.Context, user, id string) error {
	_, err := g.ws.TrashMessage(ctx, user, id)
	return mapGoogle(err)
}

func (g *Google) Untrash(ctx context.Context, user, id string) error {
	return g.gap.call(ctx, user, gworkspace.GmailScopes, http.MethodPost, "/messages/"+id+"/untrash", nil, nil)
}

func (g *Google) labelID(ctx context.Context, user, name string) (string, error) {
	key := strings.ToLower(user) + "\n" + name
	g.mu.Lock()
	if id, ok := g.labels[key]; ok {
		g.mu.Unlock()
		return id, nil
	}
	g.mu.Unlock()
	var listed struct {
		Labels []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := g.gap.call(ctx, user, gworkspace.GmailScopes, http.MethodGet, "/labels", nil, &listed); err != nil {
		return "", err
	}
	for _, l := range listed.Labels {
		if l.Name == name {
			g.remember(key, l.ID)
			return l.ID, nil
		}
	}
	var created struct {
		ID string `json:"id"`
	}
	body := map[string]string{"name": name, "labelListVisibility": "labelShow", "messageListVisibility": "show"}
	if err := g.gap.call(ctx, user, gworkspace.GmailScopes, http.MethodPost, "/labels", body, &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", fmt.Errorf("google workspace: created label %q without an id", name)
	}
	g.remember(key, created.ID)
	return created.ID, nil
}

func (g *Google) remember(key, id string) {
	g.mu.Lock()
	g.labels[key] = id
	g.mu.Unlock()
}

func splitName(display, email string) (given, family string) {
	display = strings.TrimSpace(display)
	if display == "" {
		display = email
		if i := strings.IndexByte(display, '@'); i > 0 {
			display = display[:i]
		}
	}
	parts := strings.Fields(display)
	switch len(parts) {
	case 0:
		return "Mailbox", "Desk"
	case 1:
		return parts[0], "Mailbox"
	default:
		return parts[0], strings.Join(parts[1:], " ")
	}
}

func quoteLabel(name string) string {
	if name == "" || strings.ContainsAny(name, " \"") {
		return `"` + strings.ReplaceAll(name, `"`, "") + `"`
	}
	return name
}

func mapGoogle(err error) error {
	switch {
	case err == nil:
		return nil
	case gworkspace.IsNotFound(err):
		return ErrNotFound
	case gworkspace.IsConflict(err):
		return ErrExists
	default:
		return err
	}
}

func statusOf(err error) (int, bool) {
	var ae *gworkspace.APIError
	if !errors.As(err, &ae) {
		return 0, false
	}
	return ae.Status, true
}

type gapAPI struct {
	http     gworkspace.HTTPClientFunc
	gmailURL string
	domain   string
}

func (g *gapAPI) sendAsScopes() []string {
	s := make([]string, 0, len(gworkspace.GmailScopes)+1)
	s = append(s, gworkspace.GmailScopes...)
	s = append(s, SendAsScope)
	return s
}

func (g *gapAPI) ensureSendAs(ctx context.Context, account, alias, display string) error {
	body := map[string]any{"sendAsEmail": alias, "displayName": display, "treatAsAlias": true}
	err := g.call(ctx, account, g.sendAsScopes(), http.MethodPost, "/settings/sendAs", body, nil)
	if st, ok := statusOf(err); ok && st == http.StatusConflict {
		return nil
	}
	if st, ok := statusOf(err); ok && st == http.StatusForbidden {
		return ErrSendAsDenied
	}
	return err
}

func (g *gapAPI) deleteSendAs(ctx context.Context, account, alias string) error {
	path := "/settings/sendAs/" + strings.ReplaceAll(alias, "@", "%40")
	err := g.call(ctx, account, g.sendAsScopes(), http.MethodDelete, path, nil, nil)
	if st, ok := statusOf(err); ok && st == http.StatusNotFound {
		return nil
	}
	return err
}

func (g *gapAPI) call(ctx context.Context, mailbox string, scopes []string, method, path string, in, out any) error {
	if !inDomain(g.domain, mailbox) {
		return fmt.Errorf("google workspace: mailbox %q is not in the domain %s", mailbox, g.domain)
	}
	hc, err := g.http(ctx, mailbox, scopes)
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
	req, err := http.NewRequestWithContext(ctx, method, g.gmailURL+"/users/me"+path, body)
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
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		ae := &gworkspace.APIError{Status: res.StatusCode, Message: res.Status}
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
			ae.Message = e.Error.Message
		}
		return ae
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func inDomain(domain, email string) bool {
	_, d, ok := strings.Cut(strings.ToLower(strings.TrimSpace(email)), "@")
	return ok && d != "" && d == strings.ToLower(domain)
}
