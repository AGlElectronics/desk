// Code generated from con-services shared/go/groupmatch; DO NOT EDIT. Run scripts/sync-shared.

package groupmatch

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"":                         "",
		"den-admin":                "den-admin",
		"/den-admin":               "den-admin",
		"  /Den-Admin  ":           "den-admin",
		"//double":                 "/double",
		"/Departments/Security":    "departments/security",
		"departments/security/":    "departments/security/",
		"\t/con2026/Staff/Desk \n": "con2026/staff/desk",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatch(t *testing.T) {
	full := Config{}
	last := Config{Mode: ModeLast}
	pre := Config{Prefix: "/con2026"}
	preLast := Config{Prefix: "con2026/", Mode: ModeLast}

	cases := []struct {
		name   string
		cfg    Config
		groups []string
		grant  string
		want   bool
	}{
		{"exact", full, []string{"den-admin"}, "den-admin", true},
		{"leading slash on group", full, []string{"/den-admin"}, "den-admin", true},
		{"leading slash on grant", full, []string{"den-admin"}, "/den-admin", true},
		{"case-insensitive", full, []string{"/DEN-Admin"}, "den-admin", true},
		{"spaces", full, []string{" den-admin "}, "den-admin", true},
		{"full path matches full grant", full, []string{"/departments/security"}, "departments/security", true},
		{"full path does not match last segment", full, []string{"/departments/security"}, "security", false},
		{"last segment grant does not match other path", full, []string{"/security"}, "departments/security", false},
		{"no partial prefix", full, []string{"/departments/security-lead"}, "departments/security", false},
		{"empty grant", full, []string{""}, "", false},
		{"empty groups", full, nil, "den-admin", false},
		{"double slash keeps one", full, []string{"//den-admin"}, "den-admin", false},

		{"last: path matches segment", last, []string{"/departments/security"}, "security", true},
		{"last: path matches full grant", last, []string{"/departments/security"}, "departments/security", true},
		{"last: other parent same leaf", last, []string{"/teams/security"}, "departments/security", true},
		{"last: different leaf", last, []string{"/departments/security"}, "desk", false},
		{"last: trailing slash", last, []string{"/departments/security/"}, "security", true},

		{"prefix stripped from group", pre, []string{"/con2026/departments/security"}, "departments/security", true},
		{"prefix stripped from grant too", pre, []string{"/con2026/den-admin"}, "/con2026/den-admin", true},
		{"group without prefix still matches", pre, []string{"/den-admin"}, "den-admin", true},
		{"prefix is case-insensitive", pre, []string{"/CON2026/Den-Admin"}, "den-admin", true},
		{"prefix only on segment boundary", pre, []string{"/con2026x/den-admin"}, "den-admin", false},
		{"prefix group itself matches nothing", pre, []string{"/con2026"}, "con2026", false},
		{"other year not stripped", pre, []string{"/con2025/den-admin"}, "den-admin", false},
		{"prefix + full: segment not enough", pre, []string{"/con2026/departments/security"}, "security", false},

		{"prefix + last", preLast, []string{"/con2026/departments/security"}, "security", true},
		{"prefix + last: prefix not a leaf", preLast, []string{"/con2026"}, "con2026", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.Match(tc.groups, tc.grant); got != tc.want {
				t.Errorf("%+v.Match(%q, %q) = %v, want %v", tc.cfg, tc.groups, tc.grant, got, tc.want)
			}
		})
	}
}

func TestMatching(t *testing.T) {
	c := Config{Prefix: "/con2026"}
	groups := []string{"/con2026/departments/security", "/den-member"}
	grants := []string{"departments/security", "Den-Member", "hr-admin", ""}
	got := c.Matching(groups, grants)
	want := []string{"departments/security", "Den-Member"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Matching = %q, want %q", got, want)
	}
	if !c.MatchAny(groups, []string{"hr-admin", "den-member"}) {
		t.Error("MatchAny = false, want true")
	}
	if c.MatchAny(groups, []string{"hr-admin"}) {
		t.Error("MatchAny = true, want false")
	}
}

func TestNewGrant(t *testing.T) {
	cases := []struct {
		in   string
		want Grant
	}{
		{"  /Departments/Security ", Grant{Stored: "departments/security", Display: "/Departments/Security"}},
		{"den-admin", Grant{Stored: "den-admin", Display: "den-admin"}},
		{"/con2026/Staff", Grant{Stored: "con2026/staff", Display: "/con2026/Staff"}},
	}
	for _, tc := range cases {
		if got := NewGrant(tc.in); got != tc.want {
			t.Errorf("NewGrant(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
	b, _ := json.Marshal(NewGrant("/Den-Admin"))
	if string(b) != `{"group":"den-admin","group_display":"/Den-Admin"}` {
		t.Errorf("JSON = %s", b)
	}
}

func claims(t *testing.T, js string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestGroupsFromClaims(t *testing.T) {
	cases := []struct {
		name   string
		claims string
		claim  string
		want   []string
	}{
		{"array", `{"groups":["/den-admin","hr-admin"]}`, "groups", []string{"/den-admin", "hr-admin"}},
		{"default claim", `{"groups":["a"]}`, "", []string{"a"}},
		{"single string", `{"groups":"/den-admin"}`, "groups", []string{"/den-admin"}},
		{"single string is not split", `{"groups":"a,b"}`, "groups", []string{"a,b"}},
		{"roles not merged", `{"groups":["a"],"roles":["b"]}`, "groups", []string{"a"}},
		{"roles only gives nothing", `{"roles":["b"]}`, "groups", nil},
		{"custom claim", `{"groups":["a"],"con_groups":["b"]}`, "con_groups", []string{"b"}},
		{"url claim name with dots", `{"https://example.org/groups":["x"]}`, "https://example.org/groups", []string{"x"}},
		{"nested path", `{"realm_access":{"roles":["r1","r2"]}}`, "realm_access.roles", []string{"r1", "r2"}},
		{"nested path missing", `{"realm_access":{}}`, "realm_access.roles", nil},
		{"non-strings dropped", `{"groups":["a",1,null,{"x":1},"b"]}`, "groups", []string{"a", "b"}},
		{"empty and duplicates dropped", `{"groups":["a","","  ","a","A"]}`, "groups", []string{"a", "A"}},
		{"wrong type", `{"groups":42}`, "groups", []string{}},
		{"missing", `{}`, "groups", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := GroupsFromClaims(claims(t, tc.claims), tc.claim)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
	if got := GroupsFromClaims(map[string]any{"groups": []string{"x", "y"}}, "groups"); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Errorf("[]string claim: got %q", got)
	}
}

func TestConfigGroups(t *testing.T) {
	c := Config{Claim: "con_groups"}
	got := c.Groups(claims(t, `{"groups":["a"],"con_groups":["b"]}`))
	if !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("got %q", got)
	}
	if (Config{}).ClaimName() != "groups" {
		t.Error("zero Config should read the groups claim")
	}
}

func TestFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	c, err := FromEnv(env(nil))
	if err != nil || c.Claim != "groups" || c.Mode != ModeFull || c.Prefix != "" {
		t.Errorf("defaults: %+v, %v", c, err)
	}
	c, err = FromEnv(env(map[string]string{EnvClaim: " roles ", EnvMatch: "LAST", EnvPrefix: "/con2026"}))
	if err != nil || c.Claim != "roles" || c.Mode != ModeLast || c.Prefix != "/con2026" {
		t.Errorf("set: %+v, %v", c, err)
	}
	_, err = FromEnv(env(map[string]string{EnvMatch: "prefix"}))
	if err == nil || !strings.Contains(err.Error(), "OIDC_GROUP_MATCH") {
		t.Errorf("bad mode: %v", err)
	}
}
