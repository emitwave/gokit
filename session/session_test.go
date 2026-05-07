package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager(Config{
		Store:    NewMemoryStoreWithCleanup(time.Hour),
		Lifetime: time.Hour,
	})
}

// roundtrip simulates a request, captures the Set-Cookie response, and
// returns the session cookie's value (so the next call can send it back).
func roundtrip(t *testing.T, mgr *Manager, handler http.HandlerFunc, sessionCookie string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	if sessionCookie != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: sessionCookie})
	}
	rec := httptest.NewRecorder()
	mgr.Middleware(handler).ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			return c.Value
		}
	}
	return ""
}

func TestSessionPersistsAcrossRequests(t *testing.T) {
	mgr := newTestManager(t)

	cookie := roundtrip(t, mgr, func(w http.ResponseWriter, r *http.Request) {
		s := From(r.Context())
		s.Set("user_id", "u-42")
		w.WriteHeader(200)
	}, "")

	if cookie == "" {
		t.Fatal("expected session cookie to be set")
	}

	var got any
	roundtrip(t, mgr, func(w http.ResponseWriter, r *http.Request) {
		got = From(r.Context()).Get("user_id")
	}, cookie)

	if got != "u-42" {
		t.Errorf("user_id across requests: got %v", got)
	}
}

func TestSessionNoCookieForReadOnly(t *testing.T) {
	// A request that only reads (and the session is empty / unmodified)
	// should not produce a Set-Cookie. Otherwise every healthcheck spawns
	// a session entry.
	mgr := newTestManager(t)
	cookie := roundtrip(t, mgr, func(w http.ResponseWriter, r *http.Request) {
		_ = From(r.Context()).Get("nope") // read only
	}, "")
	if cookie != "" {
		t.Errorf("read-only request should not mint a session, got cookie %q", cookie)
	}
}

func TestSessionFlashOneShot(t *testing.T) {
	mgr := newTestManager(t)

	// Request 1: flash a value.
	cookie := roundtrip(t, mgr, func(w http.ResponseWriter, r *http.Request) {
		From(r.Context()).Flash("status", "saved!")
	}, "")

	// Request 2: read the flash; it should be present and then removed.
	var v1, v2 any
	cookie = roundtrip(t, mgr, func(w http.ResponseWriter, r *http.Request) {
		s := From(r.Context())
		v1 = s.GetFlash("status")
		v2 = s.GetFlash("status") // second read in same request — should be nil
	}, cookie)

	if v1 != "saved!" {
		t.Errorf("first flash read: got %v, want saved!", v1)
	}
	if v2 != nil {
		t.Errorf("second flash read: got %v, want nil", v2)
	}

	// Request 3: flash should still be gone.
	roundtrip(t, mgr, func(w http.ResponseWriter, r *http.Request) {
		if got := From(r.Context()).GetFlash("status"); got != nil {
			t.Errorf("flash leaked to next request: %v", got)
		}
	}, cookie)
}

func TestSessionRegenerate(t *testing.T) {
	mgr := newTestManager(t)
	store := mgr.store.(*MemoryStore)

	cookie := roundtrip(t, mgr, func(w http.ResponseWriter, r *http.Request) {
		s := From(r.Context())
		s.Set("user_id", "u-1")
	}, "")

	if store.Len() != 1 {
		t.Fatalf("expected 1 session in store, got %d", store.Len())
	}
	originalID := cookie

	newCookie := roundtrip(t, mgr, func(w http.ResponseWriter, r *http.Request) {
		s := From(r.Context())
		s.Regenerate(r.Context())
		s.Set("logged_in", true) // ensure dirty so save runs
	}, cookie)

	if newCookie == "" {
		t.Fatal("expected new cookie after regenerate")
	}
	if newCookie == originalID {
		t.Error("regenerate should produce a new ID")
	}
	if store.Len() != 1 {
		t.Errorf("regenerate should replace, not append: store len=%d", store.Len())
	}

	// Data carried over.
	roundtrip(t, mgr, func(w http.ResponseWriter, r *http.Request) {
		if got := From(r.Context()).Get("user_id"); got != "u-1" {
			t.Errorf("data lost across regenerate: got %v", got)
		}
	}, newCookie)
}

func TestSessionDestroy(t *testing.T) {
	mgr := newTestManager(t)
	store := mgr.store.(*MemoryStore)

	cookie := roundtrip(t, mgr, func(w http.ResponseWriter, r *http.Request) {
		From(r.Context()).Set("k", "v")
	}, "")
	if store.Len() != 1 {
		t.Fatalf("setup failed: store len=%d", store.Len())
	}

	// Destroy should remove from store and clear the client cookie.
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	rec := httptest.NewRecorder()
	mgr.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		From(r.Context()).Destroy()
	})).ServeHTTP(rec, req)

	if store.Len() != 0 {
		t.Errorf("after destroy, store should be empty: len=%d", store.Len())
	}

	// The Set-Cookie should clear (MaxAge<=0 or Expires in the past).
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" && (c.MaxAge < 0 || (!c.Expires.IsZero() && c.Expires.Before(time.Now()))) {
			cleared = true
		}
	}
	if !cleared {
		t.Error("destroy should clear the cookie on the client")
	}
}

func TestSessionExpired(t *testing.T) {
	store := NewMemoryStoreWithCleanup(time.Hour)
	mgr := NewManager(Config{Store: store, Lifetime: 50 * time.Millisecond})

	cookie := roundtrip(t, mgr, func(w http.ResponseWriter, r *http.Request) {
		From(r.Context()).Set("k", "v")
	}, "")

	time.Sleep(80 * time.Millisecond)

	// After expiry, sending the old cookie should yield an empty session.
	roundtrip(t, mgr, func(w http.ResponseWriter, r *http.Request) {
		if got := From(r.Context()).Get("k"); got != nil {
			t.Errorf("expired session still readable: %v", got)
		}
	}, cookie)
}

func TestNewManagerPanicsWithoutStore(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on missing Store")
		}
	}()
	NewManager(Config{})
}

func TestMemoryStoreCopiesData(t *testing.T) {
	// Mutating a session's data after Save must not affect the stored copy.
	store := NewMemoryStoreWithCleanup(time.Hour)
	defer store.Close()

	s := &Session{id: "abc", data: map[string]any{"k": "v"}}
	if err := store.Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	s.data["k"] = "MUTATED"

	loaded, err := store.Find(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.data["k"] != "v" {
		t.Errorf("store leaked external mutation: got %v", loaded.data["k"])
	}
}
