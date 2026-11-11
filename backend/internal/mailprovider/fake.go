package mailprovider

import (
	"bytes"
	"context"
	"fmt"
	"net/mail"
	"strings"
	"sync"
)

// Fake is an in-memory Workspace for tests and GOOGLE_FAKE=true in
// development. Mail sent to an address the fake knows is delivered to it,
// so a whole round trip works without Google.
type Fake struct {
	mu     sync.Mutex
	users  map[string]*fakeUser
	groups map[string]*fakeGroup
	seq    int
	// Sent records every Send call.
	Sent []SentMessage
	// FailNext makes the next call of that method name fail (tests).
	FailNext map[string]error
}

// SentMessage is one Send call.
type SentMessage struct {
	User string
	From string
	To   []string
	Raw  []byte
	ID   string
}

type fakeUser struct {
	suspended bool
	sendAs    map[string]bool
	order     []string
	msgs      map[string]*fakeMsg
}

type fakeMsg struct {
	raw     []byte
	labels  map[string]bool
	trashed bool
}

type fakeGroup struct {
	deliverTo string
	suspended bool
}

// NewFake returns an empty fake Workspace.
func NewFake() *Fake {
	return &Fake{users: map[string]*fakeUser{}, groups: map[string]*fakeGroup{}, FailNext: map[string]error{}}
}

func (f *Fake) fail(method string) error {
	if err, ok := f.FailNext[method]; ok {
		delete(f.FailNext, method)
		return err
	}
	return nil
}

func norm(a string) string { return strings.ToLower(strings.TrimSpace(a)) }

// AddUser creates a licensed user outside Desk (the shared account).
func (f *Fake) AddUser(address string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureUser(norm(address))
}

func (f *Fake) ensureUser(a string) *fakeUser {
	u, ok := f.users[a]
	if !ok {
		u = &fakeUser{sendAs: map[string]bool{a: true}, msgs: map[string]*fakeMsg{}}
		f.users[a] = u
	}
	return u
}

// CreateMailbox implements Directory.
func (f *Fake) CreateMailbox(_ context.Context, m Mailbox) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("CreateMailbox"); err != nil {
		return err
	}
	a := norm(m.Address)
	if _, ok := f.users[a]; ok {
		return ErrExists
	}
	if _, ok := f.groups[a]; ok {
		return ErrExists
	}
	switch m.Kind {
	case KindUser:
		f.ensureUser(a)
	case KindGroup:
		shared, ok := f.users[norm(m.DeliverTo)]
		if !ok {
			return fmt.Errorf("shared account %s: %w", m.DeliverTo, ErrNotFound)
		}
		f.groups[a] = &fakeGroup{deliverTo: norm(m.DeliverTo)}
		shared.sendAs[a] = true
	default:
		return fmt.Errorf("unknown mailbox kind %q", m.Kind)
	}
	return nil
}

// SuspendMailbox implements Directory.
func (f *Fake) SuspendMailbox(_ context.Context, m Mailbox) error {
	return f.setSuspended(m, true)
}

// RestoreMailbox implements Directory.
func (f *Fake) RestoreMailbox(_ context.Context, m Mailbox) error {
	return f.setSuspended(m, false)
}

func (f *Fake) setSuspended(m Mailbox, v bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := norm(m.Address)
	if u, ok := f.users[a]; ok && m.Kind == KindUser {
		u.suspended = v
		return nil
	}
	if g, ok := f.groups[a]; ok && m.Kind == KindGroup {
		g.suspended = v
		if shared, ok := f.users[g.deliverTo]; ok {
			shared.sendAs[a] = !v
		}
		return nil
	}
	return ErrNotFound
}

// DeleteMailbox implements Directory.
func (f *Fake) DeleteMailbox(_ context.Context, m Mailbox) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := norm(m.Address)
	switch m.Kind {
	case KindUser:
		if _, ok := f.users[a]; !ok {
			return ErrNotFound
		}
		delete(f.users, a)
	case KindGroup:
		g, ok := f.groups[a]
		if !ok {
			return ErrNotFound
		}
		if shared, ok := f.users[g.deliverTo]; ok {
			delete(shared.sendAs, a)
		}
		delete(f.groups, a)
	}
	return nil
}

// Suspended reports whether an address is suspended (tests).
func (f *Fake) Suspended(address string) (suspended, exists bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := norm(address)
	if u, ok := f.users[a]; ok {
		return u.suspended, true
	}
	if g, ok := f.groups[a]; ok {
		return g.suspended, true
	}
	return false, false
}

// Deliver puts a message into the inbox of every account behind to.
// It returns false when nobody receives it.
func (f *Fake) Deliver(to string, raw []byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deliver(norm(to), raw)
}

func (f *Fake) deliver(to string, raw []byte) bool {
	target := to
	if g, ok := f.groups[to]; ok {
		if g.suspended {
			return false
		}
		target = g.deliverTo
	}
	u, ok := f.users[target]
	if !ok || u.suspended {
		return false
	}
	f.seq++
	id := fmt.Sprintf("m%06d", f.seq)
	u.msgs[id] = &fakeMsg{raw: append([]byte(nil), raw...), labels: map[string]bool{"INBOX": true}}
	u.order = append(u.order, id)
	return true
}

func (f *Fake) user(a string) (*fakeUser, error) {
	u, ok := f.users[norm(a)]
	if !ok {
		return nil, ErrNotFound
	}
	if u.suspended {
		return nil, fmt.Errorf("account %s is suspended", a)
	}
	return u, nil
}

// ListUnhandled implements Mail.
func (f *Fake) ListUnhandled(_ context.Context, user, label string, max int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListUnhandled"); err != nil {
		return nil, err
	}
	u, err := f.user(user)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, id := range u.order {
		m := u.msgs[id]
		if m == nil || m.trashed || !m.labels["INBOX"] || m.labels[label] {
			continue
		}
		out = append(out, id)
		if len(out) == max {
			break
		}
	}
	return out, nil
}

// Get implements Mail.
func (f *Fake) Get(_ context.Context, user, id string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, err := f.user(user)
	if err != nil {
		return nil, err
	}
	m, ok := u.msgs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), m.raw...), nil
}

// AddLabel implements Mail.
func (f *Fake) AddLabel(_ context.Context, user, id, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, err := f.user(user)
	if err != nil {
		return err
	}
	m, ok := u.msgs[id]
	if !ok {
		return ErrNotFound
	}
	m.labels[label] = true
	return nil
}

// Labels returns a message's labels (tests).
func (f *Fake) Labels(user, id string) map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[norm(user)]; ok {
		if m, ok := u.msgs[id]; ok {
			out := map[string]bool{}
			for k, v := range m.labels {
				out[k] = v
			}
			if m.trashed {
				out["TRASH"] = true
			}
			return out
		}
	}
	return nil
}

// Send implements Mail. The From address must be the user or one of its
// send-as aliases; recipients the fake knows receive the message.
func (f *Fake) Send(_ context.Context, user string, raw []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("Send"); err != nil {
		return "", err
	}
	u, err := f.user(user)
	if err != nil {
		return "", err
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("invalid message: %w", err)
	}
	from, err := mail.ParseAddress(msg.Header.Get("From"))
	if err != nil || !u.sendAs[norm(from.Address)] {
		return "", ErrSendAsDenied
	}
	var rcpts []string
	for _, h := range []string{"To", "Cc"} {
		if list, err := msg.Header.AddressList(h); err == nil {
			for _, a := range list {
				rcpts = append(rcpts, norm(a.Address))
			}
		}
	}
	f.seq++
	id := fmt.Sprintf("s%06d", f.seq)
	u.msgs[id] = &fakeMsg{raw: append([]byte(nil), raw...), labels: map[string]bool{"SENT": true}}
	f.Sent = append(f.Sent, SentMessage{User: norm(user), From: norm(from.Address), To: rcpts, Raw: raw, ID: id})
	for _, r := range rcpts {
		f.deliver(r, raw)
	}
	return id, nil
}

// Trash implements Mail.
func (f *Fake) Trash(_ context.Context, user, id string) error { return f.setTrashed(user, id, true) }

// Untrash implements Mail.
func (f *Fake) Untrash(_ context.Context, user, id string) error { return f.setTrashed(user, id, false) }

func (f *Fake) setTrashed(user, id string, v bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[norm(user)]
	if !ok {
		return ErrNotFound
	}
	m, ok := u.msgs[id]
	if !ok {
		return ErrNotFound
	}
	m.trashed = v
	return nil
}

var _ Provider = (*Fake)(nil)
