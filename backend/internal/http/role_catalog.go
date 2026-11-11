package http

import (
	"net/http"
	"sort"
	"strings"

	"github.com/rs/zerolog/hlog"

	"github.com/AGlElectronics/desk/backend/internal/auth"
	"github.com/AGlElectronics/desk/backend/internal/desk"
	"github.com/AGlElectronics/desk/backend/internal/domain"
)

type roleCatalogEntry struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Group       string   `json:"group"`
	Assignable  bool     `json:"assignable"`
	Scope       string   `json:"scope"`
	Permissions []string `json:"permissions"`
	Note        string   `json:"note,omitempty"`
}

var deskRoleDescriptions = map[string]string{
	domain.RoleOrganizationAdmin: "Desk admin: creates, configures and deletes mailboxes; full access to every mailbox " +
		"(read, answer, delete, email any address from them); retention, audit log and module settings.",
}

var levelText = map[domain.MailboxLevel]struct{ label, desc string }{
	domain.LevelViewer:  {"viewer", "Reads the conversations of %s (%s)."},
	domain.LevelManager: {"manager", "Works %s (%s): reply, note, assign, close, tags, saved replies, signature and auto-reply; can start conversations with any address from it."},
	domain.LevelDelete:  {"delete", "Deletes and restores emails and conversations of %s (%s); deleted mail also goes to the provider's trash."},
}

// roleCatalogHandler serves GET /api/role-catalog (public, read-only):
// module roles bound to an IdP group in the access config, plus three
// roles per mailbox (docs/adr/0003). HR assigns a role by adding the
// person to its group; it fetches this catalog live, so a new mailbox's
// roles show up there right away.
func roleCatalogHandler(access *auth.AccessConfig, svc *desk.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		roles := make([]roleCatalogEntry, 0)
		bindings := access.Bindings()
		for _, role := range access.Roles() {
			set := access.PermissionsForRole(role)
			perms := make([]string, 0, len(set))
			for p := range set {
				perms = append(perms, p)
			}
			sort.Strings(perms)
			desc := access.RoleDescription(role)
			if desc == "" {
				desc = deskRoleDescriptions[role]
			}
			label := humanizeRole(role)
			if role == domain.RoleOrganizationAdmin {
				label = "Desk admin"
			}
			base := roleCatalogEntry{Key: role, Label: label, Description: desc, Permissions: perms}
			for _, b := range bindings {
				if b.Role != role {
					continue
				}
				entry := base
				entry.Group = b.Group
				entry.Assignable = true
				entry.Scope = "organization:" + b.Organization
				roles = append(roles, entry)
			}
		}
		if svc != nil {
			boxes, err := svc.CatalogMailboxes(r.Context())
			if err != nil {
				hlog.FromRequest(r).Error().Err(err).Msg("role catalog: list mailboxes")
			}
			for _, mb := range boxes {
				for _, l := range domain.MailboxLevels() {
					t := levelText[l]
					roles = append(roles, roleCatalogEntry{
						Key:         domain.MailboxRoleKey(mb.Slug, l),
						Label:       mb.Name + " " + t.label,
						Description: sprintf(t.desc, mb.Name, mb.Email),
						Group:       domain.MailboxGroup(mb.Slug, l),
						Assignable:  true,
						Scope:       "mailbox:" + mb.Slug,
						Permissions: []string{},
						Note:        mb.Email,
					})
				}
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		writeJSON(w, http.StatusOK, map[string]any{
			"module":       "desk",
			"display_name": "Desk",
			"description":  "Shared support inboxes and tickets.",
			"roles":        roles,
		})
	}
}

func humanizeRole(role string) string {
	parts := strings.Split(strings.ReplaceAll(role, "_", " "), " ")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}
