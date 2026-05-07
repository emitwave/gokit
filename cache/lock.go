package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// Lock is a single acquired lock. Holders MUST Release when done — a
// dropped Lock without Release leaks until its TTL expires (which is
// the safety mechanism for crashed processes, not a substitute for
// proper cleanup).
//
// Implementations are typically obtained via Locker.Lock and used
// once. Don't reuse a Lock instance across acquire / release cycles.
type Lock interface {
	// Acquire attempts to take the lock. Returns (true, nil) on success,
	// (false, nil) if the lock is held by someone else (try again later
	// or give up), and (false, err) for backend errors.
	Acquire(ctx context.Context) (bool, error)

	// Block tries to acquire repeatedly until success or wait elapses.
	// Returns the same shape as Acquire — (false, nil) means "didn't
	// get it within wait."
	Block(ctx context.Context, wait time.Duration) (bool, error)

	// Release relinquishes the lock. Idempotent (releasing an already-
	// released or never-acquired lock is not an error). Releases ONLY
	// the holder's lock — calling Release on a lock you didn't acquire
	// is a no-op rather than stealing someone else's lock.
	Release() error
}

// Locker hands out named Locks. Backends typically implement this
// against the same underlying store as their Cache (Redis SET PX NX),
// but conceptually the two are independent.
type Locker interface {
	// Lock returns a Lock for name with the given TTL. The Lock is not
	// yet acquired — call Acquire or Block on the returned value.
	Lock(ctx context.Context, name string, ttl time.Duration) (Lock, error)
}

// WithLock acquires lock, runs fn, releases the lock, and returns
// fn's error. If acquisition fails, returns ErrLockNotAcquired without
// calling fn. Use this for the common "do work under a lock" pattern:
//
//	err := cache.WithLock(ctx, locker, "send-newsletter", 30*time.Second,
//	    func(ctx context.Context) error {
//	        return mailer.SendNewsletter(ctx)
//	    })
//
// Block-style waiting is provided by WithLockWait.
func WithLock(ctx context.Context, locker Locker, name string, ttl time.Duration, fn func(context.Context) error) error {
	lock, err := locker.Lock(ctx, name, ttl)
	if err != nil {
		return err
	}
	got, err := lock.Acquire(ctx)
	if err != nil {
		return err
	}
	if !got {
		return ErrLockNotAcquired
	}
	defer func() { _ = lock.Release() }()
	return fn(ctx)
}

// WithLockWait is WithLock that blocks up to maxWait for the lock
// before giving up. Same return shape — ErrLockNotAcquired on timeout.
func WithLockWait(ctx context.Context, locker Locker, name string, ttl, maxWait time.Duration, fn func(context.Context) error) error {
	lock, err := locker.Lock(ctx, name, ttl)
	if err != nil {
		return err
	}
	got, err := lock.Block(ctx, maxWait)
	if err != nil {
		return err
	}
	if !got {
		return ErrLockNotAcquired
	}
	defer func() { _ = lock.Release() }()
	return fn(ctx)
}

// ErrLockNotAcquired is returned by WithLock and WithLockWait when the
// lock was held by someone else (or the wait timed out, for the latter).
var ErrLockNotAcquired = errors.New("cache: could not acquire lock")

// ---------- MemoryLocker ------------------------------------------------

// MemoryLocker is an in-process Locker. It uses a mutex over a map of
// active locks; auto-expires entries on a short ticker so crashed
// holders don't deadlock forever.
//
// For multi-instance deployments, use a distributed Locker — typically
// a Redis-based one using SET key value NX PX ttl. The Lock interface
// is the same, so callers don't change.
type MemoryLocker struct {
	mu    sync.Mutex
	locks map[string]*memLock // keyed by name
}

type memLock struct {
	owner     string
	expiresAt time.Time
}

// NewMemoryLocker returns an empty in-process locker.
func NewMemoryLocker() *MemoryLocker {
	return &MemoryLocker{locks: make(map[string]*memLock)}
}

// Lock returns a Lock for name. The returned Lock holds a reference to
// the locker so Acquire / Release can mutate the shared map.
func (m *MemoryLocker) Lock(_ context.Context, name string, ttl time.Duration) (Lock, error) {
	owner, err := newOwnerToken()
	if err != nil {
		return nil, err
	}
	return &memoryLock{
		locker: m,
		name:   name,
		ttl:    ttl,
		owner:  owner,
	}, nil
}

// memoryLock is the per-acquisition Lock instance. owner is a unique
// token so Release only releases OUR lock — not someone else's that
// happens to have the same name (e.g. our TTL expired and another
// process took over before we noticed).
type memoryLock struct {
	locker *MemoryLocker
	name   string
	ttl    time.Duration
	owner  string

	mu       sync.Mutex
	acquired bool
}

func (l *memoryLock) Acquire(_ context.Context) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.acquired {
		// Already held by us — successful no-op (idempotent).
		return true, nil
	}
	l.locker.mu.Lock()
	defer l.locker.mu.Unlock()
	now := time.Now()
	if existing, ok := l.locker.locks[l.name]; ok && now.Before(existing.expiresAt) {
		return false, nil
	}
	exp := now.Add(l.ttl)
	l.locker.locks[l.name] = &memLock{owner: l.owner, expiresAt: exp}
	l.acquired = true
	return true, nil
}

func (l *memoryLock) Block(ctx context.Context, wait time.Duration) (bool, error) {
	deadline := time.Now().Add(wait)
	// Polling interval scales: short wait → fast poll; long wait →
	// slower poll. Hard-coded for simplicity; users with very different
	// needs can implement their own Locker.
	interval := 25 * time.Millisecond
	if wait > 5*time.Second {
		interval = 100 * time.Millisecond
	}

	for {
		got, err := l.Acquire(ctx)
		if err != nil || got {
			return got, err
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(interval):
			// retry
		}
	}
}

func (l *memoryLock) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.acquired {
		return nil // idempotent
	}
	l.locker.mu.Lock()
	defer l.locker.mu.Unlock()
	// Only release our own lock (defense against TTL-expired-then-stolen
	// scenarios). If the current entry's owner is someone else, we
	// silently no-op — releasing wasn't ours to do.
	if existing, ok := l.locker.locks[l.name]; ok && existing.owner == l.owner {
		delete(l.locker.locks, l.name)
	}
	l.acquired = false
	return nil
}

// newOwnerToken generates a 96-bit random hex token. Plenty of
// uniqueness for any single locker instance.
func newOwnerToken() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Compile-time assertions.
var _ Locker = (*MemoryLocker)(nil)
var _ Lock = (*memoryLock)(nil)
