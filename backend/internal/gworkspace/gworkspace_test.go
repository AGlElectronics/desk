// Code generated from con-services shared/go/gworkspace; DO NOT EDIT. Run scripts/sync-shared.

package gworkspace

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const domain = "con.example.org"

func testConfig() Config {
	return Config{CredentialsFile: "unused.json", AdminEmail: "admin@" + domain, Domain: domain}
}

func TestFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if _, err := FromEnv(env(nil)); !errors.Is(err, ErrNotConfigured) || err.Error() != "google workspace is not configured" {
		t.Errorf("empty: %v", err)
	}
	if _, err := FromEnv(env(map[string]string{EnvDomain: domain})); err == nil || !strings.HasPrefix(err.Error(), "google workspace needs") || !strings.Contains(err.Error(), EnvCredentialsFile) {
		t.Errorf("partial: %v", err)
	}
	if _, err := FromEnv(env(map[string]string{EnvCredentialsFile: "/x.json", EnvAdminEmail: "admin@other.org", EnvDomain: domain})); err == nil {
		t.Error("admin outside the domain accepted")
	}
	c, err := FromEnv(env(map[string]string{EnvCredentialsFile: "/x.json", EnvAdminEmail: "Admin@Con.Example.org", EnvDomain: " Con.Example.org "}))
	if err != nil || c.Domain != domain {
		t.Errorf("valid: %+v %v", c, err)
	}
	for email, want := range map[string]bool{"a@con.example.org": true, "A@CON.EXAMPLE.ORG": true, "a@sub.con.example.org": false, "a@example.org": false, "con.example.org": false, "a@": false} {
		if got := testConfig().InDomain(email); got != want {
			t.Errorf("InDomain(%q) = %v", email, got)
		}
	}
}

type call struct {
	subject, scopes, method, path, query, body string
}

// fakeGoogle records calls and answers like the Directory and Gmail APIs.
func fakeGoogle(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*Client, *[]call) {
	t.Helper()
	var mu sync.Mutex
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, call{r.Header.Get("X-Subject"), r.Header.Get("X-Scopes"), r.Method, r.URL.EscapedPath(), r.URL.RawQuery, string(b)})
		mu.Unlock()
		r.Body = io.NopCloser(strings.NewReader(string(b)))
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	hc := func(_ context.Context, subject string, scopes []string) (*http.Client, error) {
		return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			r.Header.Set("X-Subject", subject)
			r.Header.Set("X-Scopes", strings.Join(scopes, " "))
			return http.DefaultTransport.RoundTrip(r)
		})}, nil
	}
	c, err := New(testConfig(), Options{DirectoryURL: srv.URL + "/admin/directory/v1/", GmailURL: srv.URL + "/gmail/v1", HTTPClient: hc})
	if err != nil {
		t.Fatal(err)
	}
	return c, &calls
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDirectoryRequests(t *testing.T) {
	c, calls := fakeGoogle(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/admin/directory/v1/users/ghost@con.example.org":
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"Resource Not Found: userKey","errors":[{"reason":"notFound"}]}}`))
		case r.URL.Path == "/admin/directory/v1/groups" && r.Method == http.MethodGet:
			if r.URL.Query().Get("pageToken") == "" {
				_, _ = w.Write([]byte(`{"groups":[{"id":"g1","email":"staff@con.example.org"}],"nextPageToken":"p2"}`))
			} else {
				_, _ = w.Write([]byte(`{"groups":[{"id":"g2","email":"desk@con.example.org"}]}`))
			}
		case r.Method == http.MethodDelete:
			w.WriteHeader(204)
		default:
			_, _ = io.Copy(w, r.Body)
		}
	})
	ctx := context.Background()

	u, err := c.CreateUser(ctx, NewUser{PrimaryEmail: "fox@con.example.org", GivenName: "Fox", FamilyName: "Tail"})
	if err != nil || u.PrimaryEmail != "fox@con.example.org" {
		t.Fatalf("CreateUser: %+v %v", u, err)
	}
	var created map[string]any
	_ = json.Unmarshal([]byte((*calls)[0].body), &created)
	if pw, _ := created["password"].(string); len(pw) < 20 || created["orgUnitPath"] != "/" {
		t.Errorf("create body %v", created)
	}
	if _, err := c.CreateUser(ctx, NewUser{PrimaryEmail: "fox@evil.org", GivenName: "F", FamilyName: "T"}); err == nil {
		t.Error("user outside the domain created")
	}
	if _, err := c.CreateUser(ctx, NewUser{PrimaryEmail: "x@con.example.org"}); err == nil {
		t.Error("user without a name created")
	}
	if _, err := c.SetSuspended(ctx, "fox@con.example.org", true); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteUser(ctx, "fox@con.example.org"); err != nil {
		t.Fatal(err)
	}
	_, err = c.GetUser(ctx, "ghost@con.example.org")
	if !IsNotFound(err) || !strings.Contains(err.Error(), "notFound") {
		t.Errorf("GetUser missing: %v", err)
	}
	groups, err := c.ListGroups(ctx)
	if err != nil || len(groups) != 2 {
		t.Fatalf("ListGroups: %+v %v", groups, err)
	}
	if _, err := c.AddMember(ctx, "staff@con.example.org", Member{Email: "fox@con.example.org"}); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveMember(ctx, "staff@con.example.org", "fox@con.example.org"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateGroup(ctx, Group{Email: "x@other.org"}); err == nil {
		t.Error("group outside the domain created")
	}

	want := []call{
		{method: "POST", path: "/admin/directory/v1/users"},
		{method: "PUT", path: "/admin/directory/v1/users/fox@con.example.org", body: `{"suspended":true}`},
		{method: "DELETE", path: "/admin/directory/v1/users/fox@con.example.org"},
		{method: "GET", path: "/admin/directory/v1/users/ghost@con.example.org"},
		{method: "GET", path: "/admin/directory/v1/groups", query: "domain=con.example.org&maxResults=200"},
		{method: "GET", path: "/admin/directory/v1/groups", query: "domain=con.example.org&maxResults=200&pageToken=p2"},
		{method: "POST", path: "/admin/directory/v1/groups/staff@con.example.org/members", body: `{"email":"fox@con.example.org","role":"MEMBER"}`},
		{method: "DELETE", path: "/admin/directory/v1/groups/staff@con.example.org/members/fox@con.example.org"},
	}
	if len(*calls) != len(want) {
		t.Fatalf("%d calls, want %d: %+v", len(*calls), len(want), *calls)
	}
	for i, w := range want {
		got := (*calls)[i]
		if got.method != w.method || got.path != w.path || (w.query != "" && got.query != w.query) || (w.body != "" && got.body != w.body) {
			t.Errorf("call %d = %+v, want %+v", i, got, w)
		}
		if got.subject != "admin@con.example.org" || got.scopes != strings.Join(DirectoryScopes, " ") {
			t.Errorf("call %d signed in as %q with %q", i, got.subject, got.scopes)
		}
	}
}

func TestGmailRequests(t *testing.T) {
	c, calls := fakeGoogle(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/messages") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"messages":[{"id":"a","threadId":"t"}],"resultSizeEstimate":1}`))
		case strings.HasSuffix(r.URL.Path, "/messages/a"):
			_, _ = w.Write([]byte(`{"id":"a","threadId":"t","payload":{"mimeType":"text/plain","headers":[{"name":"Subject","value":"Hi"}],"body":{"size":5,"data":"aGVsbG8"}}}`))
		default:
			_, _ = w.Write([]byte(`{"id":"a","threadId":"t","labelIds":["INBOX"]}`))
		}
	})
	ctx := context.Background()
	mb := "desk@con.example.org"

	list, err := c.ListMessages(ctx, mb, ListQuery{Q: "is:unread", LabelIDs: []string{"INBOX"}, MaxResults: 10})
	if err != nil || len(list.Messages) != 1 {
		t.Fatalf("ListMessages %+v %v", list, err)
	}
	m, err := c.GetMessage(ctx, mb, "a", "")
	if err != nil || m.Payload.Header("subject") != "Hi" {
		t.Fatalf("GetMessage %+v %v", m, err)
	}
	if b, err := m.Payload.DecodeBody(); err != nil || string(b) != "hello" {
		t.Errorf("DecodeBody %q %v", b, err)
	}
	if _, err := c.SendMessage(ctx, mb, []byte("From: desk@con.example.org\r\nSubject: x\r\n\r\nbody"), "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SendMessage(ctx, mb, nil, ""); err == nil {
		t.Error("empty message sent")
	}
	if _, err := c.ModifyLabels(ctx, mb, "a", nil, []string{"UNREAD"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TrashMessage(ctx, mb, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListMessages(ctx, "someone@gmail.com", ListQuery{}); err == nil {
		t.Error("mailbox outside the domain used")
	}

	want := []call{
		{method: "GET", path: "/gmail/v1/users/me/messages", query: "labelIds=INBOX&maxResults=10&q=is%3Aunread"},
		{method: "GET", path: "/gmail/v1/users/me/messages/a", query: "format=full"},
		{method: "POST", path: "/gmail/v1/users/me/messages/send", body: `{"raw":"` + base64.RawURLEncoding.EncodeToString([]byte("From: desk@con.example.org\r\nSubject: x\r\n\r\nbody")) + `","threadId":"t"}`},
		{method: "POST", path: "/gmail/v1/users/me/messages/a/modify", body: `{"addLabelIds":[],"removeLabelIds":["UNREAD"]}`},
		{method: "POST", path: "/gmail/v1/users/me/messages/a/trash"},
	}
	if len(*calls) != len(want) {
		t.Fatalf("%d calls, want %d: %+v", len(*calls), len(want), *calls)
	}
	for i, w := range want {
		got := (*calls)[i]
		if got.method != w.method || got.path != w.path || (w.query != "" && got.query != w.query) || (w.body != "" && got.body != w.body) {
			t.Errorf("call %d = %+v, want %+v", i, got, w)
		}
		if got.subject != mb || got.scopes != GmailScopes[0] {
			t.Errorf("call %d signed in as %q with %q", i, got.subject, got.scopes)
		}
	}
}

// TestServiceAccountImpersonates signs in with a generated service-account
// key against a fake token endpoint and checks the JWT's sub and scope.
func TestServiceAccountImpersonates(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	var mu sync.Mutex
	var claims []map[string]any
	var api *httptest.Server
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		parts := strings.Split(r.Form.Get("assertion"), ".")
		if len(parts) != 3 {
			w.WriteHeader(400)
			return
		}
		raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var c map[string]any
		_ = json.Unmarshal(raw, &c)
		mu.Lock()
		claims = append(claims, c)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-` + c["sub"].(string) + `","token_type":"Bearer","expires_in":3600}`))
	}))
	defer tokenSrv.Close()
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"1","primaryEmail":"fox@con.example.org","authorization":"` + r.Header.Get("Authorization") + `"}`))
	}))
	defer api.Close()

	sa, _ := json.Marshal(map[string]string{
		"type": "service_account", "client_email": "conservices@project.iam.gserviceaccount.com",
		"private_key_id": "k1", "private_key": string(pemKey), "token_uri": tokenSrv.URL,
	})
	file := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(file, sa, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.CredentialsFile = file
	c, err := New(cfg, Options{DirectoryURL: api.URL, GmailURL: api.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.GetUser(ctx, "fox@con.example.org"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetUser(ctx, "fox@con.example.org"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetMessage(ctx, "desk@con.example.org", "1", FormatMinimal); err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 {
		t.Fatalf("%d token requests, want 2 (one per subject, then cached)", len(claims))
	}
	if claims[0]["sub"] != "admin@con.example.org" || claims[0]["scope"] != strings.Join(DirectoryScopes, " ") || claims[0]["iss"] != "conservices@project.iam.gserviceaccount.com" {
		t.Errorf("directory token claims %v", claims[0])
	}
	if claims[1]["sub"] != "desk@con.example.org" || claims[1]["scope"] != GmailScopes[0] {
		t.Errorf("gmail token claims %v", claims[1])
	}

	if err := os.WriteFile(file, []byte(`{"type":"authorized_user"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg, Options{}); err == nil {
		t.Error("a non-service-account file was accepted")
	}
	cfg.CredentialsFile = filepath.Join(t.TempDir(), "missing.json")
	if _, err := New(cfg, Options{}); err == nil || !strings.Contains(err.Error(), EnvCredentialsFile) {
		t.Errorf("missing file: %v", err)
	}
}

func TestFake(t *testing.T) {
	var w Workspace = NewFake(domain)
	f := w.(*Fake)
	ctx := context.Background()

	u, err := w.CreateUser(ctx, NewUser{PrimaryEmail: "Fox@con.example.org", GivenName: "Fox", FamilyName: "Tail"})
	if err != nil || u.PrimaryEmail != "fox@con.example.org" || u.Name.FullName != "Fox Tail" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := w.CreateUser(ctx, NewUser{PrimaryEmail: "fox@con.example.org"}); !IsConflict(err) {
		t.Errorf("duplicate user: %v", err)
	}
	if _, err := w.CreateUser(ctx, NewUser{PrimaryEmail: "fox@evil.org"}); err == nil {
		t.Error("outside domain accepted")
	}
	if u, _ := w.SetSuspended(ctx, u.ID, true); !u.Suspended {
		t.Error("not suspended")
	}
	g, _ := w.CreateGroup(ctx, Group{Email: "staff@con.example.org", Name: "Staff"})
	if _, err := w.AddMember(ctx, g.Email, Member{Email: "fox@con.example.org"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddMember(ctx, g.Email, Member{Email: "fox@con.example.org"}); !IsConflict(err) {
		t.Errorf("duplicate member: %v", err)
	}
	if ms, _ := w.ListMembers(ctx, g.ID); len(ms) != 1 || ms[0].Role != RoleMember || ms[0].Type != TypeUser {
		t.Errorf("members %+v", ms)
	}
	if err := w.DeleteUser(ctx, "fox@con.example.org"); err != nil {
		t.Fatal(err)
	}
	if ms, _ := w.ListMembers(ctx, g.Email); len(ms) != 0 {
		t.Error("deleted user still a member")
	}
	if _, err := w.GetUser(ctx, "fox@con.example.org"); !IsNotFound(err) {
		t.Errorf("deleted user: %v", err)
	}
	if err := w.RemoveMember(ctx, g.Email, "nobody@con.example.org"); !IsNotFound(err) {
		t.Errorf("remove missing: %v", err)
	}

	mb := "desk@con.example.org"
	in := f.Deliver(mb, []byte("Subject: hi\r\n\r\nx"))
	list, _ := w.ListMessages(ctx, mb, ListQuery{LabelIDs: []string{"UNREAD"}})
	if len(list.Messages) != 1 || list.Messages[0].ID != in.ID {
		t.Fatalf("list %+v", list)
	}
	if m, _ := w.ModifyLabels(ctx, mb, in.ID, []string{"Label_1"}, []string{"UNREAD"}); strings.Join(m.LabelIDs, ",") != "INBOX,Label_1" {
		t.Errorf("labels %v", m.LabelIDs)
	}
	if _, err := w.TrashMessage(ctx, mb, in.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := w.ListMessages(ctx, mb, ListQuery{}); len(list.Messages) != 0 {
		t.Error("trashed message listed")
	}
	sent, _ := w.SendMessage(ctx, mb, []byte("Subject: re\r\n\r\ny"), in.ThreadID)
	if raw, _ := sent.RawBytes(); string(raw) != "Subject: re\r\n\r\ny" || sent.ThreadID != in.ThreadID {
		t.Errorf("sent %+v", sent)
	}
	if got, _ := w.GetMessage(ctx, mb, sent.ID, FormatRaw); got.ThreadID != in.ThreadID {
		t.Error("thread not stored")
	}
	if _, err := w.GetMessage(ctx, mb, "nope", ""); !IsNotFound(err) {
		t.Errorf("missing message: %v", err)
	}
	if len(f.Messages(mb)) != 2 {
		t.Errorf("mailbox has %d messages", len(f.Messages(mb)))
	}
	if _, err := w.UntrashMessage(ctx, mb, in.ID); err != nil {
		t.Fatal(err)
	}
	list, _ = w.ListMessages(ctx, mb, ListQuery{})
	back := false
	for _, ref := range list.Messages {
		back = back || ref.ID == in.ID
	}
	if !back {
		t.Fatalf("untrash %+v", list)
	}
	if labels, err := w.ListLabels(ctx, mb); err != nil || len(labels) != 0 {
		t.Fatalf("no labels yet: %+v %v", labels, err)
	}
	lab, err := w.CreateLabel(ctx, mb, "Desk")
	if err != nil || lab.ID == "" || lab.Name != "Desk" || lab.LabelListVisibility != "labelShow" || lab.MessageListVisibility != "show" {
		t.Fatalf("create label %+v %v", lab, err)
	}
	if _, err := w.CreateLabel(ctx, mb, "Desk"); !IsConflict(err) {
		t.Errorf("duplicate label: %v", err)
	}
	if _, err := w.CreateLabel(ctx, mb, "  "); err == nil {
		t.Error("empty label name accepted")
	}
	if labels, _ := w.ListLabels(ctx, mb); len(labels) != 1 || labels[0].ID != lab.ID {
		t.Errorf("labels %+v", labels)
	}
	if _, err := w.CreateLabel(ctx, "someone@gmail.com", "Desk"); err == nil {
		t.Error("label outside the domain created")
	}
	alias, err := w.CreateSendAs(ctx, mb, "info@con.example.org", "Info")
	if err != nil || !alias.TreatAsAlias || alias.SendAsEmail != "info@con.example.org" || alias.DisplayName != "Info" {
		t.Fatalf("send-as %+v %v", alias, err)
	}
	if _, err := w.CreateSendAs(ctx, mb, "Info@con.example.org", "Info"); !IsConflict(err) {
		t.Errorf("duplicate send-as: %v", err)
	}
	if _, err := w.CreateSendAs(ctx, mb, "info@gmail.com", "Info"); err == nil {
		t.Error("send-as outside the domain created")
	}
	if err := w.DeleteSendAs(ctx, mb, "info@con.example.org"); err != nil {
		t.Fatal(err)
	}
	if err := w.DeleteSendAs(ctx, mb, "info@con.example.org"); !IsNotFound(err) {
		t.Errorf("delete missing send-as: %v", err)
	}
}

func TestGmailLabelsUntrashAndSendAs(t *testing.T) {
	c, calls := fakeGoogle(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/gmail/v1/users/me/labels" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"labels":[{"id":"INBOX","name":"INBOX","type":"system"},{"id":"Label_1","name":"Desk","type":"user"}]}`))
		case r.URL.Path == "/gmail/v1/users/me/labels" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"id":"Label_2","name":"Desk/Unrouted","type":"user","labelListVisibility":"labelShow","messageListVisibility":"show"}`))
		case strings.HasSuffix(r.URL.Path, "/untrash"):
			_, _ = w.Write([]byte(`{"id":"a","threadId":"t","labelIds":["INBOX"]}`))
		case r.URL.Path == "/gmail/v1/users/me/settings/sendAs" && r.Method == http.MethodPost:
			raw, _ := io.ReadAll(r.Body)
			if strings.Contains(string(raw), "taken@") {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":{"code":409,"message":"already exists","errors":[{"reason":"duplicate"}]}}`))
				return
			}
			_, _ = w.Write([]byte(`{"sendAsEmail":"info@con.example.org","displayName":"Info","treatAsAlias":true,"verificationStatus":"accepted"}`))
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/info@con.example.org"):
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"not found","errors":[{"reason":"notFound"}]}}`))
		}
	})
	ctx := context.Background()
	mb := "desk@con.example.org"

	labels, err := c.ListLabels(ctx, mb)
	if err != nil || len(labels) != 2 || labels[1].ID != "Label_1" || labels[1].Name != "Desk" {
		t.Fatalf("ListLabels %+v %v", labels, err)
	}
	created, err := c.CreateLabel(ctx, mb, " Desk/Unrouted ")
	if err != nil || created.ID != "Label_2" || created.Name != "Desk/Unrouted" {
		t.Fatalf("CreateLabel %+v %v", created, err)
	}
	if _, err := c.CreateLabel(ctx, mb, " "); err == nil {
		t.Error("empty label name sent")
	}
	m, err := c.UntrashMessage(ctx, mb, "a")
	if err != nil || m.ID != "a" || len(m.LabelIDs) != 1 || m.LabelIDs[0] != "INBOX" {
		t.Fatalf("Untrash %+v %v", m, err)
	}
	alias, err := c.CreateSendAs(ctx, mb, " info@con.example.org ", " Info ")
	if err != nil || alias.SendAsEmail != "info@con.example.org" || !alias.TreatAsAlias || alias.DisplayName != "Info" || alias.VerificationStatus != "accepted" {
		t.Fatalf("CreateSendAs %+v %v", alias, err)
	}
	if err := c.DeleteSendAs(ctx, mb, "info@con.example.org"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateSendAs(ctx, mb, "taken@con.example.org", "Taken"); !IsConflict(err) {
		t.Errorf("conflict: %v", err)
	}
	if err := c.DeleteSendAs(ctx, mb, "missing@con.example.org"); !IsNotFound(err) {
		t.Errorf("missing send-as: %v", err)
	}
	if _, err := c.ListLabels(ctx, "someone@gmail.com"); err == nil {
		t.Error("mailbox outside the domain used")
	}
	if _, err := c.CreateSendAs(ctx, mb, "out@gmail.com", "Out"); err == nil {
		t.Error("send-as outside the domain sent")
	}
	if err := c.DeleteSendAs(ctx, mb, " "); err == nil {
		t.Error("empty send-as address sent")
	}

	want := []call{
		{method: "GET", path: "/gmail/v1/users/me/labels", scopes: GmailScopes[0]},
		{method: "POST", path: "/gmail/v1/users/me/labels", body: `{"name":"Desk/Unrouted","messageListVisibility":"show","labelListVisibility":"labelShow"}`, scopes: GmailScopes[0]},
		{method: "POST", path: "/gmail/v1/users/me/messages/a/untrash", scopes: GmailScopes[0]},
		{method: "POST", path: "/gmail/v1/users/me/settings/sendAs", body: `{"sendAsEmail":"info@con.example.org","displayName":"Info","treatAsAlias":true}`, scopes: strings.Join(SendAsScopes(), " ")},
		{method: "DELETE", path: "/gmail/v1/users/me/settings/sendAs/info@con.example.org", scopes: strings.Join(SendAsScopes(), " ")},
		{method: "POST", path: "/gmail/v1/users/me/settings/sendAs", body: `{"sendAsEmail":"taken@con.example.org","displayName":"Taken","treatAsAlias":true}`, scopes: strings.Join(SendAsScopes(), " ")},
		{method: "DELETE", path: "/gmail/v1/users/me/settings/sendAs/missing@con.example.org", scopes: strings.Join(SendAsScopes(), " ")},
	}
	if len(*calls) != len(want) {
		t.Fatalf("%d calls, want %d: %+v", len(*calls), len(want), *calls)
	}
	for i, w := range want {
		got := (*calls)[i]
		if got.method != w.method || got.path != w.path || (w.body != "" && got.body != w.body) || got.scopes != w.scopes || got.subject != mb {
			t.Errorf("call %d = %+v, want %+v", i, got, w)
		}
	}
}
