package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/desk/backend/internal/domain"
)

// Resolver turns an authenticated external identity (OIDC claims, or a dev
// bypass header) into a *User backed by Desk's users table.
//
// Identity invariants (architecture.md §4.4):
//
//   - The (issuer, subject) pair is the stable identity key. Email and
//     display name are refreshed every request; never used as join keys.
//   - A user existing with the same email as an incoming OIDC login is
//     NEVER automatically linked: a never-seen-before (issuer, subject)
//     pair always creates a new local user.
//
// Authority is never persisted: the token's IdP groups map to roles through
// the access config bindings on every request, so removing someone from a
// group revokes the role on their next request.
type Resolver struct {
	pool   *pgxpool.Pool
	access *AccessConfig
	orgIDs sync.Map // organization slug -> uuid.UUID
}

// NewResolver constructs a Resolver backed by the given pool and access
// config catalog.
func NewResolver(pool *pgxpool.Pool, access *AccessConfig) *Resolver {
	return &Resolver{pool: pool, access: access}
}

// LoadOrProvisionDevUser is used by the dev bypass middleware. The user
// header becomes the OIDC subject under the reserved "dev" issuer, and the
// groups go through exactly the same access-config binding lookup as OIDC
// token groups, so dev mode exercises the real authorization path.
func (r *Resolver) LoadOrProvisionDevUser(ctx context.Context, name string, groups []string) (*User, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "dev-admin"
	}

	display := displayFromDevHeader(name)
	email := name + "@desk.local"

	memberships, err := r.groupMemberships(ctx, groups)
	if err != nil {
		return nil, fmt.Errorf("resolve dev group bindings: %w", err)
	}
	norm := NormalizeGroups(groups)
	userID, err := r.upsertUser(ctx, "dev", name, display, &email, norm, r.isAdmin(memberships))
	if err != nil {
		return nil, fmt.Errorf("provision dev user: %w", err)
	}
	u := r.buildUser(userID, "dev", name, display, email, memberships)
	u.Groups = norm
	return u, nil
}

// isAdmin: holds mailbox.manage through any membership (Desk admin).
func (r *Resolver) isAdmin(memberships []Membership) bool {
	for _, m := range memberships {
		if _, ok := r.access.PermissionsForRole(m.Role)[domain.PermMailboxManage]; ok {
			return true
		}
	}
	return false
}

// NormalizeGroups lower-cases groups and drops Keycloak's leading "/".
func NormalizeGroups(groups []string) []string {
	out := make([]string, 0, len(groups))
	seen := map[string]bool{}
	for _, g := range groups {
		if n := normalizeGroup(g); n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// LoadOrProvisionOIDCUser is called by the OIDC middleware after a
// successful JWT verification. It upserts the user row, refreshes the
// metadata (display name, email, last login), then returns the request
// principal populated from the access config catalog.
func (r *Resolver) LoadOrProvisionOIDCUser(ctx context.Context, claims *OIDCClaims) (*User, error) {
	if claims == nil || claims.Subject == "" || claims.Issuer == "" {
		return nil, errors.New("oidc claims missing issuer/subject")
	}
	display := displayNameFromClaims(claims)
	var emailPtr *string
	if e := strings.TrimSpace(claims.Email); e != "" {
		emailPtr = &e
	}
	memberships, err := r.groupMemberships(ctx, claims.Groups)
	if err != nil {
		return nil, fmt.Errorf("resolve group bindings: %w", err)
	}
	norm := NormalizeGroups(claims.Groups)
	userID, err := r.upsertUser(ctx, claims.Issuer, claims.Subject, display, emailPtr, norm, r.isAdmin(memberships))
	if err != nil {
		return nil, fmt.Errorf("upsert oidc user: %w", err)
	}

	email := ""
	if emailPtr != nil {
		email = *emailPtr
	}
	u := r.buildUser(userID, claims.Issuer, claims.Subject, display, email, memberships)
	u.Groups = norm
	return u, nil
}

// upsertUser looks up the local user by (issuer, subject). If absent, a
// fresh users row is created. Metadata (display_name, email, last_login_at)
// is refreshed on every call, and so are the groups and the Desk admin
// flag, which background jobs (purge reminders, notifications) read. They
// never grant anything to a request: authorization always uses the token.
func (r *Resolver) upsertUser(
	ctx context.Context,
	issuer, subject, display string,
	email *string,
	groups []string,
	admin bool,
) (uuid.UUID, error) {
	var userID uuid.UUID
	err := r.pool.QueryRow(ctx,
		`INSERT INTO users (issuer, subject, display_name, email, last_login_at, groups, desk_admin)
		 VALUES ($1, $2, $3, $4, now(), $5, $6)
		 ON CONFLICT (issuer, subject) DO UPDATE
		    SET display_name  = EXCLUDED.display_name,
		        email         = COALESCE(EXCLUDED.email, users.email),
		        last_login_at = now(),
		        groups        = EXCLUDED.groups,
		        desk_admin    = EXCLUDED.desk_admin,
		        updated_at    = now()
		 RETURNING id`,
		issuer, subject, display, email, groups, admin,
	).Scan(&userID)
	if err != nil {
		return uuid.Nil, err
	}
	return userID, nil
}

// buildUser assembles the request principal from the identity row and
// group-derived memberships, projecting role → permission grants from the
// access config catalog.
func (r *Resolver) buildUser(
	userID uuid.UUID,
	issuer, subject, display, email string,
	memberships []Membership,
) *User {
	global := map[string]struct{}{}
	orgPerms := map[uuid.UUID]map[string]struct{}{}

	for _, m := range memberships {
		set := r.access.PermissionsForRole(m.Role)
		if set == nil {
			continue
		}
		orgSet, ok := orgPerms[m.OrganizationID]
		if !ok {
			orgSet = map[string]struct{}{}
			orgPerms[m.OrganizationID] = orgSet
		}
		for p := range set {
			global[p] = struct{}{}
			orgSet[p] = struct{}{}
		}
	}

	return &User{
		ID:             userID,
		Issuer:         issuer,
		Subject:        subject,
		Email:          email,
		DisplayName:    display,
		Memberships:    memberships,
		Permissions:    global,
		OrgPermissions: orgPerms,
	}
}

func displayFromDevHeader(h string) string {
	h = strings.TrimSpace(h)
	if h == "" {
		return "Dev User"
	}
	// "dev-admin" → "Dev Admin". Small cosmetic nicety; the identity key
	// stays the raw header value.
	parts := strings.Split(h, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func displayNameFromClaims(c *OIDCClaims) string {
	if c == nil {
		return ""
	}
	for _, s := range []string{c.Nickname, c.Name, c.PreferredUsername, c.Email} {
		if t := strings.TrimSpace(s); t != "" {
			return t
		}
	}
	// Fallback to the subject — never pretty, but always non-empty.
	return c.Subject
}

// groupMemberships turns the token's IdP groups into organization-wide
// memberships via the access config bindings. Nothing is persisted, so
// removing someone from a group revokes the role on their next request.
// Bindings naming an organization slug that does not exist grant nothing.
func (r *Resolver) groupMemberships(ctx context.Context, groups []string) ([]Membership, error) {
	var out []Membership
	for _, b := range r.access.BindingsFor(groups) {
		orgID, ok, err := r.orgIDBySlug(ctx, b.Organization)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, Membership{OrganizationID: orgID, Role: b.Role})
		}
	}
	return out, nil
}

func (r *Resolver) orgIDBySlug(ctx context.Context, slug string) (uuid.UUID, bool, error) {
	if id, ok := r.orgIDs.Load(slug); ok {
		return id.(uuid.UUID), true, nil
	}
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT id FROM organizations WHERE slug = $1`, slug).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	r.orgIDs.Store(slug, id)
	return id, true, nil
}
