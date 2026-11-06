// Package domain holds the helpdesk vocabulary — role names, permission
// identifiers and conversation states — used by both the service layer and
// the HTTP layer. Keeping these as typed constants prevents stringly typed
// drift between tables, APIs, and the access config file.
package domain

// Role names recognized by default. The access config file (TOML) may add
// more roles or redefine the permission set attached to any of these.
const (
	// RoleOrganizationAdmin manages mailboxes, every conversation and the
	// module settings.
	RoleOrganizationAdmin = "organization_admin"
	// RoleAgent works conversations: reply, note, assign, change status.
	RoleAgent = "agent"
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
	ThreadCustomer = "customer"
	ThreadReply    = "reply"
	ThreadNote     = "note"
	ThreadSystem   = "system"
)

// Permission identifiers.
const (
	// PermTicketsWork: read and work conversations in every mailbox.
	PermTicketsWork = "tickets.work"
	// PermMailboxManage: create and edit mailboxes (addresses, IMAP/SMTP).
	PermMailboxManage = "mailbox.manage"
	// PermSettingsManage: module settings, including the Den pairing.
	PermSettingsManage = "settings.manage"
)

// AllPermissions returns every permission the access-config validator will
// accept. The access config file may only reference identifiers listed here;
// unknown names fail configuration load with a clear error.
func AllPermissions() []string {
	return []string{
		PermTicketsWork,
		PermMailboxManage,
		PermSettingsManage,
	}
}

// DefaultRolePermissions is the built-in role → permission catalog applied
// when the access config file omits a role (or isn't configured at all).
func DefaultRolePermissions() map[string][]string {
	return map[string][]string{
		RoleOrganizationAdmin: AllPermissions(),
		RoleAgent:             {PermTicketsWork},
	}
}
