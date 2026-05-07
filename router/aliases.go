package router

import (
	"fmt"
	"sync"
)

// Named middleware
//
// Aliases let you register a middleware once under a short name and
// reference it by that name on routes and groups. Middleware groups
// bundle several aliases under a single name. This mirrors Laravel's
// `$routeMiddleware` / `$middlewareGroups`:
//
//	r.AliasMiddleware("auth", authMW)
//	r.AliasMiddleware("throttle", rateLimit(100, time.Minute))
//	r.AliasMiddleware("admin", requireAdmin)
//
//	r.MiddlewareGroup("api", "auth", "throttle")
//	r.MiddlewareGroup("admin-area", "auth", "admin")
//
//	r.Get("/dashboard", dashboard).WithMiddleware("auth")
//	r.Get("/admin/users", users).WithMiddleware("admin-area")  // resolves to auth + admin
//
//	api := r.Group("/api/v1")
//	api.UseAlias("api")  // group is now wrapped in auth + throttle
//
// Resolution happens at call time. Unknown names panic — that's a
// wiring/typo bug worth catching loud rather than silently dropping
// the middleware. Aliases must be registered before they're referenced.
//
// Why a separate method instead of overloading Middleware()/Use()?
// Function- and string-typed args don't mix cleanly in Go without an
// `any` parameter (which would lose compile-time type safety). Two
// methods is the lesser evil — and the call sites read clearly:
//
//	route.Middleware(authMW)        // direct function
//	route.WithMiddleware("auth")    // by alias
//	g.Use(loggingMW)                // direct function
//	g.UseAlias("auth", "throttle")  // by alias

// aliasRegistry holds the alias / group state. Embedded in Router (not
// a separate struct) so wiring stays straightforward.
type aliasRegistry struct {
	mu      sync.RWMutex
	aliases map[string]Middleware
	groups  map[string][]string
}

func (a *aliasRegistry) ensure() {
	if a.aliases == nil {
		a.aliases = make(map[string]Middleware)
	}
	if a.groups == nil {
		a.groups = make(map[string][]string)
	}
}

// AliasMiddleware registers a Middleware under a short name. Re-aliasing
// the same name replaces the previous binding (handy in tests).
func (r *Router) AliasMiddleware(name string, mw Middleware) *Router {
	if name == "" {
		panic("router: alias name is required")
	}
	if mw == nil {
		panic(fmt.Sprintf("router: AliasMiddleware(%q): middleware is nil", name))
	}
	r.aliasRegistry.mu.Lock()
	r.aliasRegistry.ensure()
	r.aliasRegistry.aliases[name] = mw
	r.aliasRegistry.mu.Unlock()
	return r
}

// MiddlewareGroup defines a named bundle of aliases. Bundle members can
// be aliases OR other groups — groups expand recursively at resolve
// time. Cycles are detected and panic with a clear message.
//
//	r.MiddlewareGroup("api", "auth", "throttle", "json-only")
//	r.MiddlewareGroup("api-v2", "api", "version-header")  // includes the api group
func (r *Router) MiddlewareGroup(name string, members ...string) *Router {
	if name == "" {
		panic("router: middleware group name is required")
	}
	r.aliasRegistry.mu.Lock()
	r.aliasRegistry.ensure()
	cp := make([]string, len(members))
	copy(cp, members)
	r.aliasRegistry.groups[name] = cp
	r.aliasRegistry.mu.Unlock()
	return r
}

// resolveNames expands a list of alias / group names into the
// corresponding []Middleware in registration order. Panics on unknown
// names or cycles (programming errors).
func (r *Router) resolveNames(names []string) []Middleware {
	r.aliasRegistry.mu.RLock()
	defer r.aliasRegistry.mu.RUnlock()

	out := make([]Middleware, 0, len(names))
	visiting := make(map[string]bool)
	var resolve func(name string)
	resolve = func(name string) {
		if visiting[name] {
			panic(fmt.Sprintf("router: middleware group cycle detected at %q", name))
		}
		// Group expansion takes precedence over an alias of the same name —
		// gives users a single, predictable resolution rule. (Don't allow
		// shadowing both anyway in a sane setup; we just pick one.)
		if members, ok := r.aliasRegistry.groups[name]; ok {
			visiting[name] = true
			for _, m := range members {
				resolve(m)
			}
			delete(visiting, name)
			return
		}
		mw, ok := r.aliasRegistry.aliases[name]
		if !ok {
			panic(fmt.Sprintf("router: unknown middleware alias or group %q", name))
		}
		out = append(out, mw)
	}

	for _, name := range names {
		resolve(name)
	}
	return out
}

// WithMiddleware appends middleware to a route by alias / group name.
// Resolution happens immediately; unknown names panic. Equivalent to
// `route.Middleware(r.Aliased(...names)...)` but reads better.
func (rt *Route) WithMiddleware(names ...string) *Route {
	if rt.r == nil {
		panic("router: Route is not attached to a Router")
	}
	resolved := rt.r.resolveNames(names)
	rt.routeMW = append(rt.routeMW, resolved...)
	return rt
}

// UseAlias appends middleware to a group by alias / group name.
// Resolution happens at call time, so aliases must already be registered.
func (g *Group) UseAlias(names ...string) *Group {
	if g.r == nil {
		panic("router: Group is not attached to a Router")
	}
	resolved := g.r.resolveNames(names)
	g.middleware = append(g.middleware, resolved...)
	return g
}

// Aliased is the explicit, lower-level escape hatch: resolve names to
// the corresponding []Middleware so you can pass them anywhere a normal
// []Middleware is accepted (including r.Use for global middleware).
//
//	r.Use(r.Aliased("auth", "throttle")...)
func (r *Router) Aliased(names ...string) []Middleware {
	return r.resolveNames(names)
}
