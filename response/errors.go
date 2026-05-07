package response

import (
	"net/http"

	"github.com/emitwave/gokit/security"
	"github.com/emitwave/gokit/validation"
)

// Common machine-readable error codes. Use these constants in clients
// to branch on error type without parsing message strings.
const (
	CodeBadRequest        = "bad_request"
	CodeUnauthorized      = "unauthorized"
	CodeForbidden         = "forbidden"
	CodeNotFound          = "not_found"
	CodeMethodNotAllowed  = "method_not_allowed"
	CodeConflict          = "conflict"
	CodeValidationFailed  = "validation_failed"
	CodeTooManyRequests   = "too_many_requests"
	CodeInternalError     = "internal_server_error"
	CodeServiceUnavailable = "service_unavailable"
)

// Error writes an error envelope with the given status, code, and message.
// The request ID is pulled from r.Context() so clients have a correlation
// key to report. r may be nil if you don't have it (the request_id field
// will be omitted).
func Error(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	ErrorWithDetails(w, r, status, code, message, nil)
}

// ErrorWithDetails is Error plus an arbitrary details payload. Useful for
// surfacing structured per-field info or upstream service codes.
func ErrorWithDetails(w http.ResponseWriter, r *http.Request, status int, code, message string, details any) {
	env := ErrorEnvelope{
		Error: ErrorBody{
			Code:    code,
			Message: message,
			Details: details,
		},
	}
	if r != nil {
		env.RequestID = security.RequestIDFromContext(r.Context())
	}
	JSON(w, status, env)
}

// ValidationFailed writes a 422 response with the validation error bag
// as `details`. The bag's MarshalJSON produces `{field: [msg, ...]}`,
// the field-keyed shape REST clients commonly expect.
func ValidationFailed(w http.ResponseWriter, r *http.Request, errs *validation.Errors) {
	ErrorWithDetails(w, r,
		http.StatusUnprocessableEntity,
		CodeValidationFailed,
		"The given data was invalid.",
		errs,
	)
}

// ---------- shortcuts ----------------------------------------------------
//
// Each shortcut takes a custom message string but applies sensible defaults
// when message is empty. This keeps call sites short for the 90% case
// (`response.NotFound(w, r, "")`) while still allowing context-specific
// messages where useful.

func BadRequest(w http.ResponseWriter, r *http.Request, message string) {
	Error(w, r, http.StatusBadRequest, CodeBadRequest, defaultIfEmpty(message, "The request was malformed."))
}

func Unauthorized(w http.ResponseWriter, r *http.Request, message string) {
	Error(w, r, http.StatusUnauthorized, CodeUnauthorized, defaultIfEmpty(message, "Authentication is required."))
}

func Forbidden(w http.ResponseWriter, r *http.Request, message string) {
	Error(w, r, http.StatusForbidden, CodeForbidden, defaultIfEmpty(message, "You do not have permission to access this resource."))
}

func NotFound(w http.ResponseWriter, r *http.Request, message string) {
	Error(w, r, http.StatusNotFound, CodeNotFound, defaultIfEmpty(message, "The requested resource was not found."))
}

func MethodNotAllowed(w http.ResponseWriter, r *http.Request, message string) {
	Error(w, r, http.StatusMethodNotAllowed, CodeMethodNotAllowed, defaultIfEmpty(message, "This method is not allowed for the requested resource."))
}

func Conflict(w http.ResponseWriter, r *http.Request, message string) {
	Error(w, r, http.StatusConflict, CodeConflict, defaultIfEmpty(message, "The request conflicts with the current state of the resource."))
}

func TooManyRequests(w http.ResponseWriter, r *http.Request, message string) {
	Error(w, r, http.StatusTooManyRequests, CodeTooManyRequests, defaultIfEmpty(message, "Too many requests."))
}

func InternalError(w http.ResponseWriter, r *http.Request, message string) {
	Error(w, r, http.StatusInternalServerError, CodeInternalError, defaultIfEmpty(message, "An unexpected error occurred."))
}

func ServiceUnavailable(w http.ResponseWriter, r *http.Request, message string) {
	Error(w, r, http.StatusServiceUnavailable, CodeServiceUnavailable, defaultIfEmpty(message, "Service is temporarily unavailable."))
}

func defaultIfEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
