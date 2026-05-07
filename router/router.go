// Package router provides ergonomic routing on top of the stdlib
// net/http.ServeMux. Zero external dependencies.
//
// Features:
//   - Method-aware patterns ("GET /users/{id}") via stdlib ServeMux
//   - Route groups with shared prefix and middleware
//   - Per-route middleware (.Middleware())
//   - Named routes for URL generation
//   - Resource routes (Index/Show/Store/Update/Destroy in one call)
//   - Host / subdomain scoping (exact match, no wildcards — see Host docs)
//   - Fallback handler for unmatched paths
//   - Model binding: register a binder, get the resolved value on ctx
//
// Example:
//
//	r := router.New()
//	r.Use(security.RequestID(), security.Recovery())
//
//	r.Bind("user", func(ctx context.Context, id string) (any, error) {
//	    return userRepo.FindByID(ctx, id)  // returns ErrBindingNotFound on miss
//	})
//
//	api := r.Group("/api/v1")
//	api.Use(authMiddleware)
//	api.Resource("/users", router.ResourceController{
//	    Index: userIndex, Show: userShow, Store: userCreate,
//	    Update: userUpdate, Destroy: userDelete,
//	    ParamName: "user",  // path param + binder key
//	})
//
//	r.Fallback(notFound)
//
//	http.ListenAndServe(":8080", r)
package router

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// Middleware matches the security package shape so anything from the
// security package can be passed to router.Use directly.
type Middleware = func(http.Handler) http.Handler

// Router is the top-level registration surface. It implements http.Handler
// so you can pass it directly to http.ListenAndServe.
type Router struct {
	mux        *http.ServeMux
	middleware []Middleware
	routes     []*Route

	mu    sync.RWMutex
	names map[string]*Route

	// fallback is the handler called when no route matches. nil → stdlib
	// default (404 with plain text body).
	fallback http.HandlerFunc

	// binders are model-binding functions keyed by path-parameter name.
	// Read at request time, so binders can be registered any time before
	// a route's first request.
	binders          map[string]Binder
	bindingNotFound  http.HandlerFunc
	bindingError     func(http.ResponseWriter, *http.Request, error)

	// Named middleware aliases and groups (see aliases.go). Embedded so
	// methods on Router can reach the registry without an extra hop.
	aliasRegistry aliasRegistry
}

// New returns an empty Router.
func New() *Router {
	return &Router{
		mux:     http.NewServeMux(),
		names:   make(map[string]*Route),
		binders: make(map[string]Binder),
	}
}

// Use appends global middleware. Applied to every route, in the order
// added — first call is outermost. Middleware added AFTER a route is
// registered does not retroactively wrap that route.
func (r *Router) Use(mws ...Middleware) *Router {
	r.middleware = append(r.middleware, mws...)
	return r
}

// Group creates a sub-scope with its own prefix and middleware.
func (r *Router) Group(prefix string) *Group {
	return &Group{r: r, prefix: prefix}
}

// Host returns a Group scoped to an exact host. Routes registered through
// the returned group will only match requests whose Host header equals
// the given value.
//
// Note: the stdlib ServeMux does NOT support wildcard host patterns
// — e.g. "{tenant}.example.com" is treated as a literal string, not a
// pattern with a {tenant} variable. For multi-tenant subdomain routing,
// either register each subdomain explicitly or do the host parsing in
// middleware that runs before the router.
func (r *Router) Host(host string) *Group {
	return &Group{r: r, host: host}
}

// Fallback sets the handler called when no registered route matches. By
// default the stdlib ServeMux returns a plain "404 page not found"; this
// lets you produce the JSON error envelope your API uses everywhere else:
//
//	r.Fallback(func(w http.ResponseWriter, req *http.Request) {
//	    response.NotFound(w, req, "")
//	})
func (r *Router) Fallback(h http.HandlerFunc) *Router {
	r.fallback = h
	return r
}

// ---------- top-level shortcuts -----------------------------------------

func (r *Router) Handle(method, pattern string, h http.HandlerFunc) *Route {
	return r.register(method, "", pattern, pattern, h, nil)
}
func (r *Router) Get(pattern string, h http.HandlerFunc) *Route {
	return r.Handle(http.MethodGet, pattern, h)
}
func (r *Router) Post(pattern string, h http.HandlerFunc) *Route {
	return r.Handle(http.MethodPost, pattern, h)
}
func (r *Router) Put(pattern string, h http.HandlerFunc) *Route {
	return r.Handle(http.MethodPut, pattern, h)
}
func (r *Router) Patch(pattern string, h http.HandlerFunc) *Route {
	return r.Handle(http.MethodPatch, pattern, h)
}
func (r *Router) Delete(pattern string, h http.HandlerFunc) *Route {
	return r.Handle(http.MethodDelete, pattern, h)
}

// Resource registers a CRUD set of routes at the top level. See the
// Group.Resource doc for the typical pattern (resources usually live
// inside a group with auth/middleware).
func (r *Router) Resource(prefix string, c ResourceController) *ResourceRoutes {
	g := &Group{r: r}
	return registerResource(g, prefix, c)
}

// register is the single registration funnel. Splits cleanly along the
// "what we know now" / "what we know at first serve" line:
//
//	NOW (register time):
//	  - Snapshot global + group middleware (so later Use() doesn't re-wrap)
//	  - Wrap user handler with binding middleware (innermost)
//	  - Register a thin trampoline on the underlying ServeMux
//
//	LATER (first request):
//	  - Wrap with whatever route-level middleware was added via .Middleware()
//	  - Apply group then global middleware
//	  - Cache the assembled chain via sync.Once
func (r *Router) register(method, host, fullPattern, groupPattern string, h http.HandlerFunc, groupMW []Middleware) *Route {
	// Snapshot of global middleware at this moment.
	global := make([]Middleware, len(r.middleware))
	copy(global, r.middleware)

	// Group middleware was already snapshotted into the Group; we copy it
	// here onto the Route to keep Routes self-contained.
	gmw := make([]Middleware, len(groupMW))
	copy(gmw, groupMW)

	// Binding middleware is innermost — needs PathValue, which ServeMux
	// populates before the handler runs. We always wrap, because binders
	// can be registered after this route is registered (the wrapper reads
	// the live r.binders map at request time).
	core := r.bindingMiddleware(h)

	rt := &Route{
		Method:      method,
		Host:        host,
		Path:        groupPattern,
		Full:        fullPattern,
		coreHandler: core,
		groupMW:     gmw,
		globalMW:    global,
		r:           r,
	}

	// Register a trampoline that defers to rt.build() at request time.
	// build() uses sync.Once so the assembly cost is paid once per route.
	pattern := method + " " + host + fullPattern
	r.mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rt.build().ServeHTTP(w, req)
	}))

	r.routes = append(r.routes, rt)
	return rt
}

// registerName is called by Route.Name() to add a name → route entry.
func (r *Router) registerName(name string, rt *Route) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.names[name] = rt
}

// URL generates a URL for a named route, substituting path parameters
// from the supplied key/value pairs. Unused pairs are appended as query
// string params:
//
//	r.URL("users.show", "id", "42")                    // "/users/42"
//	r.URL("posts.index", "page", "2")                  // "/posts?page=2"
//	r.URL("users.show", "id", "42", "expand", "posts") // "/users/42?expand=posts"
//
// Returns "" if the name doesn't exist. Note: URL() returns the path part
// only, even if the route has a host. For absolute URLs across hosts,
// prefix the scheme + host yourself (you usually have it from config).
func (r *Router) URL(name string, kv ...string) string {
	r.mu.RLock()
	rt, ok := r.names[name]
	r.mu.RUnlock()
	if !ok {
		return ""
	}

	path := rt.Full
	pairs := make([][2]string, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		pairs = append(pairs, [2]string{kv[i], kv[i+1]})
	}

	used := make(map[string]struct{}, len(pairs))
	for _, p := range pairs {
		placeholder := "{" + p[0] + "}"
		if strings.Contains(path, placeholder) {
			path = strings.ReplaceAll(path, placeholder, p[1])
			used[p[0]] = struct{}{}
		}
	}

	var qs []string
	for _, p := range pairs {
		if _, ok := used[p[0]]; ok {
			continue
		}
		qs = append(qs, p[0]+"="+p[1])
	}
	if len(qs) > 0 {
		path += "?" + strings.Join(qs, "&")
	}
	return path
}

// Routes returns a snapshot of all registered routes.
func (r *Router) Routes() []*Route {
	out := make([]*Route, len(r.routes))
	copy(out, r.routes)
	return out
}

// ServeHTTP implements http.Handler. When a fallback is configured, we do
// a non-mutating mux.Handler() lookup first to detect "no match" (empty
// pattern) and route those to the fallback. Method-not-allowed (405) is
// still surfaced normally, since mux.Handler() returns a non-empty
// pattern for that case.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.fallback != nil {
		_, pattern := r.mux.Handler(req)
		if pattern == "" {
			r.fallback(w, req)
			return
		}
	}
	r.mux.ServeHTTP(w, req)
}

// PrintRoutes returns a tabular listing of registered routes — handy for
// a CLI subcommand or a /debug/routes endpoint. Includes host when set.
func (r *Router) PrintRoutes() string {
	var b strings.Builder
	for _, rt := range r.routes {
		name := rt.name
		if name == "" {
			name = "-"
		}
		host := rt.Host
		if host == "" {
			host = "*"
		}
		fmt.Fprintf(&b, "%-7s %-20s %-40s %s\n", rt.Method, host, rt.Full, name)
	}
	return b.String()
}
