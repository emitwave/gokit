package router

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------- per-route middleware ----------------------------------------

func TestRouteMiddleware(t *testing.T) {
	var trace []string
	mw := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				trace = append(trace, name+":in")
				next.ServeHTTP(w, r)
				trace = append(trace, name+":out")
			})
		}
	}

	r := New()
	r.Use(mw("global"))

	api := r.Group("/api")
	api.Use(mw("group"))

	api.Get("/x", func(w http.ResponseWriter, _ *http.Request) {
		trace = append(trace, "handler")
	}).Middleware(mw("route"))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/x", nil))

	want := []string{
		"global:in", "group:in", "route:in",
		"handler",
		"route:out", "group:out", "global:out",
	}
	if !equalStrings(trace, want) {
		t.Errorf("trace: got %v, want %v", trace, want)
	}
}

func TestRouteMiddlewareScopedToOneRoute(t *testing.T) {
	// Middleware on route A must NOT run when route B is hit.
	var aHit, bHit bool
	guard := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			aHit = true
			next.ServeHTTP(w, r)
		})
	}

	r := New()
	r.Get("/a", func(w http.ResponseWriter, _ *http.Request) {}).Middleware(guard)
	r.Get("/b", func(w http.ResponseWriter, _ *http.Request) { bHit = true })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/b", nil))

	if aHit {
		t.Error("route-A middleware should not run on /b")
	}
	if !bHit {
		t.Error("route /b handler did not execute")
	}
}

// ---------- fallback ----------------------------------------------------

func TestFallback(t *testing.T) {
	r := New()
	r.Get("/known", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("known"))
	})
	r.Fallback(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("custom 404"))
	})

	// Known route → handler runs as normal.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/known", nil))
	if rec.Body.String() != "known" {
		t.Errorf("known: got %q", rec.Body.String())
	}

	// Unknown route → fallback.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
	if rec.Body.String() != "custom 404" {
		t.Errorf("body: got %q, want 'custom 404'", rec.Body.String())
	}
}

// ---------- resource ----------------------------------------------------

func TestResourceRoutes(t *testing.T) {
	called := map[string]bool{}
	mark := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			called[name] = true
		}
	}

	r := New()
	api := r.Group("/api/v1")
	rr := api.Resource("/users", ResourceController{
		Index:   mark("index"),
		Show:    mark("show"),
		Store:   mark("store"),
		Update:  mark("update"),
		Destroy: mark("destroy"),
	})

	cases := []struct {
		method, path, want string
	}{
		{"GET", "/api/v1/users", "index"},
		{"POST", "/api/v1/users", "store"},
		{"GET", "/api/v1/users/42", "show"},
		{"PUT", "/api/v1/users/42", "update"},
		{"DELETE", "/api/v1/users/42", "destroy"},
	}
	for _, c := range cases {
		called = map[string]bool{}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if !called[c.want] {
			t.Errorf("%s %s did not invoke %q (called=%v)", c.method, c.path, c.want, called)
		}
	}

	// Named routes — derived name "users".
	if got := r.URL("users.show", "id", "42"); got != "/api/v1/users/42" {
		t.Errorf("users.show URL: got %q", got)
	}
	if got := r.URL("users.index"); got != "/api/v1/users" {
		t.Errorf("users.index URL: got %q", got)
	}

	// All five action routes are non-nil; create/edit are nil.
	if rr.Index == nil || rr.Show == nil || rr.Store == nil ||
		rr.Update == nil || rr.Destroy == nil {
		t.Error("expected non-nil routes for all five JSON-API actions")
	}
	if rr.Create != nil || rr.Edit != nil {
		t.Error("Create/Edit should be nil when handler not provided")
	}
}

func TestResourceCustomParamAndName(t *testing.T) {
	r := New()
	r.Resource("/posts", ResourceController{
		Show:      func(w http.ResponseWriter, _ *http.Request) {},
		ParamName: "post",
		Name:      "blog.post",
	})

	if got := r.URL("blog.post.show", "post", "abc"); got != "/posts/abc" {
		t.Errorf("custom name+param: got %q, want /posts/abc", got)
	}
}

func TestResourcePerActionMiddleware(t *testing.T) {
	guardCalled := false
	guard := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			guardCalled = true
			next.ServeHTTP(w, r)
		})
	}

	r := New()
	rr := r.Resource("/users", ResourceController{
		Index: func(w http.ResponseWriter, _ *http.Request) {},
		Show:  func(w http.ResponseWriter, _ *http.Request) {},
	})
	rr.Show.Middleware(guard) // only Show gets the guard

	// Hit Index → guard does NOT run.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/users", nil))
	if guardCalled {
		t.Error("guard should not run on Index when only Show has it")
	}

	// Hit Show → guard runs.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/users/1", nil))
	if !guardCalled {
		t.Error("guard should run on Show")
	}
}

func TestResourceAllowPatch(t *testing.T) {
	called := ""
	r := New()
	rr := r.Resource("/users", ResourceController{
		Update:     func(w http.ResponseWriter, req *http.Request) { called = req.Method },
		AllowPatch: true,
	})
	if rr.PatchUpdate == nil {
		t.Fatal("PatchUpdate should be set when AllowPatch=true")
	}

	// PUT → calls update.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/users/1", nil))
	if called != "PUT" {
		t.Errorf("PUT: got %q", called)
	}

	// PATCH → also calls update.
	called = ""
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PATCH", "/users/1", nil))
	if called != "PATCH" {
		t.Errorf("PATCH: got %q", called)
	}
}

// ---------- host / subdomain --------------------------------------------

func TestHostScoping(t *testing.T) {
	r := New()
	api := r.Host("api.emitwave.com")
	api.Get("/users", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("api"))
	})

	web := r.Host("emitwave.com")
	web.Get("/users", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("web"))
	})

	tests := []struct {
		host, want string
	}{
		{"api.emitwave.com", "api"},
		{"emitwave.com", "web"},
	}
	for _, tt := range tests {
		req := httptest.NewRequest("GET", "/users", nil)
		req.Host = tt.host
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Body.String() != tt.want {
			t.Errorf("host=%s: got %q, want %q", tt.host, rec.Body.String(), tt.want)
		}
	}
}

func TestHostWithGroup(t *testing.T) {
	// Host + nested group: api.emitwave.com / api/v1
	r := New()
	g := r.Host("api.emitwave.com").Group("/v1")
	g.Get("/ping", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})

	req := httptest.NewRequest("GET", "/v1/ping", nil)
	req.Host = "api.emitwave.com"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Body.String() != "pong" {
		t.Errorf("got %q", rec.Body.String())
	}

	// Wrong host → no match (no fallback set, so 404).
	req = httptest.NewRequest("GET", "/v1/ping", nil)
	req.Host = "other.example.com"
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("wrong host: got %d, want 404", rec.Code)
	}
}

// ---------- model binding -----------------------------------------------

type fakeUser struct{ ID, Name string }

func TestBindingHappyPath(t *testing.T) {
	r := New()
	r.Bind("user", func(_ context.Context, id string) (any, error) {
		return &fakeUser{ID: id, Name: "Tunde"}, nil
	})

	r.Get("/users/{user}", func(w http.ResponseWriter, req *http.Request) {
		u, ok := Bound[*fakeUser](req, "user")
		if !ok {
			t.Error("expected bound user, got none")
			return
		}
		_, _ = w.Write([]byte(u.ID + ":" + u.Name))
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/users/42", nil))
	if rec.Body.String() != "42:Tunde" {
		t.Errorf("got %q", rec.Body.String())
	}
}

func TestBindingNotFound(t *testing.T) {
	r := New()
	r.Bind("user", func(_ context.Context, _ string) (any, error) {
		return nil, ErrBindingNotFound
	})
	r.Get("/users/{user}", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("handler should not run when binding fails")
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/users/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

func TestBindingNotFoundCustomHandler(t *testing.T) {
	r := New()
	r.Bind("user", func(_ context.Context, _ string) (any, error) {
		return nil, ErrBindingNotFound
	})
	r.BindingNotFoundHandler(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("custom-not-found"))
	})
	r.Get("/users/{user}", func(w http.ResponseWriter, _ *http.Request) {})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/users/x", nil))
	if !strings.Contains(rec.Body.String(), "custom-not-found") {
		t.Errorf("custom handler not used: %q", rec.Body.String())
	}
}

func TestBindingErrorHandler(t *testing.T) {
	upstream := errors.New("db is on fire")
	r := New()
	r.Bind("user", func(_ context.Context, _ string) (any, error) {
		return nil, upstream
	})

	var captured error
	r.BindingErrorHandler(func(w http.ResponseWriter, _ *http.Request, err error) {
		captured = err
		w.WriteHeader(http.StatusInternalServerError)
	})

	r.Get("/users/{user}", func(w http.ResponseWriter, _ *http.Request) {})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/users/x", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d", rec.Code)
	}
	if !errors.Is(captured, upstream) {
		t.Errorf("error not propagated: got %v", captured)
	}
}

func TestBindingSkipsRoutesWithoutParam(t *testing.T) {
	binderRan := false
	r := New()
	r.Bind("user", func(_ context.Context, _ string) (any, error) {
		binderRan = true
		return nil, nil
	})
	// This route has no {user} segment — binder must not run.
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if binderRan {
		t.Error("binder should not run for routes without a matching path param")
	}
}

func TestBoundWrongType(t *testing.T) {
	r := New()
	r.Bind("user", func(_ context.Context, _ string) (any, error) {
		return &fakeUser{ID: "u1"}, nil
	})

	type otherType struct{}
	r.Get("/users/{user}", func(w http.ResponseWriter, req *http.Request) {
		// Asking for the wrong type — should return zero + false, not panic.
		_, ok := Bound[*otherType](req, "user")
		if ok {
			t.Error("expected ok=false for wrong type")
		}
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/users/x", nil))
	if rec.Code >= 500 {
		t.Errorf("type mismatch should not 500, got %d", rec.Code)
	}
}
