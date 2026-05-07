// Package logger provides production-grade structured logging on top of
// the stdlib log/slog. It adds three things over raw slog:
//
//  1. Sensible default handlers — JSON for prod, pretty-printed for dev,
//     selected by environment so you don't end up with unparseable logs
//     in CI or unreadable logs in your terminal.
//  2. Context propagation — a logger can be stored on the request context
//     by middleware, then retrieved in handlers without parameter passing.
//     The middleware also attaches request_id automatically.
//  3. HTTP middleware — logs every request (method, path, status, duration,
//     bytes, request ID) at INFO, with sane field names that line up
//     with cloud log explorers.
//
// Usage:
//
//	log := logger.New(logger.Config{Format: "json", Level: slog.LevelInfo})
//	mux := logger.HTTPMiddleware(log)(mux)
//
//	// In handlers:
//	log := logger.FromContext(r.Context())
//	log.Info("user signed up", "user_id", id)
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config controls logger construction. Zero-value Config returns a
// JSON logger at INFO level writing to stderr — safe for production.
type Config struct {
	// Format is "json" or "pretty". Empty defaults to "json".
	Format string

	// Level is the minimum level to emit. Zero-value (LevelInfo) is fine.
	Level slog.Level

	// Output is where logs are written. nil → os.Stderr.
	Output io.Writer

	// AddSource appends file:line of each log call. Useful in dev,
	// noisy in prod.
	AddSource bool

	// RedactKeys is a list of field keys whose values get replaced with
	// "[REDACTED]". Use for fields that shouldn't appear in logs even
	// by accident: "password", "authorization", "ssn", etc.
	RedactKeys []string
}

// New builds a *slog.Logger with the given config. If cfg.Format is
// "pretty" we use slog's text handler (single-line key=value); JSON
// otherwise. JSON is what you want in production — it's parseable by
// every log aggregator without configuration.
func New(cfg Config) *slog.Logger {
	if cfg.Output == nil {
		cfg.Output = os.Stderr
	}
	opts := &slog.HandlerOptions{
		Level:     cfg.Level,
		AddSource: cfg.AddSource,
	}
	if len(cfg.RedactKeys) > 0 {
		redactSet := make(map[string]struct{}, len(cfg.RedactKeys))
		for _, k := range cfg.RedactKeys {
			redactSet[strings.ToLower(k)] = struct{}{}
		}
		// ReplaceAttr fires for every attribute on every record. Keep it
		// fast: a map lookup per attr, no allocations on the hot path.
		opts.ReplaceAttr = func(_ []string, a slog.Attr) slog.Attr {
			if _, hit := redactSet[strings.ToLower(a.Key)]; hit {
				return slog.String(a.Key, "[REDACTED]")
			}
			return a
		}
	}

	var handler slog.Handler
	switch strings.ToLower(cfg.Format) {
	case "pretty", "text":
		handler = slog.NewTextHandler(cfg.Output, opts)
	default:
		handler = slog.NewJSONHandler(cfg.Output, opts)
	}
	return slog.New(handler)
}

// NewFromEnv builds a logger using common environment variables:
//
//	LOG_LEVEL  = debug | info | warn | error  (default: info)
//	LOG_FORMAT = json | pretty                (default: json)
//
// Useful when you want one config knob for ops without rebuilding.
func NewFromEnv() *slog.Logger {
	return New(Config{
		Level:  parseLevel(os.Getenv("LOG_LEVEL")),
		Format: os.Getenv("LOG_FORMAT"),
	})
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ---------- context plumbing --------------------------------------------

type ctxKey int

const loggerKey ctxKey = iota

// IntoContext attaches a logger to a context. Subsequent FromContext
// calls on derived contexts will return this logger. Middleware uses
// this to inject a request-scoped logger; handlers retrieve it.
func IntoContext(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, log)
}

// FromContext returns the logger attached to ctx, or a default fallback
// logger if none was attached. The fallback prevents nil-deref panics
// in handlers that might be invoked outside the middleware (tests, jobs).
func FromContext(ctx context.Context) *slog.Logger {
	if log, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return log
	}
	return slog.Default()
}

// SetDefault sets slog's package-level default logger. Call this once at
// startup if you want bare slog.Info / slog.Error calls (e.g. in third-
// party libs) to flow through your configured handler.
func SetDefault(log *slog.Logger) {
	slog.SetDefault(log)
}
