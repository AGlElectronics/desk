package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/desk/backend/internal/auth"
	"github.com/AGlElectronics/desk/backend/internal/blob"
	"github.com/AGlElectronics/desk/backend/internal/config"
	"github.com/AGlElectronics/desk/backend/internal/denlink"
	"github.com/AGlElectronics/desk/backend/internal/desk"
	"github.com/AGlElectronics/desk/backend/internal/secretbox"
	"github.com/AGlElectronics/desk/backend/internal/testdb"
)

type apiEnv struct {
	t     *testing.T
	srv   *httptest.Server
	svc   *desk.Service
	blobs *blob.Memory
}

func newAPI(t *testing.T) *apiEnv {
	t.Helper()
	pool := testdb.New(t)
	access, err := auth.LoadAccessConfig("../../../infra/access-config.toml")
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := secretbox.New("test-key")
	blobs := blob.NewMemory()
	svc := desk.NewService(pool, desk.Options{Keyring: keys, Development: true, Blobs: blobs})
	cfg := &config.Config{AuthMode: config.AuthModeDev, DevDefaultUser: "nobody", DenMode: denlink.ModeStandalone}
	den := denlink.New(pool, denlink.Config{Slug: "desk", Mode: denlink.ModeStandalone}, zerolog.Nop())
	srv := httptest.NewServer(NewRouter(Deps{Config: cfg, Pool: pool, Logger: zerolog.Nop(), Access: access, Den: den, Desk: svc}, "test"))
	t.Cleanup(srv.Close)
	return &apiEnv{t: t, srv: srv, svc: svc, blobs: blobs}
}

type who struct{ user, groups string }

var (
	admin   = who{"ada-admin", "desk-admin"}
	viewer  = who{"val-viewer", "desk-info-viewer"}
	manager = who{"max-manager", "/desk-info-manager"}
	other   = who{"mo-other-manager", "desk-info-manager"}
	deleter = who{"del-delete", "desk-info-delete"}
	outside = who{"olga-outside", "desk-press-viewer"}
	nobody  = who{"nina-nobody", ""}
)

func (e *apiEnv) call(w who, method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	req.Header.Set("X-Dev-User", w.user)
	req.Header.Set("X-Dev-Groups", w.groups)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func (e *apiEnv) expect(w who, method, path string, body any, want int) map[string]any {
	e.t.Helper()
	got, out := e.call(w, method, path, body)
	if got != want {
		e.t.Fatalf("%s %s %s as %s: status %d, want %d (%v)", method, path, mustJSON(body), w.user, got, want, out)
	}
	return out
}

func mustJSON(v any) string {
	if v == nil {
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func (e *apiEnv) importInto(mailboxID, from, subject string) string {
	e.t.Helper()
	ctx := context.Background()
	mb, err := e.svc.GetMailbox(ctx, uuid.MustParse(mailboxID))
	if err != nil {
		e.t.Fatal(err)
	}
	raw := []byte("From: " + from + "\r\nTo: " + mb.Email + "\r\nSubject: " + subject + "\r\nMessage-ID: <" + uuid.NewString() +
		"@example.com>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=B\r\n\r\n--B\r\nContent-Type: text/plain\r\n\r\nHello\r\n" +
		"--B\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"a.pdf\"\r\n\r\n%PDF-1.4\r\n--B--\r\n")
	p, err := desk.ParseMessage(raw)
	if err != nil {
		e.t.Fatal(err)
	}
	res, err := e.svc.Import(ctx, mb, p, raw)
	if err != nil {
		e.t.Fatal(err)
	}
	return res.ConversationID.String()
}

func TestAuthorizationMatrix(t *testing.T) {
	e := newAPI(t)
	// Everyone signs in once so Desk knows their groups.
	for _, w := range []who{admin, viewer, manager, other, deleter, outside, nobody} {
		e.expect(w, "GET", "/api/v1/me", nil, 200)
	}

	e.expect(nobody, "POST", "/api/v1/mailboxes", map[string]any{"name": "Info", "email": "info@desk.test"}, 403)
	e.expect(manager, "POST", "/api/v1/mailboxes", map[string]any{"name": "Info", "email": "info@desk.test"}, 403)
	info := e.expect(admin, "POST", "/api/v1/mailboxes", map[string]any{"name": "Info", "email": "info@desk.test", "imap_host": "imap.example.com"}, 201)
	press := e.expect(admin, "POST", "/api/v1/mailboxes", map[string]any{"name": "Press", "email": "press@desk.test"}, 201)
	infoID, pressID := info["id"].(string), press["id"].(string)
	if info["slug"] != "info" || info["imap_host"] != "imap.example.com" {
		t.Fatalf("admin view %v", info)
	}

	// Role catalog: three roles per mailbox with their groups.
	_, cat := e.call(nobody, "GET", "/api/role-catalog", nil)
	groups := map[string]string{}
	for _, r := range cat["roles"].([]any) {
		m := r.(map[string]any)
		groups[m["key"].(string)] = m["group"].(string)
	}
	for key, group := range map[string]string{
		"organization_admin": "desk-admin", "desk_info_viewer": "desk-info-viewer",
		"desk_info_manager": "desk-info-manager", "desk_info_delete": "desk-info-delete", "desk_press_viewer": "desk-press-viewer",
	} {
		if groups[key] != group {
			t.Errorf("catalog %s → %q, want %q", key, groups[key], group)
		}
	}
	if _, ok := groups["agent"]; ok {
		t.Error("the organization-wide agent role is gone")
	}

	infoConv := e.importInto(infoID, "alice@example.com", "Info question")
	pressConv := e.importInto(pressID, "bob@example.com", "Press question")

	// Viewer: sees info only, read-only, and no server settings.
	list := e.expect(viewer, "GET", "/api/v1/mailboxes", nil, 200)["mailboxes"].([]any)
	if len(list) != 1 {
		t.Fatalf("viewer sees %d mailboxes", len(list))
	}
	if mb := list[0].(map[string]any); mb["imap_host"] != nil || mb["last_fetch_error"] != nil || mb["slug"] != "info" {
		t.Fatalf("viewer must not see server settings: %v", mb)
	}
	e.expect(viewer, "GET", "/api/v1/mailboxes/"+pressID, nil, 404)
	e.expect(viewer, "GET", "/api/v1/conversations?mailbox="+pressID, nil, 404)
	convs := e.expect(viewer, "GET", "/api/v1/conversations?folder=all", nil, 200)["conversations"].([]any)
	if len(convs) != 1 || convs[0].(map[string]any)["id"] != infoConv {
		t.Fatalf("viewer list %v", convs)
	}
	detail := e.expect(viewer, "GET", "/api/v1/conversations/"+infoConv, nil, 200)
	e.expect(viewer, "GET", "/api/v1/conversations/"+pressConv, nil, 404)
	e.expect(viewer, "PATCH", "/api/v1/conversations/"+infoConv, map[string]any{"status": "closed"}, 403)
	e.expect(viewer, "POST", "/api/v1/conversations/"+infoConv+"/threads", map[string]any{"kind": "note", "body": "x"}, 403)
	e.expect(viewer, "DELETE", "/api/v1/conversations/"+infoConv, nil, 403)
	e.expect(viewer, "POST", "/api/v1/conversations/"+infoConv+"/viewing", map[string]any{"mode": "viewing"}, 204)
	e.expect(viewer, "PUT", "/api/v1/conversations/"+infoConv+"/star", nil, 204)
	e.expect(viewer, "POST", "/api/v1/conversations", map[string]any{"mailbox_id": infoID, "customer_email": "x@example.com", "subject": "s", "body": "b"}, 403)
	attID := detail["threads"].([]any)[0].(map[string]any)["attachments"].([]any)[0].(map[string]any)["id"].(string)
	e.expect(viewer, "GET", "/api/v1/attachments/"+attID, nil, 200)
	e.expect(outside, "GET", "/api/v1/attachments/"+attID, nil, 404)
	e.expect(nobody, "GET", "/api/v1/conversations/"+infoConv, nil, 404)

	// Manager: works info, can't touch connector settings or delete.
	reply := e.expect(manager, "POST", "/api/v1/conversations/"+infoConv+"/threads", map[string]any{"kind": "reply", "body_html": "<p>Hi</p>"}, 201)
	replyID := reply["id"].(string)
	e.expect(other, "POST", "/api/v1/threads/"+replyID+"/undo", nil, 403)
	e.expect(viewer, "POST", "/api/v1/threads/"+replyID+"/undo", nil, 403)
	e.expect(outside, "POST", "/api/v1/threads/"+replyID+"/undo", nil, 404)
	e.expect(manager, "POST", "/api/v1/threads/"+replyID+"/undo", nil, 204)
	e.expect(manager, "PATCH", "/api/v1/mailboxes/"+infoID, map[string]any{"signature": "-- Info team"}, 200)
	e.expect(manager, "PATCH", "/api/v1/mailboxes/"+infoID, map[string]any{"imap_host": "evil.example.net"}, 403)
	e.expect(manager, "DELETE", "/api/v1/mailboxes/"+infoID, nil, 403)
	e.expect(manager, "POST", "/api/v1/mailboxes/"+infoID+"/test", nil, 403)
	e.expect(manager, "DELETE", "/api/v1/conversations/"+infoConv, nil, 403)
	e.expect(manager, "PATCH", "/api/v1/conversations/"+infoConv, map[string]any{"tags": []string{"VIP", "hotel"}}, 200)

	// Assign only to people who can work the mailbox.
	var viewerID, managerID string
	people := e.expect(manager, "GET", "/api/v1/users?mailbox="+infoID, nil, 200)["people"].([]any)
	for _, p := range people {
		m := p.(map[string]any)
		switch m["display_name"] {
		case "Val Viewer":
			viewerID = m["id"].(string)
		case "Max Manager":
			managerID = m["id"].(string)
		}
	}
	if viewerID != "" || managerID == "" {
		t.Fatalf("assignee picker: %v", people)
	}
	_, me := e.call(viewer, "GET", "/api/v1/me", nil)
	e.expect(manager, "PATCH", "/api/v1/conversations/"+infoConv, map[string]any{"assignee_id": me["id"]}, 422)
	e.expect(manager, "PATCH", "/api/v1/conversations/"+infoConv, map[string]any{"assignee_id": managerID}, 200)

	// Customers: managers edit, viewers read, outsiders get 404.
	custID := detail["customer"].(map[string]any)["id"].(string)
	e.expect(viewer, "GET", "/api/v1/customers/"+custID, nil, 200)
	e.expect(viewer, "PATCH", "/api/v1/customers/"+custID, map[string]any{"notes": "x"}, 403)
	e.expect(outside, "PATCH", "/api/v1/customers/"+custID, map[string]any{"notes": "x"}, 404)
	e.expect(manager, "PATCH", "/api/v1/customers/"+custID, map[string]any{"notes": "Prefers email"}, 200)
	e.expect(manager, "PATCH", "/api/v1/customers/"+uuid.NewString(), map[string]any{"notes": "x"}, 404)
	if got := e.expect(viewer, "GET", "/api/v1/customers?q=alice", nil, 200)["customers"].([]any); len(got) != 1 {
		t.Fatalf("customer search %v", got)
	}
	if got := e.expect(viewer, "GET", "/api/v1/customers?q=bob", nil, 200)["customers"].([]any); len(got) != 0 {
		t.Fatalf("bob writes to press only: %v", got)
	}

	// Delete role: trash and restore; others can't see the trash.
	e.expect(deleter, "DELETE", "/api/v1/conversations/"+infoConv, nil, 200)
	trash := e.expect(deleter, "GET", "/api/v1/conversations?folder=trash", nil, 200)["conversations"].([]any)
	if len(trash) != 1 {
		t.Fatalf("trash %v", trash)
	}
	e.expect(viewer, "GET", "/api/v1/conversations/"+infoConv, nil, 404)
	e.expect(viewer, "GET", "/api/v1/conversations?mailbox="+infoID+"&folder=trash", nil, 403)
	e.expect(deleter, "POST", "/api/v1/conversations/"+infoConv+"/restore", nil, 200)
	e.expect(viewer, "GET", "/api/v1/conversations/"+infoConv, nil, 200)

	// Admin-only module routes.
	e.expect(manager, "GET", "/api/v1/settings/retention", nil, 403)
	e.expect(manager, "GET", "/api/v1/audit", nil, 403)
	e.expect(admin, "GET", "/api/v1/settings/retention", nil, 200)
	entries := e.expect(admin, "GET", "/api/v1/audit", nil, 200)["entries"].([]any)
	var actions []string
	for _, en := range entries {
		actions = append(actions, en.(map[string]any)["action"].(string))
	}
	for _, want := range []string{"mailbox.create", "mailbox.update", "conversation.delete", "conversation.restore", "customer.update"} {
		if !strings.Contains(strings.Join(actions, ","), want) {
			t.Errorf("audit lacks %s: %v", want, actions)
		}
	}

	// /me reports rights per mailbox.
	_, me = e.call(deleter, "GET", "/api/v1/me", nil)
	boxes := me["mailboxes"].([]any)
	if len(boxes) != 1 || me["is_admin"] != false {
		t.Fatalf("me %v", me)
	}
	rights := boxes[0].(map[string]any)["access"].(map[string]any)
	if rights["delete"] != true || rights["work"] != false || rights["view"] != true {
		t.Fatalf("rights %v", rights)
	}

	// Mailbox trash is for admins.
	e.expect(admin, "DELETE", "/api/v1/mailboxes/"+pressID, nil, 200)
	e.expect(outside, "GET", "/api/v1/mailboxes/"+pressID, nil, 404)
	if got := e.expect(admin, "GET", "/api/v1/mailboxes/trash", nil, 200)["mailboxes"].([]any); len(got) != 1 {
		t.Fatalf("mailbox trash %v", got)
	}
	e.expect(manager, "GET", "/api/v1/mailboxes/trash", nil, 403)
	e.expect(admin, "POST", "/api/v1/mailboxes/"+pressID+"/purge", nil, 204)
}

func TestAttachmentHeaders(t *testing.T) {
	e := newAPI(t)
	info := e.expect(admin, "POST", "/api/v1/mailboxes", map[string]any{"name": "Info", "email": "info@desk.test"}, 201)
	conv := e.importInto(info["id"].(string), "alice@example.com", "files")
	d := e.expect(admin, "GET", "/api/v1/conversations/"+conv, nil, 200)
	attID := d["threads"].([]any)[0].(map[string]any)["attachments"].([]any)[0].(map[string]any)["id"].(string)
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/attachments/"+attID, nil)
	req.Header.Set("X-Dev-User", admin.user)
	req.Header.Set("X-Dev-Groups", admin.groups)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Header.Get("Content-Type") != "application/pdf" || !strings.HasPrefix(res.Header.Get("Content-Security-Policy"), "sandbox") ||
		res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers %v", res.Header)
	}
	for declared, want := range map[string]string{
		"text/html": "application/octet-stream", "image/svg+xml": "application/octet-stream",
		"image/png": "image/png", "TEXT/PLAIN; charset=latin1": "text/plain; charset=utf-8", "garbage": "application/octet-stream",
	} {
		if got, _ := servedType(declared); got != want {
			t.Errorf("servedType(%q) = %q, want %q", declared, got, want)
		}
	}
}
