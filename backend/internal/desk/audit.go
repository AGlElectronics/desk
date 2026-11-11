package desk

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AuditEntry is one line of Desk's audit log.
type AuditEntry struct {
	ID         int64           `json:"id"`
	ActorID    *uuid.UUID      `json:"actor_id,omitempty"`
	ActorName  string          `json:"actor_name"`
	Action     string          `json:"action"`
	MailboxID  *uuid.UUID      `json:"mailbox_id,omitempty"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id"`
	Summary    string          `json:"summary"`
	Details    json.RawMessage `json:"details"`
	CreatedAt  time.Time       `json:"created_at"`
}

// audit records who did what. actor nil means Desk itself (a job). It
// never fails the caller: losing an audit line is logged by the caller's
// request log, not turned into a user-visible error.
func (s *Service) audit(ctx context.Context, actor *Person, action string, mailboxID *uuid.UUID, targetType, targetID, summary string, details any) {
	var actorID *uuid.UUID
	name := "Desk"
	if actor != nil {
		actorID, name = &actor.ID, actor.DisplayName
	}
	raw, err := json.Marshal(details)
	if err != nil || details == nil {
		raw = []byte("{}")
	}
	_, _ = s.pool.Exec(context.WithoutCancel(ctx), `
		INSERT INTO audit_log (actor_id, actor_name, action, mailbox_id, target_type, target_id, summary, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, actorID, name, action, mailboxID, targetType, targetID, summary, raw)
}

// AuditFilter narrows the audit log.
type AuditFilter struct {
	MailboxID *uuid.UUID
	Action    string
	// Before is the id to continue below (pagination).
	Before int64
	Limit  int
}

// AuditLog lists entries newest first.
func (s *Service) AuditLog(ctx context.Context, f AuditFilter) ([]AuditEntry, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, actor_id, actor_name, action, mailbox_id, target_type, target_id, summary, details, created_at
		  FROM audit_log
		 WHERE ($1::uuid IS NULL OR mailbox_id = $1) AND ($2 = '' OR action = $2) AND ($3 = 0 OR id < $3)
		 ORDER BY id DESC LIMIT $4`, f.MailboxID, f.Action, f.Before, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorName, &e.Action, &e.MailboxID, &e.TargetType, &e.TargetID,
			&e.Summary, &e.Details, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
