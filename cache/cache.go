// Package cache provides a backend-agnostic key/value cache abstraction
// modeled loosely on Laravel's Cache facade. The shape:
//
//   - A Store interface describing the common operations
//   - MemoryStore — in-process with TTL, suitable for dev / single-instance
//   - Manager — registers multiple named stores with a default
//   - Generic typed wrappers (Get[T], Put[T], Remember[T], Pull[T])
//   - Lock / Locker — distributed lock primitive (MemoryLocker included)
//
// To plug in Redis, Memcached, Valkey, or another remote cache,
// implement the Store interface against your client SDK. The interface
// is small enough that doing so is a few hundred lines.
//
// Wire-up:
//
//	mem := cache.NewMemoryStore()
//	mgr := cache.NewManager("memory", mem)
//	mgr.Register("users", cache.NewMemoryStore())
//
//	// Use a specific store
//	user, err := cache.Remember(ctx, mgr.Store("users"), "user:42", time.Hour,
//	    func(ctx context.Context) (*User, error) {
//	        return userRepo.FindByID(ctx, 42)
//	    })
//
// Values are stored as []byte (the lingua franca for cache backends).
// Use the generic helpers — Get[T], Put[T], etc. — to get type-safe
// access with JSON encoding by default. Bring-your-own encoding by
// wrapping the Store yourself.
package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Store is the core key/value cache contract. All operations take a
// context for cancellation; backends that don't naturally honor ctx
// (in-memory ops on small values) should still accept and ignore it
// for API consistency.
//
// Implementations must be safe for concurrent use across goroutines.
type Store interface {
	// Get returns the bytes stored under key, or ErrNotFound if the key
	// is missing or expired. A non-existent key is NOT an error path
	// callers should fight — it's the normal cache-miss flow:
	//
	//	val, err := s.Get(ctx, "user:42")
	//	if errors.Is(err, cache.ErrNotFound) { ... } // fetch + Put
	Get(ctx context.Context, key string) ([]byte, error)

	// Put writes value under key with the given TTL. ttl=0 means no
	// expiry (Laravel's "forever"). Replaces any existing value.
	Put(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// Add atomically writes only if key does not already exist. Returns
	// (true, nil) on success, (false, nil) if the key is already set.
	// Used for distributed coordination: the first caller "wins."
	Add(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)

	// Pull atomically reads and deletes a key. Returns ErrNotFound for
	// a missing key — same semantics as Get.
	Pull(ctx context.Context, key string) ([]byte, error)

	// Has reports whether key is present (and unexpired). Cheaper than
	// Get when you only need existence.
	Has(ctx context.Context, key string) (bool, error)

	// Forget removes a key. Idempotent — deleting a non-existent key
	// must not error.
	Forget(ctx context.Context, key string) error

	// Flush removes ALL keys from the store. Use sparingly in shared
	// stores (typical: tests, "clear cache" admin actions).
	Flush(ctx context.Context) error

	// Increment atomically adds delta to the int64 counter at key,
	// creating it (initialized to delta) if it doesn't exist. Returns
	// the new value. Errors on a key whose value isn't a valid integer.
	Increment(ctx context.Context, key string, delta int64) (int64, error)

	// Decrement is Increment with a negated delta. Provided as its own
	// method so backends can use native primitives (Redis DECR) where
	// applicable. delta should be positive — pass 5 for "decrement by 5,"
	// not -5 (that would double-negate).
	Decrement(ctx context.Context, key string, delta int64) (int64, error)
}

// ErrNotFound is returned by Get and Pull for missing or expired keys.
var ErrNotFound = errors.New("cache: not found")

// ErrInvalidType is returned by Increment / Decrement when the existing
// value at the key isn't a valid integer counter.
var ErrInvalidType = errors.New("cache: value is not an integer")

// ---------- Manager -----------------------------------------------------

// Manager is a registry of named Stores with a designated default.
// Useful for apps that segregate caches by purpose — short-lived auth
// tokens vs. long-lived computed views — across one or more backends.
//
// Manager is NOT a Store itself — call Store(name) or Default() to get
// a Store for actual operations. Same pattern as filesystem.Manager.
type Manager struct {
	mu          sync.RWMutex
	stores      map[string]Store
	defaultName string
}

// NewManager constructs a Manager with the given default store
// registered under defaultName. Panics if store is nil or defaultName
// is empty — wiring errors better caught at startup than during a
// request.
func NewManager(defaultName string, store Store) *Manager {
	if store == nil {
		panic("cache: NewManager requires a non-nil default store")
	}
	if defaultName == "" {
		panic("cache: NewManager requires a defaultName")
	}
	m := &Manager{
		stores:      map[string]Store{defaultName: store},
		defaultName: defaultName,
	}
	return m
}

// Register adds a named store. Re-registering replaces the prior
// binding (handy for swapping a real store for a fake in tests).
func (m *Manager) Register(name string, store Store) {
	if store == nil {
		panic(fmt.Sprintf("cache: cannot register nil store under %q", name))
	}
	m.mu.Lock()
	m.stores[name] = store
	m.mu.Unlock()
}

// Store returns the store registered under name. Panics if no such
// store exists — programming error, fail loud.
func (m *Manager) Store(name string) Store {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.stores[name]
	if !ok {
		panic(fmt.Sprintf("cache: no store registered under %q", name))
	}
	return s
}

// Default returns the default store.
func (m *Manager) Default() Store { return m.Store(m.DefaultName()) }

// DefaultName returns the default store's registered name.
func (m *Manager) DefaultName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaultName
}

// SetDefault changes the default store. Panics if name isn't registered.
func (m *Manager) SetDefault(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.stores[name]; !ok {
		panic(fmt.Sprintf("cache: cannot set default to unregistered store %q", name))
	}
	m.defaultName = name
}

// Names returns all registered store names.
func (m *Manager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.stores))
	for n := range m.stores {
		out = append(out, n)
	}
	return out
}
