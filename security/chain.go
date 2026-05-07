// Package security provides composable HTTP middleware for production
// services: secure headers, CORS, rate limiting, request IDs, and panic
// recovery. Everything is plain net/http middleware (func(http.Handler)
// http.Handler) so it works with the stdlib mux, chi, gorilla/mux, and any
// router that accepts http.Handler.
//
// Typical usage:
//
//	mux := http.NewServeMux()
//	mux.HandleFunc("/", handler)
//
//	stack := security.Chain(
//	    security.RequestID(),
//	    security.Recovery(logger),
//	    security.Headers(),
//	    security.CORS(security.CORSOptions{
//	        AllowedOrigins: []string{"https://app.emitwave.com"},
//	    }),
//	    security.RateLimit(security.RateLimitOptions{RPS: 10, Burst: 20}),
//	)
//
//	http.ListenAndServe(":8080", stack(mux))
package security

import "net/http"

// Middleware is the standard net/http middleware shape.
type Middleware func(http.Handler) http.Handler

// Chain composes middleware into a single Middleware. The first argument
// is the outermost layer (runs first on the request, last on the response),
// matching how middleware is typically read top-to-bottom.
//
//	Chain(A, B, C)(h) ≡ A(B(C(h)))
//
// Returning a Middleware (not a final handler) means callers can use
// Chain to build reusable stacks and apply them per-route or app-wide.
func Chain(mws ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		// Wrap from the inside out so the first argument ends up outermost.
		for i := len(mws) - 1; i >= 0; i-- {
			next = mws[i](next)
		}
		return next
	}
}
