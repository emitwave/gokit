package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func ok(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("ok"))
}

// ---------- basic routing ------------------------------------------------

func TestBasicGetRoute(t *testing.T) {
	r := New()
	r.Get("/hello", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("world"))
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/hello", nil))

	if rec.Body.String() != "world" {
		t.Errorf("body: got %q", rec.Body.String())
	}
}

func TestMethodMatching(t *testing.T) {
	r := New()
	r.Get("/x", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("get"))
	})
	r.Post("/x", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("post"))
	})

	tests := []struct{ method, want string }{
		{"GET", "get"},
		{"POST", "post"},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(tt.method, "/x", nil))
		if rec.Body.String() != tt.want {
			t.Errorf("%s /x: got %q, want %q", tt.method, rec.Body.String(), tt.want)
		}
	}
}

func TestPathParams(t *testing.T) {
	r := New()
	r.Get("/users/{id}", func(w http.ResponseWriter, req *http.Request) {
		id, ok := PathParamInt(req, "id")
		if !ok {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("user "))
		_, _ = w.Write([]byte(PathParam(req, "id")))
		_ = id
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/users/42", nil))
	if rec.Body.String() != "user 42" {
		t.Errorf("got %q", rec.Body.String())
	}
}

// ---------- middleware ---------------------------------------------------

func TestMiddlewareOrder(t *testing.T) {
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
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/x", nil))

	want := []string{"global:in", "group:in", "handler", "group:out", "global:out"}
	if !equalStrings(trace, want) {
		t.Errorf("trace: got %v, want %v", trace, want)
	}
}

func TestNestedGroups(t *testing.T) {
	r := New()
	api := r.Group("/api/v1")
	users := api.Group("/users")

	users.Get("/{id}", func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("u" + PathParam(req, "id")))
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/users/7", nil))
	if rec.Body.String() != "u7" {
		t.Errorf("got %q", rec.Body.String())
	}
}

// ---------- naming + URL generation -------------------------------------

func TestNamedRoutesAndURL(t *testing.T) {
	r := New()
	api := r.Group("/api/v1")
	api.Get("/users/{id}", ok).Name("users.show")
	api.Get("/posts", ok).Name("posts.index")

	tests := []struct {
		name string
		kv   []string
		want string
	}{
		{"users.show", []string{"id", "42"}, "/api/v1/users/42"},
		{"posts.index", nil, "/api/v1/posts"},
		{"posts.index", []string{"page", "2"}, "/api/v1/posts?page=2"},
		{"users.show", []string{"id", "42", "expand", "posts"}, "/api/v1/users/42?expand=posts"},
		{"does.not.exist", nil, ""},
	}
	for _, tt := range tests {
		got := r.URL(tt.name, tt.kv...)
		if got != tt.want {
			t.Errorf("URL(%q, %v): got %q, want %q", tt.name, tt.kv, got, tt.want)
		}
	}
}

// ---------- joinPath edge cases -----------------------------------------

func TestJoinPath(t *testing.T) {
	tests := []struct {
		prefix, pattern, want string
	}{
		{"", "/x", "/x"},
		{"/api", "/x", "/api/x"},
		{"/api/", "/x", "/api/x"},
		{"/api", "x", "/api/x"},
		{"/api", "", "/api"},
		{"", "", "/"},
	}
	for _, tt := range tests {
		got := joinPath(tt.prefix, tt.pattern)
		if got != tt.want {
			t.Errorf("joinPath(%q,%q): got %q, want %q", tt.prefix, tt.pattern, got, tt.want)
		}
	}
}

// ---------- print routes -------------------------------------------------

func TestPrintRoutes(t *testing.T) {
	r := New()
	r.Get("/", ok).Name("home")
	api := r.Group("/api")
	api.Post("/users", ok).Name("users.store")

	out := r.PrintRoutes()
	if !strings.Contains(out, "GET") || !strings.Contains(out, "/api/users") {
		t.Errorf("output missing expected lines:\n%s", out)
	}
	if !strings.Contains(out, "users.store") {
		t.Errorf("output missing route name:\n%s", out)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
