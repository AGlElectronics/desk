package desk

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

// MergeConversations moves every thread of src into dst and deletes src.
// Both must be in the same organization; the caller must work both
// mailboxes (checked by the handler).
func (s *Service) MergeConversations(ctx context.Context, src, dst Conversation, srcMB, dstMB Mailbox, actor *Person) error {
	switch {
	case src.ID == dst.ID:
		return httperror.BadRequest("a conversation cannot be merged into itself")
	case srcMB.OrganizationID != dstMB.OrganizationID:
		return httperror.BadRequest("conversations of different organizations cannot be merged")
	case src.DeletedAt != nil || dst.DeletedAt != nil:
		return httperror.Conflict("restore the conversation from the trash first")
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for _, q := range []string{
			`UPDATE threads SET conversation_id = $2 WHERE conversation_id = $1`,
			`UPDATE message_ids SET conversation_id = $2 WHERE conversation_id = $1`,
			`INSERT INTO conversation_tags (conversation_id, tag) SELECT $2, tag FROM conversation_tags WHERE conversation_id = $1 ON CONFLICT DO NOTHING`,
			`INSERT INTO conversation_stars (conversation_id, user_id) SELECT $2, user_id FROM conversation_stars WHERE conversation_id = $1 ON CONFLICT DO NOTHING`,
			`UPDATE conversations d SET last_activity_at = greatest(d.last_activity_at, s.last_activity_at)
			   FROM conversations s WHERE s.id = $1 AND d.id = $2`,
			`DELETE FROM conversations WHERE id = $1 AND $2::uuid IS NOT NULL`,
		} {
			if _, err := tx.Exec(ctx, q, src.ID, dst.ID); err != nil {
				return err
			}
		}
		return systemLine(ctx, tx, dst.ID, actor.ID, fmt.Sprintf("Merged %s #%d (%s) into this conversation", srcMB.Name, src.Number, src.Subject))
	})
	if err != nil {
		return err
	}
	s.audit(ctx, actor, "conversation.merge", &dstMB.ID, "conversation", dst.ID.String(),
		fmt.Sprintf("merged #%d into #%d", src.Number, dst.Number), map[string]any{"source_id": src.ID, "source_mailbox": srcMB.ID})
	return nil
}

// MoveConversation moves a conversation to another mailbox of the same
// organization, where it gets the next number. Replies go out from the new
// mailbox; mail already in the old mailbox's provider account stays there.
func (s *Service) MoveConversation(ctx context.Context, c Conversation, from, to Mailbox, actor *Person) error {
	switch {
	case from.ID == to.ID:
		return httperror.BadRequest("the conversation is already in this mailbox")
	case from.OrganizationID != to.OrganizationID:
		return httperror.BadRequest("conversations can only move within one organization")
	case to.DeletedAt != nil:
		return httperror.Conflict("the target mailbox is in the trash")
	case c.DeletedAt != nil:
		return httperror.Conflict("restore the conversation from the trash first")
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		n, err := nextNumber(ctx, tx, to.ID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE conversations SET mailbox_id = $2, number = $3 WHERE id = $1`, c.ID, to.ID, n); err != nil {
			return err
		}
		return systemLine(ctx, tx, c.ID, actor.ID, fmt.Sprintf("Moved from %s #%d", from.Name, c.Number))
	})
	if err != nil {
		return err
	}
	s.audit(ctx, actor, "conversation.move", &to.ID, "conversation", c.ID.String(),
		fmt.Sprintf("moved #%d from %s to %s", c.Number, from.Email, to.Email), map[string]any{"from_mailbox": from.ID})
	return nil
}
