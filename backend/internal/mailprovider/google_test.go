package mailprovider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/AGlElectronics/desk/backend/internal/gworkspace"
)

// fakeGoogle is the subset of the Directory and Gmail REST APIs Desk calls.
// The test server never contacts Google.
type fakeGoogle struct {
	mu         sync.Mutex
	users      map[string]bool
	groups     map[string][]string
	aliases    []string
	labels     map[string]string
	messages   []string // ids, newest first, as Google lists them
	labelSeq   int
	denySendAs bool
	// scopes records "subject\tscope scope" from each new authorised client.
	scopes []string
}

func (f *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && path == "/users":
		var body struct {
			PrimaryEmail string `json:"primaryEmail"`
			Name         struct {
				GivenName  string `json:"givenName"`
				FamilyName string `json:"familyName"`
			} `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Name.GivenName == "" || body.Name.FamilyName == "" {
			writeAPI(w, http.StatusBadRequest, "name required")
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.users[body.PrimaryEmail]; ok {
			writeAPI(w, http.StatusConflict, "already exists")
			return
		}
		f.users[body.PrimaryEmail] = false
		writeJSON(w, map[string]any{"primaryEmail": body.PrimaryEmail, "id": "u1"})
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/users/"):
		email := unescape(strings.TrimPrefix(path, "/users/"))
		f.mu.Lock()
		suspended, ok := f.users[email]
		f.mu.Unlock()
		if !ok {
			writeAPI(w, http.StatusNotFound, "not found")
			return
		}
		var body struct {
			Suspended bool `json:"suspended"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = suspended
		f.mu.Lock()
		f.users[email] = body.Suspended
		f.mu.Unlock()
		writeJSON(w, map[string]any{"primaryEmail": email, "suspended": body.Suspended})
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/users/") && !strings.Contains(strings.TrimPrefix(path, "/users/"), "/"):
		email := unescape(strings.TrimPrefix(path, "/users/"))
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.users[email]; !ok {
			writeAPI(w, http.StatusNotFound, "not found")
			return
		}
		delete(f.users, email)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && path == "/groups":
		var body struct {
			Email string `json:"email"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.groups[body.Email]; ok {
			writeAPI(w, http.StatusConflict, "already exists")
			return
		}
		f.groups[body.Email] = nil
		writeJSON(w, map[string]string{"email": body.Email, "id": "g1"})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/members") && strings.HasPrefix(path, "/groups/"):
		group := unescape(strings.TrimSuffix(strings.TrimPrefix(path, "/groups/"), "/members"))
		var body struct {
			Email string `json:"email"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		members, ok := f.groups[group]
		if !ok {
			writeAPI(w, http.StatusNotFound, "not found")
			return
		}
		for _, m := range members {
			if m == body.Email {
				writeAPI(w, http.StatusConflict, "already exists")
				return
			}
		}
		f.groups[group] = append(members, body.Email)
		writeJSON(w, map[string]string{"email": body.Email})
	case r.Method == http.MethodDelete && strings.Contains(path, "/members/"):
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/groups/"):
		group := unescape(strings.TrimPrefix(path, "/groups/"))
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.groups[group]; !ok {
			writeAPI(w, http.StatusNotFound, "not found")
			return
		}
		delete(f.groups, group)
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(path, "/users/me/"):
		f.gmail(w, r, strings.TrimPrefix(path, "/users/me"))
	default:
		writeAPI(w, http.StatusNotFound, "no such call "+path)
	}
}

func (f *fakeGoogle) gmail(w http.ResponseWriter, r *http.Request, path string) {
	switch {
	case r.Method == http.MethodPost && path == "/settings/sendAs":
		var body struct {
			SendAsEmail  string `json:"sendAsEmail"`
			DisplayName  string `json:"displayName"`
			TreatAsAlias bool   `json:"treatAsAlias"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !body.TreatAsAlias || body.SendAsEmail == "" || body.DisplayName == "" {
			writeAPI(w, http.StatusBadRequest, "send-as")
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.denySendAs {
			writeAPI(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		for _, a := range f.aliases {
			if a == body.SendAsEmail {
				writeAPI(w, http.StatusConflict, "already exists")
				return
			}
		}
		f.aliases = append(f.aliases, body.SendAsEmail)
		writeJSON(w, map[string]string{"sendAsEmail": body.SendAsEmail, "displayName": body.DisplayName})
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/settings/sendAs/"):
		alias := unescape(strings.TrimPrefix(path, "/settings/sendAs/"))
		f.mu.Lock()
		defer f.mu.Unlock()
		for i, a := range f.aliases {
			if a == alias {
				f.aliases = append(f.aliases[:i], f.aliases[i+1:]...)
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		writeAPI(w, http.StatusNotFound, "not found")
	case r.Method == http.MethodGet && path == "/messages":
		q := r.URL.Query().Get("q")
		if q != "in:inbox -label:Desk" {
			writeAPI(w, http.StatusBadRequest, "query "+q)
			return
		}
		f.mu.Lock()
		refs := make([]map[string]string, len(f.messages))
		for i, id := range f.messages {
			refs[i] = map[string]string{"id": id}
		}
		f.mu.Unlock()
		writeJSON(w, map[string]any{"messages": refs})
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/messages/") && r.URL.Query().Get("format") == "raw":
		id := strings.TrimPrefix(path, "/messages/")
		raw := base64.RawURLEncoding.EncodeToString([]byte("From: info@example.org\r\n\r\nhello " + id))
		writeJSON(w, map[string]string{"id": id, "raw": raw})
	case r.Method == http.MethodGet && path == "/labels":
		f.mu.Lock()
		var labels []map[string]string
		for name, id := range f.labels {
			labels = append(labels, map[string]string{"id": id, "name": name})
		}
		f.mu.Unlock()
		writeJSON(w, map[string]any{"labels": labels})
	case r.Method == http.MethodPost && path == "/labels":
		var body struct {
			Name                  string `json:"name"`
			LabelListVisibility   string `json:"labelListVisibility"`
			MessageListVisibility string `json:"messageListVisibility"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Name == "" || body.LabelListVisibility != "labelShow" || body.MessageListVisibility != "show" {
			writeAPI(w, http.StatusBadRequest, "label visibility")
			return
		}
		f.mu.Lock()
		if f.labels == nil {
			f.labels = map[string]string{}
		}
		f.labelSeq++
		id := "Label_" + strconv.Itoa(f.labelSeq)
		f.labels[body.Name] = id
		f.mu.Unlock()
		writeJSON(w, map[string]string{"id": id, "name": body.Name})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/modify"):
		writeJSON(w, map[string]string{"id": "m"})
	case r.Method == http.MethodPost && (strings.HasSuffix(path, "/trash") || strings.HasSuffix(path, "/untrash")):
		writeJSON(w, map[string]string{"id": "m"})
	case r.Method == http.MethodPost && path == "/messages/send":
		writeJSON(w, map[string]string{"id": "sent-1"})
	default:
		writeAPI(w, http.StatusNotFound, "no such gmail call "+path)
	}
}

func writeAPI(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": msg}})
}

func writeJSON(w http.ResponseWriter, v any) {
	_ = json.NewEncoder(w).Encode(v)
}

func testGoogle(t *testing.T) (*Google, *fakeGoogle) {
	t.Helper()
	fake := &fakeGoogle{users: map[string]bool{}, groups: map[string][]string{}, labels: map[string]string{}}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	g, err := NewGoogleForTest("example.org", "admin@example.org", srv.URL, func(_ context.Context, subject string, scopes []string) (*http.Client, error) {
		fake.mu.Lock()
		fake.scopes = append(fake.scopes, subject+"\t"+strings.Join(scopes, " "))
		fake.mu.Unlock()
		return srv.Client(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return g, fake
}

func TestGoogleUserAndMail(t *testing.T) {
	g, fake := testGoogle(t)
	ctx := context.Background()
	mb := Mailbox{Kind: KindUser, Address: "info@example.org", DisplayName: "Info Desk"}
	if err := g.CreateMailbox(ctx, mb); err != nil {
		t.Fatal(err)
	}
	if err := g.CreateMailbox(ctx, mb); err != ErrExists {
		t.Fatalf("second create: %v", err)
	}
	if err := g.SuspendMailbox(ctx, mb); err != nil {
		t.Fatal(err)
	}
	if err := g.RestoreMailbox(ctx, mb); err != nil {
		t.Fatal(err)
	}

	fake.mu.Lock()
	fake.messages = []string{"new", "old"}
	fake.mu.Unlock()
	ids, err := g.ListUnhandled(ctx, "info@example.org", "Desk", 10)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, ",") != "old,new" {
		t.Fatalf("oldest first, got %v", ids)
	}
	raw, err := g.Get(ctx, "info@example.org", "old")
	if err != nil || !strings.Contains(string(raw), "hello old") {
		t.Fatalf("raw %q err %v", raw, err)
	}
	if err := g.AddLabel(ctx, "info@example.org", "old", "Desk"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddLabel(ctx, "info@example.org", "new", "Desk"); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	if fake.labels["Desk"] == "" || fake.labelSeq != 1 {
		t.Fatalf("label created once, got %+v seq %d", fake.labels, fake.labelSeq)
	}
	fake.mu.Unlock()
	if _, err := g.Send(ctx, "info@example.org", []byte("From: info@example.org\r\n\r\nhi")); err != nil {
		t.Fatal(err)
	}
	if err := g.Trash(ctx, "info@example.org", "old"); err != nil {
		t.Fatal(err)
	}
	if err := g.Untrash(ctx, "info@example.org", "old"); err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteMailbox(ctx, mb); err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteMailbox(ctx, mb); err != ErrNotFound {
		t.Fatalf("second delete: %v", err)
	}
}

func unescape(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

func TestGoogleGroupSendAs(t *testing.T) {
	g, fake := testGoogle(t)
	ctx := context.Background()
	mb := Mailbox{Kind: KindGroup, Address: "press@example.org", DisplayName: "Press", DeliverTo: "desk@example.org"}
	if err := g.CreateMailbox(ctx, mb); err != nil {
		t.Fatal(err)
	}
	// The alias is already there. Restoring must treat that conflict as success
	// and must not create a second alias.
	if err := g.RestoreMailbox(ctx, mb); err != nil {
		t.Fatalf("send-as already present: %v", err)
	}
	fake.mu.Lock()
	members := append([]string{}, fake.groups["press@example.org"]...)
	aliases := append([]string{}, fake.aliases...)
	scopes := append([]string{}, fake.scopes...)
	fake.mu.Unlock()
	if strings.Join(members, ",") != "desk@example.org" {
		t.Fatalf("members %v", members)
	}
	if strings.Join(aliases, ",") != "press@example.org" {
		t.Fatalf("send-as %v", aliases)
	}
	sendAs := "desk@example.org\t" + strings.Join(gworkspace.SendAsScopes(), " ")
	if !contains(scopes, sendAs) {
		t.Fatalf("send-as scopes %q missing from %v", sendAs, scopes)
	}
	if err := g.SuspendMailbox(ctx, mb); err != nil {
		t.Fatal(err)
	}
	// The alias is gone. Suspending again must treat that as success.
	if err := g.SuspendMailbox(ctx, mb); err != nil {
		t.Fatalf("send-as already gone: %v", err)
	}
	if err := g.RestoreMailbox(ctx, mb); err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteMailbox(ctx, mb); err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteMailbox(ctx, mb); err != ErrNotFound {
		t.Fatalf("second delete: %v", err)
	}
}

func TestGoogleSendAsDenied(t *testing.T) {
	g, fake := testGoogle(t)
	fake.mu.Lock()
	fake.denySendAs = true
	fake.mu.Unlock()
	mb := Mailbox{Kind: KindGroup, Address: "press@example.org", DisplayName: "Press", DeliverTo: "desk@example.org"}
	if err := g.CreateMailbox(context.Background(), mb); err != ErrSendAsDenied {
		t.Fatalf("got %v", err)
	}
}

func TestGoogleMailUsesModifyScope(t *testing.T) {
	g, fake := testGoogle(t)
	ctx := context.Background()
	if err := g.CreateMailbox(ctx, Mailbox{Kind: KindUser, Address: "info@example.org", DisplayName: "Info Desk"}); err != nil {
		t.Fatal(err)
	}
	if err := g.AddLabel(ctx, "info@example.org", "m1", "Desk"); err != nil {
		t.Fatal(err)
	}
	if err := g.Untrash(ctx, "info@example.org", "m1"); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	scopes := append([]string{}, fake.scopes...)
	fake.mu.Unlock()
	want := "info@example.org\t" + strings.Join(gworkspace.GmailScopes, " ")
	if !contains(scopes, want) {
		t.Fatalf("gmail scopes %q missing from %v", want, scopes)
	}
	sendAs := strings.Join(gworkspace.SendAsScopes(), " ")
	for _, s := range scopes {
		if strings.Contains(s, sendAs) {
			t.Fatalf("label and untrash must not request %s: %v", gworkspace.SendAsScope, scopes)
		}
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
