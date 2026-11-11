// Code generated from con-services shared/go/gworkspace; DO NOT EDIT. Run scripts/sync-shared.

package gworkspace

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// UserName is a user's name in the Directory.
type UserName struct {
	GivenName  string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
	FullName   string `json:"fullName,omitempty"`
}

// User is a Workspace account.
type User struct {
	ID            string   `json:"id,omitempty"`
	PrimaryEmail  string   `json:"primaryEmail"`
	Name          UserName `json:"name"`
	Suspended     bool     `json:"suspended"`
	OrgUnitPath   string   `json:"orgUnitPath,omitempty"`
	RecoveryEmail string   `json:"recoveryEmail,omitempty"`
	CreationTime  string   `json:"creationTime,omitempty"`
	LastLoginTime string   `json:"lastLoginTime,omitempty"`
}

// NewUser is what CreateUser needs.
type NewUser struct {
	PrimaryEmail string
	GivenName    string
	FamilyName   string
	// Password is the initial password; empty means RandomPassword (for
	// people who sign in through SSO or get a reset link).
	Password                  string
	ChangePasswordAtNextLogin bool
	// OrgUnitPath defaults to "/".
	OrgUnitPath   string
	RecoveryEmail string
}

// Group is a Workspace group.
type Group struct {
	ID          string `json:"id,omitempty"`
	Email       string `json:"email"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// Member roles and types.
const (
	RoleMember  = "MEMBER"
	RoleManager = "MANAGER"
	RoleOwner   = "OWNER"
	TypeUser    = "USER"
	TypeGroup   = "GROUP"
)

// Member is a group membership.
type Member struct {
	ID    string `json:"id,omitempty"`
	Email string `json:"email"`
	// Role is RoleMember (default), RoleManager or RoleOwner.
	Role string `json:"role,omitempty"`
	// Type is TypeUser, TypeGroup, … (set by Google).
	Type   string `json:"type,omitempty"`
	Status string `json:"status,omitempty"`
}

func (c *Client) checkDomain(what, email string) error {
	if !c.cfg.InDomain(email) {
		return fmt.Errorf("google workspace: %s %q is not in the domain %s", what, email, c.cfg.Domain)
	}
	return nil
}

// GetUser loads a user by primary email, alias or ID.
func (c *Client) GetUser(ctx context.Context, userKey string) (*User, error) {
	var u User
	if err := c.dir(ctx, http.MethodGet, "/users/"+esc(userKey), nil, nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateUser creates an account in Domain.
func (c *Client) CreateUser(ctx context.Context, n NewUser) (*User, error) {
	if err := c.checkDomain("user", n.PrimaryEmail); err != nil {
		return nil, err
	}
	if strings.TrimSpace(n.GivenName) == "" || strings.TrimSpace(n.FamilyName) == "" {
		return nil, fmt.Errorf("google workspace: a user needs a given and a family name")
	}
	pw := n.Password
	if pw == "" {
		pw = RandomPassword()
	}
	ou := n.OrgUnitPath
	if ou == "" {
		ou = "/"
	}
	body := map[string]any{
		"primaryEmail":              n.PrimaryEmail,
		"name":                      UserName{GivenName: n.GivenName, FamilyName: n.FamilyName},
		"password":                  pw,
		"changePasswordAtNextLogin": n.ChangePasswordAtNextLogin,
		"orgUnitPath":               ou,
	}
	if n.RecoveryEmail != "" {
		body["recoveryEmail"] = n.RecoveryEmail
	}
	var u User
	if err := c.dir(ctx, http.MethodPost, "/users", nil, body, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// SetSuspended suspends (true) or restores (false) a user.
func (c *Client) SetSuspended(ctx context.Context, userKey string, suspended bool) (*User, error) {
	var u User
	if err := c.dir(ctx, http.MethodPut, "/users/"+esc(userKey), nil, map[string]any{"suspended": suspended}, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// DeleteUser deletes a user. Google keeps it restorable for 20 days.
func (c *Client) DeleteUser(ctx context.Context, userKey string) error {
	return c.dir(ctx, http.MethodDelete, "/users/"+esc(userKey), nil, nil, nil)
}

// ListGroups lists every group in Domain.
func (c *Client) ListGroups(ctx context.Context) ([]Group, error) {
	var out []Group
	q := url.Values{"domain": {c.cfg.Domain}, "maxResults": {"200"}}
	for {
		var page struct {
			Groups        []Group `json:"groups"`
			NextPageToken string  `json:"nextPageToken"`
		}
		if err := c.dir(ctx, http.MethodGet, "/groups", q, nil, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Groups...)
		if page.NextPageToken == "" {
			return out, nil
		}
		q.Set("pageToken", page.NextPageToken)
	}
}

// GetGroup loads a group by email, alias or ID.
func (c *Client) GetGroup(ctx context.Context, groupKey string) (*Group, error) {
	var g Group
	if err := c.dir(ctx, http.MethodGet, "/groups/"+esc(groupKey), nil, nil, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

// CreateGroup creates a group in Domain.
func (c *Client) CreateGroup(ctx context.Context, g Group) (*Group, error) {
	if err := c.checkDomain("group", g.Email); err != nil {
		return nil, err
	}
	var out Group
	if err := c.dir(ctx, http.MethodPost, "/groups", nil, Group{Email: g.Email, Name: g.Name, Description: g.Description}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteGroup deletes a group.
func (c *Client) DeleteGroup(ctx context.Context, groupKey string) error {
	return c.dir(ctx, http.MethodDelete, "/groups/"+esc(groupKey), nil, nil, nil)
}

// ListMembers lists a group's direct members.
func (c *Client) ListMembers(ctx context.Context, groupKey string) ([]Member, error) {
	var out []Member
	q := url.Values{"maxResults": {"200"}}
	for {
		var page struct {
			Members       []Member `json:"members"`
			NextPageToken string   `json:"nextPageToken"`
		}
		if err := c.dir(ctx, http.MethodGet, "/groups/"+esc(groupKey)+"/members", q, nil, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Members...)
		if page.NextPageToken == "" {
			return out, nil
		}
		q.Set("pageToken", page.NextPageToken)
	}
}

// AddMember adds a member (role defaults to MEMBER). An existing member
// gives an error for which IsConflict is true.
func (c *Client) AddMember(ctx context.Context, groupKey string, m Member) (*Member, error) {
	if m.Role == "" {
		m.Role = RoleMember
	}
	var out Member
	if err := c.dir(ctx, http.MethodPost, "/groups/"+esc(groupKey)+"/members", nil, Member{Email: m.Email, Role: m.Role}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveMember removes a member by email or ID.
func (c *Client) RemoveMember(ctx context.Context, groupKey, memberKey string) error {
	return c.dir(ctx, http.MethodDelete, "/groups/"+esc(groupKey)+"/members/"+esc(memberKey), nil, nil, nil)
}
