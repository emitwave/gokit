package cache

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// MemoryStore is an in-process Store. Suitable for dev, single-instance
// services, and tests. Lazy expiry on read + periodic background sweep
// keeps memory bounded.
//
// Safe for concurrent use. NOT durable — contents vanish when the
// process exits.
type MemoryStore struct {
	mu sync.RWMutex
	m  map[string]*memEntry

	// Background sweeper. Cleanup interval is configurable via
	// NewMemoryStoreWithCleanup; default 5 min works for most workloads.
	stop      chan struct{}
	stopOnce  sync.Once
	closed    bool
	closedMu  sync.Mutex
}

type memEntry struct {
	value     []byte
	expiresAt time.Time // zero = no expiry
}

// expired reports whether e is past its TTL. Zero ExpiresAt means
// no-expiry, which is never "expired."
func (e *memEntry) expired(now time.Time) bool {
	return !e.expiresAt.IsZero() && now.After(e.expiresAt)
}

// NewMemoryStore returns an empty store with the default 5-minute
// cleanup interval. Call Close() at shutdown to stop the sweeper.
func NewMemoryStore() *MemoryStore {
	return NewMemoryStoreWithCleanup(5 * time.Minute)
}

// NewMemoryStoreWithCleanup is NewMemoryStore with a custom cleanup
// interval. Useful for tests where you want faster eviction.
func NewMemoryStoreWithCleanup(interval time.Duration) *MemoryStore {
	s := &MemoryStore{
		m:    make(map[string]*memEntry),
		stop: make(chan struct{}),
	}
	if interval > 0 {
		go s.sweep(interval)
	}
	return s
}

// Close stops the background sweeper. Idempotent — safe to call
// multiple times.
func (s *MemoryStore) Close() {
	s.stopOnce.Do(func() {
		s.closedMu.Lock()
		s.closed = true
		s.closedMu.Unlock()
		close(s.stop)
	})
}

// sweep evicts expired entries periodically. We hold the write lock
// only briefly per pass — fine for the cardinalities most apps see.
// For workloads with millions of keys, a sharded store or a different
// backend is the answer.
func (s *MemoryStore) sweep(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-t.C:
			s.mu.Lock()
			for k, e := range s.m {
				if e.expired(now) {
					delete(s.m, k)
				}
			}
			s.mu.Unlock()
		}
	}
}

// ---------- Store interface ---------------------------------------------

// Get returns a copy of the stored bytes. Returns ErrNotFound for
// missing OR expired keys; the latter is also evicted as a side effect
// (lazy GC).
func (s *MemoryStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	e, ok := s.m[key]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	if e.expired(time.Now()) {
		// Evict expired entry. Take the write lock briefly.
		s.mu.Lock()
		// Re-check under write lock — another goroutine may have
		// already replaced or evicted the entry.
		if cur, still := s.m[key]; still && cur == e {
			delete(s.m, key)
		}
		s.mu.Unlock()
		return nil, ErrNotFound
	}
	// Return a copy so callers can't mutate our backing storage.
	out := make([]byte, len(e.value))
	copy(out, e.value)
	return out, nil
}

// Put replaces or creates an entry.
func (s *MemoryStore) Put(_ context.Context, key string, value []byte, ttl time.Duration) error {
	cp := make([]byte, len(value))
	copy(cp, value)

	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	s.mu.Lock()
	s.m[key] = &memEntry{value: cp, expiresAt: exp}
	s.mu.Unlock()
	return nil
}

// Add succeeds (returns true) only if the key wasn't already set.
// Treats expired entries as "not set" — they're replaced by the new
// value. Atomic with respect to other Add / Put calls.
func (s *MemoryStore) Add(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	cp := make([]byte, len(value))
	copy(cp, value)

	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.m[key]; ok && !e.expired(time.Now()) {
		return false, nil
	}
	s.m[key] = &memEntry{value: cp, expiresAt: exp}
	return true, nil
}

// Pull atomically reads-and-deletes a key.
func (s *MemoryStore) Pull(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[key]
	if !ok || e.expired(time.Now()) {
		if ok {
			delete(s.m, key)
		}
		return nil, ErrNotFound
	}
	out := make([]byte, len(e.value))
	copy(out, e.value)
	delete(s.m, key)
	return out, nil
}

// Has reports presence. Lazy-evicts expired entries.
func (s *MemoryStore) Has(_ context.Context, key string) (bool, error) {
	s.mu.RLock()
	e, ok := s.m[key]
	s.mu.RUnlock()
	if !ok {
		return false, nil
	}
	if e.expired(time.Now()) {
		s.mu.Lock()
		if cur, still := s.m[key]; still && cur == e {
			delete(s.m, key)
		}
		s.mu.Unlock()
		return false, nil
	}
	return true, nil
}

func (s *MemoryStore) Forget(_ context.Context, key string) error {
	s.mu.Lock()
	delete(s.m, key)
	s.mu.Unlock()
	return nil
}

func (s *MemoryStore) Flush(_ context.Context) error {
	s.mu.Lock()
	s.m = make(map[string]*memEntry)
	s.mu.Unlock()
	return nil
}

// Increment adds delta to the int64 counter at key. Creates the key
// (initialized to delta) if it doesn't exist. Errors if the existing
// value isn't a parseable integer.
func (s *MemoryStore) Increment(_ context.Context, key string, delta int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	e, ok := s.m[key]
	if !ok || e.expired(now) {
		// Create a counter starting at delta. No expiry — counters
		// are typically long-lived. If you need TTL on a counter, Put
		// it first then Increment.
		val := strconv.FormatInt(delta, 10)
		s.m[key] = &memEntry{value: []byte(val)}
		return delta, nil
	}
	cur, err := strconv.ParseInt(string(e.value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w (key %q has value %q)", ErrInvalidType, key, string(e.value))
	}
	cur += delta
	e.value = []byte(strconv.FormatInt(cur, 10))
	return cur, nil
}

// Decrement is Increment(-delta). delta should be positive; passing 5
// means "subtract 5."
func (s *MemoryStore) Decrement(ctx context.Context, key string, delta int64) (int64, error) {
	return s.Increment(ctx, key, -delta)
}

// Len reports the entry count. Includes not-yet-swept-but-expired
// entries. Mostly useful in tests.
func (s *MemoryStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.m)
}

// Compile-time assertion.
var _ Store = (*MemoryStore)(nil)
