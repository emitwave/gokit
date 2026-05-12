// Package session provides cookie-based HTTP session management with a
// pluggable server-side store. Sessions hold arbitrary key/value data,
// support flash messages (one-shot data that survives exactly one
// request), and rotate IDs on demand for post-login fixation defense.
//
// Two pieces compose:
//
//   - A Store backend (memory included; swap for Redis/SQL in production)
//   - A Manager that owns cookie config and exposes Middleware()
//
// Wire-up:
//
//	mgr := session.NewManager(session.Config{
//	    Store:      session.NewMemoryStore(),
//	    CookieName: "emitwave_session",
//	    Lifetime:   24 * time.Hour,
//	    Secure:     true,
//	    HTTPOnly:   true,
//	    SameSite:   http.SameSiteLaxMode,
//	})
//
//	r := router.New()
//	r.Use(mgr.Middleware)
//
// In handlers:
//
//	s := session.From(r.Context())
//	s.Set("user_id", u.ID)
//	s.Flash("status", "Welcome back!")
//	s.Regenerate(r.Context())  // after login, before issuing a new ID
//
// The middleware loads the session at the top of the request, attaches it
// to the context, and saves it on the way out if the session was modified.
//
// Concurrency note: a Session is request-scoped — created at the top of
// the request, read/written by the handler, persisted on the way out. It
// is NOT safe for concurrent access across goroutines within the same
// request. If your handler spawns goroutines that need session data,
// snapshot the values you need into local variables before fanning out.
package session

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"
)

// Session is the per-user state container. Mutate via Get/Set/Delete;
// the manager's middleware persists it after the request completes.
type Session struct {
	id             string
	data           map[string]any
	flash          map[string]any // values popped on read
	createdAt      time.Time
	expiresAt      time.Time
	customLifetime time.Duration

	dirty bool // set when modified; the middleware uses this to decide whether to save

	// regenerate is set by Regenerate() — the middleware uses it to delete
	// the old store entry and assign a fresh ID before saving.
	regenerate bool
	oldID      string

	// destroy is set by Destroy() — middleware deletes from store and
	// clears the cookie on the response.
	destroy bool
}

// ID returns the session's identifier. Empty for unsaved sessions.
func (s *Session) ID() string { return s.id }

// Get returns a value from session data. Returns nil if the key is unset.
// Use type assertion on the result.
func (s *Session) Get(key string) any { return s.data[key] }

// Has reports whether a key exists in session data.
func (s *Session) Has(key string) bool {
	_, ok := s.data[key]
	return ok
}

// Set stores a value. Marks the session dirty so the middleware persists
// it after the request.
func (s *Session) Set(key string, value any) {
	if s.data == nil {
		s.data = make(map[string]any)
	}
	s.data[key] = value
	s.dirty = true
}

// Delete removes a key. No-op if the key wasn't present.
func (s *Session) Delete(key string) {
	if _, ok := s.data[key]; ok {
		delete(s.data, key)
		s.dirty = true
	}
}

// Clear empties the session's data (but keeps the ID and flash).
func (s *Session) Clear() {
	s.data = make(map[string]any)
	s.dirty = true
}

// Flash writes a one-shot value: it's available via GetFlash on this OR
// the next request, and is removed once read. Common uses are status
// messages (form submissions, auth events) that should survive a redirect
// but not persist beyond it.
func (s *Session) Flash(key string, value any) {
	if s.flash == nil {
		s.flash = make(map[string]any)
	}
	s.flash[key] = value
	s.dirty = true
}

// GetFlash reads and removes a flash value. Returns nil if the key wasn't
// flashed.
func (s *Session) GetFlash(key string) any {
	v, ok := s.flash[key]
	if !ok {
		return nil
	}
	delete(s.flash, key)
	s.dirty = true
	return v
}

// Regenerate marks the session for ID rotation. The old ID is deleted
// from the store and a new one is assigned, preserving the data. Call
// this right after authentication to defeat session-fixation attacks.
//
// The ctx parameter is reserved for future store-backed implementations
// that need a context for the delete operation; the default behavior
// just defers to the manager's persist step.
func (s *Session) Regenerate(_ context.Context) {
	s.regenerate = true
	s.oldID = s.id
	s.dirty = true
}

// Destroy marks the session for deletion. After the request completes,
// the middleware removes it from the store and clears the cookie. Use
// this on logout.
func (s *Session) Destroy() {
	s.destroy = true
	s.dirty = true
}

// IsDirty reports whether the session has unsaved changes. The manager's
// middleware checks this to decide whether to call Store.Save.
func (s *Session) IsDirty() bool { return s.dirty }

// ExpiresAt returns the session's expiry time.
func (s *Session) ExpiresAt() time.Time { return s.expiresAt }

// CreatedAt returns the session's creation time.
func (s *Session) CreatedAt() time.Time { return s.createdAt }

// SetLifetime overrides the global session lifetime for this session.
// The custom lifetime is stored in session data so it survives serialization.
func (s *Session) SetLifetime(d time.Duration) {
	s.customLifetime = d
	if s.data == nil {
		s.data = make(map[string]any)
	}
	s.data["_session_lifetime"] = int64(d)
	s.dirty = true
}

// Lifetime returns the per-session lifetime override, or 0 if none is set.
func (s *Session) Lifetime() time.Duration {
	if s.customLifetime > 0 {
		return s.customLifetime
	}
	if v, ok := s.data["_session_lifetime"]; ok {
		switch n := v.(type) {
		case int64:
			return time.Duration(n)
		case float64:
			return time.Duration(int64(n))
		}
	}
	return 0
}

// Data returns a copy of the session's data map. Useful for serialization
// in external Store implementations.
func (s *Session) Data() map[string]any {
	if len(s.data) == 0 {
		return nil
	}
	out := make(map[string]any, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	return out
}

// FlashData returns a copy of the session's flash data map. Useful for
// serialization in external Store implementations.
func (s *Session) FlashData() map[string]any {
	if len(s.flash) == 0 {
		return nil
	}
	out := make(map[string]any, len(s.flash))
	for k, v := range s.flash {
		out[k] = v
	}
	return out
}

// Restore reconstructs a Session from serialized components. This is the
// entry point for external Store implementations (Redis, SQL, etc.) that
// need to rebuild a Session from stored data. The returned session is not
// dirty — it represents persisted state.
func Restore(id string, data, flash map[string]any, createdAt, expiresAt time.Time) *Session {
	return &Session{
		id:        id,
		data:      data,
		flash:     flash,
		createdAt: createdAt,
		expiresAt: expiresAt,
	}
}

// ---------- Store interface ---------------------------------------------

// Store is the persistence backend for sessions. The MemoryStore (in this
// package) is fine for dev and single-instance deployments; for clustered
// production setups, implement Store against Redis, Memcached, or your DB.
//
// Implementations must be safe for concurrent use across requests.
type Store interface {
	// Find looks up a session by ID. Returns ErrSessionNotFound if there
	// is no matching session, or if the matching session has expired.
	// Other errors (network, etc.) should be returned as-is.
	Find(ctx context.Context, id string) (*Session, error)

	// Save persists the session. Implementations should treat this as
	// upsert — same call for create and update.
	Save(ctx context.Context, s *Session) error

	// Delete removes a session by ID. Idempotent — deleting a non-existent
	// session must not error.
	Delete(ctx context.Context, id string) error
}

// ErrSessionNotFound is returned by Store.Find when the ID has no entry
// or the entry has expired. The manager treats this as "issue a new
// session" rather than as a hard error.
var ErrSessionNotFound = errors.New("session: not found")

// generateID produces a 256-bit random session ID, base64url-encoded.
// 256 bits is more than enough entropy to make collisions and brute
// force attacks negligible.
func generateID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
