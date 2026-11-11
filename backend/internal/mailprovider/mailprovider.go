// Package mailprovider is the small surface Desk needs from a hosted mail
// provider (Google Workspace): create and suspend mailboxes, read and label
// mail, send as a mailbox address and trash messages.
//
// Desk is built and tested against this interface with the in-memory Fake.
// The real Gmail API / Admin SDK client implements it outside Desk
// (shared/go/gworkspace) and is plugged in with app.GoogleProvider.
//
// Two kinds of mailbox (docs/adr/0001):
//
//   - KindUser: the mailbox is its own licensed Workspace user. Desk reads
//     and sends as that user (domain-wide delegation).
//   - KindGroup: the mailbox is a free group (alias) whose only member is
//     the shared licensed account (DeliverTo, e.g. desk@). Desk reads the
//     shared account, routes each message by recipient address and sends
//     "as" the group address through a send-as alias on the shared account.
package mailprovider

import (
	"context"
	"errors"
)

// Kind is how a mailbox exists in the provider.
type Kind string

const (
	KindUser  Kind = "user"
	KindGroup Kind = "group"
)

// Mailbox describes a provider mailbox.
type Mailbox struct {
	Kind        Kind
	Address     string
	DisplayName string
	// DeliverTo is the shared account a KindGroup mailbox delivers into.
	DeliverTo string
}

var (
	// ErrNotFound: the user, group or message does not exist.
	ErrNotFound = errors.New("not found at the mail provider")
	// ErrExists: the address is already taken in the provider.
	ErrExists = errors.New("address already exists at the mail provider")
	// ErrSendAsDenied: the account may not send with that From address.
	ErrSendAsDenied = errors.New("the account may not send as this address")
)

// Directory manages mailboxes (Admin SDK Directory API plus Gmail send-as
// settings).
type Directory interface {
	// CreateMailbox creates a licensed user (KindUser) or a group that
	// delivers into DeliverTo with a send-as alias there (KindGroup).
	CreateMailbox(ctx context.Context, m Mailbox) error
	// SuspendMailbox stops a mailbox without deleting it: a user is
	// suspended; a group stops delivering and its send-as alias is removed.
	SuspendMailbox(ctx context.Context, m Mailbox) error
	// RestoreMailbox undoes SuspendMailbox.
	RestoreMailbox(ctx context.Context, m Mailbox) error
	// DeleteMailbox removes it for good.
	DeleteMailbox(ctx context.Context, m Mailbox) error
}

// Mail reads and writes one account's mail (Gmail API as that user).
type Mail interface {
	// ListUnhandled returns up to max ids of inbox messages of user that do
	// not carry label yet, oldest first.
	ListUnhandled(ctx context.Context, user, label string, max int) ([]string, error)
	// Get returns the raw RFC 5322 message.
	Get(ctx context.Context, user, id string) ([]byte, error)
	// AddLabel puts label on a message, creating the label if needed.
	AddLabel(ctx context.Context, user, id, label string) error
	// Send sends raw (its From header picks the send-as address) and
	// returns the new message's id.
	Send(ctx context.Context, user string, raw []byte) (string, error)
	// Trash moves a message to the provider's trash (Gmail keeps it for
	// 30 days); Untrash brings it back.
	Trash(ctx context.Context, user, id string) error
	Untrash(ctx context.Context, user, id string) error
}

// Provider is everything Desk uses.
type Provider interface {
	Directory
	Mail
}
