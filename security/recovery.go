package security

import (
	"fmt"
	"net/http"
	"runtime/debug"
)

// PanicLogger is the minimal interface Recovery needs for logging.
// Any logger that exposes an Error(msg, fields...) shape can be adapted
// trivially. We keep this tiny so users aren't forced to depend on a
// specific logging library.
type PanicLogger interface {
	Error(msg string, fields ...any)
}

// RecoveryOptions configures the Recovery middleware.
type RecoveryOptions struct {
	// Logger receives panic messages and stack traces. If nil, panics
	// are still recovered but not logged — strongly recommended to
	// pass one.
	Logger PanicLogger

	// OnPanic is called after recovery. Default writes a 500 JSON body.
	// Useful to override for HTML apps or to add error tracking
	// (Sentry, Honeybadger) in addition to the response.
	OnPanic func(w http.ResponseWriter, r *http.Request, recovered any)

	// IncludeStackInResponse exposes the panic value and stack to
	// clients. Only enable in development — in production this leaks
	// internals to attackers.
	IncludeStackInResponse bool
}

// Recovery returns middleware that turns panics into proper 500 responses
// instead of dropping the connection. It logs the recovered value with
// the request ID (if RequestID middleware ran first) so the panic is
// correlatable with other logs from the same request.
//
// Place Recovery near the outside of your middleware chain — but inside
// RequestID — so almost every panic gets caught and logged with context.
func Recovery(opts ...RecoveryOptions) Middleware {
	cfg := RecoveryOptions{}
	if len(opts) > 0 {
		cfg = opts[0]
	}
	if cfg.OnPanic == nil {
		cfg.OnPanic = func(w http.ResponseWriter, r *http.Request, _ any) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"internal_server_error","message":"An unexpected error occurred."}`))
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				// http.ErrAbortHandler is the documented "abort silently"
				// signal — re-panic so the server treats it normally.
				if rec == http.ErrAbortHandler {
					panic(rec)
				}

				stack := debug.Stack()
				if cfg.Logger != nil {
					cfg.Logger.Error(
						"panic recovered",
						"error", fmt.Sprint(rec),
						"method", r.Method,
						"path", r.URL.Path,
						"request_id", RequestIDFromContext(r.Context()),
						"stack", string(stack),
					)
				}

				if cfg.IncludeStackInResponse {
					w.Header().Set("Content-Type", "text/plain; charset=utf-8")
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprintf(w, "panic: %v\n\n%s", rec, stack)
					return
				}

				cfg.OnPanic(w, r, rec)
			}()
			next.ServeHTTP(w, r)
		})
	}
}
