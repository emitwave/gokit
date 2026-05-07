package session

import (
	"context"
	"sync"
	"time"
)

// MemoryStore is an in-process session store. Suitable for dev and
// single-instance production deployments. For multi-instance setups,
// replace with a Redis or DB-backed Store implementation.
//
// The store auto-expires entries on Find (lazy) and runs a periodic
// sweeper to bound memory in case some sessions are written but never
// read again.
type MemoryStore struct {
	mu       sync.RWMutex
	sessions map[string]*memEntry

	stop chan struct{}
}

type memEntry struct {
	// We deep-copy session data on Save so subsequent mutations on the
	// returned Session don't leak back into the store. This matches what
	// a network-backed store would do (serialize on save, deserialize
	// on load).
	id        string
	data      map[string]any
	flash     map[string]any
	createdAt time.Time
	expiresAt time.Time
}

// NewMemoryStore returns a store with a background cleanup goroutine
// that evicts expired entries every 5 minutes. Call Close() during
// shutdown to stop the goroutine.
func NewMemoryStore() *MemoryStore {
	return NewMemoryStoreWithCleanup(5 * time.Minute)
}

// NewMemoryStoreWithCleanup is NewMemoryStore with a custom cleanup
// interval. Useful in tests where you want faster eviction.
func NewMemoryStoreWithCleanup(interval time.Duration) *MemoryStore {
	s := &MemoryStore{
		sessions: make(map[string]*memEntry),
		stop:     make(chan struct{}),
	}
	go s.cleanupLoop(interval)
	return s
}

// Close stops the cleanup goroutine. Idempotent.
func (s *MemoryStore) Close() {
	select {
	case <-s.stop:
		// already closed
	default:
		close(s.stop)
	}
}

// Find returns a session by ID, or ErrSessionNotFound if it doesn't exist
// or has expired. Expired entries are evicted lazily here so callers
// don't need a separate "is expired?" check.
func (s *MemoryStore) Find(_ context.Context, id string) (*Session, error) {
	s.mu.RLock()
	e, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrSessionNotFound
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
		return nil, ErrSessionNotFound
	}

	// Reconstruct a fresh Session — copies of data and flash so caller
	// mutations don't bleed back.
	return &Session{
		id:        e.id,
		data:      copyMap(e.data),
		flash:     copyMap(e.flash),
		createdAt: e.createdAt,
		expiresAt: e.expiresAt,
	}, nil
}

// Save upserts. Copies maps so external mutation doesn't corrupt store
// state — same contract as a network-backed store would have.
func (s *MemoryStore) Save(_ context.Context, sess *Session) error {
	e := &memEntry{
		id:        sess.id,
		data:      copyMap(sess.data),
		flash:     copyMap(sess.flash),
		createdAt: sess.createdAt,
		expiresAt: sess.expiresAt,
	}

	s.mu.Lock()
	s.sessions[e.id] = e
	s.mu.Unlock()
	return nil
}

// Delete is idempotent — removing an unknown ID is not an error.
func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
	return nil
}

// Len reports the number of stored sessions. Mostly useful in tests.
func (s *MemoryStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// cleanupLoop periodically evicts expired entries. We sweep with the
// store lock held briefly per pass — fine for the cardinalities most
// apps see (millions of sessions would warrant a different store).
func (s *MemoryStore) cleanupLoop(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-t.C:
			s.evictExpired(now)
		}
	}
}

func (s *MemoryStore) evictExpired(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range s.sessions {
		if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
			delete(s.sessions, id)
		}
	}
}

func copyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
