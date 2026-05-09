package security

import (
	"net/http"
	"strings"
)

// BodyLimitOptions configures the BodyLimit middleware.
type BodyLimitOptions struct {
	// MaxBytes is the maximum allowed request body size in bytes.
	// Default: 20 * 1024 (20KB).
	MaxBytes int64

	// SkipPrefixes is a list of URL path prefixes that are exempt
	// from the body size limit (e.g. template endpoints that carry
	// large HTML email bodies).
	SkipPrefixes []string
}

// BodyLimit returns middleware that caps request body size using
// http.MaxBytesReader. When the limit is exceeded, subsequent reads
// on the body return an http.MaxBytesError which handler-level JSON
// decoders surface as a 400-level error naturally.
func BodyLimit(opts ...BodyLimitOptions) Middleware {
	cfg := BodyLimitOptions{MaxBytes: 20 * 1024}
	if len(opts) > 0 {
		if opts[0].MaxBytes > 0 {
			cfg.MaxBytes = opts[0].MaxBytes
		}
		cfg.SkipPrefixes = opts[0].SkipPrefixes
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, prefix := range cfg.SkipPrefixes {
				if strings.HasPrefix(r.URL.Path, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}
			r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxBytes)
			next.ServeHTTP(w, r)
		})
	}
}
