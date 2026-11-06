package http

import (
	"net/http"
	"sort"
	"strings"

	"github.com/AGlElectronics/desk/backend/internal/auth"
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
	domain.RoleOrganizationAdmin: "Manages mailboxes, every conversation and the module settings.",
	domain.RoleAgent:             "Answers support conversations: reply, note, assign, close.",
}

// roleCatalogHandler serves GET /api/role-catalog (public, read-only). Roles
// bound to an IdP group in the access config are assignable from HR.
func roleCatalogHandler(access *auth.AccessConfig) http.HandlerFunc {
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
			base := roleCatalogEntry{
				Key:         role,
				Label:       humanizeRole(role),
				Description: desc,
				Permissions: perms,
			}
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
