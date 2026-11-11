package http

import (
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/hlog"

	"github.com/AGlElectronics/desk/backend/internal/auth"
	"github.com/AGlElectronics/desk/backend/internal/desk"
	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

// deskHandler owns mailbox and conversation routes. Authorization model
// (MVP): tickets.work sees and works every mailbox; mailbox.manage edits
// mailbox settings. Per-mailbox grants come later (docs/plan.md).
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

// ----------------------------------------------------------------------------
// Mailboxes
// ----------------------------------------------------------------------------

func (h *deskHandler) listMailboxes(w http.ResponseWriter, r *http.Request) {
	u, err := caller(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.ListMailboxes(r.Context(), u.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if out == nil {
		out = []desk.MailboxWithCounts{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"mailboxes": out})
}

func (h *deskHandler) createMailbox(w http.ResponseWriter, r *http.Request) {
	var in desk.MailboxInput
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	orgID, err := h.svc.DefaultOrganization(r.Context())
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	mb, err := h.svc.CreateMailbox(r.Context(), orgID, in)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	h.svc.RequestFetch(mb.ID)
	writeJSON(w, http.StatusCreated, mb)
}

func (h *deskHandler) getMailbox(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "mailbox")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	mb, err := h.svc.GetMailbox(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mb)
}

func (h *deskHandler) updateMailbox(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "mailbox")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in desk.MailboxInput
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	if _, err := h.svc.GetMailbox(r.Context(), id); err != nil {
		httperror.Render(w, r, err)
		return
	}
	mb, err := h.svc.UpdateMailbox(r.Context(), id, in)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	h.svc.RequestFetch(mb.ID)
	writeJSON(w, http.StatusOK, mb)
}

func (h *deskHandler) deleteMailbox(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "mailbox")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.DeleteMailbox(r.Context(), id); err != nil {
		httperror.Render(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *deskHandler) testMailbox(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "mailbox")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	out, err := h.svc.TestConnection(r.Context(), id, *hlog.FromRequest(r))
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *deskHandler) fetchMailbox(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "mailbox")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	if _, err := h.svc.GetMailbox(r.Context(), id); err != nil {
		httperror.Render(w, r, err)
		return
	}
	h.svc.RequestFetch(id)
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
}

// ----------------------------------------------------------------------------
// Conversations
// ----------------------------------------------------------------------------

func (h *deskHandler) listConversations(w http.ResponseWriter, r *http.Request) {
	u, err := caller(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	q := r.URL.Query()
	f := desk.ListFilter{Folder: q.Get("folder"), Query: q.Get("q"), UserID: u.ID}
	if raw := q.Get("mailbox"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httperror.Render(w, r, httperror.BadRequest("invalid mailbox id"))
			return
		}
		f.MailboxID = &id
	}
	out, err := h.svc.ListConversations(r.Context(), f)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": out})
}

func (h *deskHandler) createConversation(w http.ResponseWriter, r *http.Request) {
	u, err := caller(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
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
		httperror.Render(w, r, err)
		return
	}
	c, err := h.svc.CreateConversation(r.Context(), person(u), desk.NewConversationInput{
		MailboxID: in.MailboxID, CustomerEmail: in.CustomerEmail, CustomerName: in.CustomerName,
		Subject: in.Subject, Body: in.Body, BodyHTML: in.BodyHTML, CC: in.CC,
	})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (h *deskHandler) loadConversation(r *http.Request) (desk.Conversation, *auth.User, error) {
	u, err := caller(r)
	if err != nil {
		return desk.Conversation{}, nil, err
	}
	id, err := parseUUIDParam(r, "conversation")
	if err != nil {
		return desk.Conversation{}, nil, err
	}
	c, err := h.svc.GetConversation(r.Context(), id)
	return c, u, err
}

func (h *deskHandler) getConversation(w http.ResponseWriter, r *http.Request) {
	c, u, err := h.loadConversation(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	d, err := h.svc.Detail(r.Context(), c, u.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *deskHandler) updateConversation(w http.ResponseWriter, r *http.Request) {
	c, u, err := h.loadConversation(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Status *string `json:"status"`
		// "" unassigns; a user id assigns; absent leaves it unchanged.
		AssigneeID *string `json:"assignee_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	upd := desk.UpdateInput{Status: in.Status}
	if in.AssigneeID != nil {
		if *in.AssigneeID == "" {
			upd.ClearAssignee = true
		} else {
			id, err := uuid.Parse(*in.AssigneeID)
			if err != nil {
				httperror.Render(w, r, httperror.BadRequest("invalid assignee_id"))
				return
			}
			upd.AssigneeID = &id
		}
	}
	if err := h.svc.UpdateConversation(r.Context(), c, u.ID, upd); err != nil {
		httperror.Render(w, r, err)
		return
	}
	updated, err := h.svc.GetConversation(r.Context(), c.ID)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *deskHandler) addThread(w http.ResponseWriter, r *http.Request) {
	c, u, err := h.loadConversation(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Kind     string    `json:"kind"`
		Body     string    `json:"body"`
		BodyHTML string    `json:"body_html"`
		CC       *[]string `json:"cc"`
		Status   *string   `json:"status"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	id, err := h.svc.AddThread(r.Context(), c, person(u), desk.ThreadInput{
		Kind: in.Kind, Body: in.Body, BodyHTML: in.BodyHTML, CC: in.CC, Status: in.Status,
	})
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (h *deskHandler) heartbeat(w http.ResponseWriter, r *http.Request) {
	c, u, err := h.loadConversation(r)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Mode string `json:"mode"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.Heartbeat(r.Context(), c.ID, u.ID, in.Mode); err != nil {
		httperror.Render(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *deskHandler) threadAction(action func(*desk.Service, *http.Request, uuid.UUID) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parseUUIDParam(r, "thread")
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		if _, err := h.svc.ThreadConversation(r.Context(), id); err != nil {
			httperror.Render(w, r, err)
			return
		}
		if err := action(h.svc, r, id); err != nil {
			httperror.Render(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *deskHandler) updateCustomer(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "customer")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	var in struct {
		Name  *string `json:"name"`
		Notes *string `json:"notes"`
	}
	if err := decodeJSON(r, &in); err != nil {
		httperror.Render(w, r, err)
		return
	}
	if err := h.svc.UpdateCustomer(r.Context(), id, in.Name, in.Notes); err != nil {
		httperror.Render(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *deskHandler) downloadAttachment(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "attachment")
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	a, _, key, err := h.svc.AttachmentInfo(r.Context(), id)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	body, size, err := h.svc.OpenAttachment(r.Context(), key)
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	defer body.Close()
	ct, disposition := servedType(a.ContentType)
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": a.Filename}))
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

func (h *deskHandler) listPeople(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.ListPeople(r.Context())
	if err != nil {
		httperror.Render(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"people": out})
}
