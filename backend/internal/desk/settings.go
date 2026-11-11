package desk

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

// Retention says how long Desk keeps what (docs/adr/0005). A nil period
// means "keep": the defaults for conversations, attachments and raw mail
// are pending legal review, so nothing is purged until someone decides.
type Retention struct {
	// ClosedConversationDays: closed conversations (with their threads,
	// attachments and raw mail) are deleted this long after closing.
	ClosedConversationDays *int `json:"closed_conversation_days"`
	// AttachmentDays: attachments are deleted this long after arrival.
	AttachmentDays *int `json:"attachment_days"`
	// RawMailDays: the raw .eml copies are deleted this long after arrival.
	RawMailDays *int `json:"raw_mail_days"`
	// TrashDays: deleted emails and conversations stay in Desk's trash this
	// long (Gmail keeps its trash 30 days).
	TrashDays *int `json:"trash_days"`
	// MailboxTrashDays: a deleted mailbox is purged this long after deletion.
	MailboxTrashDays *int `json:"mailbox_trash_days"`
}

const (
	defaultTrashDays        = 30
	defaultMailboxTrashDays = 30
	maxRetentionDays        = 3650
)

// RetentionView is what the settings page shows.
type RetentionView struct {
	// Effective is what the purge jobs use.
	Effective Retention `json:"effective"`
	// Desk is Desk's own setting (used where Den sets nothing).
	Desk Retention `json:"desk"`
	// Den is what Den's module settings set (nil fields: not set by Den).
	Den *Retention `json:"den,omitempty"`
	// ManagedBy lists the fields Den controls; Desk cannot change those.
	ManagedBy map[string]string `json:"managed_by"`
	// PendingLegalReview lists the fields still on "keep" by default.
	PendingLegalReview []string `json:"pending_legal_review"`
}

func (r Retention) fields() map[string]*int {
	return map[string]*int{
		"closed_conversation_days": r.ClosedConversationDays,
		"attachment_days":          r.AttachmentDays,
		"raw_mail_days":            r.RawMailDays,
		"trash_days":               r.TrashDays,
		"mailbox_trash_days":       r.MailboxTrashDays,
	}
}

func (r *Retention) set(field string, v *int) {
	switch field {
	case "closed_conversation_days":
		r.ClosedConversationDays = v
	case "attachment_days":
		r.AttachmentDays = v
	case "raw_mail_days":
		r.RawMailDays = v
	case "trash_days":
		r.TrashDays = v
	case "mailbox_trash_days":
		r.MailboxTrashDays = v
	}
}

// denRetention reads desk.retention.* from Den's module settings. A key Den
// leaves out stays on Desk's own setting. The shared retention section
// (retention.module_data_days and the like) is not applied: those periods
// are not the same decisions as Desk's closed-conversation and trash periods.
func (s *Service) denRetention(context.Context) (*Retention, error) {
	ms := s.moduleSettings()
	if ms == nil {
		return nil, nil
	}
	r := Retention{}
	anySet := false
	set := func(field, key string) {
		d, ok := ms.Days("desk.retention." + key)
		if !ok {
			return
		}
		anySet = true
		r.set(field, &d)
	}
	set("closed_conversation_days", "closed_conversation_days")
	set("attachment_days", "attachment_days")
	set("raw_mail_days", "raw_mail_days")
	set("trash_days", "trash_days")
	set("mailbox_trash_days", "mailbox_trash_days")
	if !anySet {
		return nil, nil
	}
	return &r, nil
}

// RetentionSettings returns the effective retention and where it comes from.
func (s *Service) RetentionSettings(ctx context.Context) (RetentionView, error) {
	var own Retention
	if err := s.getSetting(ctx, "retention", &own); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return RetentionView{}, err
	}
	den, err := s.denRetention(ctx)
	if err != nil {
		return RetentionView{}, err
	}
	v := RetentionView{Desk: own, Den: den, ManagedBy: map[string]string{}, PendingLegalReview: []string{}}
	eff := own
	if den != nil {
		for field, val := range den.fields() {
			if val != nil {
				eff.set(field, val)
				v.ManagedBy[field] = "den"
			}
		}
	}
	if eff.TrashDays == nil {
		eff.TrashDays = ptrInt(defaultTrashDays)
	}
	if eff.MailboxTrashDays == nil {
		eff.MailboxTrashDays = ptrInt(defaultMailboxTrashDays)
	}
	for _, f := range []string{"closed_conversation_days", "attachment_days", "raw_mail_days"} {
		if eff.fields()[f] == nil {
			v.PendingLegalReview = append(v.PendingLegalReview, f)
		}
	}
	v.Effective = eff
	return v, nil
}

// SetRetention stores Desk's own retention. Fields Den manages are refused.
func (s *Service) SetRetention(ctx context.Context, in Retention, actor *Person) (RetentionView, error) {
	cur, err := s.RetentionSettings(ctx)
	if err != nil {
		return cur, err
	}
	issues := map[string]string{}
	for field, val := range in.fields() {
		if val == nil {
			continue
		}
		if _, den := cur.ManagedBy[field]; den {
			issues[field] = "set in Den (Modules → Desk → settings); change it there"
		} else if *val < 1 || *val > maxRetentionDays {
			issues[field] = "must be between 1 and 3650 days, or null to keep"
		}
	}
	if len(issues) > 0 {
		return cur, httperror.Validation("invalid retention", issues)
	}
	var actorID *uuid.UUID
	if actor != nil {
		actorID = &actor.ID
	}
	if err := s.putSetting(ctx, "retention", in, actorID); err != nil {
		return cur, err
	}
	s.audit(ctx, actor, "settings.retention", nil, "settings", "retention", "retention changed", in)
	return s.RetentionSettings(ctx)
}

func ptrInt(v int) *int { return &v }
