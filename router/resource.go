package router

import (
	"net/http"
	"strings"
)

// ResourceController is the bundle of handlers for a CRUD resource. Any
// nil field is skipped — that's how you opt out of an action without
// fighting an Only/Except list:
//
//	api.Resource("/users", router.ResourceController{
//	    Index: list, Show: show, Store: create,
//	    // Update and Destroy left nil → not registered
//	})
//
// The seven handler fields map to the seven canonical resource actions:
//
//	Index   GET    /resource          list collection
//	Create  GET    /resource/create   form to create (HTML apps)
//	Store   POST   /resource          persist a new item
//	Show    GET    /resource/{id}     fetch one
//	Edit    GET    /resource/{id}/edit form to update (HTML apps)
//	Update  PUT    /resource/{id}     replace
//	Destroy DELETE /resource/{id}     remove
//
// API-only services typically use Index/Show/Store/Update/Destroy and
// leave Create/Edit nil.
type ResourceController struct {
	Index   http.HandlerFunc
	Show    http.HandlerFunc
	Store   http.HandlerFunc
	Update  http.HandlerFunc
	Destroy http.HandlerFunc

	// HTML form actions — leave nil for JSON APIs.
	Create http.HandlerFunc
	Edit   http.HandlerFunc

	// ParamName is the path parameter for the id segment. Defaults to
	// "id". Set to your model name (e.g. "user") to align with model
	// binders registered via Router.Bind.
	ParamName string

	// Name is the prefix for named routes. If empty, it's derived from
	// the prefix path: "/users" → "users", "/api/v1/blog-posts" →
	// "blog-posts". Routes get suffixes ".index", ".show", etc.
	Name string

	// AllowPatch additionally registers PATCH on the update path. Default
	// is PUT only — set true if your API treats partial updates as PATCH.
	AllowPatch bool
}

// ResourceRoutes is the set of routes created by Resource(). Each field
// is the *Route returned from registration, or nil if that action's
// handler was not provided. Use them to chain per-action configuration:
//
//	rr := api.Resource("/users", controller)
//	rr.Update.Middleware(requireAdmin)
//	rr.Destroy.Middleware(requireSuperuser)
type ResourceRoutes struct {
	Index   *Route
	Show    *Route
	Store   *Route
	Update  *Route
	Destroy *Route
	Create  *Route
	Edit    *Route

	// PatchUpdate is set when AllowPatch is true; it points to the same
	// handler as Update but registered for PATCH. Configure both via
	// rr.Update.Middleware(...) followed by rr.PatchUpdate.Middleware(...)
	// if needed.
	PatchUpdate *Route
}

// registerResource is the shared implementation used by Router.Resource
// and Group.Resource. It always registers via a Group so the host /
// prefix / middleware composition is consistent across both entry points.
func registerResource(g *Group, prefix string, c ResourceController) *ResourceRoutes {
	param := c.ParamName
	if param == "" {
		param = "id"
	}
	nameBase := c.Name
	if nameBase == "" {
		nameBase = deriveResourceName(prefix)
	}
	idSeg := "/{" + param + "}"
	rr := &ResourceRoutes{}

	if c.Index != nil {
		rr.Index = g.Get(prefix, c.Index).Name(nameBase + ".index")
	}
	if c.Create != nil {
		rr.Create = g.Get(prefix+"/create", c.Create).Name(nameBase + ".create")
	}
	if c.Store != nil {
		rr.Store = g.Post(prefix, c.Store).Name(nameBase + ".store")
	}
	if c.Show != nil {
		rr.Show = g.Get(prefix+idSeg, c.Show).Name(nameBase + ".show")
	}
	if c.Edit != nil {
		rr.Edit = g.Get(prefix+idSeg+"/edit", c.Edit).Name(nameBase + ".edit")
	}
	if c.Update != nil {
		rr.Update = g.Put(prefix+idSeg, c.Update).Name(nameBase + ".update")
		if c.AllowPatch {
			rr.PatchUpdate = g.Patch(prefix+idSeg, c.Update).Name(nameBase + ".update.patch")
		}
	}
	if c.Destroy != nil {
		rr.Destroy = g.Delete(prefix+idSeg, c.Destroy).Name(nameBase + ".destroy")
	}
	return rr
}

// deriveResourceName picks the last non-empty path segment as the name
// base. "/api/v1/blog-posts" → "blog-posts". "/users/" → "users".
func deriveResourceName(prefix string) string {
	clean := strings.Trim(prefix, "/")
	if clean == "" {
		return "resource"
	}
	if i := strings.LastIndex(clean, "/"); i >= 0 {
		return clean[i+1:]
	}
	return clean
}
