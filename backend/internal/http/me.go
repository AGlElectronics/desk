package http

import (
	"fmt"
	"net/http"

	"github.com/AGlElectronics/desk/backend/internal/auth"
	"github.com/AGlElectronics/desk/backend/internal/desk"
	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

func sprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }

type meMailbox struct {
	ID     string             `json:"id"`
	Slug   string             `json:"slug"`
	Name   string             `json:"name"`
	Email  string             `json:"email"`
	Rights desk.MailboxRights `json:"access"`
}

// meHandler returns the authenticated user, their memberships, effective
// permissions and their rights per mailbox. The SPA uses this on boot to
// gate navigation; precise authorization stays server-side.
func meHandler(svc *desk.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := auth.FromContext(r.Context())
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		memberships := make([]map[string]any, 0, len(u.Memberships))
		for _, m := range u.Memberships {
			memberships = append(memberships, map[string]any{
				"organization_id": m.OrganizationID.String(),
				"role":            m.Role,
			})
		}
		perms := u.EffectivePermissions()
		if perms == nil {
			perms = []string{}
		}
		a := accessOf(u)
		mailboxes := []meMailbox{}
		if svc != nil {
			boxes, err := svc.VisibleMailboxes(r.Context(), a)
			if err != nil {
				httperror.Render(w, r, err)
				return
			}
			for _, mb := range boxes {
				mailboxes = append(mailboxes, meMailbox{ID: mb.ID.String(), Slug: mb.Slug, Name: mb.Name, Email: mb.Email, Rights: a.Rights(mb)})
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":           u.ID.String(),
			"display_name": u.DisplayName,
			"email":        u.Email,
			"issuer":       u.Issuer,
			"subject":      u.Subject,
			"memberships":  memberships,
			"permissions":  perms,
			"is_admin":     a.AnyAdmin(),
			"mailboxes":    mailboxes,
		})
	}
}
