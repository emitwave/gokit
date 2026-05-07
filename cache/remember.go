package cache

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Generic typed wrappers around the byte-oriented Store interface.
//
// Encoding defaults to JSON because:
//   - It works for any JSON-serializable value (most application data).
//   - The bytes are debuggable in cache GUIs / Redis CLI / log dumps.
//   - It's stable across Go versions (gob isn't).
//
// To use a different codec — protobuf, msgpack, custom binary — call
// the byte-oriented Store methods directly with your own encoding.

// Get retrieves and JSON-decodes the value at key. ErrNotFound is
// returned for cache misses; decode errors come back as JSON parse
// errors wrapped for caller inspection.
//
//	user, err := cache.Get[*User](ctx, store, "user:42")
//	if errors.Is(err, cache.ErrNotFound) { ... }
func Get[T any](ctx context.Context, s Store, key string) (T, error) {
	var zero T
	raw, err := s.Get(ctx, key)
	if err != nil {
		return zero, err
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, err
	}
	return out, nil
}

// Put JSON-encodes value and stores it with the given TTL.
func Put[T any](ctx context.Context, s Store, key string, value T, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.Put(ctx, key, raw, ttl)
}

// Add stores value only if the key isn't already set. Returns true
// if the value was stored, false if a (non-expired) entry already
// existed.
func Add[T any](ctx context.Context, s Store, key string, value T, ttl time.Duration) (bool, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	return s.Add(ctx, key, raw, ttl)
}

// Pull is Get + Forget atomically.
func Pull[T any](ctx context.Context, s Store, key string) (T, error) {
	var zero T
	raw, err := s.Pull(ctx, key)
	if err != nil {
		return zero, err
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, err
	}
	return out, nil
}

// Remember is the canonical "get or compute" pattern: returns the
// cached value if present, otherwise calls fn, caches its result with
// the given TTL, and returns it. The cache miss path is the only path
// that calls fn — concurrent Remember calls for the same key may all
// invoke fn before any of them complete the Put. If you need at-most-
// once execution across instances, wrap fn in a Locker (see lock.go).
//
//	user, err := cache.Remember(ctx, store, "user:42", time.Hour,
//	    func(ctx context.Context) (*User, error) {
//	        return userRepo.FindByID(ctx, 42)
//	    })
//
// fn errors are returned as-is and NOT cached — failures shouldn't
// poison the cache.
func Remember[T any](ctx context.Context, s Store, key string, ttl time.Duration, fn func(context.Context) (T, error)) (T, error) {
	if v, err := Get[T](ctx, s, key); err == nil {
		return v, nil
	} else if !errors.Is(err, ErrNotFound) {
		// Decode failure on a present-but-corrupt entry — log + recompute.
		// We don't surface the error because the caller asked for fresh
		// data via fn; a corrupted cache shouldn't be visible to them.
		// (Backend errors other than ErrNotFound are also treated as miss
		// here for the same reason — Remember's job is to return data.)
		_ = err
	}

	v, err := fn(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	if putErr := Put(ctx, s, key, v, ttl); putErr != nil {
		// Stash failed but we already have the value — return it. The
		// next call will recompute. We don't log here because we don't
		// have a logger; users who want this signal should wrap Remember.
		_ = putErr
	}
	return v, nil
}

// RememberForever is Remember with no TTL. Useful for values that
// are derived from immutable inputs — once cached, never invalidated
// (until something explicitly Forgets the key).
func RememberForever[T any](ctx context.Context, s Store, key string, fn func(context.Context) (T, error)) (T, error) {
	return Remember(ctx, s, key, 0, fn)
}
