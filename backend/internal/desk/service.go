// Package desk is the helpdesk service layer: mailboxes, customers,
// conversations made of threads, the IMAP fetcher and the SMTP sender.
// Handlers own authorization (permissions come from the access config);
// this package owns state and invariants and returns typed httperror values
// for business failures.
package desk

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

// SMTPSettings is the module-wide fallback for mailboxes without their own
// SMTP host.
type SMTPSettings struct {
	Host     string
	Port     int
	Security string
	Username string
	Password string
}

// Service is safe for concurrent use.
type Service struct {
	pool *pgxpool.Pool
	gcm  cipher.AEAD
	smtp SMTPSettings
	// fetchNow wakes the fetcher early ("Fetch now" in the UI).
	fetchNow chan uuid.UUID
}

// NewService constructs the helpdesk service. secretKey encrypts mailbox
// passwords; any length works (it is hashed to an AES-256 key).
func NewService(pool *pgxpool.Pool, secretKey string, smtp SMTPSettings) *Service {
	key := sha256.Sum256([]byte(secretKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		panic(err) // 32-byte key: cannot fail
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return &Service{pool: pool, gcm: gcm, smtp: smtp, fetchNow: make(chan uuid.UUID, 16)}
}

func (s *Service) encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(s.gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

func (s *Service) decrypt(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(raw) < s.gcm.NonceSize() {
		return "", errors.New("stored password is unreadable")
	}
	plain, err := s.gcm.Open(nil, raw[:s.gcm.NonceSize()], raw[s.gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("stored password cannot be decrypted (was DESK_SECRET_KEY changed?)")
	}
	return string(plain), nil
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
	LastFetchAt     *time.Time `json:"last_fetch_at,omitempty"`
	LastFetchError  string     `json:"last_fetch_error"`
	CreatedAt       time.Time  `json:"created_at"`

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
	last_fetch_at, last_fetch_error, created_at, last_uid, uid_validity`

func scanMailbox(row pgx.Row) (Mailbox, error) {
	var m Mailbox
	var lastUID, uidValidity int64
	err := row.Scan(&m.ID, &m.OrganizationID, &m.Name, &m.Email, &m.Signature,
		&m.IMAPHost, &m.IMAPPort, &m.IMAPSecurity, &m.IMAPUsername, &m.imapPasswordEnc,
		&m.SMTPHost, &m.SMTPPort, &m.SMTPSecurity, &m.SMTPUsername, &m.smtpPasswordEnc,
		&m.LastFetchAt, &m.LastFetchError, &m.CreatedAt, &lastUID, &uidValidity)
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
	rows, err := s.pool.Query(ctx, `SELECT `+mailboxCols+` FROM mailboxes ORDER BY name`)
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
	Name         *string `json:"name"`
	Email        *string `json:"email"`
	Signature    *string `json:"signature"`
	IMAPHost     *string `json:"imap_host"`
	IMAPPort     *int    `json:"imap_port"`
	IMAPSecurity *string `json:"imap_security"`
	IMAPUsername *string `json:"imap_username"`
	IMAPPassword *string `json:"imap_password"`
	SMTPHost     *string `json:"smtp_host"`
	SMTPPort     *int    `json:"smtp_port"`
	SMTPSecurity *string `json:"smtp_security"`
	SMTPUsername *string `json:"smtp_username"`
	SMTPPassword *string `json:"smtp_password"`
}

func validSecurity(v *string) bool {
	if v == nil {
		return true
	}
	switch *v {
	case "tls", "starttls", "none":
		return true
	}
	return false
}

func (in *MailboxInput) validate(create bool) error {
	issues := map[string]string{}
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
	if !validSecurity(in.IMAPSecurity) {
		issues["imap_security"] = "must be tls, starttls or none"
	}
	if !validSecurity(in.SMTPSecurity) {
		issues["smtp_security"] = "must be tls, starttls or none"
	}
	if len(issues) > 0 {
		return httperror.Validation("invalid mailbox", issues)
	}
	return nil
}

// CreateMailbox stores a new mailbox in orgID.
func (s *Service) CreateMailbox(ctx context.Context, orgID uuid.UUID, in MailboxInput) (Mailbox, error) {
	if err := in.validate(true); err != nil {
		return Mailbox{}, err
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mailboxes (organization_id, name, email) VALUES ($1, $2, $3) RETURNING id
	`, orgID, strings.TrimSpace(*in.Name), strings.TrimSpace(*in.Email)).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return Mailbox{}, httperror.Conflict("a mailbox with this email address already exists")
		}
		return Mailbox{}, err
	}
	in.Name, in.Email = nil, nil
	return s.UpdateMailbox(ctx, id, in)
}

// UpdateMailbox applies the non-nil fields of in.
func (s *Service) UpdateMailbox(ctx context.Context, id uuid.UUID, in MailboxInput) (Mailbox, error) {
	if err := in.validate(false); err != nil {
		return Mailbox{}, err
	}
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
		add("imap_host", trim(in.IMAPHost))
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
		enc, err := s.encrypt(*in.IMAPPassword)
		if err != nil {
			return Mailbox{}, err
		}
		add("imap_password_enc", enc)
	}
	if in.SMTPHost != nil {
		add("smtp_host", trim(in.SMTPHost))
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
		enc, err := s.encrypt(*in.SMTPPassword)
		if err != nil {
			return Mailbox{}, err
		}
		add("smtp_password_enc", enc)
	}
	if in.IMAPHost != nil || in.IMAPUsername != nil {
		// A different server or account: start from its first message.
		sets = append(sets, "last_uid = 0", "uid_validity = 0")
	}
	if len(sets) > 0 {
		sets = append(sets, "updated_at = now()")
		_, err := s.pool.Exec(ctx, `UPDATE mailboxes SET `+strings.Join(sets, ", ")+` WHERE id = $1`, args...)
		if err != nil {
			if isUniqueViolation(err) {
				return Mailbox{}, httperror.Conflict("a mailbox with this email address already exists")
			}
			return Mailbox{}, err
		}
	}
	return s.GetMailbox(ctx, id)
}

// DeleteMailbox removes a mailbox and every conversation in it.
func (s *Service) DeleteMailbox(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM mailboxes WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httperror.NotFound("mailbox")
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
