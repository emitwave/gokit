package logger

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/emitwave/gokit/security"
)

// statusCapture wraps http.ResponseWriter so we can read back the status
// code and byte count after the handler runs. We don't implement the
// optional interfaces (Hijacker, Flusher, Pusher) here because most JSON
// APIs don't need them — if you need WebSockets/SSE through this
// middleware, swap in a richer wrapper.
type statusCapture struct {
	http.ResponseWriter
	status int
	bytes  int
	wrote  bool
}

func (sc *statusCapture) WriteHeader(code int) {
	if !sc.wrote {
		sc.status = code
		sc.wrote = true
	}
	sc.ResponseWriter.WriteHeader(code)
}

func (sc *statusCapture) Write(b []byte) (int, error) {
	// Implicit 200 if the handler writes without calling WriteHeader.
	if !sc.wrote {
		sc.status = http.StatusOK
		sc.wrote = true
	}
	n, err := sc.ResponseWriter.Write(b)
	sc.bytes += n
	return n, err
}

// HTTPOptions configures HTTPMiddleware.
type HTTPOptions struct {
	// SkipPaths are URL paths that should not produce a request log.
	// Useful for noisy endpoints like /healthz or /metrics.
	SkipPaths []string

	// LevelFor lets you customize the log level per response. Default:
	//   <500 → INFO
	//   >=500 → ERROR
	// Override e.g. to log all 4xx at WARN.
	LevelFor func(status int) slog.Level
}

// HTTPMiddleware returns middleware that:
//
//  1. Attaches a request-scoped logger to the request context. The logger
//     carries request_id (from security.RequestID), method, and path so
//     every log line from the handler shares those fields.
//  2. Logs the request itself once it completes, with status + duration.
//
// Place AFTER security.RequestID and security.Recovery so the logger
// has the request ID available and panics are still caught.
func HTTPMiddleware(base *slog.Logger, opts ...HTTPOptions) func(http.Handler) http.Handler {
	cfg := HTTPOptions{}
	if len(opts) > 0 {
		cfg = opts[0]
	}
	if cfg.LevelFor == nil {
		cfg.LevelFor = defaultLevelFor
	}
	skip := make(map[string]struct{}, len(cfg.SkipPaths))
	for _, p := range cfg.SkipPaths {
		skip[p] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			reqID := security.RequestIDFromContext(r.Context())

			// Build a logger pre-loaded with request fields. Calls inside
			// the handler that use FromContext will inherit these.
			scoped := base.With(
				"request_id", reqID,
				"method", r.Method,
				"path", r.URL.Path,
			)
			ctx := IntoContext(r.Context(), scoped)

			sc := &statusCapture{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sc, r.WithContext(ctx))

			if _, skipped := skip[r.URL.Path]; skipped {
				return
			}

			level := cfg.LevelFor(sc.status)
			scoped.LogAttrs(r.Context(), level, "http request",
				slog.Int("status", sc.status),
				slog.Int("bytes", sc.bytes),
				slog.Duration("duration", time.Since(start)),
				slog.String("remote_ip", clientIP(r)),
				slog.String("user_agent", r.UserAgent()),
			)
		})
	}
}

func defaultLevelFor(status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status >= 400:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// clientIP is a duplicate of the security package's helper, intentionally
// inlined to avoid creating a circular import between security and logger.
// The logger should be addable independently of how the security package
// is configured (proxy trust etc.); we use the safe non-proxy path.
func clientIP(r *http.Request) string {
	// Strip port if present.
	addr := r.RemoteAddr
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return addr
}
