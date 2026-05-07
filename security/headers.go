package security

import "net/http"

// HeaderOptions configures the Headers middleware. Zero values mean "use
// the secure default" — pass the explicit "off" sentinel to disable a
// header outright. This makes the common path (just call Headers()) safe.
type HeaderOptions struct {
	// ContentSecurityPolicy sets the CSP header. Default is a conservative
	// policy suitable for JSON APIs ("default-src 'none'; frame-ancestors
	// 'none'"). For HTML apps you almost certainly want to override this.
	ContentSecurityPolicy string

	// StrictTransportSecurity (HSTS) value. Default enables HSTS for one
	// year on all subdomains. Set to "off" to disable (e.g. behind a
	// proxy that already adds it, or for non-HTTPS dev).
	StrictTransportSecurity string

	// ReferrerPolicy. Default: "strict-origin-when-cross-origin".
	ReferrerPolicy string

	// PermissionsPolicy. Default disables a broad set of browser features
	// that APIs never need. Override for HTML apps that legitimately use
	// camera/mic/geolocation.
	PermissionsPolicy string

	// FrameOptions controls X-Frame-Options. Default: "DENY".
	FrameOptions string

	// CrossOriginOpenerPolicy. Default: "same-origin".
	CrossOriginOpenerPolicy string

	// CrossOriginResourcePolicy. Default: "same-origin".
	CrossOriginResourcePolicy string

	// HideServerHeader removes the Server response header if it was set
	// upstream (some proxies leak version info there). Default: true.
	HideServerHeader *bool
}

// "off" disables a default header without us needing a separate bool per
// field. It's a sentinel — any non-empty value other than "off" is sent.
const headerOff = "off"

// Headers returns middleware that sets a hardened set of response headers
// suitable for JSON APIs. Customize via options:
//
//	security.Headers(security.HeaderOptions{
//	    ContentSecurityPolicy: "default-src 'self'",
//	    FrameOptions:          "SAMEORIGIN",
//	})
func Headers(opts ...HeaderOptions) Middleware {
	cfg := HeaderOptions{}
	if len(opts) > 0 {
		cfg = opts[0]
	}
	cfg = applyHeaderDefaults(cfg)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()

			setIfActive(h, "Content-Security-Policy", cfg.ContentSecurityPolicy)
			setIfActive(h, "Referrer-Policy", cfg.ReferrerPolicy)
			setIfActive(h, "Permissions-Policy", cfg.PermissionsPolicy)
			setIfActive(h, "X-Frame-Options", cfg.FrameOptions)
			setIfActive(h, "Cross-Origin-Opener-Policy", cfg.CrossOriginOpenerPolicy)
			setIfActive(h, "Cross-Origin-Resource-Policy", cfg.CrossOriginResourcePolicy)

			// HSTS only meaningful over TLS. Browsers ignore it on HTTP,
			// but we still skip it to avoid noise in dev. r.TLS is set
			// when the server itself terminated TLS; behind a TLS-
			// terminating proxy, set the X-Forwarded-Proto check too.
			if cfg.StrictTransportSecurity != headerOff &&
				(r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https") {
				setIfActive(h, "Strict-Transport-Security", cfg.StrictTransportSecurity)
			}

			// Belt-and-suspenders for legacy browsers; modern browsers ignore.
			h.Set("X-Content-Type-Options", "nosniff")

			if cfg.HideServerHeader == nil || *cfg.HideServerHeader {
				h.Del("Server")
				h.Del("X-Powered-By")
			}

			next.ServeHTTP(w, r)
		})
	}
}

func setIfActive(h http.Header, key, value string) {
	if value == "" || value == headerOff {
		return
	}
	h.Set(key, value)
}

func applyHeaderDefaults(cfg HeaderOptions) HeaderOptions {
	if cfg.ContentSecurityPolicy == "" {
		cfg.ContentSecurityPolicy = "default-src 'none'; frame-ancestors 'none'"
	}
	if cfg.StrictTransportSecurity == "" {
		cfg.StrictTransportSecurity = "max-age=31536000; includeSubDomains"
	}
	if cfg.ReferrerPolicy == "" {
		cfg.ReferrerPolicy = "strict-origin-when-cross-origin"
	}
	if cfg.PermissionsPolicy == "" {
		cfg.PermissionsPolicy = "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()"
	}
	if cfg.FrameOptions == "" {
		cfg.FrameOptions = "DENY"
	}
	if cfg.CrossOriginOpenerPolicy == "" {
		cfg.CrossOriginOpenerPolicy = "same-origin"
	}
	if cfg.CrossOriginResourcePolicy == "" {
		cfg.CrossOriginResourcePolicy = "same-origin"
	}
	return cfg
}
