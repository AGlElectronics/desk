package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AGlElectronics/desk/backend/internal/authbase"
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

// maxJSONBody is authbase's default (1 MiB). Mail and saved-reply bodies,
// which can carry pasted images, use maxMailJSON instead.
const (
	maxJSONBody = authbase.DefaultMaxBodyBytes
	maxMailJSON = 20 << 20
)

// decodeJSON strict-decodes the request body into dst with the shared
// decoder (1 MiB, unknown fields refused) and returns a typed error
// handlers can hand to httperror.Render: 400 or 413.
func decodeJSON(r *http.Request, dst any) error {
	return decodeJSONLimit(r, dst, maxJSONBody)
}

// decodeMailJSON is decodeJSON with the larger cap used for messages.
func decodeMailJSON(r *http.Request, dst any) error {
	return decodeJSONLimit(r, dst, maxMailJSON)
}

func decodeJSONLimit(r *http.Request, dst any, max int64) error {
	err := authbase.DecodeJSON(nil, r, dst, authbase.DecodeOptions{MaxBytes: max, DisallowUnknownFields: true})
	var de *authbase.DecodeError
	if errors.As(err, &de) {
		return httperror.New(httperror.Code(de.Code), de.Message).WithCause(err)
	}
	return err
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
