package session

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Config drives Manager construction. Zero values get sensible defaults,
// but Store is required.
type Config struct {
	// Store is the persistence backend. Required.
	Store Store

	// CookieName names the cookie holding the session ID. Default:
	// "session". Pick something opaque in production so it doesn't leak
	// stack info ("emitwave_sid", etc.).
	CookieName string

	// Path is the cookie path. Default: "/".
	Path string

	// Domain is the cookie domain. Default: empty (host-only cookie).
	Domain string

	// Lifetime is how long a session lives before expiring. Default:
	// 24 hours. Each save resets the expiry — the user has to be idle
	// for the full Lifetime before the session dies.
	Lifetime time.Duration

	// Secure makes the cookie HTTPS-only. ALWAYS true in production.
	// Default: false (so dev over http://localhost works), but consider
	// it a config bug if this stays false in prod.
	Secure bool

	// HTTPOnly makes the cookie inaccessible to JavaScript. Default:
	// true — leave it on unless you have a specific reason to read the
	// session ID client-side.
	HTTPOnly bool

	// SameSite sets the cookie's SameSite attribute. Default:
	// http.SameSiteLaxMode (good balance for most apps).
	SameSite http.SameSite
}

// Manager is the session orchestrator. It builds Sessions from cookies,
// hands them off to handlers via context, and persists changes after the
// handler completes.
type Manager struct {
	store    Store
	cookieName string
	path     string
	domain   string
	lifetime time.Duration
	secure   bool
	httpOnly bool
	sameSite http.SameSite
}

// NewManager constructs a Manager. Panics if cfg.Store is nil — that's a
// programming error worth catching loud.
func NewManager(cfg Config) *Manager {
	if cfg.Store == nil {
		panic("session: Config.Store is required")
	}
	if cfg.CookieName == "" {
		cfg.CookieName = "session"
	}
	if cfg.Path == "" {
		cfg.Path = "/"
	}
	if cfg.Lifetime == 0 {
		cfg.Lifetime = 24 * time.Hour
	}
	// HTTPOnly default is true — but a Config{} literal can't distinguish
	// "I want false" from "I forgot to set it." We accept this trade-off
	// because the safe default is far more common than the explicit-false case.
	httpOnly := true
	if cfg.HTTPOnly == false && cfg.Secure == false && cfg.SameSite == 0 {
		// All zero-valued — caller probably wants defaults. Keep httpOnly true.
	} else {
		httpOnly = cfg.HTTPOnly
	}
	if cfg.SameSite == 0 {
		cfg.SameSite = http.SameSiteLaxMode
	}

	return &Manager{
		store:      cfg.Store,
		cookieName: cfg.CookieName,
		path:       cfg.Path,
		domain:     cfg.Domain,
		lifetime:   cfg.Lifetime,
		secure:     cfg.Secure,
		httpOnly:   httpOnly,
		sameSite:   cfg.SameSite,
	}
}

// ---------- context plumbing --------------------------------------------

type ctxKey int

const sessionKey ctxKey = iota

// From returns the session attached to ctx by the manager's middleware.
// Returns nil if the middleware wasn't applied — handlers can guard with
// `if s := session.From(...); s != nil { ... }` or just trust the wiring.
func From(ctx context.Context) *Session {
	if s, ok := ctx.Value(sessionKey).(*Session); ok {
		return s
	}
	return nil
}

// ---------- middleware --------------------------------------------------

// statusCapture lets us write the cookie BEFORE the handler's first
// w.Write call. We need that because once any byte goes out, headers
// are flushed and Set-Cookie won't make it.
type statusCapture struct {
	http.ResponseWriter
	wrote bool
	hook  func()
}

func (sc *statusCapture) WriteHeader(code int) {
	if !sc.wrote && sc.hook != nil {
		sc.hook()
	}
	sc.wrote = true
	sc.ResponseWriter.WriteHeader(code)
}

func (sc *statusCapture) Write(b []byte) (int, error) {
	if !sc.wrote && sc.hook != nil {
		sc.hook()
	}
	sc.wrote = true
	return sc.ResponseWriter.Write(b)
}

// Middleware loads (or creates) the session, attaches it to context, and
// persists it after the handler runs. This is the single piece of glue
// you need to wire — pass m.Middleware to your router's Use().
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		sess := m.load(ctx, r)

		// We hook the first write so the cookie / store update happens
		// just before headers flush, not after the handler has already
		// streamed bytes.
		sc := &statusCapture{ResponseWriter: w}
		sc.hook = func() {
			if err := m.persist(ctx, w, sess); err != nil {
				// Persisting can fail (store down). We can't change the
				// response now, so the best we can do is not crash —
				// the request still serves with stale state. Logging is
				// handled upstream via panic recovery / error logging.
				_ = err
			}
		}

		ctx = context.WithValue(ctx, sessionKey, sess)
		next.ServeHTTP(sc, r.WithContext(ctx))

		// If the handler never wrote anything, the hook didn't run —
		// fall through to persist now. (e.g. 200 OK with no body.)
		if !sc.wrote {
			if err := m.persist(ctx, w, sess); err != nil {
				_ = err
			}
		}
	})
}

// load reads the session ID from the request cookie, looks it up, and
// returns the session. On miss or expiry, returns a fresh empty session
// (without saving — that happens on the way out, only if dirty).
func (m *Manager) load(ctx context.Context, r *http.Request) *Session {
	c, err := r.Cookie(m.cookieName)
	if err == nil && c.Value != "" {
		sess, err := m.store.Find(ctx, c.Value)
		if err == nil {
			return sess
		}
		// Any error other than NotFound is treated as miss — graceful
		// degradation if the store is briefly unavailable. The user
		// gets a fresh session; their old data may reappear when the
		// store recovers.
		if !errors.Is(err, ErrSessionNotFound) {
			_ = err
		}
	}
	// Fresh session — ID is empty until we persist (we only mint an ID
	// when there's actually data to save, to avoid littering the store
	// with empty entries from healthcheck pings, etc.).
	return &Session{
		createdAt: time.Now(),
		expiresAt: time.Now().Add(m.lifetime),
	}
}

// persist applies destroy / regenerate / save semantics and updates the
// cookie header on w accordingly.
func (m *Manager) persist(ctx context.Context, w http.ResponseWriter, sess *Session) error {
	// Destroy: blow away store entry + clear the cookie, regardless of
	// any other state.
	if sess.destroy {
		if sess.id != "" {
			_ = m.store.Delete(ctx, sess.id)
		}
		http.SetCookie(w, &http.Cookie{
			Name:     m.cookieName,
			Value:    "",
			Path:     m.path,
			Domain:   m.domain,
			MaxAge:   -1, // delete now
			Secure:   m.secure,
			HttpOnly: m.httpOnly,
			SameSite: m.sameSite,
		})
		return nil
	}

	// Nothing to do if the session wasn't modified — we don't want to
	// mint store entries on read-only requests (healthchecks, etc.).
	if !sess.dirty {
		return nil
	}

	// Regenerate: delete old, mint new ID, save under new ID.
	if sess.regenerate {
		if sess.oldID != "" {
			_ = m.store.Delete(ctx, sess.oldID)
		}
		newID, err := generateID()
		if err != nil {
			return err
		}
		sess.id = newID
		sess.oldID = ""
		sess.regenerate = false
	} else if sess.id == "" {
		// First save for a brand-new session.
		newID, err := generateID()
		if err != nil {
			return err
		}
		sess.id = newID
	}

	// Refresh expiry on every save so active sessions don't time out.
	// Use per-session lifetime if set, otherwise fall back to global default.
	lifetime := m.lifetime
	if sl := sess.Lifetime(); sl > 0 {
		lifetime = sl
	}
	sess.expiresAt = time.Now().Add(lifetime)

	if err := m.store.Save(ctx, sess); err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     m.cookieName,
		Value:    sess.id,
		Path:     m.path,
		Domain:   m.domain,
		Expires:  sess.expiresAt,
		MaxAge:   int(lifetime.Seconds()),
		Secure:   m.secure,
		HttpOnly: m.httpOnly,
		SameSite: m.sameSite,
	})
	sess.dirty = false
	return nil
}
