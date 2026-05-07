package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/emitwave/gokit/security"
)

// slogAdapter bridges Go's stdlib slog logger to security.PanicLogger.
// We keep the security package's logger interface tiny so it doesn't
// pull in any specific logging library — adapters live next to usage.
type slogAdapter struct{ l *slog.Logger }

func (a slogAdapter) Error(msg string, fields ...any) { a.l.Error(msg, fields...) }

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Pull the request ID set by the middleware — useful for logging
		// from inside the handler with the same correlation key.
		reqID := security.RequestIDFromContext(r.Context())
		fmt.Fprintf(w, "hello (request: %s)\n", reqID)
	})

	mux.HandleFunc("/panic", func(w http.ResponseWriter, r *http.Request) {
		panic("intentional panic for demo")
	})

	// Order matters:
	//   RequestID first → all later layers (incl. Recovery's logger)
	//                     have access to the correlation ID
	//   Recovery next   → catches panics from everything below it
	//   Headers, CORS, RateLimit → applied to all real requests
	stack := security.Chain(
		security.RequestID(),
		security.Recovery(security.RecoveryOptions{Logger: slogAdapter{logger}}),
		security.Headers(),
		security.CORS(security.CORSOptions{
			AllowedOrigins:   []string{"https://app.emitwave.com"},
			AllowCredentials: true,
			ExposedHeaders:   []string{"X-Request-ID"},
			MaxAge:           600,
		}),
		security.RateLimit(security.RateLimitOptions{
			RPS:               10,
			Burst:             20,
			TrustForwardedFor: false, // set true if behind a trusted proxy
		}),
	)

	fmt.Println("listening on :8080")
	_ = http.ListenAndServe(":8080", stack(mux))
}
