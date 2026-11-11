package desk

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

// TrashWarning is returned when Desk's trash worked but the provider's
// did not (the email stays in the mailbox at Google until retried).
type TrashWarning struct {
	Warning string `json:"warning,omitempty"`
}

// DeleteThread moves one email (or note) to Desk's trash and, for Google
// mailboxes, the message to Gmail's trash. Needs the delete role.
func (s *Service) DeleteThread(ctx context.Context, mb Mailbox, threadID uuid.UUID, actor *Person) (TrashWarning, error) {
	var kind, providerID string
	var status *string
	var deleted *time.Time
	var convID uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT conversation_id, kind, send_status, provider_message_id, deleted_at FROM threads WHERE id = $1`, threadID).
		Scan(&convID, &kind, &status, &providerID, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return TrashWarning{}, httperror.NotFound("thread")
	}
	if err != nil {
		return TrashWarning{}, err
	}
	switch {
	case deleted != nil:
		return TrashWarning{}, httperror.Conflict("already in the trash")
	case kind == "system":
		return TrashWarning{}, httperror.BadRequest("system lines cannot be deleted")
	case status != nil && (*status == "queued" || *status == "sending"):
		return TrashWarning{}, httperror.Conflict("this reply is still being sent; undo it instead")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE threads SET deleted_at = now(), deleted_by = $2 WHERE id = $1`, threadID, actor.ID); err != nil {
		return TrashWarning{}, err
	}
	var w TrashWarning
	if providerID != "" {
		if err := s.trashProviderMessages(ctx, mb, []string{providerID}, true); err != nil {
			w.Warning = "deleted in Desk, but Google Workspace did not move it to its trash: " + providerMessage(err)
		}
	}
	s.audit(ctx, actor, "thread.delete", &mb.ID, "thread", threadID.String(), "deleted a "+kind+" email",
		map[string]any{"conversation_id": convID, "provider_message_id": providerID, "warning": w.Warning})
	return w, nil
}

// RestoreThread brings an email back from Desk's (and Gmail's) trash.
func (s *Service) RestoreThread(ctx context.Context, mb Mailbox, threadID uuid.UUID, actor *Person) (TrashWarning, error) {
	var providerID string
	err := s.pool.QueryRow(ctx, `UPDATE threads SET deleted_at = NULL, deleted_by = NULL WHERE id = $1 AND deleted_at IS NOT NULL RETURNING provider_message_id`, threadID).Scan(&providerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return TrashWarning{}, httperror.Conflict("the email is not in the trash")
	}
	if err != nil {
		return TrashWarning{}, err
	}
	var w TrashWarning
	if err := s.trashProviderMessages(ctx, mb, []string{providerID}, false); err != nil {
		w.Warning = "restored in Desk, but Google Workspace did not take it out of its trash: " + providerMessage(err)
	}
	s.audit(ctx, actor, "thread.restore", &mb.ID, "thread", threadID.String(), "restored an email", nil)
	return w, nil
}

func (s *Service) conversationProviderIDs(ctx context.Context, convID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT provider_message_id FROM threads WHERE conversation_id = $1 AND provider_message_id <> '' AND deleted_at IS NULL`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// DeleteConversation moves a conversation with all its emails to Desk's
// trash (and Gmail's). Queued replies in it are cancelled.
func (s *Service) DeleteConversation(ctx context.Context, c Conversation, mb Mailbox, actor *Person) (TrashWarning, error) {
	if c.DeletedAt != nil {
		return TrashWarning{}, httperror.Conflict("already in the trash")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE conversations SET deleted_at = now(), deleted_by = $2 WHERE id = $1`, c.ID, actor.ID); err != nil {
		return TrashWarning{}, err
	}
	_, _ = s.pool.Exec(ctx, `
		UPDATE threads SET send_status = 'failed', send_error = 'the conversation was deleted', next_attempt_at = NULL
		 WHERE conversation_id = $1 AND send_status = 'queued'`, c.ID)
	var w TrashWarning
	ids, err := s.conversationProviderIDs(ctx, c.ID)
	if err == nil {
		if err := s.trashProviderMessages(ctx, mb, ids, true); err != nil {
			w.Warning = "deleted in Desk, but Google Workspace did not move every email to its trash: " + providerMessage(err)
		}
	}
	s.audit(ctx, actor, "conversation.delete", &mb.ID, "conversation", c.ID.String(),
		fmt.Sprintf("deleted conversation #%d %q", c.Number, c.Subject), map[string]any{"provider_messages": len(ids), "warning": w.Warning})
	return w, nil
}

// RestoreConversation brings a conversation back from the trash.
func (s *Service) RestoreConversation(ctx context.Context, c Conversation, mb Mailbox, actor *Person) (TrashWarning, error) {
	if c.DeletedAt == nil {
		return TrashWarning{}, httperror.Conflict("the conversation is not in the trash")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE conversations SET deleted_at = NULL, deleted_by = NULL WHERE id = $1`, c.ID); err != nil {
		return TrashWarning{}, err
	}
	var w TrashWarning
	if ids, err := s.conversationProviderIDs(ctx, c.ID); err == nil {
		if err := s.trashProviderMessages(ctx, mb, ids, false); err != nil {
			w.Warning = "restored in Desk, but Google Workspace did not restore every email: " + providerMessage(err)
		}
	}
	s.audit(ctx, actor, "conversation.restore", &mb.ID, "conversation", c.ID.String(), fmt.Sprintf("restored conversation #%d", c.Number), nil)
	return w, nil
}

// RetentionResult counts what one retention run removed.
type RetentionResult struct {
	TrashedConversations int `json:"trashed_conversations"`
	TrashedThreads       int `json:"trashed_threads"`
	ClosedConversations  int `json:"closed_conversations"`
	Attachments          int `json:"attachments"`
	RawMessages          int `json:"raw_messages"`
}

func (r RetentionResult) total() int {
	return r.TrashedConversations + r.TrashedThreads + r.ClosedConversations + r.Attachments + r.RawMessages
}

func days(n int) string { return fmt.Sprintf("%d days", n) }

// applyRetention empties the trashes and applies the retention periods
// (docs/adr/0005). Files are deleted after their rows.
func (s *Service) applyRetention(ctx context.Context, now time.Time, log zerolog.Logger) {
	res, err := s.ApplyRetention(ctx, now)
	if err != nil {
		log.Error().Err(err).Msg("retention")
		return
	}
	if res.total() > 0 {
		log.Info().Interface("purged", res).Msg("retention applied")
		s.audit(ctx, nil, "retention.purge", nil, "settings", "retention", "retention purge", res)
	}
}

// ApplyRetention runs one retention pass as of now.
func (s *Service) ApplyRetention(ctx context.Context, now time.Time) (RetentionResult, error) {
	var res RetentionResult
	view, err := s.RetentionSettings(ctx)
	if err != nil {
		return res, err
	}
	r := view.Effective
	convFiles := `
		SELECT a.storage_key FROM attachments a JOIN threads t ON t.id = a.thread_id WHERE t.conversation_id = ANY($1) AND a.storage_key <> ''
		UNION ALL SELECT raw_key FROM threads WHERE conversation_id = ANY($1) AND raw_key <> ''`
	purgeConversations := func(where string, args ...any) (int, error) {
		ids, err := s.ids(ctx, `SELECT id FROM conversations WHERE `+where+` LIMIT 1000`, args...)
		if err != nil || len(ids) == 0 {
			return 0, err
		}
		keys, err := s.blobKeys(ctx, convFiles, ids)
		if err != nil {
			return 0, err
		}
		tag, err := s.pool.Exec(ctx, `DELETE FROM conversations WHERE id = ANY($1)`, ids)
		if err != nil {
			return 0, err
		}
		s.deleteBlobs(ctx, keys)
		return int(tag.RowsAffected()), nil
	}

	if res.TrashedConversations, err = purgeConversations(`deleted_at < $1::timestamptz - $2::interval`, now, days(*r.TrashDays)); err != nil {
		return res, err
	}
	threadIDs, err := s.ids(ctx, `SELECT id FROM threads WHERE deleted_at < $1::timestamptz - $2::interval LIMIT 5000`, now, days(*r.TrashDays))
	if err != nil {
		return res, err
	}
	if len(threadIDs) > 0 {
		keys, err := s.blobKeys(ctx, `
			SELECT storage_key FROM attachments WHERE thread_id = ANY($1) AND storage_key <> ''
			UNION ALL SELECT raw_key FROM threads WHERE id = ANY($1) AND raw_key <> ''`, threadIDs)
		if err != nil {
			return res, err
		}
		tag, err := s.pool.Exec(ctx, `DELETE FROM threads WHERE id = ANY($1)`, threadIDs)
		if err != nil {
			return res, err
		}
		s.deleteBlobs(ctx, keys)
		res.TrashedThreads = int(tag.RowsAffected())
	}
	if r.ClosedConversationDays != nil {
		if res.ClosedConversations, err = purgeConversations(`status = 'closed' AND closed_at < $1::timestamptz - $2::interval`, now, days(*r.ClosedConversationDays)); err != nil {
			return res, err
		}
	}
	if r.AttachmentDays != nil {
		keys, err := s.blobKeys(ctx, `
			DELETE FROM attachments WHERE created_at < $1::timestamptz - $2::interval RETURNING storage_key`, now, days(*r.AttachmentDays))
		if err != nil {
			return res, err
		}
		s.deleteBlobs(ctx, keys)
		res.Attachments = len(keys)
	}
	if r.RawMailDays != nil {
		keys, err := s.blobKeys(ctx, `
			UPDATE threads t SET raw_key = '' FROM (SELECT id, raw_key FROM threads
			  WHERE raw_key <> '' AND created_at < $1::timestamptz - $2::interval FOR UPDATE) old
			 WHERE t.id = old.id RETURNING old.raw_key`, now, days(*r.RawMailDays))
		if err != nil {
			return res, err
		}
		s.deleteBlobs(ctx, keys)
		res.RawMessages = len(keys)
	}
	return res, nil
}

func (s *Service) ids(ctx context.Context, sql string, args ...any) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
