package security

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// requestIDKey is the context key under which the request ID is stored.
// Unexported type prevents collisions with other packages' context keys.
type ctxKey int

const requestIDKey ctxKey = iota

// RequestIDOptions configures the RequestID middleware.
type RequestIDOptions struct {
	// HeaderName is both read (incoming) and written (response). Default:
	// "X-Request-ID".
	HeaderName string

	// Generator returns a new request ID when one isn't supplied by the
	// client. Default: UUIDv4.
	Generator func() string

	// TrustClient controls whether we accept client-supplied request IDs
	// from the incoming header. Useful for distributed tracing where the
	// caller already minted a correlation ID. Default: true.
	//
	// Set to false if you don't want clients to influence your log
	// correlation keys (e.g. abuse scenarios).
	TrustClient *bool
}

// RequestID returns middleware that ensures every request has a stable
// ID. The ID is:
//
//  1. Read from the incoming header if present and TrustClient is true
//  2. Otherwise generated fresh
//  3. Stored on the request context (retrieve via RequestIDFromContext)
//  4. Echoed back in the response header
//
// Pair this with the logger so every log line in a request includes
// the same correlation key.
func RequestID(opts ...RequestIDOptions) Middleware {
	cfg := RequestIDOptions{}
	if len(opts) > 0 {
		cfg = opts[0]
	}
	if cfg.HeaderName == "" {
		cfg.HeaderName = "X-Request-ID"
	}
	if cfg.Generator == nil {
		cfg.Generator = func() string { return uuid.NewString() }
	}
	trustClient := true
	if cfg.TrustClient != nil {
		trustClient = *cfg.TrustClient
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := ""
			if trustClient {
				id = r.Header.Get(cfg.HeaderName)
			}
			if id == "" {
				id = cfg.Generator()
			}

			ctx := context.WithValue(r.Context(), requestIDKey, id)
			w.Header().Set(cfg.HeaderName, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestIDFromContext retrieves the request ID set by the middleware.
// Returns empty string if the middleware wasn't applied (so callers
// don't need to handle a separate "ok" return for the common case).
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}
