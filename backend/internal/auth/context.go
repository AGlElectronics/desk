// Package auth manages the authenticated principal attached to each request
// and exposes RBAC helpers. It ships with a dev bypass (AUTH_MODE=dev) and a
// verified-OIDC path (AUTH_MODE=oidc); authorization decisions use the
// access config loaded at startup.
//
// Desk's scopes are simpler than Rally's: roles are organization-wide (no
// edition scoping). The permission catalog answers "may this person use the
// feature at all, in this organization?".
package auth

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"
)

// Membership records that a user holds one role organization-wide. A user
// may hold many memberships; permission checks iterate them.
type Membership struct {
	OrganizationID uuid.UUID
	Role           string
}

// User is the authenticated principal for a single HTTP request. It is
// constructed per-request by the Resolver and attached to the context.
type User struct {
	ID          uuid.UUID
	Issuer      string
	Subject     string
	Email       string
	DisplayName string
	Memberships []Membership

	// Permissions holds the union of every role's permissions across every
	// membership; useful for coarse "is this feature visible?" checks.
	Permissions map[string]struct{}

	// OrgPermissions holds the permissions the user has when operating on
	// resources owned by a specific organization.
	OrgPermissions map[uuid.UUID]map[string]struct{}
}

// HasAny returns true if the user holds `perm` anywhere (any org). Use for
// navigation gating; never for authorization decisions on a specific
// resource.
func (u *User) HasAny(perm string) bool {
	if u == nil {
		return false
	}
	_, ok := u.Permissions[perm]
	return ok
}

// HasInOrg returns true if the user holds `perm` for a specific
// organization. Resource handlers resolve the owning org from the resource
// row (never the request body) and pass it here.
func (u *User) HasInOrg(perm string, orgID uuid.UUID) bool {
	if u == nil {
		return false
	}
	if perms, ok := u.OrgPermissions[orgID]; ok {
		if _, ok := perms[perm]; ok {
			return true
		}
	}
	return false
}

// EffectivePermissions returns the sorted union of every grant. /api/v1/me
// exposes this so the SPA can gate navigation; precise enforcement always
// happens server-side via HasInOrg.
func (u *User) EffectivePermissions() []string {
	if u == nil {
		return nil
	}
	out := make([]string, 0, len(u.Permissions))
	for p := range u.Permissions {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

type ctxKey int

const userKey ctxKey = 0

// WithUser stores the authenticated user on the context.
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// FromContext returns the authenticated user, if any.
func FromContext(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(userKey).(*User)
	if !ok || u == nil {
		return nil, false
	}
	return u, true
}

// MustFromContext returns the user or an error if the request is
// unauthenticated. Handlers behind the auth middleware can rely on this.
func MustFromContext(ctx context.Context) (*User, error) {
	u, ok := FromContext(ctx)
	if !ok {
		return nil, errors.New("no authenticated user on context")
	}
	return u, nil
}
