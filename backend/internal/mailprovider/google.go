// Google is a mailprovider.Provider on top of the shared Workspace client
// (shared/go/gworkspace): a service account with domain-wide delegation.
//
// Labels, untrash and send-as aliases go through that client. Creating or
// deleting a send-as alias requests gworkspace.SendAsScopes (gmail.modify
// plus gworkspace.SendAsScope, gmail.settings.sharing). A duplicate alias
// and a missing alias are success: creating and suspending a group mailbox
// can be repeated. Directory user-alias APIs are not used. A google_group
// mailbox is a Workspace group whose only member is the shared account,
// plus a send-as alias on that account (docs/adr/0001).
package mailprovider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/AGlElectronics/desk/backend/internal/gworkspace"
)

// Google implements Provider with the shared Workspace client.
type Google struct {
	ws     *gworkspace.Client
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
	return &Google{ws: client, labels: map[string]string{}}, nil
}

// NewGoogleForTest points the shared client at base (an httptest server).
// hc authorises every request; tests ignore it.
func NewGoogleForTest(domain, admin, base string, hc gworkspace.HTTPClientFunc) (*Google, error) {
	cfg := gworkspace.Config{CredentialsFile: "unused", AdminEmail: admin, Domain: domain}
	client, err := gworkspace.New(cfg, gworkspace.Options{DirectoryURL: base, GmailURL: base, HTTPClient: hc})
	if err != nil {
		return nil, err
	}
	return &Google{ws: client, labels: map[string]string{}}, nil
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
		return g.ensureSendAs(ctx, m.DeliverTo, m.Address, name)
	default:
		return fmt.Errorf("unknown mailbox kind %q", m.Kind)
	}
}

func (g *Google) SuspendMailbox(ctx context.Context, m Mailbox) error {
	if m.Kind == KindGroup {
		if err := g.ws.RemoveMember(ctx, m.Address, m.DeliverTo); err != nil && !gworkspace.IsNotFound(err) {
			return mapGoogle(err)
		}
		return g.deleteSendAs(ctx, m.DeliverTo, m.Address)
	}
	_, err := g.ws.SetSuspended(ctx, m.Address, true)
	return mapGoogle(err)
}

func (g *Google) RestoreMailbox(ctx context.Context, m Mailbox) error {
	if m.Kind == KindGroup {
		if _, err := g.ws.AddMember(ctx, m.Address, gworkspace.Member{Email: m.DeliverTo, Role: gworkspace.RoleMember}); err != nil && !gworkspace.IsConflict(err) {
			return mapGoogle(err)
		}
		return g.ensureSendAs(ctx, m.DeliverTo, m.Address, m.DisplayName)
	}
	_, err := g.ws.SetSuspended(ctx, m.Address, false)
	return mapGoogle(err)
}

func (g *Google) DeleteMailbox(ctx context.Context, m Mailbox) error {
	if m.Kind == KindGroup {
		_ = g.deleteSendAs(ctx, m.DeliverTo, m.Address)
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
	_, err := g.ws.UntrashMessage(ctx, user, id)
	return mapGoogle(err)
}

func (g *Google) labelID(ctx context.Context, user, name string) (string, error) {
	key := strings.ToLower(user) + "\n" + name
	g.mu.Lock()
	if id, ok := g.labels[key]; ok {
		g.mu.Unlock()
		return id, nil
	}
	g.mu.Unlock()
	listed, err := g.ws.ListLabels(ctx, user)
	if err != nil {
		return "", mapGoogle(err)
	}
	for _, l := range listed {
		if l.Name == name {
			g.remember(key, l.ID)
			return l.ID, nil
		}
	}
	created, err := g.ws.CreateLabel(ctx, user, name)
	if err != nil {
		return "", mapGoogle(err)
	}
	if created == nil || created.ID == "" {
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

// ensureSendAs lets account send as alias. An alias that already exists is
// success, so creating or restoring a group mailbox can be repeated.
func (g *Google) ensureSendAs(ctx context.Context, account, alias, display string) error {
	_, err := g.ws.CreateSendAs(ctx, account, alias, display)
	if gworkspace.IsConflict(err) {
		return nil
	}
	if st, ok := statusOf(err); ok && st == http.StatusForbidden {
		return ErrSendAsDenied
	}
	return mapGoogle(err)
}

// deleteSendAs removes alias from account. An alias that is already gone is
// success, so suspending or deleting a group mailbox can be repeated.
func (g *Google) deleteSendAs(ctx context.Context, account, alias string) error {
	err := g.ws.DeleteSendAs(ctx, account, alias)
	if gworkspace.IsNotFound(err) {
		return nil
	}
	return mapGoogle(err)
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
