package security

import (
	"net/http"
	"strconv"
	"strings"
)

// CORSOptions configures the CORS middleware.
//
// The defaults are deliberately strict — empty AllowedOrigins means
// no cross-origin requests are accepted. You opt into wider access
// explicitly to avoid surprise wildcards in production.
type CORSOptions struct {
	// AllowedOrigins is the exact list of origins permitted (e.g.
	// "https://app.emitwave.com"). Use ["*"] for fully public APIs that
	// don't carry credentials. Mixing "*" with AllowCredentials=true is
	// invalid per spec and we'll refuse to send the credentials header
	// in that case.
	AllowedOrigins []string

	// AllowedMethods is the set of HTTP methods to allow. Defaults to
	// the common safe set if empty.
	AllowedMethods []string

	// AllowedHeaders mirrored on preflight responses. If empty, we echo
	// whatever the client sent in Access-Control-Request-Headers, which
	// is the most permissive but lowest-config option.
	AllowedHeaders []string

	// ExposedHeaders are surfaced to JS via Access-Control-Expose-Headers
	// (e.g. "X-Request-ID", "X-RateLimit-Remaining").
	ExposedHeaders []string

	// AllowCredentials enables cookies / Authorization on cross-origin
	// requests. When AllowedOrigins is ["*"], the specific request origin
	// is echoed (not "*") so credentials remain spec-compliant.
	AllowCredentials bool

	// MaxAge is the preflight cache duration in seconds. 0 → omitted.
	MaxAge int
}

// CORS returns middleware that handles CORS preflight (OPTIONS) and
// adds the appropriate response headers to actual requests. Preflight
// responses short-circuit with 204 No Content.
func CORS(opts CORSOptions) Middleware {
	cfg := applyCORSDefaults(opts)
	originSet := make(map[string]struct{}, len(cfg.AllowedOrigins))
	allowAll := false
	for _, o := range cfg.AllowedOrigins {
		if o == "*" {
			allowAll = true
		}
		originSet[o] = struct{}{}
	}

	allowedMethods := strings.Join(cfg.AllowedMethods, ", ")
	allowedHeaders := strings.Join(cfg.AllowedHeaders, ", ")
	exposedHeaders := strings.Join(cfg.ExposedHeaders, ", ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Non-CORS request → pass through untouched.
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			allowed := allowAll
			if !allowed {
				_, allowed = originSet[origin]
			}

			if allowed {
				// Echo the specific origin (not "*") whenever credentials
				// are enabled or we have a non-wildcard match — Vary lets
				// caches handle multiple origins correctly.
				if allowAll && !cfg.AllowCredentials {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Add("Vary", "Origin")
				}

				if cfg.AllowCredentials {
					// When allowAll is true we already echo the specific
					// origin (not "*") above, so this is spec-compliant.
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
				if exposedHeaders != "" {
					w.Header().Set("Access-Control-Expose-Headers", exposedHeaders)
				}
			}

			// Preflight: respond and stop. We only emit method/header
			// echoes when the origin was actually allowed, otherwise the
			// browser would interpret a 204 with no headers as success.
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				if allowed {
					w.Header().Set("Access-Control-Allow-Methods", allowedMethods)

					if allowedHeaders != "" {
						w.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
					} else if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
						// Echo what the client asked for as a sane default.
						w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
					}
					if cfg.MaxAge > 0 {
						w.Header().Set("Access-Control-Max-Age", strconv.Itoa(cfg.MaxAge))
					}
					w.Header().Add("Vary", "Access-Control-Request-Method")
					w.Header().Add("Vary", "Access-Control-Request-Headers")
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func applyCORSDefaults(o CORSOptions) CORSOptions {
	if len(o.AllowedMethods) == 0 {
		o.AllowedMethods = []string{
			http.MethodGet,
			http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
			http.MethodOptions,
		}
	}
	return o
}
