package desk

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/AGlElectronics/desk/backend/internal/domain"
	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

// Access is what one caller may do in which mailbox (docs/adr/0003).
// Desk admins (mailbox.manage in a mailbox's organization) may do
// everything there; everyone else needs the mailbox's roles, granted by the
// groups desk-<slug>-viewer, -manager and -delete.
type Access struct {
	UserID    uuid.UUID
	adminOrgs map[uuid.UUID]bool
	groups    map[string]bool
}

// NewAccess builds a caller's access from their normalized groups and the
// organizations where they hold mailbox.manage.
func NewAccess(userID uuid.UUID, groups []string, adminOrgs []uuid.UUID) Access {
	a := Access{UserID: userID, adminOrgs: map[uuid.UUID]bool{}, groups: map[string]bool{}}
	for _, o := range adminOrgs {
		a.adminOrgs[o] = true
	}
	for _, g := range groups {
		a.groups[strings.ToLower(strings.TrimPrefix(g, "/"))] = true
	}
	return a
}

// IsAdmin: Desk admin in org.
func (a Access) IsAdmin(org uuid.UUID) bool { return a.adminOrgs[org] }

// AnyAdmin: Desk admin somewhere.
func (a Access) AnyAdmin() bool { return len(a.adminOrgs) > 0 }

// AdminOrgs lists the organizations where the caller is Desk admin.
func (a Access) AdminOrgs() []uuid.UUID {
	out := make([]uuid.UUID, 0, len(a.adminOrgs))
	for o := range a.adminOrgs {
		out = append(out, o)
	}
	return out
}

func (a Access) has(slug string, l domain.MailboxLevel) bool {
	return a.groups[domain.MailboxGroup(slug, l)]
}

// CanView: read the mailbox's conversations (any of its roles).
func (a Access) CanView(mb Mailbox) bool {
	return a.IsAdmin(mb.OrganizationID) || a.has(mb.Slug, domain.LevelViewer) ||
		a.has(mb.Slug, domain.LevelManager) || a.has(mb.Slug, domain.LevelDelete)
}

// CanWork: reply, note, assign, change status, tags, saved replies,
// signature and auto-reply.
func (a Access) CanWork(mb Mailbox) bool {
	return a.IsAdmin(mb.OrganizationID) || a.has(mb.Slug, domain.LevelManager)
}

// CanDelete: move emails and conversations to the trash and restore them.
func (a Access) CanDelete(mb Mailbox) bool {
	return a.IsAdmin(mb.OrganizationID) || a.has(mb.Slug, domain.LevelDelete)
}

// MailboxRights is what the web app shows and gates on.
type MailboxRights struct {
	View   bool `json:"view"`
	Work   bool `json:"work"`
	Delete bool `json:"delete"`
	Admin  bool `json:"admin"`
}

// Rights summarises the caller's access to mb.
func (a Access) Rights(mb Mailbox) MailboxRights {
	return MailboxRights{View: a.CanView(mb), Work: a.CanWork(mb), Delete: a.CanDelete(mb), Admin: a.IsAdmin(mb.OrganizationID)}
}

// slugs the caller holds any role for.
func (a Access) slugs() []string {
	set := map[string]bool{}
	for g := range a.groups {
		if !strings.HasPrefix(g, "desk-") {
			continue
		}
		for _, l := range domain.MailboxLevels() {
			if suffix := "-" + string(l); strings.HasSuffix(g, suffix) {
				set[strings.TrimSuffix(strings.TrimPrefix(g, "desk-"), suffix)] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	return out
}

// VisibleMailboxes returns the mailboxes (not in the trash) the caller can
// view, by name.
func (s *Service) VisibleMailboxes(ctx context.Context, a Access) ([]Mailbox, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+mailboxCols+` FROM mailboxes
		 WHERE deleted_at IS NULL AND (organization_id = ANY($1) OR slug = ANY($2)) ORDER BY name`, a.AdminOrgs(), a.slugs())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Mailbox{}
	for rows.Next() {
		m, err := scanMailbox(rows)
		if err != nil {
			return nil, err
		}
		if a.CanView(m) {
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

// CatalogMailboxes lists the mailboxes (not in the trash) whose roles the
// role catalog publishes.
func (s *Service) CatalogMailboxes(ctx context.Context) ([]Mailbox, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+mailboxCols+` FROM mailboxes WHERE deleted_at IS NULL ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Mailbox{}
	for rows.Next() {
		m, err := scanMailbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MailboxFor loads a mailbox the caller may view; others look missing
// (404), so mailbox ids cannot be probed.
func (s *Service) MailboxFor(ctx context.Context, a Access, id uuid.UUID) (Mailbox, error) {
	mb, err := s.GetMailbox(ctx, id)
	if err != nil {
		return mb, err
	}
	if mb.DeletedAt != nil && !a.IsAdmin(mb.OrganizationID) {
		return Mailbox{}, httperror.NotFound("mailbox")
	}
	if !a.CanView(mb) {
		return Mailbox{}, httperror.NotFound("mailbox")
	}
	return mb, nil
}

// ConversationFor loads a conversation and its mailbox if the caller may
// view it (404 otherwise).
func (s *Service) ConversationFor(ctx context.Context, a Access, id uuid.UUID) (Conversation, Mailbox, error) {
	c, err := s.GetConversation(ctx, id)
	if err != nil {
		return c, Mailbox{}, err
	}
	mb, err := s.GetMailbox(ctx, c.MailboxID)
	if err != nil {
		return c, mb, err
	}
	if mb.DeletedAt != nil || !a.CanView(mb) {
		return Conversation{}, Mailbox{}, httperror.NotFound("conversation")
	}
	return c, mb, nil
}

// errForbidden: the caller sees the mailbox but lacks the role for this.
func errForbidden(what string) error {
	return httperror.New(httperror.CodeForbidden, "you need the "+what+" role of this mailbox")
}

// RequireWork fails unless the caller may work mb.
func RequireWork(a Access, mb Mailbox) error {
	if !a.CanWork(mb) {
		return errForbidden("manager")
	}
	return nil
}

// RequireDelete fails unless the caller may delete in mb.
func RequireDelete(a Access, mb Mailbox) error {
	if !a.CanDelete(mb) {
		return errForbidden("delete")
	}
	return nil
}

// RequireAdmin fails unless the caller is Desk admin in mb's organization.
func RequireAdmin(a Access, mb Mailbox) error {
	if !a.IsAdmin(mb.OrganizationID) {
		return httperror.New(httperror.CodeForbidden, "only Desk admins may do this")
	}
	return nil
}
