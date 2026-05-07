package router

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Route is a single registered route. The fields you'll read most are
// Method/Path/Full and (after Name() has been called) the registered name.
//
// The middleware fields and lazy-build machinery exist to support adding
// per-route middleware via .Middleware() after the route is registered
// but before the first request reaches it. After the first request, the
// chain is frozen via sync.Once — adding middleware later is a no-op.
type Route struct {
	Method string
	Path   string // pattern within the group (e.g. "/{id}")
	Full   string // pattern with all group prefixes ("/api/v1/users/{id}")
	Host   string // host pattern, empty when matching any host

	name    string
	routeMW []Middleware

	// global + group middleware, captured at register time so later Use()
	// calls don't retroactively wrap already-registered routes.
	groupMW  []Middleware
	globalMW []Middleware

	// coreHandler is the innermost wrapper around the user handler — it
	// includes the binding middleware so that PathValue (set by ServeMux)
	// is available before the binders run.
	coreHandler http.Handler

	once  sync.Once
	final http.Handler

	r *Router
}

// Name registers a name for this route, enabling URL generation:
//
//	r.Get("/users/{id}", show).Name("users.show")
//	url := r.URL("users.show", "id", "42") // → "/users/42"
func (rt *Route) Name(name string) *Route {
	rt.name = name
	if rt.r != nil {
		rt.r.registerName(name, rt)
	}
	return rt
}

// Middleware appends per-route middleware. Useful for guards that apply
// to one endpoint but not the rest of its group:
//
//	r.Get("/admin/dashboard", dashboard).Middleware(requireSuperuser)
//
// Per-route middleware runs INSIDE group and global middleware (i.e. it's
// the closest wrapper around the handler aside from the auto-applied
// binding middleware). This call must happen before the route serves its
// first request — additions after that are silently ignored.
func (rt *Route) Middleware(mws ...Middleware) *Route {
	rt.routeMW = append(rt.routeMW, mws...)
	return rt
}

// build assembles the final http.Handler by wrapping the core handler in
// route → group → global middleware (innermost to outermost). It runs at
// most once per route, on the first request that reaches it.
//
// Wrapping order, from outside in:
//
//	global → group → routeMW → binding → user handler
//
// global and group are captured at registration time; routeMW is whatever
// was on the route by the time the first request arrives.
func (rt *Route) build() http.Handler {
	rt.once.Do(func() {
		h := rt.coreHandler
		for i := len(rt.routeMW) - 1; i >= 0; i-- {
			h = rt.routeMW[i](h)
		}
		for i := len(rt.groupMW) - 1; i >= 0; i-- {
			h = rt.groupMW[i](h)
		}
		for i := len(rt.globalMW) - 1; i >= 0; i-- {
			h = rt.globalMW[i](h)
		}
		rt.final = h
	})
	return rt.final
}

// ---------- path parameter helpers --------------------------------------

// PathParam returns the named path parameter as a string. Empty string
// if the route doesn't have that segment.
func PathParam(r *http.Request, name string) string {
	return r.PathValue(name)
}

// PathParamInt parses a path parameter as int. Returns ok=false if the
// param is missing or not a valid int — handlers should respond with 400
// in that case rather than 0-fallback (which can mask bugs).
func PathParamInt(r *http.Request, name string) (int, bool) {
	v := r.PathValue(name)
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	return n, err == nil
}

// PathParamInt64 is PathParamInt for int64 (database IDs, timestamps).
func PathParamInt64(r *http.Request, name string) (int64, bool) {
	v := r.PathValue(name)
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil
}

// joinPath joins a group prefix with a route pattern, normalizing slashes
// so callers don't have to think about whether their prefix or pattern
// ends/starts with "/".
func joinPath(prefix, pattern string) string {
	prefix = strings.TrimRight(prefix, "/")
	if pattern == "" {
		if prefix == "" {
			return "/"
		}
		return prefix
	}
	if !strings.HasPrefix(pattern, "/") {
		pattern = "/" + pattern
	}
	return prefix + pattern
}
