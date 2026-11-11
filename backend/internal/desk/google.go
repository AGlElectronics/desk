package desk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/desk/backend/internal/httperror"
	"github.com/AGlElectronics/desk/backend/internal/mailprovider"
)

// Mailbox connectors (docs/adr/0001).
const (
	ConnectorIMAP        = "imap"
	ConnectorGoogleUser  = "google_user"
	ConnectorGoogleGroup = "google_group"
)

// GoogleOptions turns on the Google Workspace connectors.
type GoogleOptions struct {
	Provider mailprovider.Provider
	// Domain is GOOGLE_WORKSPACE_DOMAIN; mailbox addresses must be in it.
	Domain string
	// SharedMailbox is the licensed account group mailboxes deliver into
	// (GOOGLE_SHARED_MAILBOX); empty: only user mailboxes.
	SharedMailbox string
	// Label marks handled mail in Gmail (GOOGLE_LABEL, default "Desk").
	Label string
}

// IsGoogle reports whether the mailbox lives in Google Workspace.
func (m Mailbox) IsGoogle() bool {
	return m.Connector == ConnectorGoogleUser || m.Connector == ConnectorGoogleGroup
}

func (s *Service) providerRef(mb Mailbox) mailprovider.Mailbox {
	ref := mailprovider.Mailbox{Kind: mailprovider.KindUser, Address: mb.Email, DisplayName: mb.Name}
	if mb.Connector == ConnectorGoogleGroup {
		ref.Kind = mailprovider.KindGroup
		ref.DeliverTo = s.google.SharedMailbox
	}
	return ref
}

// providerUser is the Workspace account whose mail Desk reads and sends
// for mb: the mailbox itself, or the shared account for group mailboxes.
func (s *Service) providerUser(mb Mailbox) string {
	if mb.Connector == ConnectorGoogleGroup {
		return s.google.SharedMailbox
	}
	return mb.Email
}

func (s *Service) googleReady(mb Mailbox) error {
	if !mb.IsGoogle() {
		return nil
	}
	if s.google == nil {
		return displayError{msg: "Google Workspace is not configured on this Desk (GOOGLE_WORKSPACE_DOMAIN)"}
	}
	return nil
}

// Provision creates the mailbox in Google Workspace (again, after an
// error). An address that already exists there is taken over as it is.
func (s *Service) Provision(ctx context.Context, mb Mailbox) (Mailbox, error) {
	if !mb.IsGoogle() {
		return mb, httperror.BadRequest("only Google Workspace mailboxes are provisioned")
	}
	if err := s.googleReady(mb); err != nil {
		return mb, httperror.Conflict(err.Error())
	}
	state, msg := "active", ""
	err := s.google.Provider.CreateMailbox(ctx, s.providerRef(mb))
	switch {
	case err == nil:
	case errors.Is(err, mailprovider.ErrExists):
		msg = "the address already existed in Google Workspace; Desk uses it as it is"
	default:
		state, msg = "error", "could not create the mailbox in Google Workspace: "+providerMessage(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE mailboxes SET provider_state = $2, provider_error = $3, updated_at = now() WHERE id = $1`,
		mb.ID, state, msg); err != nil {
		return mb, err
	}
	return s.GetMailbox(ctx, mb.ID)
}

// providerMessage keeps provider errors short and free of credentials.
func providerMessage(err error) string {
	switch {
	case errors.Is(err, mailprovider.ErrNotFound):
		return "not found"
	case errors.Is(err, mailprovider.ErrSendAsDenied):
		return "the shared account may not send as this address"
	}
	return truncate(err.Error(), 300)
}

// fetchGoogleUser imports unhandled inbox mail of a google_user mailbox
// and labels each handled message.
func (s *Service) fetchGoogleUser(ctx context.Context, mb Mailbox, log zerolog.Logger) (int, error) {
	if err := s.googleReady(mb); err != nil {
		return 0, err
	}
	p := s.google.Provider
	ids, err := p.ListUnhandled(ctx, mb.Email, s.google.Label, maxPerFetch)
	if err != nil {
		return 0, displayError{msg: "could not read the mailbox in Google Workspace: " + providerMessage(err), err: err}
	}
	imported := 0
	for _, id := range ids {
		raw, err := p.Get(ctx, mb.Email, id)
		if err != nil {
			return imported, fmt.Errorf("get message: %w", err)
		}
		ok, err := s.ingestProvider(ctx, mb, raw, id, log)
		if err != nil {
			return imported, err
		}
		if ok {
			imported++
		}
		if err := p.AddLabel(ctx, mb.Email, id, s.google.Label); err != nil {
			return imported, fmt.Errorf("label message: %w", err)
		}
	}
	return imported, nil
}

func (s *Service) ingestProvider(ctx context.Context, mb Mailbox, raw []byte, providerID string, log zerolog.Logger) (bool, error) {
	return s.ingest(ctx, mb, raw, "gmail:"+providerID, providerID, log)
}

// Integrations is what the settings page shows about mail providers and
// notifications.
type Integrations struct {
	Google *GoogleInfo `json:"google"`
	// Notifications: Desk can send its own emails (NOTIFY_FROM set and a
	// way to send them).
	Notifications bool   `json:"notifications"`
	NotifyFrom    string `json:"notify_from"`
	ModuleSMTP    bool   `json:"module_smtp"`
}

// GoogleInfo describes the Google Workspace connector.
type GoogleInfo struct {
	Domain        string       `json:"domain"`
	SharedMailbox string       `json:"shared_mailbox"`
	Label         string       `json:"label"`
	Shared        SharedStatus `json:"shared_status"`
}

// Integrations reports the configured connectors.
func (s *Service) Integrations(ctx context.Context) Integrations {
	out := Integrations{Notifications: s.NotificationsEnabled(), NotifyFrom: s.notifySender(), ModuleSMTP: s.moduleSMTPHost() != ""}
	if s.google != nil {
		out.Google = &GoogleInfo{Domain: s.google.Domain, SharedMailbox: s.google.SharedMailbox, Label: s.google.Label, Shared: s.sharedStatus(ctx)}
	}
	return out
}

// SharedStatus is the state of the shared account's polling.
type SharedStatus struct {
	LastFetchAt *time.Time `json:"last_fetch_at,omitempty"`
	Error       string     `json:"error"`
	Unrouted    int        `json:"unrouted"`
}

// fetchShared reads the shared account once for every group mailbox and
// routes each message by recipient address (Delivered-To, X-Original-To,
// To, Cc). Mail for no known mailbox is labelled "<label>/Unrouted".
func (s *Service) fetchShared(ctx context.Context, log zerolog.Logger) {
	if s.google == nil || s.google.SharedMailbox == "" {
		return
	}
	var groups int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM mailboxes WHERE connector = 'google_group' AND deleted_at IS NULL`).Scan(&groups); err != nil || groups == 0 {
		return
	}
	st := s.sharedStatus(ctx)
	unrouted, err := s.fetchSharedOnce(ctx, log)
	if ctx.Err() != nil {
		return
	}
	now := time.Now()
	st.LastFetchAt, st.Error = &now, ""
	st.Unrouted += unrouted
	if err != nil {
		log.Warn().Err(err).Str("account", s.google.SharedMailbox).Msg("shared account fetch failed")
		st.Error = describeMailError(err)
	}
	_ = s.putSetting(ctx, "google_shared_status", st, nil)
}

func (s *Service) fetchSharedOnce(ctx context.Context, log zerolog.Logger) (int, error) {
	p, shared := s.google.Provider, s.google.SharedMailbox
	ids, err := p.ListUnhandled(ctx, shared, s.google.Label, maxPerFetch)
	if err != nil {
		return 0, displayError{msg: "could not read the shared account: " + providerMessage(err), err: err}
	}
	unrouted := 0
	for _, id := range ids {
		raw, err := p.Get(ctx, shared, id)
		if err != nil {
			return unrouted, fmt.Errorf("get message: %w", err)
		}
		mb, found, err := s.routeShared(ctx, raw)
		if err != nil {
			return unrouted, err
		}
		if !found {
			unrouted++
			if err := p.AddLabel(ctx, shared, id, s.google.Label+"/Unrouted"); err != nil {
				return unrouted, err
			}
		} else if _, err := s.ingestProvider(ctx, mb, raw, id, log); err != nil {
			return unrouted, err
		}
		if err := p.AddLabel(ctx, shared, id, s.google.Label); err != nil {
			return unrouted, err
		}
		if found {
			_, _ = s.pool.Exec(ctx, `UPDATE mailboxes SET last_fetch_at = now(), last_fetch_error = '' WHERE id = $1`, mb.ID)
		}
	}
	return unrouted, nil
}

// routeShared finds the group mailbox a message in the shared account was
// sent to.
func (s *Service) routeShared(ctx context.Context, raw []byte) (Mailbox, bool, error) {
	p, err := ParseMessage(raw)
	if err != nil {
		return Mailbox{}, false, nil
	}
	var candidates []string
	for _, list := range [][]string{p.DeliveredTo, p.To, p.CC} {
		for _, a := range list {
			if a != "" && !strings.EqualFold(a, s.google.SharedMailbox) {
				candidates = append(candidates, strings.ToLower(a))
			}
		}
	}
	if len(candidates) == 0 {
		return Mailbox{}, false, nil
	}
	mb, err := scanMailbox(s.pool.QueryRow(ctx, `
		SELECT `+mailboxCols+` FROM mailboxes
		 WHERE connector = 'google_group' AND deleted_at IS NULL AND lower(email::text) = ANY($1)
		 ORDER BY array_position($1, lower(email::text)) LIMIT 1`, candidates))
	if errors.Is(err, pgx.ErrNoRows) {
		return Mailbox{}, false, nil
	}
	if err != nil {
		return Mailbox{}, false, err
	}
	return mb, true, nil
}

func (s *Service) sharedStatus(ctx context.Context) SharedStatus {
	var st SharedStatus
	_ = s.getSetting(ctx, "google_shared_status", &st)
	return st
}

// googleTransport sends through Gmail as the mailbox (or as its group
// address from the shared account).
func (s *Service) googleTransport(ctx context.Context, mb Mailbox, raw []byte) (string, error) {
	if err := s.googleReady(mb); err != nil {
		return "", err
	}
	id, err := s.google.Provider.Send(ctx, s.providerUser(mb), raw)
	if err != nil {
		return "", displayError{msg: "Google Workspace refused the message: " + providerMessage(err), err: err}
	}
	return id, nil
}

// trashProviderMessages moves messages to Gmail's trash (or back). Errors
// are collected, not fatal: Desk's own trash is the source of truth.
func (s *Service) trashProviderMessages(ctx context.Context, mb Mailbox, ids []string, trash bool) error {
	if !mb.IsGoogle() || s.google == nil || len(ids) == 0 {
		return nil
	}
	user := s.providerUser(mb)
	var errs []error
	for _, id := range ids {
		var err error
		if trash {
			err = s.google.Provider.Trash(ctx, user, id)
		} else {
			err = s.google.Provider.Untrash(ctx, user, id)
		}
		if err != nil && !errors.Is(err, mailprovider.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) getSetting(ctx context.Context, key string, dst any) error {
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

func (s *Service) putSetting(ctx context.Context, key string, v any, actor *uuid.UUID) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO settings (key, value, updated_at, updated_by) VALUES ($1, $2, now(), $3)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now(), updated_by = EXCLUDED.updated_by`,
		key, raw, actor)
	return err
}
