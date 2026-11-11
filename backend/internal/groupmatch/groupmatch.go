// Code generated from con-services shared/go/groupmatch; DO NOT EDIT. Run scripts/sync-shared.

// Package groupmatch decides whether a person's identity-provider groups
// match the groups a module grants access to. Every Con Services backend
// uses the same rule, so a grant behaves the same in Den and in every module.
//
// The rule:
//
//   - A group name is normalised: surrounding spaces trimmed, one leading
//     '/' removed (Keycloak's full-path form), lower-cased. The original is
//     kept for display.
//   - When a prefix is configured (OIDC_GROUP_PREFIX, for example
//     "/con2026"), it is removed from names that start with it before
//     comparing. Names without the prefix are compared as they are.
//   - By default the whole path must match (OIDC_GROUP_MATCH=full):
//     "/departments/security" matches the grant "departments/security" but
//     not "security". With OIDC_GROUP_MATCH=last only the last path segment
//     is compared, so it also matches "security".
//   - Groups are read only from the claim named by OIDC_GROUPS_CLAIM
//     (default "groups"). The "roles" claim is never merged in.
package groupmatch

import (
	"fmt"
	"strings"
)

// Mode is OIDC_GROUP_MATCH.
type Mode string

const (
	// ModeFull compares the whole normalised path (the default).
	ModeFull Mode = "full"
	// ModeLast compares only the last path segment.
	ModeLast Mode = "last"
)

// DefaultClaim is the default OIDC_GROUPS_CLAIM.
const DefaultClaim = "groups"

// Environment variable names read by FromEnv.
const (
	EnvClaim  = "OIDC_GROUPS_CLAIM"
	EnvMatch  = "OIDC_GROUP_MATCH"
	EnvPrefix = "OIDC_GROUP_PREFIX"
)

// Config holds the matching settings. The zero value matches full paths,
// reads the "groups" claim and has no prefix.
type Config struct {
	// Claim is the token claim that holds the groups (OIDC_GROUPS_CLAIM).
	Claim string
	// Mode is OIDC_GROUP_MATCH; empty means ModeFull.
	Mode Mode
	// Prefix is OIDC_GROUP_PREFIX, in any form ("/con2026", "con2026/").
	Prefix string
}

// FromEnv reads OIDC_GROUPS_CLAIM, OIDC_GROUP_MATCH and OIDC_GROUP_PREFIX
// through getenv (usually os.Getenv) and validates them.
func FromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		Claim:  strings.TrimSpace(getenv(EnvClaim)),
		Mode:   Mode(strings.ToLower(strings.TrimSpace(getenv(EnvMatch)))),
		Prefix: getenv(EnvPrefix),
	}
	if c.Claim == "" {
		c.Claim = DefaultClaim
	}
	if c.Mode == "" {
		c.Mode = ModeFull
	}
	return c, c.Validate()
}

// Validate rejects an unknown Mode.
func (c Config) Validate() error {
	switch c.Mode {
	case "", ModeFull, ModeLast:
		return nil
	}
	return fmt.Errorf("%s must be %q or %q, got %q", EnvMatch, ModeFull, ModeLast, string(c.Mode))
}

// ClaimName is Claim, or DefaultClaim when empty.
func (c Config) ClaimName() string {
	if c.Claim == "" {
		return DefaultClaim
	}
	return c.Claim
}

// Normalize trims spaces, strips one leading '/' and lower-cases name.
func Normalize(name string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "/"))
}

func (c Config) prefix() string {
	return strings.TrimSuffix(Normalize(c.Prefix), "/")
}

// Key is the form two names are compared in: normalised, without the
// prefix and, with ModeLast, reduced to the last segment. An empty Key never
// matches anything.
func (c Config) Key(name string) string {
	k := Normalize(name)
	if p := c.prefix(); p != "" {
		if k == p {
			return ""
		}
		k = strings.TrimPrefix(k, p+"/")
	}
	if c.Mode == ModeLast {
		k = strings.TrimSuffix(k, "/")
		k = k[strings.LastIndex(k, "/")+1:]
	}
	return k
}

// Match reports whether any of groups matches grant.
func (c Config) Match(groups []string, grant string) bool {
	g := c.Key(grant)
	if g == "" {
		return false
	}
	for _, name := range groups {
		if c.Key(name) == g {
			return true
		}
	}
	return false
}

// MatchAny reports whether any of groups matches any of grants.
func (c Config) MatchAny(groups, grants []string) bool {
	return len(c.Matching(groups, grants)) > 0
}

// Matching returns the grants (as given) that at least one of groups matches.
func (c Config) Matching(groups, grants []string) []string {
	have := make(map[string]bool, len(groups))
	for _, name := range groups {
		if k := c.Key(name); k != "" {
			have[k] = true
		}
	}
	var out []string
	for _, grant := range grants {
		if k := c.Key(grant); k != "" && have[k] {
			out = append(out, grant)
		}
	}
	return out
}

// Grant is a group name as a module stores it: Stored is the normalised
// form used for comparisons and unique indexes, Display the name as the
// admin typed it (trimmed).
type Grant struct {
	Stored  string `json:"group"`
	Display string `json:"group_display"`
}

// NewGrant returns the stored and display form of a group an admin entered.
// Stored keeps the prefix (if any) so the grant still means the same group
// when OIDC_GROUP_PREFIX changes; Config.Key removes it when comparing.
func NewGrant(name string) Grant {
	return Grant{Stored: Normalize(name), Display: strings.TrimSpace(name)}
}

// Groups reads the groups from a token's claims using c.ClaimName.
func (c Config) Groups(claims map[string]any) []string {
	return GroupsFromClaims(claims, c.ClaimName())
}

// GroupsFromClaims returns the group names in claims[claim]. The claim may
// be an array of strings or a single string (one group). When no top-level
// claim has that exact name and the name contains dots, it is followed as a
// path through nested objects ("realm_access.roles"). Empty names and exact
// duplicates are dropped; names are returned as in the token. Other claims,
// "roles" included, are never read.
func GroupsFromClaims(claims map[string]any, claim string) []string {
	if claim == "" {
		claim = DefaultClaim
	}
	v, ok := claims[claim]
	if !ok && strings.Contains(claim, ".") {
		v, ok = lookupPath(claims, strings.Split(claim, "."))
	}
	if !ok {
		return nil
	}
	var raw []string
	switch t := v.(type) {
	case string:
		raw = []string{t}
	case []string:
		raw = t
	case []any:
		for _, e := range t {
			if s, isStr := e.(string); isStr {
				raw = append(raw, s)
			}
		}
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		if strings.TrimSpace(s) == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func lookupPath(m map[string]any, parts []string) (any, bool) {
	var cur any = m
	for _, p := range parts {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = obj[p]; !ok {
			return nil, false
		}
	}
	return cur, true
}
