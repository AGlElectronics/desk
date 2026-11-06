package auth

import (
	"context"

	"github.com/google/uuid"

	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

// RequireAny verifies the user holds `perm` somewhere (any org). Use it for
// navigation / list routes that filter server-side by scope.
func RequireAny(ctx context.Context, perm string) error {
	u, ok := FromContext(ctx)
	if !ok {
		return httperror.New(httperror.CodeUnauthorized, "not authenticated")
	}
	if u.HasAny(perm) {
		return nil
	}
	return httperror.New(httperror.CodeForbidden, "missing permission: "+perm)
}

// RequireInOrg enforces `perm` within a specific organization scope.
// Resource handlers resolve the owning org from the resource row (never the
// request body) and pass it here.
func RequireInOrg(ctx context.Context, perm string, orgID uuid.UUID) error {
	u, ok := FromContext(ctx)
	if !ok {
		return httperror.New(httperror.CodeUnauthorized, "not authenticated")
	}
	if u.HasInOrg(perm, orgID) {
		return nil
	}
	return httperror.New(httperror.CodeForbidden, "missing permission: "+perm)
}
