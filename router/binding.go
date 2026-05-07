package router

import (
	"context"
	"errors"
	"net/http"
)

// Binder resolves a path parameter value to a domain object. It runs
// before the handler, on every request that has a matching path param.
//
// Return ErrBindingNotFound to signal a 404 (the most common case — bad
// id in the URL). Any other error is treated as a 500 and routed through
// the binding-error handler.
//
// Example:
//
//	r.Bind("user", func(ctx context.Context, id string) (any, error) {
//	    u, err := userRepo.FindByID(ctx, id)
//	    if errors.Is(err, sql.ErrNoRows) {
//	        return nil, router.ErrBindingNotFound
//	    }
//	    return u, err
//	})
type Binder func(ctx context.Context, value string) (any, error)

// ErrBindingNotFound signals a 404 from a Binder. Wrap your repository's
// not-found error or return this directly — the binding middleware checks
// with errors.Is so wrapped errors work.
var ErrBindingNotFound = errors.New("binding: not found")

// boundKey is a typed context key — unexported so external packages can't
// collide with it. We use a single string-based key per param name.
type boundKey string

// Bind registers a binder for a path parameter. The first time a request
// with a matching path parameter arrives, the binder is called and the
// result is stashed on the request context for the handler to retrieve
// via Bound[T].
//
// Binders are matched by path-parameter name, not by route. So a binder
// registered for "user" runs on every route that has a {user} segment.
// This matches the typical usage where the same model resolves the same
// way regardless of which route uses it.
func (r *Router) Bind(name string, b Binder) *Router {
	if r.binders == nil {
		r.binders = make(map[string]Binder)
	}
	r.binders[name] = b
	return r
}

// BindingNotFoundHandler customizes the response when a binder returns
// ErrBindingNotFound. Default is plain text "Not Found" with 404. Wire
// this up to your response package for consistent error envelopes:
//
//	r.BindingNotFoundHandler(func(w http.ResponseWriter, req *http.Request) {
//	    response.NotFound(w, req, "")
//	})
func (r *Router) BindingNotFoundHandler(h http.HandlerFunc) *Router {
	r.bindingNotFound = h
	return r
}

// BindingErrorHandler customizes the response when a binder returns an
// error other than ErrBindingNotFound. Default is plain 500.
func (r *Router) BindingErrorHandler(h func(http.ResponseWriter, *http.Request, error)) *Router {
	r.bindingError = h
	return r
}

// Bound retrieves a previously-bound value of type T from the request
// context. Use it in handlers after the binding middleware has run:
//
//	r.Bind("user", lookupUser)
//	r.Get("/users/{user}", func(w http.ResponseWriter, req *http.Request) {
//	    u, ok := router.Bound[*models.User](req, "user")
//	    if !ok { /* never bound — programming error */ }
//	    response.OK(w, u)
//	})
//
// Returns the zero value of T and false if there's no bound value, or if
// the bound value is the wrong type for T (e.g. you registered a binder
// returning *User but asked for *Post).
func Bound[T any](r *http.Request, name string) (T, bool) {
	var zero T
	v := r.Context().Value(boundKey(name))
	if v == nil {
		return zero, false
	}
	t, ok := v.(T)
	if !ok {
		return zero, false
	}
	return t, true
}

// bindingMiddleware wraps a handler so registered binders run on each
// request that has a matching path parameter. We always wrap (regardless
// of whether r.binders has any entries at register time), because binders
// can be added between route registration and the route's first request.
//
// Performance: a no-binders fast path skips all the work in <50ns. With
// binders, each one costs a map lookup + a PathValue read; the binder
// itself is the dominant cost.
func (r *Router) bindingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Fast path: no binders registered.
		if len(r.binders) == 0 {
			next.ServeHTTP(w, req)
			return
		}

		ctx := req.Context()
		for name, binder := range r.binders {
			val := req.PathValue(name)
			if val == "" {
				// Binder registered but this route has no {name} segment.
				continue
			}

			obj, err := binder(ctx, val)
			if err != nil {
				if errors.Is(err, ErrBindingNotFound) {
					if r.bindingNotFound != nil {
						r.bindingNotFound(w, req)
					} else {
						http.Error(w, "Not Found", http.StatusNotFound)
					}
					return
				}
				if r.bindingError != nil {
					r.bindingError(w, req, err)
				} else {
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
				return
			}
			ctx = context.WithValue(ctx, boundKey(name), obj)
		}
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}
