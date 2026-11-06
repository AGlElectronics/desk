package auth

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/AGlElectronics/desk/backend/internal/domain"
)

// AccessConfig is the in-memory snapshot of the role/permission catalog and
// the OIDC group → role bindings. It is immutable after construction and safe
// for concurrent reads. Role revocation is a configuration-and-restart
// operation (catalog) or an IdP group change (bindings), never a DB write.
type AccessConfig struct {
	rolePerms map[string]map[string]struct{}
	roleDesc  map[string]string
	bindings  []GroupBinding
}

// GroupBinding grants Role organization-wide in Organization (a slug) to
// everyone whose token carries Group.
type GroupBinding struct {
	Group        string
	Role         string
	Organization string
}

// DefaultAccessConfig returns the built-in role catalog with no bindings.
// Used in dev mode when no access config file is set.
func DefaultAccessConfig() *AccessConfig {
	return &AccessConfig{rolePerms: defaultRolePermSets(), roleDesc: map[string]string{}}
}

// PermissionsForRole returns the permission set for a role or nil if the
// role is unknown. The returned map MUST NOT be mutated.
func (c *AccessConfig) PermissionsForRole(role string) map[string]struct{} {
	if c == nil {
		return nil
	}
	return c.rolePerms[role]
}

// RoleDescription returns the description from the config file ("" if none).
func (c *AccessConfig) RoleDescription(role string) string {
	if c == nil {
		return ""
	}
	return c.roleDesc[role]
}

// HasRole reports whether the catalog knows about `role`.
func (c *AccessConfig) HasRole(role string) bool {
	if c == nil {
		return false
	}
	_, ok := c.rolePerms[role]
	return ok
}

// Roles returns the sorted role names in the catalog (for debug/admin APIs).
func (c *AccessConfig) Roles() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.rolePerms))
	for name := range c.rolePerms {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Bindings returns every group binding in file order.
func (c *AccessConfig) Bindings() []GroupBinding {
	if c == nil {
		return nil
	}
	return append([]GroupBinding(nil), c.bindings...)
}

// BindingsFor returns the bindings matching any of the token's groups.
// Groups compare case-insensitively and ignore a leading "/" (Keycloak's
// full-path form).
func (c *AccessConfig) BindingsFor(groups []string) []GroupBinding {
	if c == nil || len(groups) == 0 {
		return nil
	}
	have := map[string]struct{}{}
	for _, g := range groups {
		have[normalizeGroup(g)] = struct{}{}
	}
	var out []GroupBinding
	for _, b := range c.bindings {
		if _, ok := have[normalizeGroup(b.Group)]; ok {
			out = append(out, b)
		}
	}
	return out
}

func normalizeGroup(g string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(g), "/"))
}

type accessFile struct {
	// Organization is the default organization slug for bindings.
	Organization string                    `toml:"organization"`
	Roles        map[string]accessFileRole `toml:"roles"`
	Bindings     []accessFileBinding       `toml:"bindings"`
}

type accessFileRole struct {
	Description string   `toml:"description"`
	Permissions []string `toml:"permissions"`
}

type accessFileBinding struct {
	Group        string `toml:"group"`
	Role         string `toml:"role"`
	Organization string `toml:"organization"`
}

// LoadAccessConfig reads and parses the access config TOML file at path.
// See infra/access-config.toml for the canonical example.
func LoadAccessConfig(path string) (*AccessConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("access config %q: %w", path, err)
	}
	cfg, err := parseAccessConfig(data)
	if err != nil {
		return nil, fmt.Errorf("access config %q: %w", path, err)
	}
	return cfg, nil
}

func parseAccessConfig(data []byte) (*AccessConfig, error) {
	var f accessFile
	if err := toml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse toml: %w", err)
	}

	known := map[string]struct{}{}
	for _, p := range domain.AllPermissions() {
		known[p] = struct{}{}
	}

	// A role listed in the file replaces its built-in permission set.
	rolePerms := defaultRolePermSets()
	roleDesc := map[string]string{}
	for name, entry := range f.Roles {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("roles: role name must not be empty")
		}
		roleDesc[name] = entry.Description
		set := map[string]struct{}{}
		for _, p := range entry.Permissions {
			if _, ok := known[p]; !ok {
				return nil, fmt.Errorf("roles.%s: unknown permission %q", name, p)
			}
			set[p] = struct{}{}
		}
		rolePerms[name] = set
	}

	bindings := make([]GroupBinding, 0, len(f.Bindings))
	for i, b := range f.Bindings {
		b.Group, b.Role = strings.TrimSpace(b.Group), strings.TrimSpace(b.Role)
		org := strings.TrimSpace(b.Organization)
		if org == "" {
			org = strings.TrimSpace(f.Organization)
		}
		switch {
		case b.Group == "":
			return nil, fmt.Errorf("bindings[%d]: group is required", i)
		case b.Role == "":
			return nil, fmt.Errorf("bindings[%d]: role is required", i)
		case org == "":
			return nil, fmt.Errorf("bindings[%d]: organization is required (set it on the binding or at the top of the file)", i)
		}
		if _, ok := rolePerms[b.Role]; !ok {
			return nil, fmt.Errorf("bindings[%d]: group %q references unknown role %q", i, b.Group, b.Role)
		}
		bindings = append(bindings, GroupBinding{Group: b.Group, Role: b.Role, Organization: org})
	}

	return &AccessConfig{rolePerms: rolePerms, roleDesc: roleDesc, bindings: bindings}, nil
}

func defaultRolePermSets() map[string]map[string]struct{} {
	out := map[string]map[string]struct{}{}
	for role, perms := range domain.DefaultRolePermissions() {
		set := make(map[string]struct{}, len(perms))
		for _, p := range perms {
			set[p] = struct{}{}
		}
		out[role] = set
	}
	return out
}
