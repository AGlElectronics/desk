// Code generated from con-services shared/go/gworkspace; DO NOT EDIT. Run scripts/sync-shared.

// Package gworkspace is a small Google Workspace client for Con Services
// modules: Admin SDK Directory (users, groups, group members) and the Gmail
// API (list, read, send, label, trash). It signs in as a service account
// with domain-wide delegation and impersonates a person: the admin in
// GOOGLE_WORKSPACE_ADMIN_EMAIL for Directory calls, the mailbox owner for
// Gmail calls.
//
// Modules depend on the Directory and Gmail interfaces and use Fake in
// tests.
package gworkspace

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"golang.org/x/oauth2/google"
)

// Environment variables read by FromEnv.
const (
	EnvCredentialsFile = "GOOGLE_WORKSPACE_CREDENTIALS_FILE"
	EnvAdminEmail      = "GOOGLE_WORKSPACE_ADMIN_EMAIL"
	EnvDomain          = "GOOGLE_WORKSPACE_DOMAIN"
)

// OAuth scopes. Authorise exactly these for the service account's client ID
// in the Admin console (Security → API controls → Domain-wide delegation).
var (
	DirectoryScopes = []string{
		"https://www.googleapis.com/auth/admin.directory.user",
		"https://www.googleapis.com/auth/admin.directory.group",
	}
	GmailScopes = []string{"https://www.googleapis.com/auth/gmail.modify"}
)

// Default API endpoints.
const (
	DefaultDirectoryURL = "https://admin.googleapis.com/admin/directory/v1"
	DefaultGmailURL     = "https://gmail.googleapis.com/gmail/v1"
)

// ErrNotConfigured is returned by FromEnv when none of the variables is set:
// the module runs without Google Workspace.
var ErrNotConfigured = errors.New("Google Workspace is not configured")

// Config is the connection to one Workspace domain.
type Config struct {
	// CredentialsFile is the service-account key JSON
	// (GOOGLE_WORKSPACE_CREDENTIALS_FILE). Mount it from a Secret.
	CredentialsFile string
	// AdminEmail is the Workspace admin that Directory calls impersonate
	// (GOOGLE_WORKSPACE_ADMIN_EMAIL).
	AdminEmail string
	// Domain is the primary domain (GOOGLE_WORKSPACE_DOMAIN). Users and
	// groups outside it are refused.
	Domain string
}

// FromEnv reads the three variables. All empty gives ErrNotConfigured;
// only some set is an error.
func FromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		CredentialsFile: strings.TrimSpace(getenv(EnvCredentialsFile)),
		AdminEmail:      strings.TrimSpace(getenv(EnvAdminEmail)),
		Domain:          strings.ToLower(strings.TrimSpace(getenv(EnvDomain))),
	}
	if c.CredentialsFile == "" && c.AdminEmail == "" && c.Domain == "" {
		return c, ErrNotConfigured
	}
	return c, c.Validate()
}

// Validate checks that every field is set and AdminEmail is in Domain.
func (c Config) Validate() error {
	var missing []string
	if c.CredentialsFile == "" {
		missing = append(missing, EnvCredentialsFile)
	}
	if c.AdminEmail == "" {
		missing = append(missing, EnvAdminEmail)
	}
	if c.Domain == "" {
		missing = append(missing, EnvDomain)
	}
	if len(missing) > 0 {
		return fmt.Errorf("Google Workspace needs %s", strings.Join(missing, ", "))
	}
	if !c.InDomain(c.AdminEmail) {
		return fmt.Errorf("%s %q is not in %s %q", EnvAdminEmail, c.AdminEmail, EnvDomain, c.Domain)
	}
	return nil
}

// InDomain reports whether email belongs to Domain (subdomains excluded).
func (c Config) InDomain(email string) bool {
	_, d, ok := strings.Cut(strings.ToLower(strings.TrimSpace(email)), "@")
	return ok && d != "" && d == strings.ToLower(c.Domain)
}

// HTTPClientFunc returns an HTTP client whose requests are authorised as
// subject with scopes.
type HTTPClientFunc func(ctx context.Context, subject string, scopes []string) (*http.Client, error)

// Options tune a Client; the zero value talks to Google.
type Options struct {
	DirectoryURL string
	GmailURL     string
	// HTTPClient replaces the service-account sign-in (tests, proxies).
	HTTPClient HTTPClientFunc
}

// Directory is the Admin SDK Directory API surface modules use.
type Directory interface {
	GetUser(ctx context.Context, userKey string) (*User, error)
	CreateUser(ctx context.Context, u NewUser) (*User, error)
	SetSuspended(ctx context.Context, userKey string, suspended bool) (*User, error)
	DeleteUser(ctx context.Context, userKey string) error

	ListGroups(ctx context.Context) ([]Group, error)
	GetGroup(ctx context.Context, groupKey string) (*Group, error)
	CreateGroup(ctx context.Context, g Group) (*Group, error)
	DeleteGroup(ctx context.Context, groupKey string) error
	ListMembers(ctx context.Context, groupKey string) ([]Member, error)
	AddMember(ctx context.Context, groupKey string, m Member) (*Member, error)
	RemoveMember(ctx context.Context, groupKey, memberKey string) error
}

// Gmail is the Gmail API surface modules use. mailbox is the address of the
// mailbox owner, who is impersonated.
type Gmail interface {
	ListMessages(ctx context.Context, mailbox string, q ListQuery) (*MessageList, error)
	GetMessage(ctx context.Context, mailbox, id string, format Format) (*Message, error)
	SendMessage(ctx context.Context, mailbox string, raw []byte, threadID string) (*Message, error)
	ModifyLabels(ctx context.Context, mailbox, id string, add, remove []string) (*Message, error)
	TrashMessage(ctx context.Context, mailbox, id string) (*Message, error)
}

// Workspace is both.
type Workspace interface {
	Directory
	Gmail
}

// Client implements Workspace against Google's REST APIs.
type Client struct {
	cfg          Config
	directoryURL string
	gmailURL     string
	httpClient   HTTPClientFunc

	mu      sync.Mutex
	clients map[string]*http.Client
}

var _ Workspace = (*Client)(nil)

// New reads cfg.CredentialsFile and returns a Client.
func New(cfg Config, o Options) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if o.HTTPClient == nil {
		key, err := os.ReadFile(cfg.CredentialsFile)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", EnvCredentialsFile, err)
		}
		if o.HTTPClient, err = ServiceAccount(key); err != nil {
			return nil, err
		}
	}
	return newClient(cfg, o), nil
}

func newClient(cfg Config, o Options) *Client {
	c := &Client{cfg: cfg, directoryURL: o.DirectoryURL, gmailURL: o.GmailURL, httpClient: o.HTTPClient, clients: map[string]*http.Client{}}
	if c.directoryURL == "" {
		c.directoryURL = DefaultDirectoryURL
	}
	if c.gmailURL == "" {
		c.gmailURL = DefaultGmailURL
	}
	c.directoryURL = strings.TrimRight(c.directoryURL, "/")
	c.gmailURL = strings.TrimRight(c.gmailURL, "/")
	return c
}

// ServiceAccount returns an HTTPClientFunc that signs in with a
// service-account key (JSON) and impersonates subject through domain-wide
// delegation.
func ServiceAccount(keyJSON []byte) (HTTPClientFunc, error) {
	if _, err := google.JWTConfigFromJSON(keyJSON); err != nil {
		return nil, fmt.Errorf("%s is not a service-account key: %w", EnvCredentialsFile, err)
	}
	return func(ctx context.Context, subject string, scopes []string) (*http.Client, error) {
		jc, err := google.JWTConfigFromJSON(keyJSON, scopes...)
		if err != nil {
			return nil, err
		}
		jc.Subject = subject
		// The client lives as long as the Client; ctx only covers sign-in.
		return jc.Client(context.WithoutCancel(ctx)), nil
	}, nil
}

func (c *Client) clientFor(ctx context.Context, subject string, scopes []string) (*http.Client, error) {
	key := strings.ToLower(subject) + " " + strings.Join(scopes, " ")
	c.mu.Lock()
	defer c.mu.Unlock()
	if hc, ok := c.clients[key]; ok {
		return hc, nil
	}
	hc, err := c.httpClient(ctx, subject, scopes)
	if err != nil {
		return nil, err
	}
	c.clients[key] = hc
	return hc, nil
}

// RandomPassword returns a 24-character password for new accounts that
// sign in through SSO or reset it on first login.
func RandomPassword() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
