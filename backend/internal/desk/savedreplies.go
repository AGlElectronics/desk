package desk

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

// SavedReply is a reusable answer, for one mailbox or for all of them.
type SavedReply struct {
	ID        uuid.UUID  `json:"id"`
	MailboxID *uuid.UUID `json:"mailbox_id"`
	Name      string     `json:"name"`
	BodyHTML  string     `json:"body_html"`
	BodyText  string     `json:"body_text"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// SavedReplyInput creates or changes a saved reply.
type SavedReplyInput struct {
	MailboxID *uuid.UUID `json:"mailbox_id"`
	Name      string     `json:"name"`
	BodyHTML  string     `json:"body_html"`
	BodyText  string     `json:"body_text"`
}

func (in *SavedReplyInput) normalize() error {
	in.Name = strings.TrimSpace(clean(in.Name))
	in.BodyHTML, in.BodyText = composeBodies(in.BodyText, in.BodyHTML)
	issues := map[string]string{}
	if in.Name == "" || len([]rune(in.Name)) > 120 {
		issues["name"] = "required, at most 120 characters"
	}
	if in.BodyText == "" {
		issues["body_html"] = "required"
	}
	if len(issues) > 0 {
		return httperror.Validation("invalid saved reply", issues)
	}
	return nil
}

// SavedReplies lists the replies for a mailbox (its own and the global
// ones), by name.
func (s *Service) SavedReplies(ctx context.Context, mailboxID uuid.UUID) ([]SavedReply, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, mailbox_id, name, body_html, body_text, updated_at FROM saved_replies
		 WHERE mailbox_id = $1 OR mailbox_id IS NULL ORDER BY name`, mailboxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SavedReply{}
	for rows.Next() {
		var r SavedReply
		if err := rows.Scan(&r.ID, &r.MailboxID, &r.Name, &r.BodyHTML, &r.BodyText, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetSavedReply loads one saved reply.
func (s *Service) GetSavedReply(ctx context.Context, id uuid.UUID) (SavedReply, error) {
	var r SavedReply
	err := s.pool.QueryRow(ctx, `SELECT id, mailbox_id, name, body_html, body_text, updated_at FROM saved_replies WHERE id = $1`, id).
		Scan(&r.ID, &r.MailboxID, &r.Name, &r.BodyHTML, &r.BodyText, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, httperror.NotFound("saved reply")
	}
	return r, err
}

// CreateSavedReply stores a saved reply.
func (s *Service) CreateSavedReply(ctx context.Context, in SavedReplyInput, actor *Person) (SavedReply, error) {
	if err := in.normalize(); err != nil {
		return SavedReply{}, err
	}
	var id uuid.UUID
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO saved_replies (mailbox_id, name, body_html, body_text, created_by) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		in.MailboxID, in.Name, in.BodyHTML, in.BodyText, actor.ID).Scan(&id); err != nil {
		return SavedReply{}, err
	}
	return s.GetSavedReply(ctx, id)
}

// UpdateSavedReply changes name and text (not the mailbox).
func (s *Service) UpdateSavedReply(ctx context.Context, id uuid.UUID, in SavedReplyInput) (SavedReply, error) {
	if err := in.normalize(); err != nil {
		return SavedReply{}, err
	}
	if _, err := s.pool.Exec(ctx, `UPDATE saved_replies SET name = $2, body_html = $3, body_text = $4, updated_at = now() WHERE id = $1`,
		id, in.Name, in.BodyHTML, in.BodyText); err != nil {
		return SavedReply{}, err
	}
	return s.GetSavedReply(ctx, id)
}

// DeleteSavedReply removes a saved reply.
func (s *Service) DeleteSavedReply(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM saved_replies WHERE id = $1`, id)
	return err
}
