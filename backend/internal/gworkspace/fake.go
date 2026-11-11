// Code generated from con-services shared/go/gworkspace; DO NOT EDIT. Run scripts/sync-shared.

package gworkspace

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Fake is an in-memory Workspace for module tests. It enforces the same
// domain rule as Client and answers missing objects with an APIError for
// which IsNotFound is true. ListQuery.Q is not interpreted; LabelIDs are.
type Fake struct {
	Domain string

	mu      sync.Mutex
	users   map[string]*User
	groups  map[string]*Group
	members map[string]map[string]Member
	mail    map[string][]*Message
	labels  map[string]map[string]*Label
	sendAs  map[string]map[string]SendAs
	nextID  int
}

var _ Workspace = (*Fake)(nil)

// NewFake returns an empty Fake for domain.
func NewFake(domain string) *Fake {
	return &Fake{
		Domain: strings.ToLower(domain),
		users:  map[string]*User{}, groups: map[string]*Group{},
		members: map[string]map[string]Member{}, mail: map[string][]*Message{},
		labels: map[string]map[string]*Label{}, sendAs: map[string]map[string]SendAs{},
	}
}

func notFound(what, key string) error {
	return &APIError{Status: http.StatusNotFound, Message: what + " " + key + " not found", Reason: "notFound"}
}

func conflict(what, key string) error {
	return &APIError{Status: http.StatusConflict, Message: what + " " + key + " already exists", Reason: "duplicate"}
}

func (f *Fake) id() string {
	f.nextID++
	return strconv.Itoa(f.nextID)
}

func (f *Fake) inDomain(what, email string) error {
	if !(Config{Domain: f.Domain}).InDomain(email) {
		return fmt.Errorf("google workspace: %s %q is not in the domain %s", what, email, f.Domain)
	}
	return nil
}

func (f *Fake) user(key string) *User {
	if u, ok := f.users[strings.ToLower(key)]; ok {
		return u
	}
	for _, u := range f.users {
		if u.ID == key {
			return u
		}
	}
	return nil
}

func (f *Fake) group(key string) *Group {
	if g, ok := f.groups[strings.ToLower(key)]; ok {
		return g
	}
	for _, g := range f.groups {
		if g.ID == key {
			return g
		}
	}
	return nil
}

func (f *Fake) GetUser(_ context.Context, key string) (*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u := f.user(key); u != nil {
		cp := *u
		return &cp, nil
	}
	return nil, notFound("user", key)
}

func (f *Fake) CreateUser(_ context.Context, n NewUser) (*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.inDomain("user", n.PrimaryEmail); err != nil {
		return nil, err
	}
	if f.user(n.PrimaryEmail) != nil {
		return nil, conflict("user", n.PrimaryEmail)
	}
	ou := n.OrgUnitPath
	if ou == "" {
		ou = "/"
	}
	u := &User{ID: f.id(), PrimaryEmail: strings.ToLower(n.PrimaryEmail), OrgUnitPath: ou, RecoveryEmail: n.RecoveryEmail,
		Name: UserName{GivenName: n.GivenName, FamilyName: n.FamilyName, FullName: strings.TrimSpace(n.GivenName + " " + n.FamilyName)}}
	f.users[u.PrimaryEmail] = u
	cp := *u
	return &cp, nil
}

func (f *Fake) SetSuspended(_ context.Context, key string, suspended bool) (*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.user(key)
	if u == nil {
		return nil, notFound("user", key)
	}
	u.Suspended = suspended
	cp := *u
	return &cp, nil
}

func (f *Fake) DeleteUser(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.user(key)
	if u == nil {
		return notFound("user", key)
	}
	delete(f.users, u.PrimaryEmail)
	for _, ms := range f.members {
		delete(ms, u.PrimaryEmail)
	}
	return nil
}

func (f *Fake) ListGroups(context.Context) ([]Group, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Group, 0, len(f.groups))
	for _, g := range f.groups {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, nil
}

func (f *Fake) GetGroup(_ context.Context, key string) (*Group, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if g := f.group(key); g != nil {
		cp := *g
		return &cp, nil
	}
	return nil, notFound("group", key)
}

func (f *Fake) CreateGroup(_ context.Context, g Group) (*Group, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.inDomain("group", g.Email); err != nil {
		return nil, err
	}
	if f.group(g.Email) != nil {
		return nil, conflict("group", g.Email)
	}
	ng := &Group{ID: f.id(), Email: strings.ToLower(g.Email), Name: g.Name, Description: g.Description}
	f.groups[ng.Email] = ng
	f.members[ng.Email] = map[string]Member{}
	cp := *ng
	return &cp, nil
}

func (f *Fake) DeleteGroup(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.group(key)
	if g == nil {
		return notFound("group", key)
	}
	delete(f.groups, g.Email)
	delete(f.members, g.Email)
	return nil
}

func (f *Fake) ListMembers(_ context.Context, key string) ([]Member, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.group(key)
	if g == nil {
		return nil, notFound("group", key)
	}
	out := make([]Member, 0, len(f.members[g.Email]))
	for _, m := range f.members[g.Email] {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, nil
}

func (f *Fake) AddMember(_ context.Context, key string, m Member) (*Member, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.group(key)
	if g == nil {
		return nil, notFound("group", key)
	}
	email := strings.ToLower(m.Email)
	if _, ok := f.members[g.Email][email]; ok {
		return nil, conflict("member", m.Email)
	}
	if m.Role == "" {
		m.Role = RoleMember
	}
	typ := TypeUser
	if f.group(email) != nil {
		typ = TypeGroup
	}
	nm := Member{ID: f.id(), Email: email, Role: m.Role, Type: typ, Status: "ACTIVE"}
	f.members[g.Email][email] = nm
	return &nm, nil
}

func (f *Fake) RemoveMember(_ context.Context, key, member string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.group(key)
	if g == nil {
		return notFound("group", key)
	}
	member = strings.ToLower(member)
	for email, m := range f.members[g.Email] {
		if email == member || m.ID == member {
			delete(f.members[g.Email], email)
			return nil
		}
	}
	return notFound("member", member)
}

// Deliver puts a message into mailbox (test setup). It gets an ID, the
// labels given (default INBOX, UNREAD) and Raw from raw.
func (f *Fake) Deliver(mailbox string, raw []byte, labels ...string) *Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(labels) == 0 {
		labels = []string{"INBOX", "UNREAD"}
	}
	id := f.id()
	m := &Message{ID: "m" + id, ThreadID: "t" + id, LabelIDs: slices.Clone(labels), Raw: base64.RawURLEncoding.EncodeToString(raw)}
	key := strings.ToLower(mailbox)
	f.mail[key] = append(f.mail[key], m)
	cp := *m
	return &cp
}

// Messages returns every message in mailbox, oldest first (assertions).
func (f *Fake) Messages(mailbox string) []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Message
	for _, m := range f.mail[strings.ToLower(mailbox)] {
		out = append(out, *m)
	}
	return out
}

func (f *Fake) message(mailbox, id string) (*Message, error) {
	if err := f.inDomain("mailbox", mailbox); err != nil {
		return nil, err
	}
	for _, m := range f.mail[strings.ToLower(mailbox)] {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, notFound("message", id)
}

func (f *Fake) ListMessages(_ context.Context, mailbox string, q ListQuery) (*MessageList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.inDomain("mailbox", mailbox); err != nil {
		return nil, err
	}
	out := &MessageList{}
	msgs := f.mail[strings.ToLower(mailbox)]
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if slices.Contains(m.LabelIDs, "TRASH") && !slices.Contains(q.LabelIDs, "TRASH") {
			continue
		}
		ok := true
		for _, l := range q.LabelIDs {
			ok = ok && slices.Contains(m.LabelIDs, l)
		}
		if ok {
			out.Messages = append(out.Messages, MessageRef{ID: m.ID, ThreadID: m.ThreadID})
		}
	}
	if q.MaxResults > 0 && len(out.Messages) > q.MaxResults {
		out.Messages = out.Messages[:q.MaxResults]
	}
	out.ResultSizeEstimate = len(out.Messages)
	return out, nil
}

func (f *Fake) GetMessage(_ context.Context, mailbox, id string, _ Format) (*Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.message(mailbox, id)
	if err != nil {
		return nil, err
	}
	cp := *m
	cp.LabelIDs = slices.Clone(m.LabelIDs)
	return &cp, nil
}

func (f *Fake) SendMessage(_ context.Context, mailbox string, raw []byte, threadID string) (*Message, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("google workspace: empty message")
	}
	if err := f.inDomain("mailbox", mailbox); err != nil {
		return nil, err
	}
	m := f.Deliver(mailbox, raw, "SENT")
	if threadID != "" {
		f.mu.Lock()
		for _, s := range f.mail[strings.ToLower(mailbox)] {
			if s.ID == m.ID {
				s.ThreadID = threadID
			}
		}
		f.mu.Unlock()
		m.ThreadID = threadID
	}
	return m, nil
}

func (f *Fake) ModifyLabels(_ context.Context, mailbox, id string, add, remove []string) (*Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.message(mailbox, id)
	if err != nil {
		return nil, err
	}
	for _, l := range add {
		if !slices.Contains(m.LabelIDs, l) {
			m.LabelIDs = append(m.LabelIDs, l)
		}
	}
	m.LabelIDs = slices.DeleteFunc(m.LabelIDs, func(l string) bool { return slices.Contains(remove, l) })
	cp := *m
	cp.LabelIDs = slices.Clone(m.LabelIDs)
	return &cp, nil
}

func (f *Fake) TrashMessage(ctx context.Context, mailbox, id string) (*Message, error) {
	return f.ModifyLabels(ctx, mailbox, id, []string{"TRASH"}, []string{"INBOX"})
}

// UntrashMessage removes TRASH and puts the message back in INBOX.
func (f *Fake) UntrashMessage(ctx context.Context, mailbox, id string) (*Message, error) {
	return f.ModifyLabels(ctx, mailbox, id, []string{"INBOX"}, []string{"TRASH"})
}

// ListLabels returns labels created with CreateLabel. The real API also
// returns system labels such as INBOX; the fake does not invent those.
func (f *Fake) ListLabels(_ context.Context, mailbox string) ([]Label, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.inDomain("mailbox", mailbox); err != nil {
		return nil, err
	}
	var out []Label
	for _, l := range f.labels[strings.ToLower(mailbox)] {
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if out == nil {
		out = []Label{}
	}
	return out, nil
}

// CreateLabel stores a user label. A second create with the same name is
// a conflict.
func (f *Fake) CreateLabel(_ context.Context, mailbox, name string) (*Label, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("google workspace: empty label name")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.inDomain("mailbox", mailbox); err != nil {
		return nil, err
	}
	key := strings.ToLower(mailbox)
	if f.labels[key] == nil {
		f.labels[key] = map[string]*Label{}
	}
	if _, ok := f.labels[key][name]; ok {
		return nil, conflict("label", name)
	}
	l := &Label{
		ID: "Label_" + f.id(), Name: name, Type: "user",
		MessageListVisibility: "show", LabelListVisibility: "labelShow",
	}
	f.labels[key][name] = l
	cp := *l
	return &cp, nil
}

// CreateSendAs records an alias mailbox may send as. The alias must be in
// the domain. Creating it again is a conflict.
func (f *Fake) CreateSendAs(_ context.Context, mailbox, alias, displayName string) (*SendAs, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil, fmt.Errorf("google workspace: empty send-as address")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.inDomain("mailbox", mailbox); err != nil {
		return nil, err
	}
	if err := f.inDomain("send-as", alias); err != nil {
		return nil, err
	}
	key := strings.ToLower(mailbox)
	norm := strings.ToLower(alias)
	if f.sendAs[key] == nil {
		f.sendAs[key] = map[string]SendAs{}
	}
	if _, ok := f.sendAs[key][norm]; ok {
		return nil, conflict("send-as", alias)
	}
	s := SendAs{
		SendAsEmail: alias, DisplayName: strings.TrimSpace(displayName),
		TreatAsAlias: true, VerificationStatus: "accepted",
	}
	f.sendAs[key][norm] = s
	return &s, nil
}

// DeleteSendAs removes an alias. A missing alias is not found.
func (f *Fake) DeleteSendAs(_ context.Context, mailbox, alias string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return fmt.Errorf("google workspace: empty send-as address")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.inDomain("mailbox", mailbox); err != nil {
		return err
	}
	key := strings.ToLower(mailbox)
	norm := strings.ToLower(alias)
	if _, ok := f.sendAs[key][norm]; !ok {
		return notFound("send-as", alias)
	}
	delete(f.sendAs[key], norm)
	return nil
}
