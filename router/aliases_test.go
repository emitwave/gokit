package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// tagger returns a middleware that appends `tag` to a comma-separated
// header. Lets us assert ordering of nested middleware.
func tagger(tag string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			existing := w.Header().Get("X-Tags")
			if existing != "" {
				existing += ","
			}
			w.Header().Set("X-Tags", existing+tag)
			next.ServeHTTP(w, r)
		})
	}
}

// blocker returns a middleware that 401s without calling next. Used to
// verify alias resolution actually attaches middleware.
func blocker() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "blocked", http.StatusUnauthorized)
		})
	}
}

func TestAliasMiddleware_RouteWithMiddleware(t *testing.T) {
	r := New()
	r.AliasMiddleware("auth", blocker())

	r.Get("/protected", ok).WithMiddleware("auth")
	r.Get("/public", ok)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("/protected: got %d, want 401", rec.Code)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/public", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/public: got %d, want 200", rec.Code)
	}
}

func TestAliasMiddleware_GroupUseAlias(t *testing.T) {
	r := New()
	r.AliasMiddleware("auth", blocker())

	api := r.Group("/api")
	api.UseAlias("auth")
	api.Get("/me", ok)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/me", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401 (group middleware should apply)", rec.Code)
	}
}

func TestMiddlewareGroup_ExpandsToMembers(t *testing.T) {
	r := New()
	r.AliasMiddleware("a", tagger("a"))
	r.AliasMiddleware("b", tagger("b"))
	r.AliasMiddleware("c", tagger("c"))
	r.MiddlewareGroup("api", "a", "b", "c")

	r.Get("/x", ok).WithMiddleware("api")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	tags := rec.Header().Get("X-Tags")
	if tags != "a,b,c" {
		t.Errorf("expected outermost-to-innermost order a,b,c — got %q", tags)
	}
}

func TestMiddlewareGroup_NestedGroups(t *testing.T) {
	// One group can include another group by name.
	r := New()
	r.AliasMiddleware("a", tagger("a"))
	r.AliasMiddleware("b", tagger("b"))
	r.AliasMiddleware("v", tagger("v"))

	r.MiddlewareGroup("api", "a", "b")
	r.MiddlewareGroup("api-v2", "api", "v") // includes the api group + v

	r.Get("/v2", ok).WithMiddleware("api-v2")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/v2", nil))
	if got := rec.Header().Get("X-Tags"); got != "a,b,v" {
		t.Errorf("nested group expansion: got %q, want a,b,v", got)
	}
}

func TestMiddlewareGroup_CycleDetection(t *testing.T) {
	r := New()
	r.AliasMiddleware("real", tagger("real"))
	r.MiddlewareGroup("a", "b", "real")
	r.MiddlewareGroup("b", "a") // cycle: a -> b -> a

	defer func() {
		if rec := recover(); rec == nil {
			t.Error("expected panic on cycle")
		} else if !strings.Contains(rec.(string), "cycle") {
			t.Errorf("panic message should mention cycle, got %q", rec)
		}
	}()
	_ = r.Aliased("a")
}

func TestAliasMiddleware_UnknownNamePanics(t *testing.T) {
	r := New()
	defer func() {
		if rec := recover(); rec == nil {
			t.Error("expected panic for unknown alias")
		}
	}()
	r.Get("/x", ok).WithMiddleware("nonexistent")
}

func TestAliasMiddleware_AliasedReturnsResolvedSlice(t *testing.T) {
	r := New()
	r.AliasMiddleware("a", tagger("a"))
	r.AliasMiddleware("b", tagger("b"))

	// Aliased returns []Middleware so it can be passed to anything that
	// accepts middleware functions — including r.Use for global.
	r.Use(r.Aliased("a", "b")...)
	r.Get("/x", ok)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if got := rec.Header().Get("X-Tags"); got != "a,b" {
		t.Errorf("got %q, want a,b", got)
	}
}

func TestAliasMiddleware_OrderingGlobalGroupRoute(t *testing.T) {
	// Confirm the documented wrapping order — global outermost, route
	// innermost — when all three layers come from aliases.
	r := New()
	r.AliasMiddleware("g", tagger("global"))
	r.AliasMiddleware("p", tagger("group"))
	r.AliasMiddleware("r", tagger("route"))

	r.Use(r.Aliased("g")...)

	api := r.Group("/api")
	api.UseAlias("p")
	api.Get("/x", ok).WithMiddleware("r")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/x", nil))
	// Each tagger appends its tag in execution order, so the recorded
	// order is global,group,route (outermost first).
	if got := rec.Header().Get("X-Tags"); got != "global,group,route" {
		t.Errorf("got %q, want global,group,route", got)
	}
}

func TestAliasMiddleware_RealMiddlewareCallsNext(t *testing.T) {
	// Confirm that aliased middleware actually pass through to the next
	// handler — handlerCalled should reach the user handler.
	r := New()
	var hits atomic.Int32
	r.AliasMiddleware("count", func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			hits.Add(1)
			next.ServeHTTP(w, req)
		})
	})

	r.Get("/x", ok).WithMiddleware("count")
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	if hits.Load() != 2 {
		t.Errorf("middleware should run on each request: got %d", hits.Load())
	}
}

func TestAliasMiddleware_ReplaceExisting(t *testing.T) {
	// Re-aliasing the same name replaces the prior binding (useful in
	// tests where you swap a real middleware for a fake).
	r := New()
	r.AliasMiddleware("auth", tagger("real"))
	r.AliasMiddleware("auth", tagger("fake"))
	r.Get("/x", ok).WithMiddleware("auth")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if got := rec.Header().Get("X-Tags"); got != "fake" {
		t.Errorf("got %q, want fake (re-alias should replace)", got)
	}
}
