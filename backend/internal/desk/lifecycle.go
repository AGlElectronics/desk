package desk

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/desk/backend/internal/httperror"
	"github.com/AGlElectronics/desk/backend/internal/mailprovider"
)

// execer is a pool or a transaction.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// reminderDays: Desk admins are emailed this many days before a trashed
// mailbox is purged.
var reminderDays = []int{7, 1}

// TrashMailbox moves a mailbox to Desk's trash (docs/adr/0006). Its mail
// stops being fetched and sent, a Google mailbox is suspended (not
// deleted), and it is purged after the mailbox trash period unless an admin
// restores it first.
func (s *Service) TrashMailbox(ctx context.Context, id uuid.UUID, actor *Person) (Mailbox, error) {
	mb, err := s.GetMailbox(ctx, id)
	if err != nil {
		return mb, err
	}
	if mb.DeletedAt != nil {
		return mb, httperror.Conflict("the mailbox is already in the trash")
	}
	ret, err := s.RetentionSettings(ctx)
	if err != nil {
		return mb, err
	}
	days := *ret.Effective.MailboxTrashDays
	if _, err := s.pool.Exec(ctx, `
		UPDATE mailboxes SET deleted_at = now(), deleted_by = $2, purge_after = now() + $3::interval,
		       purge_reminders_sent = 0, updated_at = now()
		 WHERE id = $1`, id, actor.ID, fmt.Sprintf("%d days", days)); err != nil {
		return mb, err
	}
	// Replies still waiting would go out from a mailbox that is gone.
	_, _ = s.pool.Exec(ctx, `
		UPDATE threads SET send_status = 'failed', send_error = 'the mailbox was deleted', next_attempt_at = NULL
		 WHERE send_status = 'queued' AND conversation_id IN (SELECT id FROM conversations WHERE mailbox_id = $1)`, id)
	if mb.IsGoogle() && s.google != nil {
		state, msg := "suspended", ""
		if err := s.google.Provider.SuspendMailbox(ctx, s.providerRef(mb)); err != nil {
			state, msg = "error", "could not suspend the mailbox in Google Workspace: "+providerMessage(err)
		}
		_, _ = s.pool.Exec(ctx, `UPDATE mailboxes SET provider_state = $2, provider_error = $3 WHERE id = $1`, id, state, msg)
	}
	s.audit(ctx, actor, "mailbox.trash", &id, "mailbox", id.String(), "moved "+mb.Email+" to the trash",
		map[string]any{"email": mb.Email, "connector": mb.Connector, "purge_days": days})
	return s.GetMailbox(ctx, id)
}

// RestoreMailbox takes a mailbox out of the trash and resumes it in Google.
func (s *Service) RestoreMailbox(ctx context.Context, id uuid.UUID, actor *Person) (Mailbox, error) {
	mb, err := s.GetMailbox(ctx, id)
	if err != nil {
		return mb, err
	}
	if mb.DeletedAt == nil {
		return mb, httperror.Conflict("the mailbox is not in the trash")
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE mailboxes SET deleted_at = NULL, deleted_by = NULL, purge_after = NULL, purge_reminders_sent = 0,
		       fetch_failures = 0, next_fetch_at = NULL, updated_at = now()
		 WHERE id = $1`, id); err != nil {
		return mb, err
	}
	if mb.IsGoogle() && s.google != nil {
		state, msg := "active", ""
		if err := s.google.Provider.RestoreMailbox(ctx, s.providerRef(mb)); err != nil {
			state, msg = "error", "could not resume the mailbox in Google Workspace: "+providerMessage(err)
		}
		_, _ = s.pool.Exec(ctx, `UPDATE mailboxes SET provider_state = $2, provider_error = $3 WHERE id = $1`, id, state, msg)
	}
	s.audit(ctx, actor, "mailbox.restore", &id, "mailbox", id.String(), "restored "+mb.Email+" from the trash", map[string]any{"email": mb.Email})
	return s.GetMailbox(ctx, id)
}

// PurgeMailbox deletes a trashed mailbox for good: in Google Workspace, its
// files in S3 and every conversation in Desk. actor nil: the daily job.
func (s *Service) PurgeMailbox(ctx context.Context, id uuid.UUID, actor *Person) error {
	mb, err := s.GetMailbox(ctx, id)
	if err != nil {
		return err
	}
	if mb.DeletedAt == nil {
		return httperror.Conflict("only mailboxes in the trash can be purged; delete it first")
	}
	if mb.IsGoogle() && s.google != nil {
		if err := s.google.Provider.DeleteMailbox(ctx, s.providerRef(mb)); err != nil && !isNotFound(err) {
			_, _ = s.pool.Exec(ctx, `UPDATE mailboxes SET provider_state = 'error', provider_error = $2 WHERE id = $1`,
				id, "could not delete the mailbox in Google Workspace: "+providerMessage(err))
			return httperror.Conflict("Google Workspace refused to delete the mailbox; it stays in the trash: " + providerMessage(err))
		}
	}
	keys, err := s.blobKeys(ctx, `
		SELECT a.storage_key FROM attachments a JOIN threads t ON t.id = a.thread_id JOIN conversations c ON c.id = t.conversation_id
		 WHERE c.mailbox_id = $1 AND a.storage_key <> ''
		UNION ALL
		SELECT t.raw_key FROM threads t JOIN conversations c ON c.id = t.conversation_id WHERE c.mailbox_id = $1 AND t.raw_key <> ''`, id)
	if err != nil {
		return err
	}
	var convs int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM conversations WHERE mailbox_id = $1`, id).Scan(&convs)
	if _, err := s.pool.Exec(ctx, `DELETE FROM mailboxes WHERE id = $1`, id); err != nil {
		return err
	}
	s.deleteBlobs(ctx, keys)
	s.audit(ctx, actor, "mailbox.purge", &id, "mailbox", id.String(), "purged "+mb.Email,
		map[string]any{"email": mb.Email, "conversations": convs, "files": len(keys)})
	return nil
}

func isNotFound(err error) bool { return errors.Is(err, mailprovider.ErrNotFound) }

func (s *Service) blobKeys(ctx context.Context, sql string, args ...any) ([]string, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if rows.Scan(&k) == nil && k != "" {
			keys = append(keys, k)
		}
	}
	return keys, rows.Err()
}

// deleteBlobs removes files whose rows are already gone. A failure leaves
// an orphan object, never a row without its file.
func (s *Service) deleteBlobs(ctx context.Context, keys []string) {
	for _, k := range keys {
		_ = s.blobs.Delete(context.WithoutCancel(ctx), k)
	}
}

// TrashedMailboxes lists mailboxes in the trash, soonest purge first.
func (s *Service) TrashedMailboxes(ctx context.Context) ([]Mailbox, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+mailboxCols+` FROM mailboxes WHERE deleted_at IS NOT NULL ORDER BY purge_after`)
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

// purgeDueMailboxes purges trashed mailboxes past purge_after and emails
// Desk admins the reminders that are due.
func (s *Service) purgeDueMailboxes(ctx context.Context, now time.Time, log zerolog.Logger) {
	boxes, err := s.TrashedMailboxes(ctx)
	if err != nil {
		log.Error().Err(err).Msg("list trashed mailboxes")
		return
	}
	for _, mb := range boxes {
		if mb.PurgeAfter == nil {
			continue
		}
		if !now.Before(*mb.PurgeAfter) {
			if err := s.PurgeMailbox(ctx, mb.ID, nil); err != nil {
				log.Warn().Err(err).Str("mailbox", mb.Email).Msg("automatic purge failed")
			} else {
				log.Info().Str("mailbox", mb.Email).Msg("mailbox purged")
			}
			continue
		}
		s.remindPurge(ctx, mb, now, log)
	}
}

func (s *Service) remindPurge(ctx context.Context, mb Mailbox, now time.Time, log zerolog.Logger) {
	var sent int
	if err := s.pool.QueryRow(ctx, `SELECT purge_reminders_sent FROM mailboxes WHERE id = $1`, mb.ID).Scan(&sent); err != nil {
		return
	}
	left := mb.PurgeAfter.Sub(now)
	due := 0
	for i, d := range reminderDays {
		if left <= time.Duration(d)*24*time.Hour {
			due = i + 1
		}
	}
	if due <= sent {
		return
	}
	admins, err := s.adminEmails(ctx, mb.OrganizationID)
	if err != nil {
		return
	}
	subject := fmt.Sprintf("Desk: mailbox %s will be deleted for good on %s", mb.Email, mb.PurgeAfter.UTC().Format("2 January 2006"))
	body := fmt.Sprintf(`The mailbox %s (%s) is in Desk's trash.

On %s Desk deletes it for good: every conversation, attachment and stored
email in Desk, and the mailbox in Google Workspace if it has one. This
cannot be undone.

To keep it, open %s/mailboxes, choose "Trash" and restore it before then.

You get this email because you are a Desk admin.`, mb.Name, mb.Email, formatDate(*mb.PurgeAfter), s.baseURL)
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.queueEmail(ctx, tx, admins, subject, body); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE mailboxes SET purge_reminders_sent = $2 WHERE id = $1`, mb.ID, due)
		return err
	})
	if err != nil {
		log.Warn().Err(err).Str("mailbox", mb.Email).Msg("purge reminder failed")
		return
	}
	s.audit(ctx, nil, "mailbox.purge_reminder", &mb.ID, "mailbox", mb.ID.String(),
		fmt.Sprintf("reminded %d admins about the purge of %s", len(admins), mb.Email), map[string]any{"recipients": admins})
}
