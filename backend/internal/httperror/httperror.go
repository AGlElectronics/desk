// Package httperror defines a typed service error and an HTTP renderer that
// turns it into a consistent JSON shape: {"error":{"code","message","details"}}.
//
// All API handlers call Render with either an *Error or a plain error (which
// is converted to an opaque 500). This matches the Stash convention and
// keeps client parsing simple.
package httperror

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/rs/zerolog/log"
)

// Code is a stable string returned to API clients. Add codes sparingly —
// the SPA and third-party integrators branch on them.
type Code string

const (
	CodeBadRequest   Code = "bad_request"
	CodeValidation   Code = "validation_failed"
	CodeUnauthorized Code = "unauthorized"
	CodeForbidden    Code = "forbidden"
	CodeNotFound     Code = "not_found"
	CodeConflict     Code = "conflict"
	CodeInternal     Code = "internal"
)

// Error is the canonical service error. Services return *Error or wrap one
// via httperror.Internal(err) when passing an opaque upstream failure
// through to the client.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
	cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.cause != nil {
		return string(e.Code) + ": " + e.Message + ": " + e.cause.Error()
	}
	return string(e.Code) + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.cause }

// New constructs a typed error. Message is user-visible: avoid leaking
// internal details or stack content.
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// WithDetails attaches arbitrary details (e.g. validation issues).
func (e *Error) WithDetails(d any) *Error {
	e.Details = d
	return e
}

// WithCause keeps the original wrapped error for logging while continuing
// to expose only Message to the client.
func (e *Error) WithCause(cause error) *Error {
	e.cause = cause
	return e
}

var (
	ErrNotFound     = New(CodeNotFound, "resource not found")
	ErrUnauthorized = New(CodeUnauthorized, "authentication required")
	ErrForbidden    = New(CodeForbidden, "permission denied")
)

// NotFound returns a 404 for a named resource ("edition", "submission"...).
func NotFound(resource string) *Error {
	return New(CodeNotFound, resource+" not found")
}

// Validation builds a 422 with structured issue details.
func Validation(message string, details any) *Error {
	return New(CodeValidation, message).WithDetails(details)
}

// Conflict builds a 409.
func Conflict(message string) *Error {
	return New(CodeConflict, message)
}

// BadRequest builds a 400.
func BadRequest(message string) *Error {
	return New(CodeBadRequest, message)
}

// Internal wraps an opaque error. The client sees a generic message; the
// cause is logged with the full request context.
func Internal(cause error) *Error {
	return New(CodeInternal, "internal server error").WithCause(cause)
}

type response struct {
	Error *Error `json:"error"`
}

// Render writes err as JSON with the appropriate status code. If err is not
// an *Error it is treated as internal. 5xx responses are logged with the
// request path/method; 4xx responses are not (they're client problems).
func Render(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = Internal(err)
	}

	status := statusFor(e.Code)
	if status >= 500 {
		log.Ctx(r.Context()).Error().Err(err).
			Str("path", r.URL.Path).
			Str("method", r.Method).
			Msg("http handler error")
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response{Error: e})
}

func statusFor(code Code) int {
	switch code {
	case CodeBadRequest:
		return http.StatusBadRequest
	case CodeValidation:
		return http.StatusUnprocessableEntity
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
