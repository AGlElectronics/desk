// Package domain holds the helpdesk vocabulary — role names, permission
// identifiers and conversation states — used by both the service layer and
// the HTTP layer. Keeping these as typed constants prevents stringly typed
// drift between tables, APIs, and the access config file.
package domain

import (
	"regexp"
	"strings"
)

// Role names recognized by default. The access config file (TOML) may add
// more roles or redefine the permission set attached to any of these.
const (
	// RoleOrganizationAdmin is the Desk admin: every mailbox of the
	// organization with every right, mailbox and module settings.
	RoleOrganizationAdmin = "organization_admin"
)

// Conversation statuses.
const (
	StatusActive  = "active"
	StatusPending = "pending"
	StatusClosed  = "closed"
	StatusSpam    = "spam"
)

// Thread kinds.
const (
	ThreadCustomer  = "customer"
	ThreadReply     = "reply"
	ThreadNote      = "note"
	ThreadSystem    = "system"
	ThreadForward   = "forward"
	ThreadAutoReply = "auto_reply"
)

// Permission identifiers (module-wide roles from the access config).
const (
	// PermMailboxManage: Desk admin. Create, configure, delete and purge
	// mailboxes; full access to every mailbox; retention and audit log.
	PermMailboxManage = "mailbox.manage"
	// PermSettingsManage: module settings, including the Den pairing.
	PermSettingsManage = "settings.manage"
)

// AllPermissions returns every permission the access-config validator will
// accept. The access config file may only reference identifiers listed here;
// unknown names fail configuration load with a clear error.
func AllPermissions() []string {
	return []string{
		PermMailboxManage,
		PermSettingsManage,
	}
}

// DefaultRolePermissions is the built-in role → permission catalog applied
// when the access config file omits a role (or isn't configured at all).
// Per-mailbox roles are not listed here: every mailbox publishes its own
// (MailboxRoles).
func DefaultRolePermissions() map[string][]string {
	return map[string][]string{
		RoleOrganizationAdmin: AllPermissions(),
	}
}

// MailboxLevel is one of the three roles every mailbox publishes.
type MailboxLevel string

const (
	// LevelViewer reads the mailbox's conversations.
	LevelViewer MailboxLevel = "viewer"
	// LevelManager works them: reply, note, assign, status, tags, saved
	// replies, signature and auto-reply.
	LevelManager MailboxLevel = "manager"
	// LevelDelete deletes and restores emails and conversations (Desk's
	// trash and the provider's trash).
	LevelDelete MailboxLevel = "delete"
)

// MailboxLevels lists the levels in catalog order.
func MailboxLevels() []MailboxLevel { return []MailboxLevel{LevelViewer, LevelManager, LevelDelete} }

// MailboxRoleKey is the role catalog key, e.g. desk_info_viewer.
func MailboxRoleKey(slug string, l MailboxLevel) string {
	return "desk_" + slug + "_" + string(l)
}

// MailboxGroup is the IdP group that grants the role, e.g. desk-info-viewer.
func MailboxGroup(slug string, l MailboxLevel) string {
	return "desk-" + slug + "-" + string(l)
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

// ValidSlug reports whether s can name a mailbox's roles.
func ValidSlug(s string) bool { return slugPattern.MatchString(s) }

var slugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

// SlugFromEmail suggests a slug from an address' local part
// ("Lost.Found@x" → "lost-found").
func SlugFromEmail(email string) string {
	local := strings.ToLower(email)
	if i := strings.IndexByte(local, '@'); i >= 0 {
		local = local[:i]
	}
	s := strings.Trim(slugUnsafe.ReplaceAllString(local, "-"), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}
