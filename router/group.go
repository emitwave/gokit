package router

import "net/http"

// Group is a scoped route registration unit. It carries a host pattern,
// path prefix, and middleware stack that get applied to every route
// registered through it. Groups can be nested — child groups inherit and
// extend the parent's prefix and middleware.
//
//	api := r.Group("/api/v1")
//	api.Use(authMiddleware)
//
//	users := api.Group("/users")
//	users.Get("/", index)            // GET /api/v1/users/
//	users.Get("/{id}", show).Name("users.show")
//
// A group's host is set when the group is created from Router.Host:
//
//	api := r.Host("api.emitwave.com").Group("/v1")
//	api.Get("/users", index)         // GET api.emitwave.com/v1/users
type Group struct {
	r          *Router
	host       string // empty = match any host (current default mux behavior)
	prefix     string
	middleware []Middleware
}

// Use appends middleware to this group's stack. Middleware applies to
// routes registered AFTER the Use() call.
func (g *Group) Use(mws ...Middleware) *Group {
	g.middleware = append(g.middleware, mws...)
	return g
}

// Group creates a child group. The child inherits the parent's host,
// extended path prefix, and a SNAPSHOT of the parent's middleware.
// Middleware added to the parent later does NOT propagate down to existing
// children — this matches user expectation and avoids surprises.
func (g *Group) Group(prefix string) *Group {
	mw := make([]Middleware, len(g.middleware))
	copy(mw, g.middleware)
	return &Group{
		r:          g.r,
		host:       g.host,
		prefix:     joinPath(g.prefix, prefix),
		middleware: mw,
	}
}

// Handle is the most general registration. Use the per-method shortcuts
// (Get, Post, ...) for clearer call sites.
func (g *Group) Handle(method, pattern string, h http.HandlerFunc) *Route {
	return g.r.register(method, g.host, joinPath(g.prefix, pattern), pattern, h, g.middleware)
}

func (g *Group) Get(pattern string, h http.HandlerFunc) *Route {
	return g.Handle(http.MethodGet, pattern, h)
}
func (g *Group) Post(pattern string, h http.HandlerFunc) *Route {
	return g.Handle(http.MethodPost, pattern, h)
}
func (g *Group) Put(pattern string, h http.HandlerFunc) *Route {
	return g.Handle(http.MethodPut, pattern, h)
}
func (g *Group) Patch(pattern string, h http.HandlerFunc) *Route {
	return g.Handle(http.MethodPatch, pattern, h)
}
func (g *Group) Delete(pattern string, h http.HandlerFunc) *Route {
	return g.Handle(http.MethodDelete, pattern, h)
}
func (g *Group) Options(pattern string, h http.HandlerFunc) *Route {
	return g.Handle(http.MethodOptions, pattern, h)
}

// Resource registers a CRUD set of routes for the given controller.
// See ResourceController for the shape and ResourceRoutes for the return
// value. This is a Group-method so resources inherit the group's prefix
// and middleware:
//
//	api := r.Group("/api/v1")
//	api.Use(auth)
//	rr := api.Resource("/users", router.ResourceController{
//	    Index: userIndex, Show: userShow, Store: userCreate,
//	    Update: userUpdate, Destroy: userDelete,
//	})
//	rr.Update.Middleware(requireAdmin)  // per-action override
func (g *Group) Resource(prefix string, c ResourceController) *ResourceRoutes {
	return registerResource(g, prefix, c)
}
