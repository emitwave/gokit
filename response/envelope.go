// Package response provides JSON API helpers with a consistent envelope
// so every endpoint returns the same shape:
//
//	// Success (200/201/202):
//	{ "data": ... , "meta": { ... }? }
//
//	// Error (4xx/5xx):
//	{ "error": { "code": "...", "message": "...", "details": { ... }? }, "request_id": "..." }
//
// All helpers set Content-Type, write the status, and serialize the body.
// They never panic on JSON encode errors (they fall back to a plain text
// 500) so handlers can call them as a final step without defer'd cleanup.
package response

// SuccessEnvelope wraps successful payloads. Meta is omitted when empty.
type SuccessEnvelope struct {
	Data any            `json:"data"`
	Meta map[string]any `json:"meta,omitempty"`
}

// ErrorEnvelope wraps failure payloads. RequestID is populated from the
// request context (when the security.RequestID middleware ran) so client
// bug reports include the correlation key.
type ErrorEnvelope struct {
	Error     ErrorBody `json:"error"`
	RequestID string    `json:"request_id,omitempty"`
}

// ErrorBody is the structured error inside ErrorEnvelope. Code is a stable,
// machine-readable identifier (e.g. "validation_failed", "not_found") that
// frontends can branch on. Message is human-readable. Details carries
// extra structured info — for validation errors that's the field-keyed
// error bag.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}
