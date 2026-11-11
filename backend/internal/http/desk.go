package http

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/hlog"

	"github.com/AGlElectronics/desk/backend/internal/auth"
	"github.com/AGlElectronics/desk/backend/internal/desk"
	"github.com/AGlElectronics/desk/backend/internal/domain"
	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

// deskHandler owns mailbox and conversation routes. Authorization
// (docs/adr/0003): Desk admins (mailbox.manage in the mailbox's
// organization) may do everything there; everyone else needs the mailbox's
// viewer, manager or delete role. A mailbox or conversation the caller may
// not view answers 404, one they may view but not change answers 403.
type deskHandler struct {
	svc *desk.Service
}

func caller(r *http.Request) (*auth.User, error) {
	u, err := auth.MustFromContext(r.Context())
	if err != nil {
		return nil, httperror.ErrUnauthorized
	}
	return u, nil
}

func person(u *auth.User) *desk.Person {
	return &desk.Person{ID: u.ID, DisplayName: u.DisplayName, Email: u.Email}
}

// accessOf derives the caller's Desk access from their token.
func accessOf(u *auth.User) desk.Access {
	var orgs []uuid.UUID
	for org, perms := range u.OrgPermissions {
		if _, ok := perms[domain.PermMailboxManage]; ok {
			orgs = append(orgs, org)
		}
	}
	return desk.NewAccess(u.ID, u.Groups, orgs)
}

func (h *deskHandler) who(r *http.Request) (desk.Access, *desk.Person, error) {
	u, err := caller(r)
	if err != nil {
		return desk.Access{}, nil, err
	}
	return accessOf(u), person(u), nil
}

// handle wraps a handler that returns (status, body, error).
func handle(fn func(r *http.Request) (int, any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, body, err := fn(r)
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		if status == http.StatusNoContent {
			w.WriteHeader(status)
			return
		}
		writeJSON(w, status, body)
	}
}

func (h *deskHandler) mailboxParam(r *http.Request, a desk.Access) (desk.Mailbox, error) {
	id, err := parseUUIDParam(r, "mailbox")
	if err != nil {
		return desk.Mailbox{}, err
	}
	return h.svc.MailboxFor(r.Context(), a, id)
}

func (h *deskHandler) adminMailbox(r *http.Request) (desk.Mailbox, desk.Access, *desk.Person, error) {
	a, p, err := h.who(r)
	if err != nil {
		return desk.Mailbox{}, a, p, err
	}
	mb, err := h.mailboxParam(r, a)
	if err != nil {
		return mb, a, p, err
	}
	return mb, a, p, desk.RequireAdmin(a, mb)
}

// ----------------------------------------------------------------------------
// Mailboxes
// ----------------------------------------------------------------------------

func (h *deskHandler) listMailboxes(r *http.Request) (int, any, error) {
	a, _, err := h.who(r)
	if err != nil {
		return 0, nil, err
	}
	out, err := h.svc.ListMailboxes(r.Context(), a)
	return http.StatusOK, map[string]any{"mailboxes": out}, err
}

func (h *deskHandler) createMailbox(r *http.Request) (int, any, error) {
	a, p, err := h.who(r)
	if err != nil {
		return 0, nil, err
	}
	var in struct {
		desk.MailboxInput
		OrganizationID *uuid.UUID `json:"organization_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return 0, nil, err
	}
	org, err := pickOrganization(a, in.OrganizationID)
	if err != nil {
		return 0, nil, err
	}
	mb, err := h.svc.CreateMailbox(r.Context(), org, in.MailboxInput)
	if err != nil {
		return 0, nil, err
	}
	h.svc.Audit(r.Context(), p, "mailbox.create", &mb.ID, "mailbox", mb.ID.String(), "created "+mb.Email,
		map[string]any{"email": mb.Email, "connector": mb.Connector, "slug": mb.Slug})
	h.svc.RequestFetch(mb.ID)
	counts, _ := h.svc.MailboxCounts(r.Context(), mb, a.UserID)
	return http.StatusCreated, h.svc.View(a, mb, counts), nil
}

// pickOrganization: the organization a new mailbox belongs to. Without
// organization_id the caller must be Desk admin in exactly one.
func pickOrganization(a desk.Access, requested *uuid.UUID) (uuid.UUID, error) {
	if requested != nil {
		if !a.IsAdmin(*requested) {
			return uuid.Nil, httperror.New(httperror.CodeForbidden, "only Desk admins of that organization may create mailboxes there")
		}
		return *requested, nil
	}
	orgs := a.AdminOrgs()
	switch len(orgs) {
	case 0:
		return uuid.Nil, httperror.New(httperror.CodeForbidden, "only Desk admins may create mailboxes")
	case 1:
		return orgs[0], nil
	}
	return uuid.Nil, httperror.Validation("organization required", map[string]string{"organization_id": "you are Desk admin in several organizations; choose one"})
}

func (h *deskHandler) getMailbox(r *http.Request) (int, any, error) {
	a, _, err := h.who(r)
	if err != nil {
		return 0, nil, err
	}
	mb, err := h.mailboxParam(r, a)
	if err != nil {
		return 0, nil, err
	}
	counts, err := h.svc.MailboxCounts(r.Context(), mb, a.UserID)
	return http.StatusOK, h.svc.View(a, mb, counts), err
}

// updateMailbox: managers may change the signature and the auto-reply;
// everything else is for Desk admins.
func (h *deskHandler) updateMailbox(r *http.Request) (int, any, error) {
	a, p, err := h.who(r)
	if err != nil {
		return 0, nil, err
	}
	mb, err := h.mailboxParam(r, a)
	if err != nil {
		return 0, nil, err
	}
	var in desk.MailboxInput
	if err := decodeJSON(r, &in); err != nil {
		return 0, nil, err
	}
	if err := desk.RequireWork(a, mb); err != nil {
		return 0, nil, err
	}
	managerOnly := desk.MailboxInput{Signature: in.Signature, AutoReply: in.AutoReply}
	if !a.IsAdmin(mb.OrganizationID) && in != managerOnly {
		return 0, nil, httperror.New(httperror.CodeForbidden, "managers may change the signature and the auto-reply; the rest is for Desk admins")
	}
	updated, err := h.svc.UpdateMailbox(r.Context(), mb.ID, in)
	if err != nil {
		return 0, nil, err
	}
	h.svc.Audit(r.Context(), p, "mailbox.update", &mb.ID, "mailbox", mb.ID.String(), "changed "+mb.Email, changedFields(in))
	if in.IMAPHost != nil || in.IMAPPassword != nil || in.IMAPUsername != nil {
		h.svc.RequestFetch(mb.ID)
	}
	counts, _ := h.svc.MailboxCounts(r.Context(), updated, a.UserID)
	return http.StatusOK, h.svc.View(a, updated, counts), nil
}

// changedFields lists which fields a mailbox update touched, never their
// values (passwords).
func changedFields(in desk.MailboxInput) map[string]any {
	raw, _ := json.Marshal(in)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	var fields []string
	for k, v := range m {
		if v != nil {
			fields = append(fields, k)
		}
	}
	return map[string]any{"fields": fields}
}

func (h *deskHandler) deleteMailbox(r *http.Request) (int, any, error) {
	mb, a, p, err := h.adminMailbox(r)
	if err != nil {
		return 0, nil, err
	}
	out, err := h.svc.TrashMailbox(r.Context(), mb.ID, p)
	return http.StatusOK, h.svc.View(a, out, desk.FolderCounts{}), err
}

func (h *deskHandler) restoreMailbox(r *http.Request) (int, any, error) {
	mb, a, p, err := h.adminMailbox(r)
	if err != nil {
		return 0, nil, err
	}
	out, err := h.svc.RestoreMailbox(r.Context(), mb.ID, p)
	return http.StatusOK, h.svc.View(a, out, desk.FolderCounts{}), err
}

func (h *deskHandler) purgeMailbox(r *http.Request) (int, any, error) {
	mb, _, p, err := h.adminMailbox(r)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusNoContent, nil, h.svc.PurgeMailbox(r.Context(), mb.ID, p)
}

func (h *deskHandler) trashedMailboxes(r *http.Request) (int, any, error) {
	a, _, err := h.who(r)
	if err != nil {
		return 0, nil, err
	}
	if !a.AnyAdmin() {
		return 0, nil, httperror.New(httperror.CodeForbidden, "only Desk admins may see the mailbox trash")
	}
	all, err := h.svc.TrashedMailboxes(r.Context())
	out := []any{}
	for _, mb := range all {
		if a.IsAdmin(mb.OrganizationID) {
			out = append(out, h.svc.View(a, mb, desk.FolderCounts{}))
		}
	}
	return http.StatusOK, map[string]any{"mailboxes": out}, err
}

func (h *deskHandler) provisionMailbox(r *http.Request) (int, any, error) {
	mb, a, p, err := h.adminMailbox(r)
	if err != nil {
		return 0, nil, err
	}
	out, err := h.svc.Provision(r.Context(), mb)
	if err == nil {
		h.svc.Audit(r.Context(), p, "mailbox.provision", &mb.ID, "mailbox", mb.ID.String(), "provisioned "+mb.Email+" in Google Workspace",
			map[string]any{"state": out.ProviderState, "error": out.ProviderError})
	}
	return http.StatusOK, h.svc.View(a, out, desk.FolderCounts{}), err
}

func (h *deskHandler) testMailbox(r *http.Request) (int, any, error) {
	mb, _, _, err := h.adminMailbox(r)
	if err != nil {
		return 0, nil, err
	}
	out, err := h.svc.TestConnection(r.Context(), mb.ID, *hlog.FromRequest(r))
	return http.StatusOK, out, err
}

func (h *deskHandler) fetchMailbox(r *http.Request) (int, any, error) {
	mb, _, _, err := h.adminMailbox(r)
	if err != nil {
		return 0, nil, err
	}
	h.svc.RequestFetch(mb.ID)
	return http.StatusAccepted, map[string]any{"queued": true}, nil
}

func (h *deskHandler) importFailures(r *http.Request) (int, any, error) {
	mb, _, _, err := h.adminMailbox(r)
	if err != nil {
		return 0, nil, err
	}
	out, err := h.svc.ImportFailures(r.Context(), mb.ID)
	return http.StatusOK, map[string]any{"failures": out}, err
}

// ----------------------------------------------------------------------------
// Conversations
// ----------------------------------------------------------------------------

func (h *deskHandler) listConversations(r *http.Request) (int, any, error) {
	a, _, err := h.who(r)
	if err != nil {
		return 0, nil, err
	}
	q := r.URL.Query()
	visible, err := h.svc.VisibleMailboxes(r.Context(), a)
	if err != nil {
		return 0, nil, err
	}
	f := desk.ListFilter{Folder: q.Get("folder"), Query: q.Get("q"), Tag: q.Get("tag"), UserID: a.UserID, Cursor: q.Get("cursor")}
	for _, m := range visible {
		if f.Folder == "trash" && !a.CanDelete(m) {
			continue
		}
		f.MailboxIDs = append(f.MailboxIDs, m.ID)
	}
	if raw := q.Get("mailbox"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return 0, nil, httperror.BadRequest("invalid mailbox id")
		}
		mb, err := h.svc.MailboxFor(r.Context(), a, id)
		if err != nil {
			return 0, nil, err
		}
		if f.Folder == "trash" {
			if err := desk.RequireDelete(a, mb); err != nil {
				return 0, nil, err
			}
		}
		f.MailboxID = &id
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return 0, nil, httperror.BadRequest("limit must be a positive number")
		}
		f.Limit = n
	}
	page, err := h.svc.ListConversations(r.Context(), f)
	return http.StatusOK, page, err
}

func (h *deskHandler) createConversation(r *http.Request) (int, any, error) {
	a, p, err := h.who(r)
	if err != nil {
		return 0, nil, err
	}
	var in struct {
		MailboxID     uuid.UUID `json:"mailbox_id"`
		CustomerEmail string    `json:"customer_email"`
		CustomerName  string    `json:"customer_name"`
		Subject       string    `json:"subject"`
		Body          string    `json:"body"`
		BodyHTML      string    `json:"body_html"`
		CC            []string  `json:"cc"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return 0, nil, err
	}
	mb, err := h.svc.MailboxFor(r.Context(), a, in.MailboxID)
	if err != nil {
		return 0, nil, err
	}
	if err := desk.RequireWork(a, mb); err != nil {
		return 0, nil, err
	}
	c, err := h.svc.CreateConversation(r.Context(), p, desk.NewConversationInput{
		MailboxID: mb.ID, CustomerEmail: in.CustomerEmail, CustomerName: in.CustomerName,
		Subject: in.Subject, Body: in.Body, BodyHTML: in.BodyHTML, CC: in.CC,
	})
	return http.StatusCreated, c, err
}

type convCtx struct {
	a  desk.Access
	p  *desk.Person
	c  desk.Conversation
	mb desk.Mailbox
}

func (h *deskHandler) conversation(r *http.Request) (convCtx, error) {
	a, p, err := h.who(r)
	if err != nil {
		return convCtx{}, err
	}
	id, err := parseUUIDParam(r, "conversation")
	if err != nil {
		return convCtx{}, err
	}
	c, mb, err := h.svc.ConversationFor(r.Context(), a, id)
	if err != nil {
		return convCtx{}, err
	}
	// Conversations in Desk's trash are only visible to those who may
	// restore them.
	if c.DeletedAt != nil && !a.CanDelete(mb) {
		return convCtx{}, httperror.NotFound("conversation")
	}
	return convCtx{a: a, p: p, c: c, mb: mb}, nil
}

func (h *deskHandler) getConversation(r *http.Request) (int, any, error) {
	cc, err := h.conversation(r)
	if err != nil {
		return 0, nil, err
	}
	d, err := h.svc.Detail(r.Context(), cc.a, cc.c, cc.mb)
	return http.StatusOK, d, err
}

func (h *deskHandler) updateConversation(r *http.Request) (int, any, error) {
	cc, err := h.conversation(r)
	if err != nil {
		return 0, nil, err
	}
	var in struct {
		Status *string `json:"status"`
		// "" unassigns; a user id assigns; absent leaves it unchanged.
		AssigneeID *string   `json:"assignee_id"`
		Tags       *[]string `json:"tags"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return 0, nil, err
	}
	if err := desk.RequireWork(cc.a, cc.mb); err != nil {
		return 0, nil, err
	}
	upd := desk.UpdateInput{Status: in.Status, Tags: in.Tags}
	if in.AssigneeID != nil {
		if *in.AssigneeID == "" {
			upd.ClearAssignee = true
		} else {
			id, err := uuid.Parse(*in.AssigneeID)
			if err != nil {
				return 0, nil, httperror.BadRequest("invalid assignee_id")
			}
			upd.AssigneeID = &id
		}
	}
	if err := h.svc.UpdateConversation(r.Context(), cc.c, cc.mb, cc.p, upd); err != nil {
		return 0, nil, err
	}
	c, _, err := h.svc.ConversationFor(r.Context(), cc.a, cc.c.ID)
	return http.StatusOK, c, err
}

func (h *deskHandler) deleteConversation(r *http.Request) (int, any, error) {
	cc, err := h.conversation(r)
	if err != nil {
		return 0, nil, err
	}
	if err := desk.RequireDelete(cc.a, cc.mb); err != nil {
		return 0, nil, err
	}
	w, err := h.svc.DeleteConversation(r.Context(), cc.c, cc.mb, cc.p)
	return http.StatusOK, w, err
}

func (h *deskHandler) restoreConversation(r *http.Request) (int, any, error) {
	cc, err := h.conversation(r)
	if err != nil {
		return 0, nil, err
	}
	if err := desk.RequireDelete(cc.a, cc.mb); err != nil {
		return 0, nil, err
	}
	w, err := h.svc.RestoreConversation(r.Context(), cc.c, cc.mb, cc.p)
	return http.StatusOK, w, err
}

func (h *deskHandler) addThread(r *http.Request) (int, any, error) {
	cc, err := h.conversation(r)
	if err != nil {
		return 0, nil, err
	}
	var in struct {
		Kind     string    `json:"kind"`
		Body     string    `json:"body"`
		BodyHTML string    `json:"body_html"`
		CC       *[]string `json:"cc"`
		Status   *string   `json:"status"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return 0, nil, err
	}
	if err := desk.RequireWork(cc.a, cc.mb); err != nil {
		return 0, nil, err
	}
	id, err := h.svc.AddThread(r.Context(), cc.c, cc.p, desk.ThreadInput{
		Kind: in.Kind, Body: in.Body, BodyHTML: in.BodyHTML, CC: in.CC, Status: in.Status,
	})
	return http.StatusCreated, map[string]any{"id": id}, err
}

func (h *deskHandler) forward(r *http.Request) (int, any, error) {
	cc, err := h.conversation(r)
	if err != nil {
		return 0, nil, err
	}
	var in struct {
		ThreadID *uuid.UUID `json:"thread_id"`
		To       []string   `json:"to"`
		Body     string     `json:"body"`
		BodyHTML string     `json:"body_html"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return 0, nil, err
	}
	if err := desk.RequireWork(cc.a, cc.mb); err != nil {
		return 0, nil, err
	}
	id, err := h.svc.Forward(r.Context(), cc.c, cc.p, desk.ForwardInput{ThreadID: in.ThreadID, To: in.To, Body: in.Body, BodyHTML: in.BodyHTML})
	if err == nil {
		h.svc.Audit(r.Context(), cc.p, "conversation.forward", &cc.mb.ID, "conversation", cc.c.ID.String(),
			"forwarded to "+strings.Join(in.To, ", "), map[string]any{"thread_id": id})
	}
	return http.StatusCreated, map[string]any{"id": id}, err
}

func (h *deskHandler) mergeConversation(r *http.Request) (int, any, error) {
	cc, err := h.conversation(r)
	if err != nil {
		return 0, nil, err
	}
	var in struct {
		IntoID uuid.UUID `json:"into_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return 0, nil, err
	}
	dst, dstMB, err := h.svc.ConversationFor(r.Context(), cc.a, in.IntoID)
	if err != nil {
		return 0, nil, err
	}
	if err := desk.RequireWork(cc.a, cc.mb); err != nil {
		return 0, nil, err
	}
	if err := desk.RequireWork(cc.a, dstMB); err != nil {
		return 0, nil, err
	}
	if err := h.svc.MergeConversations(r.Context(), cc.c, dst, cc.mb, dstMB, cc.p); err != nil {
		return 0, nil, err
	}
	c, _, err := h.svc.ConversationFor(r.Context(), cc.a, dst.ID)
	return http.StatusOK, c, err
}

func (h *deskHandler) moveConversation(r *http.Request) (int, any, error) {
	cc, err := h.conversation(r)
	if err != nil {
		return 0, nil, err
	}
	var in struct {
		MailboxID uuid.UUID `json:"mailbox_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return 0, nil, err
	}
	to, err := h.svc.MailboxFor(r.Context(), cc.a, in.MailboxID)
	if err != nil {
		return 0, nil, err
	}
	if err := desk.RequireWork(cc.a, cc.mb); err != nil {
		return 0, nil, err
	}
	if err := desk.RequireWork(cc.a, to); err != nil {
		return 0, nil, err
	}
	if err := h.svc.MoveConversation(r.Context(), cc.c, cc.mb, to, cc.p); err != nil {
		return 0, nil, err
	}
	c, _, err := h.svc.ConversationFor(r.Context(), cc.a, cc.c.ID)
	return http.StatusOK, c, err
}

func (h *deskHandler) star(on bool) http.HandlerFunc {
	return handle(func(r *http.Request) (int, any, error) {
		cc, err := h.conversation(r)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusNoContent, nil, h.svc.SetStar(r.Context(), cc.c.ID, cc.a.UserID, on)
	})
}

func (h *deskHandler) heartbeat(r *http.Request) (int, any, error) {
	cc, err := h.conversation(r)
	if err != nil {
		return 0, nil, err
	}
	var in struct {
		Mode string `json:"mode"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return 0, nil, err
	}
	return http.StatusNoContent, nil, h.svc.Heartbeat(r.Context(), cc.c.ID, cc.a.UserID, in.Mode)
}

// thread resolves a thread's conversation and mailbox with access checks.
func (h *deskHandler) thread(r *http.Request) (convCtx, uuid.UUID, error) {
	a, p, err := h.who(r)
	if err != nil {
		return convCtx{}, uuid.Nil, err
	}
	id, err := parseUUIDParam(r, "thread")
	if err != nil {
		return convCtx{}, uuid.Nil, err
	}
	c, err := h.svc.ThreadConversation(r.Context(), id)
	if err != nil {
		return convCtx{}, uuid.Nil, err
	}
	c, mb, err := h.svc.ConversationFor(r.Context(), a, c.ID)
	if err != nil {
		return convCtx{}, uuid.Nil, httperror.NotFound("thread")
	}
	return convCtx{a: a, p: p, c: c, mb: mb}, id, nil
}

func (h *deskHandler) undoThread(r *http.Request) (int, any, error) {
	cc, id, err := h.thread(r)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusNoContent, nil, h.svc.UndoReply(r.Context(), cc.a, cc.mb, id)
}

func (h *deskHandler) retryThread(r *http.Request) (int, any, error) {
	cc, id, err := h.thread(r)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusNoContent, nil, h.svc.RetryReply(r.Context(), cc.a, cc.mb, id)
}

func (h *deskHandler) deleteThread(r *http.Request) (int, any, error) {
	cc, id, err := h.thread(r)
	if err != nil {
		return 0, nil, err
	}
	if err := desk.RequireDelete(cc.a, cc.mb); err != nil {
		return 0, nil, err
	}
	w, err := h.svc.DeleteThread(r.Context(), cc.mb, id, cc.p)
	return http.StatusOK, w, err
}

func (h *deskHandler) restoreThread(r *http.Request) (int, any, error) {
	cc, id, err := h.thread(r)
	if err != nil {
		return 0, nil, err
	}
	if err := desk.RequireDelete(cc.a, cc.mb); err != nil {
		return 0, nil, err
	}
	w, err := h.svc.RestoreThread(r.Context(), cc.mb, id, cc.p)
	return http.StatusOK, w, err
}

// ----------------------------------------------------------------------------
// Customers, attachments, people, tags, saved replies
// ----------------------------------------------------------------------------

func (h *deskHandler) getCustomer(r *http.Request) (int, any, error) {
	a, _, err := h.who(r)
	if err != nil {
		return 0, nil, err
	}
	id, err := parseUUIDParam(r, "customer")
	if err != nil {
		return 0, nil, err
	}
	out, err := h.svc.CustomerProfile(r.Context(), a, id)
	return http.StatusOK, out, err
}

func (h *deskHandler) updateCustomer(r *http.Request) (int, any, error) {
	a, p, err := h.who(r)
	if err != nil {
		return 0, nil, err
	}
	id, err := parseUUIDParam(r, "customer")
	if err != nil {
		return 0, nil, err
	}
	var in desk.CustomerInput
	if err := decodeJSON(r, &in); err != nil {
		return 0, nil, err
	}
	if err := h.svc.UpdateCustomer(r.Context(), a, id, in, p); err != nil {
		return 0, nil, err
	}
	out, err := h.svc.CustomerProfile(r.Context(), a, id)
	return http.StatusOK, out, err
}

func (h *deskHandler) mergeCustomer(r *http.Request) (int, any, error) {
	a, p, err := h.who(r)
	if err != nil {
		return 0, nil, err
	}
	id, err := parseUUIDParam(r, "customer")
	if err != nil {
		return 0, nil, err
	}
	var in struct {
		SourceID uuid.UUID `json:"source_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return 0, nil, err
	}
	if err := h.svc.MergeCustomers(r.Context(), a, id, in.SourceID, p); err != nil {
		return 0, nil, err
	}
	out, err := h.svc.CustomerProfile(r.Context(), a, id)
	return http.StatusOK, out, err
}

func (h *deskHandler) downloadAttachment(w http.ResponseWriter, r *http.Request) {
	a, _, err := h.who(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	id, err := parseUUIDParam(r, "attachment")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	att, convID, key, err := h.svc.AttachmentInfo(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if _, _, err := h.svc.ConversationFor(r.Context(), a, convID); err != nil {
		httperror.Render(w, r, httperror.NotFound("attachment"))
		return
	}
	body, size, err := h.svc.OpenAttachment(r.Context(), key)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	defer body.Close()
	ct, disposition := servedType(att.ContentType)
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": att.Filename}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Even if a browser renders the file, it gets no scripts, no forms and
	// no access to Desk's origin.
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = io.Copy(w, body)
}

// inlineTypes may be shown in the browser (images for inline cid: parts,
// PDFs and plain text for previews). Everything else, including HTML and
// SVG from senders, is served as a download of application/octet-stream.
var inlineTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"application/pdf": true, "text/plain": true,
}

func servedType(declared string) (contentType, disposition string) {
	mt, _, err := mime.ParseMediaType(declared)
	if err != nil || !inlineTypes[strings.ToLower(mt)] {
		return "application/octet-stream", "attachment"
	}
	mt = strings.ToLower(mt)
	if mt == "text/plain" {
		return "text/plain; charset=utf-8", "inline"
	}
	return mt, "inline"
}

// listPeople: the assignee picker. With ?mailbox= it lists who may work
// that mailbox (caller needs to see it); without, Desk admins get everyone.
func (h *deskHandler) listPeople(r *http.Request) (int, any, error) {
	a, _, err := h.who(r)
	if err != nil {
		return 0, nil, err
	}
	var mb *desk.Mailbox
	if raw := r.URL.Query().Get("mailbox"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return 0, nil, httperror.BadRequest("invalid mailbox id")
		}
		m, err := h.svc.MailboxFor(r.Context(), a, id)
		if err != nil {
			return 0, nil, err
		}
		mb = &m
	} else if !a.AnyAdmin() {
		return 0, nil, httperror.BadRequest("mailbox is required")
	}
	out, err := h.svc.ListPeople(r.Context(), mb, r.URL.Query().Get("q"))
	return http.StatusOK, map[string]any{"people": out}, err
}

func (h *deskHandler) listTags(r *http.Request) (int, any, error) {
	a, _, err := h.who(r)
	if err != nil {
		return 0, nil, err
	}
	var ids []uuid.UUID
	if raw := r.URL.Query().Get("mailbox"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return 0, nil, httperror.BadRequest("invalid mailbox id")
		}
		if _, err := h.svc.MailboxFor(r.Context(), a, id); err != nil {
			return 0, nil, err
		}
		ids = []uuid.UUID{id}
	} else {
		visible, err := h.svc.VisibleMailboxes(r.Context(), a)
		if err != nil {
			return 0, nil, err
		}
		for _, m := range visible {
			ids = append(ids, m.ID)
		}
	}
	out, err := h.svc.Tags(r.Context(), ids)
	return http.StatusOK, map[string]any{"tags": out}, err
}

func (h *deskHandler) listSavedReplies(r *http.Request) (int, any, error) {
	a, _, err := h.who(r)
	if err != nil {
		return 0, nil, err
	}
	mb, err := h.mailboxParam(r, a)
	if err != nil {
		return 0, nil, err
	}
	out, err := h.svc.SavedReplies(r.Context(), mb.ID)
	return http.StatusOK, map[string]any{"saved_replies": out}, err
}

// savedReplyRights: a mailbox's saved replies are managed by its managers,
// global ones (no mailbox) by Desk admins.
func (h *deskHandler) savedReplyRights(r *http.Request, a desk.Access, mailboxID *uuid.UUID) error {
	if mailboxID == nil {
		if !a.AnyAdmin() {
			return httperror.New(httperror.CodeForbidden, "only Desk admins manage saved replies for every mailbox")
		}
		return nil
	}
	mb, err := h.svc.MailboxFor(r.Context(), a, *mailboxID)
	if err != nil {
		return err
	}
	return desk.RequireWork(a, mb)
}

func (h *deskHandler) createSavedReply(r *http.Request) (int, any, error) {
	a, p, err := h.who(r)
	if err != nil {
		return 0, nil, err
	}
	var in desk.SavedReplyInput
	if err := decodeJSON(r, &in); err != nil {
		return 0, nil, err
	}
	if err := h.savedReplyRights(r, a, in.MailboxID); err != nil {
		return 0, nil, err
	}
	out, err := h.svc.CreateSavedReply(r.Context(), in, p)
	return http.StatusCreated, out, err
}

func (h *deskHandler) savedReply(r *http.Request) (desk.SavedReply, desk.Access, error) {
	a, _, err := h.who(r)
	if err != nil {
		return desk.SavedReply{}, a, err
	}
	id, err := parseUUIDParam(r, "reply")
	if err != nil {
		return desk.SavedReply{}, a, err
	}
	sr, err := h.svc.GetSavedReply(r.Context(), id)
	if err != nil {
		return sr, a, err
	}
	return sr, a, h.savedReplyRights(r, a, sr.MailboxID)
}

func (h *deskHandler) updateSavedReply(r *http.Request) (int, any, error) {
	sr, _, err := h.savedReply(r)
	if err != nil {
		return 0, nil, err
	}
	var in desk.SavedReplyInput
	if err := decodeJSON(r, &in); err != nil {
		return 0, nil, err
	}
	out, err := h.svc.UpdateSavedReply(r.Context(), sr.ID, in)
	return http.StatusOK, out, err
}

func (h *deskHandler) deleteSavedReply(r *http.Request) (int, any, error) {
	sr, _, err := h.savedReply(r)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusNoContent, nil, h.svc.DeleteSavedReply(r.Context(), sr.ID)
}

// ----------------------------------------------------------------------------
// Settings, audit, notifications
// ----------------------------------------------------------------------------

func (h *deskHandler) requireAnyAdmin(r *http.Request) (desk.Access, *desk.Person, error) {
	a, p, err := h.who(r)
	if err == nil && !a.AnyAdmin() {
		err = httperror.New(httperror.CodeForbidden, "only Desk admins may do this")
	}
	return a, p, err
}

func (h *deskHandler) getRetention(r *http.Request) (int, any, error) {
	if _, _, err := h.requireAnyAdmin(r); err != nil {
		return 0, nil, err
	}
	out, err := h.svc.RetentionSettings(r.Context())
	return http.StatusOK, out, err
}

func (h *deskHandler) putRetention(r *http.Request) (int, any, error) {
	_, p, err := h.requireAnyAdmin(r)
	if err != nil {
		return 0, nil, err
	}
	var in desk.Retention
	if err := decodeJSON(r, &in); err != nil {
		return 0, nil, err
	}
	out, err := h.svc.SetRetention(r.Context(), in, p)
	return http.StatusOK, out, err
}

func (h *deskHandler) getIntegrations(r *http.Request) (int, any, error) {
	if _, _, err := h.requireAnyAdmin(r); err != nil {
		return 0, nil, err
	}
	return http.StatusOK, h.svc.Integrations(r.Context()), nil
}

func (h *deskHandler) auditLog(r *http.Request) (int, any, error) {
	if _, _, err := h.requireAnyAdmin(r); err != nil {
		return 0, nil, err
	}
	q := r.URL.Query()
	f := desk.AuditFilter{Action: q.Get("action")}
	if raw := q.Get("mailbox"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return 0, nil, httperror.BadRequest("invalid mailbox id")
		}
		f.MailboxID = &id
	}
	if raw := q.Get("before"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, nil, httperror.BadRequest("before must be an entry id")
		}
		f.Before = n
	}
	if raw := q.Get("limit"); raw != "" {
		f.Limit, _ = strconv.Atoi(raw)
	}
	out, err := h.svc.AuditLog(r.Context(), f)
	return http.StatusOK, map[string]any{"entries": out}, err
}

func (h *deskHandler) getNotifications(r *http.Request) (int, any, error) {
	a, _, err := h.who(r)
	if err != nil {
		return 0, nil, err
	}
	out, err := h.svc.GetNotificationPrefs(r.Context(), a.UserID)
	return http.StatusOK, out, err
}

func (h *deskHandler) putNotifications(r *http.Request) (int, any, error) {
	a, _, err := h.who(r)
	if err != nil {
		return 0, nil, err
	}
	var in struct {
		NewConversation bool `json:"new_conversation"`
		AssignedToMe    bool `json:"assigned_to_me"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return 0, nil, err
	}
	out, err := h.svc.SetNotificationPrefs(r.Context(), a.UserID, desk.NotificationPrefs{NewConversation: in.NewConversation, AssignedToMe: in.AssignedToMe})
	return http.StatusOK, out, err
}

// mailboxRoutes mounts /mailboxes.
func (h *deskHandler) mailboxRoutes(r chi.Router) {
	r.Get("/", handle(h.listMailboxes))
	r.Post("/", handle(h.createMailbox))
	r.Get("/trash", handle(h.trashedMailboxes))
	r.Route("/{mailbox}", func(r chi.Router) {
		r.Get("/", handle(h.getMailbox))
		r.Patch("/", handle(h.updateMailbox))
		r.Delete("/", handle(h.deleteMailbox))
		r.Post("/restore", handle(h.restoreMailbox))
		r.Post("/purge", handle(h.purgeMailbox))
		r.Post("/provision", handle(h.provisionMailbox))
		r.Post("/test", handle(h.testMailbox))
		r.Post("/fetch", handle(h.fetchMailbox))
		r.Get("/import-failures", handle(h.importFailures))
		r.Get("/saved-replies", handle(h.listSavedReplies))
	})
}

// conversationRoutes mounts /conversations.
func (h *deskHandler) conversationRoutes(r chi.Router) {
	r.Get("/", handle(h.listConversations))
	r.Post("/", handle(h.createConversation))
	r.Route("/{conversation}", func(r chi.Router) {
		r.Get("/", handle(h.getConversation))
		r.Patch("/", handle(h.updateConversation))
		r.Delete("/", handle(h.deleteConversation))
		r.Post("/restore", handle(h.restoreConversation))
		r.Post("/threads", handle(h.addThread))
		r.Post("/forward", handle(h.forward))
		r.Post("/merge", handle(h.mergeConversation))
		r.Post("/move", handle(h.moveConversation))
		r.Put("/star", h.star(true))
		r.Delete("/star", h.star(false))
		r.Post("/viewing", handle(h.heartbeat))
	})
}
