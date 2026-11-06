package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AGlElectronics/desk/backend/internal/httperror"
)

// writeJSON serializes body with the standard content-type. Errors on the
// writer path (e.g. client disconnected) are intentionally swallowed — the
// standard access log already records the status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

// decodeJSON strict-decodes the request body into dst and returns a
// typed BadRequest on malformed input so handlers can hand the error
// straight to httperror.Render.
func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return httperror.BadRequest("request body is empty")
		}
		return httperror.BadRequest("invalid JSON: " + err.Error())
	}
	return nil
}

// parseUUIDParam reads a chi URL parameter and parses it as a UUID. The
// `name` is used verbatim in the error message, so pass a human-friendly
// label like "organization" rather than the raw URL fragment name.
func parseUUIDParam(r *http.Request, name string) (uuid.UUID, error) {
	raw := chi.URLParam(r, name)
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, httperror.BadRequest("invalid " + name + " id: " + raw)
	}
	return id, nil
}
