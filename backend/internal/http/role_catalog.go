package http

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/rs/zerolog/hlog"

	"github.com/AGlElectronics/desk/backend/internal/auth"
	"github.com/AGlElectronics/desk/backend/internal/desk"
	"github.com/AGlElectronics/desk/backend/internal/domain"
	"github.com/AGlElectronics/desk/backend/internal/groupmatch"
)

type roleCatalogEntry struct {
	Key          string   `json:"key"`
	Label        string   `json:"label"`
	Description  string   `json:"description"`
	Group        string   `json:"group"`
	GroupDisplay string   `json:"group_display"`
	Assignable   bool     `json:"assignable"`
	Default      bool     `json:"default"`
	Scope        string   `json:"scope"`
	Privileged   bool     `json:"privileged"`
	Dynamic      bool     `json:"dynamic"`
	Permissions  []string `json:"permissions"`
	Note         string   `json:"note,omitempty"`
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

// roleCatalogAuth admits HR's backend with HR_SERVICE_TOKEN (constant-time
// compare) and otherwise requires a normal sign-in. Without a configured
// token only signed-in staff can read the catalog.
func roleCatalogAuth(hrToken string, authMW func(http.Handler) http.Handler, next http.Handler) http.HandlerFunc {
	signedIn := authMW(next)
	return func(w http.ResponseWriter, r *http.Request) {
		if hrToken != "" {
			got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if subtle.ConstantTimeCompare([]byte(got), []byte(hrToken)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		}
		signedIn.ServeHTTP(w, r)
	}
}

// roleCatalogHandler serves GET /api/role-catalog. Static roles come from
// the access config; each mailbox adds three dynamic roles. group is the
// normalised form, group_display the name as written. Delete roles and the
// Desk admin role are privileged.
func roleCatalogHandler(access *auth.AccessConfig, svc *desk.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		roles := make([]roleCatalogEntry, 0)
		if access != nil {
			bindings := access.Bindings()
			for _, role := range access.Roles() {
				set := access.PermissionsForRole(role)
				perms := make([]string, 0, len(set))
				privileged := false
				for p := range set {
					perms = append(perms, p)
					if p == domain.PermMailboxManage || p == domain.PermSettingsManage {
						privileged = true
					}
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
				base := roleCatalogEntry{
					Key: role, Label: label, Description: desc, Permissions: perms,
					Scope: "organization", Privileged: privileged,
				}
				for _, b := range bindings {
					if b.Role != role {
						continue
					}
					g := groupmatch.NewGrant(b.Group)
					entry := base
					entry.Group, entry.GroupDisplay = g.Stored, g.Display
					entry.Assignable = true
					roles = append(roles, entry)
				}
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
					g := groupmatch.NewGrant(domain.MailboxGroup(mb.Slug, l))
					roles = append(roles, roleCatalogEntry{
						Key:          domain.MailboxRoleKey(mb.Slug, l),
						Label:        mb.Name + " " + t.label,
						Description:  sprintf(t.desc, mb.Name, mb.Email),
						Group:        g.Stored,
						GroupDisplay: g.Display,
						Assignable:   true,
						Scope:        "mailbox",
						Privileged:   l == domain.LevelDelete,
						Dynamic:      true,
						Permissions:  []string{},
						Note:         mb.Email,
					})
				}
			}
		}
		raw, _ := json.Marshal(roles)
		sum := sha256.Sum256(raw)
		revision := hex.EncodeToString(sum[:8])
		body := map[string]any{
			"module":       "desk",
			"display_name": "Desk",
			"description":  "Shared support inboxes and tickets.",
			"revision":     revision,
			"roles":        roles,
		}
		etag := `"` + revision + `"`
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		writeJSON(w, http.StatusOK, body)
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
