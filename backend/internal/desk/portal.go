package desk

import (
	"context"
)

// PortalTodo is one item of GET /api/portal/todos (Den's portal contract).
type PortalTodo struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	DueAt    any    `json:"due_at,omitempty"`
	URL      string `json:"url"`
	Severity string `json:"severity,omitempty"`
}

// PortalTodos lists open conversations assigned to the person Den asked
// about. An unknown person gets an empty list, not an error. At most 50.
func (s *Service) PortalTodos(ctx context.Context, issuer, subject string) ([]PortalTodo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id::text, c.subject, m.name, c.status
		  FROM conversations c
		  JOIN mailboxes m ON m.id = c.mailbox_id
		  JOIN users u ON u.id = c.assignee_id
		 WHERE u.issuer = $1 AND u.subject = $2
		   AND c.status IN ('active', 'pending')
		   AND c.deleted_at IS NULL
		   AND m.deleted_at IS NULL
		 ORDER BY c.last_activity_at
		 LIMIT 50`, issuer, subject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PortalTodo{}
	for rows.Next() {
		var id, subject, mailbox, status string
		if err := rows.Scan(&id, &subject, &mailbox, &status); err != nil {
			return nil, err
		}
		title := subject
		if title == "" {
			title = "Conversation"
		}
		severity := "info"
		if status == "pending" {
			severity = "warning"
		}
		out = append(out, PortalTodo{
			ID: id, Title: title, Detail: mailbox, URL: s.baseURL + "/conversations/" + id, Severity: severity,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []PortalTodo{}
	}
	return out, nil
}
