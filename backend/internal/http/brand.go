package http

import (
	"net/http"

	"github.com/AGlElectronics/desk/backend/internal/auth"
	"github.com/AGlElectronics/desk/backend/internal/brand"
	"github.com/AGlElectronics/desk/backend/internal/denlink"
	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

// brandHandler serves the active brand package. The /api/v1/brand endpoint
// is PUBLIC (unauthenticated) because the login screen needs product name &
// colors before the user signs in. When paired with Den, Den's organisation
// name, logo and theme override the local file.
type brandHandler struct {
	provider brand.Provider
	den      *denlink.Client
}

const brandAssetBase = "/api/v1/brand"

// requirePermission rejects callers that hold perm nowhere.
func requirePermission(perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := auth.RequireAny(r.Context(), perm); err != nil {
				httperror.Render(w, r, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (h *brandHandler) get(w http.ResponseWriter, r *http.Request) {
	b, err := h.provider.Get(r.Context())
	if err != nil {
		httperror.Render(w, r, httperror.Internal(err))
		return
	}
	if bundle := h.den.Bundle(); bundle != nil {
		b.Source, b.Revision = "den", bundle.Brand.Revision
		if bundle.Brand.OrganizationName != "" {
			b.OrganizationName = bundle.Brand.OrganizationName
		}
		if logo := h.den.AssetURL(brandAssetBase, denlink.AssetLogo); logo != "" {
			b.LogoURL = logo
		}
		b.LoginBackgroundURL = h.den.AssetURL(brandAssetBase, denlink.AssetLoginBackground)
		b.LoginLayout, _ = bundle.Brand.Copy["login_layout"].(string)
		b.LoginCaption, _ = bundle.Brand.Copy["login_caption"].(string)
		b.Theme = bundle.Brand.Theme
		if p, ok := bundle.Brand.Theme["primary"].(string); ok && p != "" {
			if b.Colors == nil {
				b.Colors = map[string]string{}
			}
			b.Colors["primary"] = p
		}
	}
	w.Header().Set("Cache-Control", "no-cache")
	writeJSON(w, http.StatusOK, b)
}
