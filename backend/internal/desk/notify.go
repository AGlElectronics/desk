package desk

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/desk/backend/internal/domain"
)

// NotificationPrefs are a user's email notifications (off by default).
type NotificationPrefs struct {
	NewConversation bool `json:"new_conversation"`
	AssignedToMe    bool `json:"assigned_to_me"`
	// Available: Desk can send email (NOTIFY_FROM and a way to send).
	Available bool `json:"available"`
}

// GetNotificationPrefs returns userID's preferences.
func (s *Service) GetNotificationPrefs(ctx context.Context, userID uuid.UUID) (NotificationPrefs, error) {
	p := NotificationPrefs{Available: s.NotificationsEnabled()}
	err := s.pool.QueryRow(ctx, `SELECT new_conversation, assigned_to_me FROM notification_prefs WHERE user_id = $1`, userID).
		Scan(&p.NewConversation, &p.AssignedToMe)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return p, err
	}
	return p, nil
}

// SetNotificationPrefs stores userID's preferences.
func (s *Service) SetNotificationPrefs(ctx context.Context, userID uuid.UUID, p NotificationPrefs) (NotificationPrefs, error) {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO notification_prefs (user_id, new_conversation, assigned_to_me) VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET new_conversation = EXCLUDED.new_conversation,
		       assigned_to_me = EXCLUDED.assigned_to_me, updated_at = now()`, userID, p.NewConversation, p.AssignedToMe); err != nil {
		return p, err
	}
	return s.GetNotificationPrefs(ctx, userID)
}

func (s *Service) conversationURL(id uuid.UUID) string {
	return s.baseURL + "/conversations/" + id.String()
}

// notifyNewConversation emails everyone who wants it and can see mb.
func (s *Service) notifyNewConversation(ctx context.Context, tx pgx.Tx, mb Mailbox, convID uuid.UUID, number int, subject, from string) error {
	if s.notifySender() == "" {
		return nil
	}
	grants := []string{}
	for _, l := range domain.MailboxLevels() {
		grants = append(grants, domain.MailboxGroup(mb.Slug, l))
	}
	// Groups are matched in Go (groupmatch): OIDC_GROUP_PREFIX and
	// OIDC_GROUP_MATCH=last make SQL equality miss.
	rows, err := tx.Query(ctx, `
		SELECT u.email, u.desk_admin, u.groups FROM users u JOIN notification_prefs p ON p.user_id = u.id
		 WHERE p.new_conversation AND COALESCE(u.email, '') <> ''`)
	if err != nil {
		return err
	}
	var to []string
	for rows.Next() {
		var e string
		var admin bool
		var groups []string
		if rows.Scan(&e, &admin, &groups) == nil && (admin || s.groups.MatchAny(groups, grants)) {
			to = append(to, e)
		}
	}
	rows.Close()
	body := fmt.Sprintf("New conversation #%d in %s from %s:\n\n%s\n\nOpen it: %s\n\nChange your notifications in Desk under Settings.",
		number, mb.Name, from, subject, s.conversationURL(convID))
	return s.queueEmail(ctx, tx, to, fmt.Sprintf("[%s #%d] New: %s", mb.Name, number, subject), body)
}

// notifyAssigned emails the new assignee if they want it.
func (s *Service) notifyAssigned(ctx context.Context, tx pgx.Tx, mb Mailbox, c Conversation, assignee uuid.UUID, actor *Person) error {
	if s.notifySender() == "" {
		return nil
	}
	var email string
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(u.email, '') FROM users u JOIN notification_prefs p ON p.user_id = u.id
		 WHERE u.id = $1 AND p.assigned_to_me`, assignee).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && email == "") {
		return nil
	}
	if err != nil {
		return err
	}
	body := fmt.Sprintf("%s assigned conversation #%d in %s to you:\n\n%s\n\nOpen it: %s",
		actor.DisplayName, c.Number, mb.Name, c.Subject, s.conversationURL(c.ID))
	return s.queueEmail(ctx, tx, []string{email}, fmt.Sprintf("[%s #%d] Assigned to you: %s", mb.Name, c.Number, c.Subject), body)
}
