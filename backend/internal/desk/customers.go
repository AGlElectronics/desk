package desk

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

// CustomerProfile is a customer with every address and their
// conversations in mailboxes the caller may see.
type CustomerProfile struct {
	Customer
	OrganizationID uuid.UUID      `json:"organization_id"`
	Emails         []string       `json:"emails"`
	Conversations  []Conversation `json:"conversations"`
	CreatedAt      time.Time      `json:"created_at"`
	// CanEdit: the caller may change notes and addresses.
	CanEdit bool `json:"can_edit"`
}

type customerRow struct {
	Customer
	org       uuid.UUID
	createdAt time.Time
}

func (s *Service) loadCustomer(ctx context.Context, id uuid.UUID) (customerRow, error) {
	var c customerRow
	err := s.pool.QueryRow(ctx, `SELECT id, email::text, name, notes, organization_id, created_at FROM customers WHERE id = $1`, id).
		Scan(&c.ID, &c.Email, &c.Name, &c.Notes, &c.org, &c.createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, httperror.NotFound("customer")
	}
	return c, err
}

// customerAccess tells whether the caller may see (a conversation of the
// customer is in a mailbox they can view, or they are admin of its
// organization) and edit (they may work one of those mailboxes) a customer.
func (s *Service) customerAccess(ctx context.Context, a Access, c customerRow) (see, edit bool, err error) {
	if a.IsAdmin(c.org) {
		return true, true, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT `+prefixed("m", mailboxCols)+` FROM conversations c JOIN mailboxes m ON m.id = c.mailbox_id
		 WHERE c.customer_id = $1 AND m.deleted_at IS NULL`, c.ID)
	if err != nil {
		return false, false, err
	}
	defer rows.Close()
	for rows.Next() {
		mb, err := scanMailbox(rows)
		if err != nil {
			return false, false, err
		}
		see = see || a.CanView(mb)
		edit = edit || a.CanWork(mb)
	}
	return see, edit, rows.Err()
}

// prefixed qualifies a column list with a table alias.
func prefixed(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// CustomerProfile loads a customer the caller may see (404 otherwise).
func (s *Service) CustomerProfile(ctx context.Context, a Access, id uuid.UUID) (CustomerProfile, error) {
	c, err := s.loadCustomer(ctx, id)
	if err != nil {
		return CustomerProfile{}, err
	}
	see, edit, err := s.customerAccess(ctx, a, c)
	if err != nil {
		return CustomerProfile{}, err
	}
	if !see {
		return CustomerProfile{}, httperror.NotFound("customer")
	}
	p := CustomerProfile{Customer: c.Customer, OrganizationID: c.org, CreatedAt: c.createdAt, CanEdit: edit, Emails: []string{}}
	rows, err := s.pool.Query(ctx, `SELECT email::text FROM customer_emails WHERE customer_id = $1 ORDER BY created_at`, id)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var e string
		if rows.Scan(&e) == nil {
			p.Emails = append(p.Emails, e)
		}
	}
	rows.Close()
	visible, err := s.VisibleMailboxes(ctx, a)
	if err != nil {
		return p, err
	}
	ids := make([]uuid.UUID, 0, len(visible))
	for _, m := range visible {
		ids = append(ids, m.ID)
	}
	p.Conversations, err = s.queryConversations(ctx,
		"WHERE c.customer_id = $2 AND c.mailbox_id = ANY($3) AND c.deleted_at IS NULL ORDER BY c.last_activity_at DESC LIMIT 100", a.UserID, id, ids)
	return p, err
}

// SearchCustomers finds customers by name or address among those with a
// conversation in a mailbox the caller may view (Desk admins: their
// organizations), at most 20.
func (s *Service) SearchCustomers(ctx context.Context, a Access, q string) ([]Customer, error) {
	q = strings.TrimSpace(q)
	if len([]rune(q)) < 2 {
		return []Customer{}, nil
	}
	visible, err := s.VisibleMailboxes(ctx, a)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(visible))
	for _, m := range visible {
		ids = append(ids, m.ID)
	}
	like := "%" + escapeLike(q) + "%"
	rows, err := s.pool.Query(ctx, `
		SELECT cu.id, cu.email::text, cu.name, cu.notes FROM customers cu
		 WHERE (cu.organization_id = ANY($1)
		        OR EXISTS (SELECT 1 FROM conversations c WHERE c.customer_id = cu.id AND c.mailbox_id = ANY($2)))
		   AND (cu.name ILIKE $3 OR EXISTS (SELECT 1 FROM customer_emails ce WHERE ce.customer_id = cu.id AND ce.email ILIKE $3))
		 ORDER BY cu.name, cu.email LIMIT 20`, a.AdminOrgs(), ids, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Customer{}
	for rows.Next() {
		var c Customer
		if err := rows.Scan(&c.ID, &c.Email, &c.Name, &c.Notes); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CustomerInput changes a customer.
type CustomerInput struct {
	Name  *string `json:"name"`
	Notes *string `json:"notes"`
	// AddEmail adds another address the customer writes from.
	AddEmail *string `json:"add_email"`
	// RemoveEmail removes an extra address (not the main one).
	RemoveEmail *string `json:"remove_email"`
}

// UpdateCustomer changes a customer the caller may edit and records who
// did it.
func (s *Service) UpdateCustomer(ctx context.Context, a Access, id uuid.UUID, in CustomerInput, actor *Person) error {
	c, err := s.loadCustomer(ctx, id)
	if err != nil {
		return err
	}
	see, edit, err := s.customerAccess(ctx, a, c)
	if err != nil {
		return err
	}
	if !see {
		return httperror.NotFound("customer")
	}
	if !edit {
		return errForbidden("manager")
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if in.Name != nil || in.Notes != nil {
			tag, err := tx.Exec(ctx, `
				UPDATE customers SET name = COALESCE($2, name), notes = COALESCE($3, notes), updated_at = now(), updated_by = $4
				 WHERE id = $1`, id, trimPtr(in.Name), in.Notes, actor.ID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return httperror.NotFound("customer")
			}
		}
		if in.AddEmail != nil {
			addr, err := mail.ParseAddress(strings.TrimSpace(*in.AddEmail))
			if err != nil {
				return httperror.Validation("invalid address", map[string]string{"add_email": "must be an email address"})
			}
			var owner uuid.UUID
			err = tx.QueryRow(ctx, `SELECT customer_id FROM customer_emails WHERE organization_id = $1 AND email = $2`, c.org, addr.Address).Scan(&owner)
			switch {
			case err == nil && owner == id:
			case err == nil:
				return httperror.Conflict("another customer uses this address; merge the two customers instead")
			case errors.Is(err, pgx.ErrNoRows):
				if _, err := tx.Exec(ctx, `INSERT INTO customer_emails (customer_id, organization_id, email) VALUES ($1, $2, $3)`, id, c.org, addr.Address); err != nil {
					return err
				}
			default:
				return err
			}
		}
		if in.RemoveEmail != nil {
			if strings.EqualFold(strings.TrimSpace(*in.RemoveEmail), c.Email) {
				return httperror.Validation("main address", map[string]string{"remove_email": "the main address cannot be removed"})
			}
			if _, err := tx.Exec(ctx, `DELETE FROM customer_emails WHERE customer_id = $1 AND email = $2`, id, strings.TrimSpace(*in.RemoveEmail)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.audit(ctx, actor, "customer.update", nil, "customer", id.String(), "changed customer "+c.Email, map[string]any{
		"name": in.Name != nil, "notes": in.Notes != nil, "add_email": in.AddEmail, "remove_email": in.RemoveEmail,
	})
	return nil
}

func trimPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	return &v
}

// MergeCustomers moves everything of source (addresses, conversations,
// notes) into target and deletes source. The caller must be able to edit
// both, and both must belong to the same organization.
func (s *Service) MergeCustomers(ctx context.Context, a Access, targetID, sourceID uuid.UUID, actor *Person) error {
	if targetID == sourceID {
		return httperror.BadRequest("a customer cannot be merged into itself")
	}
	target, err := s.loadCustomer(ctx, targetID)
	if err != nil {
		return err
	}
	source, err := s.loadCustomer(ctx, sourceID)
	if err != nil {
		return err
	}
	for _, c := range []customerRow{target, source} {
		see, edit, err := s.customerAccess(ctx, a, c)
		if err != nil {
			return err
		}
		if !see {
			return httperror.NotFound("customer")
		}
		if !edit {
			return errForbidden("manager")
		}
	}
	if target.org != source.org {
		return httperror.BadRequest("customers of different organizations cannot be merged")
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE customer_emails SET customer_id = $1 WHERE customer_id = $2`, targetID, sourceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE conversations SET customer_id = $1 WHERE customer_id = $2`, targetID, sourceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE customers SET notes = CASE WHEN $2 = '' THEN notes WHEN notes = '' THEN $2 ELSE notes || E'\n\n' || $2 END,
			       name = CASE WHEN name = '' THEN $3 ELSE name END, updated_at = now(), updated_by = $4
			 WHERE id = $1`, targetID, source.Notes, source.Name, actor.ID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM customers WHERE id = $1`, sourceID)
		return err
	})
	if err != nil {
		return err
	}
	s.audit(ctx, actor, "customer.merge", nil, "customer", targetID.String(), "merged "+source.Email+" into "+target.Email,
		map[string]any{"source_id": sourceID, "source_email": source.Email})
	return nil
}
