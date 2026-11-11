// Code generated from con-services shared/go/authbase; DO NOT EDIT. Run scripts/sync-shared.

package authbase

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// Error codes authbase writes in {"error":{"code":…,"message":…}}. They are
// part of the API contract (docs/concepts/shared-code.md#error-codes):
// modules return them unchanged and web apps may switch on them.
const (
	CodeBadRequest           = "bad_request"            // 400: body is not one valid JSON value
	CodeRequestTooLarge      = "request_too_large"      // 413: body over DecodeOptions.MaxBytes
	CodeUnsupportedMediaType = "unsupported_media_type" // 415: Content-Type is not JSON
	CodeRateLimited          = "rate_limited"           // 429: with Retry-After
)

// DefaultMaxBodyBytes is the request body limit DecodeJSON uses when
// DecodeOptions.MaxBytes is 0 (1 MiB).
const DefaultMaxBodyBytes int64 = 1 << 20

// DecodeOptions configures DecodeJSON.
type DecodeOptions struct {
	// MaxBytes limits the body (http.MaxBytesReader). 0 means
	// DefaultMaxBodyBytes.
	MaxBytes int64
	// DisallowUnknownFields rejects fields dst does not have.
	DisallowUnknownFields bool
	// RequireJSONContentType rejects a Content-Type other than
	// application/json or */*+json with 415.
	RequireJSONContentType bool
}

// DecodeError is what DecodeJSON returns. Status, Code and Message are
// meant for the API error response {"error":{"code":…,"message":…}}.
type DecodeError struct {
	Status  int
	Code    string
	Message string
	Err     error
}

func (e *DecodeError) Error() string { return e.Message }
func (e *DecodeError) Unwrap() error { return e.Err }

// DecodeJSON reads one JSON value from r's body into dst. The body is
// limited with http.MaxBytesReader (so the server also stops reading and
// closes the connection on overflow). Errors are *DecodeError: 413
// request_too_large, 415 unsupported_media_type, or 400 bad_request.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any, o DecodeOptions) error {
	if o.RequireJSONContentType {
		if ct := r.Header.Get("Content-Type"); !isJSONContentType(ct) {
			return &DecodeError{Status: http.StatusUnsupportedMediaType, Code: CodeUnsupportedMediaType, Message: "Content-Type must be application/json"}
		}
	}
	limit := o.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBodyBytes
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if o.DisallowUnknownFields {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(dst); err != nil {
		return decodeError(err, limit)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return decodeError(err, limit)
		}
		return &DecodeError{Status: http.StatusBadRequest, Code: CodeBadRequest, Message: "request body must contain a single JSON value", Err: err}
	}
	return nil
}

// WriteDecodeError writes err as the standard API error body. Errors that
// are not *DecodeError become 400 bad_request.
func WriteDecodeError(w http.ResponseWriter, err error) {
	var de *DecodeError
	if !errors.As(err, &de) {
		de = &DecodeError{Status: http.StatusBadRequest, Code: CodeBadRequest, Message: "invalid JSON body"}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(de.Status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": de.Code, "message": de.Message}})
}

func isJSONContentType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

func decodeError(err error, limit int64) error {
	var (
		tooBig    *http.MaxBytesError
		syntax    *json.SyntaxError
		typeError *json.UnmarshalTypeError
	)
	bad := func(msg string) error {
		return &DecodeError{Status: http.StatusBadRequest, Code: CodeBadRequest, Message: msg, Err: err}
	}
	switch {
	case errors.As(err, &tooBig):
		return &DecodeError{Status: http.StatusRequestEntityTooLarge, Code: CodeRequestTooLarge, Message: fmt.Sprintf("request body is larger than %d bytes", limit), Err: err}
	case errors.Is(err, io.EOF):
		return bad("request body is empty")
	case errors.Is(err, io.ErrUnexpectedEOF):
		return bad("request body is not valid JSON (it ends too early)")
	case errors.As(err, &syntax):
		return bad(fmt.Sprintf("request body is not valid JSON (at byte %d)", syntax.Offset))
	case errors.As(err, &typeError):
		if typeError.Field != "" {
			return bad(fmt.Sprintf("field %q must be %s", typeError.Field, typeError.Type))
		}
		return bad(fmt.Sprintf("request body must be %s", typeError.Type))
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return bad("unknown field " + strings.TrimPrefix(err.Error(), "json: unknown field "))
	}
	return bad("invalid JSON body")
}
