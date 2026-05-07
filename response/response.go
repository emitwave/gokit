package response

import (
	"encoding/json"
	"net/http"
)

// JSON writes status + JSON-encoded body. It's the lowest-level helper —
// most handlers should reach for OK/Error/etc. instead, but this is here
// when you need full control over the envelope shape.
//
// On encode failure we fall back to a plain 500 with no body, since the
// status line has likely already been committed at that point.
func JSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// We can't change the status now — best effort is to log via the
		// caller's logger; the connection will close.
		return
	}
}

// success wraps data in the success envelope and writes it.
// meta is optional — pass nil to omit the meta key entirely.
func success(w http.ResponseWriter, status int, data any, meta map[string]any) {
	env := SuccessEnvelope{Data: data}
	if len(meta) > 0 {
		env.Meta = meta
	}
	JSON(w, status, env)
}

// OK writes 200 with the success envelope. Use for read endpoints.
func OK(w http.ResponseWriter, data any) {
	success(w, http.StatusOK, data, nil)
}

// OKWithMeta writes 200 with arbitrary meta — use for endpoints that
// need to surface counts, cursors, or feature flags alongside data.
func OKWithMeta(w http.ResponseWriter, data any, meta map[string]any) {
	success(w, http.StatusOK, data, meta)
}

// Created writes 201 — for resource creation. Set a Location header on
// w before calling if you want to point clients at the new resource.
func Created(w http.ResponseWriter, data any) {
	success(w, http.StatusCreated, data, nil)
}

// Accepted writes 202 — for async work that's been queued.
func Accepted(w http.ResponseWriter, data any) {
	success(w, http.StatusAccepted, data, nil)
}

// NoContent writes 204 with an empty body. Most often used for DELETE.
// Note: HTTP forbids a body on 204, so we don't write the envelope.
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}
