// Package desk is the helpdesk service layer: mailboxes, customers,
// conversations made of threads, the IMAP fetcher and the SMTP sender.
// Handlers own authorization (permissions come from the access config);
// this package owns state and invariants and returns typed httperror values
// for business failures.
package desk

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/desk/backend/internal/blob"
	"github.com/AGlElectronics/desk/backend/internal/httperror"
	"github.com/AGlElectronics/desk/backend/internal/netguard"
	"github.com/AGlElectronics/desk/backend/internal/secretbox"
)

// SMTPSettings is the module-wide fallback for mailboxes without their own
// SMTP host.
type SMTPSettings struct {
	Host     string
	Port     int
	Security string
	Username string
	Password string

	moduleWide bool
}

// Options configures a Service.
type Options struct {
	// Keyring seals mailbox passwords (DESK_SECRET_KEY and previous keys).
	Keyring *secretbox.Keyring
	// SMTP is the module-wide fallback server.
	SMTP SMTPSettings
	// Development unlocks plaintext mail connections (APP_ENV=development).
	Development bool
	// AllowedMailHosts may resolve to private addresses
	// (DESK_ALLOWED_MAIL_HOSTS); every other mailbox host may not.
	AllowedMailHosts []string
	// Blobs stores attachments and raw mail (S3).
	Blobs blob.Store
	// Google turns on the Google Workspace connectors (nil: IMAP only).
	Google *GoogleOptions
	// NotifyFrom is the From of Desk's own emails ("" = none are sent).
	NotifyFrom string
	// BaseURL is the web app address used in links in Desk's emails.
	BaseURL string
}

// Service is safe for concurrent use.
type Service struct {
	pool  *pgxpool.Pool
	keys  *secretbox.Keyring
	smtp  SMTPSettings
	dev   bool
	guard *netguard.Guard
	blobs blob.Store
	// google is nil when Google Workspace is not configured.
	google     *GoogleOptions
	notifyFrom string
	baseURL    string
	// fetchInterval is the polling interval back-off starts from.
	fetchInterval time.Duration
	// fetchNow wakes the fetcher early ("Fetch now" in the UI).
	fetchNow chan uuid.UUID
}

// NewService constructs the helpdesk service.
func NewService(pool *pgxpool.Pool, opts Options) *Service {
	if opts.Keyring == nil {
		panic("desk: Options.Keyring is required")
	}
	if opts.Blobs == nil {
		opts.Blobs = blob.NewMemory()
	}
	if opts.Google != nil && opts.Google.Label == "" {
		opts.Google.Label = "Desk"
	}
	return &Service{
		pool: pool, keys: opts.Keyring, smtp: opts.SMTP, dev: opts.Development,
		guard:         netguard.New(opts.AllowedMailHosts),
		blobs:         opts.Blobs,
		google:        opts.Google,
		notifyFrom:    opts.NotifyFrom,
		baseURL:       strings.TrimRight(opts.BaseURL, "/"),
		fetchInterval: time.Minute,
		fetchNow:      make(chan uuid.UUID, 16),
	}
}

// KeyID names the current DESK_SECRET_KEY (safe to log).
func (s *Service) KeyID() string { return s.keys.CurrentKeyID() }

// passwordAAD binds a sealed password to its mailbox and column.
func passwordAAD(mailboxID uuid.UUID, column string) string {
	return "mailbox:" + mailboxID.String() + ":" + column
}

func (s *Service) encrypt(mailboxID uuid.UUID, column, plain string) (string, error) {
	return s.keys.Seal(plain, passwordAAD(mailboxID, column))
}

func (s *Service) decrypt(mailboxID uuid.UUID, column, enc string) (string, error) {
	plain, err := s.keys.Open(enc, passwordAAD(mailboxID, column))
	if err != nil {
		return "", errors.New("the stored password cannot be decrypted (DESK_SECRET_KEY changed without DESK_SECRET_KEY_PREVIOUS?); enter it again")
	}
	return plain, nil
}

// ReencryptResult counts what Reencrypt did.
type ReencryptResult struct {
	Resealed int      `json:"resealed"`
	Current  int      `json:"current"`
	Failed   []string `json:"failed"`
}

// Reencrypt seals every stored mailbox password again under the current
// key, so DESK_SECRET_KEY_PREVIOUS can be removed afterwards. Values no key
// opens are reported and left alone.
func (s *Service) Reencrypt(ctx context.Context) (ReencryptResult, error) {
	res := ReencryptResult{Failed: []string{}}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	rows, err := tx.Query(ctx, `SELECT id, email::text, imap_password_enc, smtp_password_enc FROM mailboxes FOR UPDATE`)
	if err != nil {
		return res, err
	}
	type row struct {
		id         uuid.UUID
		email      string
		imap, smtp string
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.email, &r.imap, &r.smtp); err != nil {
			rows.Close()
			return res, err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	for _, r := range all {
		for _, col := range []struct {
			name string
			enc  string
		}{{"imap_password", r.imap}, {"smtp_password", r.smtp}} {
			if col.enc == "" {
				continue
			}
			if !s.keys.NeedsReseal(col.enc) {
				res.Current++
				continue
			}
			plain, err := s.keys.Open(col.enc, passwordAAD(r.id, col.name))
			if err != nil {
				res.Failed = append(res.Failed, r.email+" ("+col.name+")")
				continue
			}
			sealed, err := s.encrypt(r.id, col.name, plain)
			if err != nil {
				return res, err
			}
			if _, err := tx.Exec(ctx, `UPDATE mailboxes SET `+col.name+`_enc = $2 WHERE id = $1`, r.id, sealed); err != nil {
				return res, err
			}
			res.Resealed++
		}
	}
	return res, tx.Commit(ctx)
}

// ----------------------------------------------------------------------------
// Mailboxes
// ----------------------------------------------------------------------------

// Mailbox is one shared support address. Passwords are never returned.
type Mailbox struct {
	ID              uuid.UUID  `json:"id"`
	OrganizationID  uuid.UUID  `json:"organization_id"`
	Name            string     `json:"name"`
	Email           string     `json:"email"`
	Signature       string     `json:"signature"`
	IMAPHost        string     `json:"imap_host"`
	IMAPPort        int        `json:"imap_port"`
	IMAPSecurity    string     `json:"imap_security"`
	IMAPUsername    string     `json:"imap_username"`
	IMAPPasswordSet bool       `json:"imap_password_set"`
	SMTPHost        string     `json:"smtp_host"`
	SMTPPort        int        `json:"smtp_port"`
	SMTPSecurity    string     `json:"smtp_security"`
	SMTPUsername    string     `json:"smtp_username"`
	SMTPPasswordSet bool       `json:"smtp_password_set"`
	IMAPMoveTo      string     `json:"imap_move_to"`
	LastFetchAt     *time.Time `json:"last_fetch_at,omitempty"`
	LastFetchError  string     `json:"last_fetch_error"`
	FetchFailures   int        `json:"fetch_failures"`
	NextFetchAt     *time.Time `json:"next_fetch_at,omitempty"`
	// Connector: imap | google_user | google_group (docs/adr/0001).
	Connector     string     `json:"connector"`
	ProviderState string     `json:"provider_state"`
	ProviderError string     `json:"provider_error"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`
	PurgeAfter    *time.Time `json:"purge_after,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`

	imapPasswordEnc string
	smtpPasswordEnc string
	lastUID         uint32
	uidValidity     uint32
}

// FolderCounts are the sidebar numbers for one mailbox, from the caller's
// point of view ("mine").
type FolderCounts struct {
	Unassigned int `json:"unassigned"`
	Mine       int `json:"mine"`
	Assigned   int `json:"assigned"`
	Pending    int `json:"pending"`
	Closed     int `json:"closed"`
	Spam       int `json:"spam"`
}

// MailboxWithCounts is a mailbox in the sidebar.
type MailboxWithCounts struct {
	Mailbox
	Counts FolderCounts `json:"counts"`
}

const mailboxCols = `id, organization_id, name, email::text, signature,
	imap_host, imap_port, imap_security, imap_username, imap_password_enc,
	smtp_host, smtp_port, smtp_security, smtp_username, smtp_password_enc,
	imap_move_to, last_fetch_at, last_fetch_error, fetch_failures, next_fetch_at,
	connector, provider_state, provider_error, deleted_at, purge_after,
	created_at, last_uid, uid_validity`

func scanMailbox(row pgx.Row) (Mailbox, error) {
	var m Mailbox
	var lastUID, uidValidity int64
	err := row.Scan(&m.ID, &m.OrganizationID, &m.Name, &m.Email, &m.Signature,
		&m.IMAPHost, &m.IMAPPort, &m.IMAPSecurity, &m.IMAPUsername, &m.imapPasswordEnc,
		&m.SMTPHost, &m.SMTPPort, &m.SMTPSecurity, &m.SMTPUsername, &m.smtpPasswordEnc,
		&m.IMAPMoveTo, &m.LastFetchAt, &m.LastFetchError, &m.FetchFailures, &m.NextFetchAt,
		&m.Connector, &m.ProviderState, &m.ProviderError, &m.DeletedAt, &m.PurgeAfter,
		&m.CreatedAt, &lastUID, &uidValidity)
	m.IMAPPasswordSet = m.imapPasswordEnc != ""
	m.SMTPPasswordSet = m.smtpPasswordEnc != ""
	m.lastUID = uint32(lastUID)
	m.uidValidity = uint32(uidValidity)
	return m, err
}

// GetMailbox loads one mailbox or a typed 404.
func (s *Service) GetMailbox(ctx context.Context, id uuid.UUID) (Mailbox, error) {
	m, err := scanMailbox(s.pool.QueryRow(ctx, `SELECT `+mailboxCols+` FROM mailboxes WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Mailbox{}, httperror.NotFound("mailbox")
	}
	return m, err
}

// ListMailboxes returns every mailbox with folder counts for userID.
func (s *Service) ListMailboxes(ctx context.Context, userID uuid.UUID) ([]MailboxWithCounts, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+mailboxCols+` FROM mailboxes WHERE deleted_at IS NULL ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var boxes []Mailbox
	for rows.Next() {
		m, err := scanMailbox(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		boxes = append(boxes, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]MailboxWithCounts, 0, len(boxes))
	for _, m := range boxes {
		var c FolderCounts
		err := s.pool.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE status = 'active' AND assignee_id IS NULL),
			       count(*) FILTER (WHERE status IN ('active', 'pending') AND assignee_id = $2),
			       count(*) FILTER (WHERE status IN ('active', 'pending') AND assignee_id IS NOT NULL),
			       count(*) FILTER (WHERE status = 'pending'),
			       count(*) FILTER (WHERE status = 'closed'),
			       count(*) FILTER (WHERE status = 'spam')
			  FROM conversations WHERE mailbox_id = $1
		`, m.ID, userID).Scan(&c.Unassigned, &c.Mine, &c.Assigned, &c.Pending, &c.Closed, &c.Spam)
		if err != nil {
			return nil, err
		}
		out = append(out, MailboxWithCounts{Mailbox: m, Counts: c})
	}
	return out, nil
}

// MailboxInput creates or updates a mailbox. Nil fields are left unchanged
// on update; a nil password keeps the stored one, "" clears it.
type MailboxInput struct {
	// Connector is set on create only: imap (default), google_user or
	// google_group.
	Connector    *string `json:"connector"`
	Name         *string `json:"name"`
	Email        *string `json:"email"`
	Signature    *string `json:"signature"`
	IMAPHost     *string `json:"imap_host"`
	IMAPPort     *int    `json:"imap_port"`
	IMAPSecurity *string `json:"imap_security"`
	IMAPUsername *string `json:"imap_username"`
	IMAPPassword *string `json:"imap_password"`
	IMAPMoveTo   *string `json:"imap_move_to"`
	SMTPHost     *string `json:"smtp_host"`
	SMTPPort     *int    `json:"smtp_port"`
	SMTPSecurity *string `json:"smtp_security"`
	SMTPUsername *string `json:"smtp_username"`
	SMTPPassword *string `json:"smtp_password"`
}

func (s *Service) checkSecurity(v *string) string {
	if v == nil {
		return ""
	}
	switch *v {
	case "tls", "starttls":
		return ""
	case "none":
		if s.dev {
			return ""
		}
		return "plaintext (none) is only allowed in development; use tls or starttls"
	}
	return "must be tls or starttls"
}

func checkPort(v *int) string {
	if v != nil && (*v < 1 || *v > 65535) {
		return "must be between 1 and 65535"
	}
	return ""
}

func (s *Service) validateMailbox(in *MailboxInput, create bool) map[string]string {
	issues := map[string]string{}
	set := func(field, msg string) {
		if msg != "" {
			issues[field] = msg
		}
	}
	if create || in.Name != nil {
		if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
			issues["name"] = "required"
		}
	}
	if create || in.Email != nil {
		if in.Email == nil {
			issues["email"] = "required"
		} else if a, err := mail.ParseAddress(strings.TrimSpace(*in.Email)); err != nil || a.Name != "" {
			issues["email"] = "must be a plain email address"
		}
	}
	if in.Connector != nil && !create {
		issues["connector"] = "cannot be changed; create a new mailbox instead"
	}
	connector := ConnectorIMAP
	if in.Connector != nil {
		connector = *in.Connector
	}
	switch connector {
	case ConnectorIMAP:
	case ConnectorGoogleUser, ConnectorGoogleGroup:
		switch {
		case s.google == nil:
			issues["connector"] = "Google Workspace is not configured (GOOGLE_WORKSPACE_DOMAIN)"
		case connector == ConnectorGoogleGroup && s.google.SharedMailbox == "":
			issues["connector"] = "group mailboxes need GOOGLE_SHARED_MAILBOX"
		case in.Email != nil && !strings.HasSuffix(strings.ToLower(strings.TrimSpace(*in.Email)), "@"+s.google.Domain):
			issues["email"] = "must be an address in " + s.google.Domain
		case in.Email != nil && s.google.SharedMailbox != "" && strings.EqualFold(strings.TrimSpace(*in.Email), s.google.SharedMailbox):
			issues["email"] = "is the shared account itself; choose another address"
		}
		for field, v := range map[string]bool{
			"imap_host": in.IMAPHost != nil, "imap_username": in.IMAPUsername != nil, "imap_password": in.IMAPPassword != nil,
			"smtp_host": in.SMTPHost != nil, "smtp_username": in.SMTPUsername != nil, "smtp_password": in.SMTPPassword != nil,
		} {
			if v && create {
				issues[field] = "not used by Google Workspace mailboxes"
			}
		}
	default:
		issues["connector"] = "must be imap, google_user or google_group"
	}
	set("imap_security", s.checkSecurity(in.IMAPSecurity))
	set("smtp_security", s.checkSecurity(in.SMTPSecurity))
	set("imap_port", checkPort(in.IMAPPort))
	set("smtp_port", checkPort(in.SMTPPort))
	for field, host := range map[string]*string{"imap_host": in.IMAPHost, "smtp_host": in.SMTPHost} {
		if host != nil && strings.ContainsAny(strings.TrimSpace(*host), " /:@") {
			issues[field] = "must be a host name or address without port or scheme"
		}
	}
	return issues
}

// changed reports whether a non-nil input differs from the current value.
func changed(in *string, current string) bool {
	return in != nil && !strings.EqualFold(strings.TrimSpace(*in), current)
}

// CreateMailbox stores a new mailbox in orgID.
func (s *Service) CreateMailbox(ctx context.Context, orgID uuid.UUID, in MailboxInput) (Mailbox, error) {
	if issues := s.validateMailbox(&in, true); len(issues) > 0 {
		return Mailbox{}, httperror.Validation("invalid mailbox", issues)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Mailbox{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	connector, state := ConnectorIMAP, ""
	if in.Connector != nil && *in.Connector != ConnectorIMAP {
		connector, state = *in.Connector, "provisioning"
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO mailboxes (organization_id, name, email, connector, provider_state) VALUES ($1, $2, $3, $4, $5) RETURNING id
	`, orgID, strings.TrimSpace(*in.Name), strings.ToLower(strings.TrimSpace(*in.Email)), connector, state).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return Mailbox{}, httperror.Conflict("a mailbox with this email address already exists (also in Desk's trash)")
		}
		return Mailbox{}, err
	}
	in.Name, in.Email, in.Connector = nil, nil, nil
	if err := s.applyMailboxInput(ctx, tx, Mailbox{ID: id}, in); err != nil {
		return Mailbox{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Mailbox{}, err
	}
	mb, err := s.GetMailbox(ctx, id)
	if err != nil || connector == ConnectorIMAP {
		return mb, err
	}
	// The row exists first, so a failed Google call leaves a mailbox the
	// admin can see, fix and provision again.
	return s.Provision(ctx, mb)
}

// UpdateMailbox applies the non-nil fields of in.
//
// Moving a mailbox to another IMAP or SMTP server or account needs its
// password in the same request: Desk never sends a stored password to a
// host it was not entered for.
func (s *Service) UpdateMailbox(ctx context.Context, id uuid.UUID, in MailboxInput) (Mailbox, error) {
	if issues := s.validateMailbox(&in, false); len(issues) > 0 {
		return Mailbox{}, httperror.Validation("invalid mailbox", issues)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Mailbox{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	cur, err := scanMailbox(tx.QueryRow(ctx, `SELECT `+mailboxCols+` FROM mailboxes WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Mailbox{}, httperror.NotFound("mailbox")
	}
	if err != nil {
		return Mailbox{}, err
	}
	issues := map[string]string{}
	if (changed(in.IMAPHost, cur.IMAPHost) || changed(in.IMAPUsername, cur.IMAPUsername)) && cur.imapPasswordEnc != "" && in.IMAPPassword == nil {
		issues["imap_password"] = "enter the password again when the IMAP server or username changes"
	}
	if (changed(in.SMTPHost, cur.SMTPHost) || changed(in.SMTPUsername, cur.SMTPUsername)) && cur.smtpPasswordEnc != "" && in.SMTPPassword == nil {
		issues["smtp_password"] = "enter the password again when the SMTP server or username changes"
	}
	if len(issues) > 0 {
		return Mailbox{}, httperror.Validation("password required", issues)
	}
	if err := s.applyMailboxInput(ctx, tx, cur, in); err != nil {
		return Mailbox{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Mailbox{}, err
	}
	return s.GetMailbox(ctx, id)
}

func (s *Service) applyMailboxInput(ctx context.Context, tx pgx.Tx, cur Mailbox, in MailboxInput) error {
	id := cur.ID
	sets := []string{}
	args := []any{id}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	trim := func(p *string) string { return strings.TrimSpace(*p) }
	if in.Name != nil {
		add("name", trim(in.Name))
	}
	if in.Email != nil {
		add("email", trim(in.Email))
	}
	if in.Signature != nil {
		add("signature", *in.Signature)
	}
	if in.IMAPHost != nil {
		add("imap_host", strings.ToLower(trim(in.IMAPHost)))
	}
	if in.IMAPPort != nil {
		add("imap_port", *in.IMAPPort)
	}
	if in.IMAPSecurity != nil {
		add("imap_security", *in.IMAPSecurity)
	}
	if in.IMAPUsername != nil {
		add("imap_username", trim(in.IMAPUsername))
	}
	if in.IMAPPassword != nil {
		enc, err := s.encrypt(id, "imap_password", *in.IMAPPassword)
		if err != nil {
			return err
		}
		add("imap_password_enc", enc)
	}
	if in.IMAPMoveTo != nil {
		add("imap_move_to", trim(in.IMAPMoveTo))
	}
	if in.SMTPHost != nil {
		add("smtp_host", strings.ToLower(trim(in.SMTPHost)))
	}
	if in.SMTPPort != nil {
		add("smtp_port", *in.SMTPPort)
	}
	if in.SMTPSecurity != nil {
		add("smtp_security", *in.SMTPSecurity)
	}
	if in.SMTPUsername != nil {
		add("smtp_username", trim(in.SMTPUsername))
	}
	if in.SMTPPassword != nil {
		enc, err := s.encrypt(id, "smtp_password", *in.SMTPPassword)
		if err != nil {
			return err
		}
		add("smtp_password_enc", enc)
	}
	if changed(in.IMAPHost, cur.IMAPHost) || changed(in.IMAPUsername, cur.IMAPUsername) {
		// A different server or account: start from its first message.
		sets = append(sets, "last_uid = 0", "uid_validity = 0")
	}
	if in.IMAPHost != nil || in.IMAPPort != nil || in.IMAPSecurity != nil || in.IMAPUsername != nil || in.IMAPPassword != nil {
		// New settings deserve a try right away, not after the back-off.
		sets = append(sets, "fetch_failures = 0", "next_fetch_at = NULL")
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = now()")
	if _, err := tx.Exec(ctx, `UPDATE mailboxes SET `+strings.Join(sets, ", ")+` WHERE id = $1`, args...); err != nil {
		if isUniqueViolation(err) {
			return httperror.Conflict("a mailbox with this email address already exists")
		}
		return err
	}
	return nil
}

// RequestFetch wakes the fetcher for one mailbox.
func (s *Service) RequestFetch(id uuid.UUID) {
	select {
	case s.fetchNow <- id:
	default:
	}
}

// ----------------------------------------------------------------------------
// People
// ----------------------------------------------------------------------------

// Person is a users row projected for the assignee picker.
type Person struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email"`
}

// ListPeople returns everyone who has signed in to Desk (assignable agents).
func (s *Service) ListPeople(ctx context.Context) ([]Person, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, display_name, COALESCE(email, '') FROM users ORDER BY display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Person{}
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.DisplayName, &p.Email); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DefaultOrganization returns the first organization (single-org slice).
func (s *Service) DefaultOrganization(ctx context.Context) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM organizations ORDER BY slug LIMIT 1`).Scan(&id)
	return id, err
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "SQLSTATE 23505")
}
