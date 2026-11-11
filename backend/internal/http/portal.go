package http

import (
	"net/http"

	"github.com/AGlElectronics/desk/backend/internal/denlink"
	"github.com/AGlElectronics/desk/backend/internal/desk"
	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

// portalTodosHandler serves GET /api/portal/todos. Den's backend calls it
// with the pairing credential. Not paired: 404. A wrong or revoked
// credential, and a credential that cannot be decrypted, is 401 (shared
// denlink.RequireDen, so the answer does not say which). Missing identity
// headers: 400. An unknown person gets an empty list.
func portalTodosHandler(den *denlink.Client, svc *desk.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if den == nil || !den.Linked() {
			httperror.Render(w, r, httperror.New(httperror.CodeNotFound, "Desk is not paired with Den"))
			return
		}
		den.RequireDen(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller, ok := denlink.PortalCallerFrom(r.Context())
			if !ok || svc == nil {
				writeJSON(w, http.StatusOK, map[string]any{"items": []desk.PortalTodo{}})
				return
			}
			items, err := svc.PortalTodos(r.Context(), caller.Issuer, caller.Subject)
			if err != nil {
				httperror.Render(w, r, httperror.Internal(err))
				return
			}
			if items == nil {
				items = []desk.PortalTodo{}
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
		})).ServeHTTP(w, r)
	}
}
