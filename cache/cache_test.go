package cache

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------- Store interface compliance ---------------------------------

// runStoreCompliance runs the full Store contract against any backend.
// MemoryStore is the only built-in; future Redis/Memcached backends
// drop in here too.
type storeFactory func(t *testing.T) Store

func memStoreFactory(t *testing.T) Store {
	t.Helper()
	// Disable the sweeper for deterministic tests — we control eviction
	// via Forget / sleeps where needed.
	return NewMemoryStoreWithCleanup(0)
}

func TestMemoryStore_Compliance(t *testing.T) { runStoreTests(t, memStoreFactory) }

func runStoreTests(t *testing.T, mk storeFactory) {
	ctx := context.Background()

	t.Run("PutAndGet", func(t *testing.T) {
		s := mk(t)
		if err := s.Put(ctx, "k", []byte("v"), time.Hour); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx, "k")
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "v" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("GetMissingErrNotFound", func(t *testing.T) {
		s := mk(t)
		_, err := s.Get(ctx, "missing")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("got %v, want ErrNotFound", err)
		}
	})

	t.Run("Has", func(t *testing.T) {
		s := mk(t)
		if ok, _ := s.Has(ctx, "k"); ok {
			t.Error("missing should be false")
		}
		_ = s.Put(ctx, "k", []byte("v"), time.Hour)
		if ok, _ := s.Has(ctx, "k"); !ok {
			t.Error("present should be true")
		}
	})

	t.Run("Forget", func(t *testing.T) {
		s := mk(t)
		_ = s.Put(ctx, "k", []byte("v"), 0)
		_ = s.Forget(ctx, "k")
		if ok, _ := s.Has(ctx, "k"); ok {
			t.Error("Forget should remove")
		}
		// Idempotent — forgetting a missing key isn't an error.
		if err := s.Forget(ctx, "missing"); err != nil {
			t.Errorf("Forget missing: %v", err)
		}
	})

	t.Run("Pull", func(t *testing.T) {
		s := mk(t)
		_ = s.Put(ctx, "k", []byte("payload"), 0)
		v, err := s.Pull(ctx, "k")
		if err != nil || string(v) != "payload" {
			t.Errorf("Pull: %q %v", v, err)
		}
		if ok, _ := s.Has(ctx, "k"); ok {
			t.Error("Pull should remove")
		}
		_, err = s.Pull(ctx, "k")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Pull missing: %v", err)
		}
	})

	t.Run("Add", func(t *testing.T) {
		s := mk(t)
		ok, _ := s.Add(ctx, "k", []byte("first"), time.Hour)
		if !ok {
			t.Fatal("Add to empty key should succeed")
		}
		ok, _ = s.Add(ctx, "k", []byte("second"), time.Hour)
		if ok {
			t.Error("Add to existing key should return false")
		}
		got, _ := s.Get(ctx, "k")
		if string(got) != "first" {
			t.Errorf("Add should not overwrite: got %q", got)
		}
	})

	t.Run("Flush", func(t *testing.T) {
		s := mk(t)
		_ = s.Put(ctx, "a", []byte("1"), 0)
		_ = s.Put(ctx, "b", []byte("2"), 0)
		_ = s.Flush(ctx)
		if ok, _ := s.Has(ctx, "a"); ok {
			t.Error("Flush should clear all")
		}
		if ok, _ := s.Has(ctx, "b"); ok {
			t.Error("Flush should clear all")
		}
	})

	t.Run("Increment", func(t *testing.T) {
		s := mk(t)
		// Non-existent key starts at 0 + delta.
		v, err := s.Increment(ctx, "counter", 5)
		if err != nil || v != 5 {
			t.Errorf("incr from missing: v=%d err=%v", v, err)
		}
		v, err = s.Increment(ctx, "counter", 3)
		if err != nil || v != 8 {
			t.Errorf("incr: v=%d err=%v", v, err)
		}
		v, err = s.Decrement(ctx, "counter", 3)
		if err != nil || v != 5 {
			t.Errorf("decr: v=%d err=%v", v, err)
		}
	})

	t.Run("IncrementOnNonInteger", func(t *testing.T) {
		s := mk(t)
		_ = s.Put(ctx, "k", []byte("not-a-number"), 0)
		_, err := s.Increment(ctx, "k", 1)
		if !errors.Is(err, ErrInvalidType) {
			t.Errorf("got %v, want ErrInvalidType", err)
		}
	})

	t.Run("TTLExpiry", func(t *testing.T) {
		s := mk(t)
		_ = s.Put(ctx, "k", []byte("v"), 50*time.Millisecond)
		if ok, _ := s.Has(ctx, "k"); !ok {
			t.Error("k should exist immediately")
		}
		time.Sleep(80 * time.Millisecond)
		if ok, _ := s.Has(ctx, "k"); ok {
			t.Error("k should be expired")
		}
		if _, err := s.Get(ctx, "k"); !errors.Is(err, ErrNotFound) {
			t.Errorf("expired Get: %v", err)
		}
	})

	t.Run("ZeroTTLMeansForever", func(t *testing.T) {
		s := mk(t)
		_ = s.Put(ctx, "forever", []byte("eternal"), 0)
		time.Sleep(50 * time.Millisecond)
		got, err := s.Get(ctx, "forever")
		if err != nil || string(got) != "eternal" {
			t.Errorf("ttl=0 should not expire: %q %v", got, err)
		}
	})
}

// ---------- MemoryStore-specific tests ---------------------------------

func TestMemoryStore_GetReturnsCopy(t *testing.T) {
	// Mutating the bytes returned by Get must not mutate the stored value.
	s := NewMemoryStoreWithCleanup(0)
	_ = s.Put(context.Background(), "k", []byte("hello"), 0)
	v, _ := s.Get(context.Background(), "k")
	v[0] = 'H' // mutate
	got, _ := s.Get(context.Background(), "k")
	if string(got) != "hello" {
		t.Errorf("Get leaked external mutation: %q", got)
	}
}

func TestMemoryStore_PutCopiesInput(t *testing.T) {
	// Mutating the bytes passed to Put must not affect stored value.
	s := NewMemoryStoreWithCleanup(0)
	in := []byte("hello")
	_ = s.Put(context.Background(), "k", in, 0)
	in[0] = 'H'
	got, _ := s.Get(context.Background(), "k")
	if string(got) != "hello" {
		t.Errorf("Put leaked external mutation: %q", got)
	}
}

func TestMemoryStore_Close(t *testing.T) {
	s := NewMemoryStore()
	s.Close()
	s.Close() // idempotent
}

// ---------- typed wrappers ---------------------------------------------

type user struct {
	ID    string
	Email string
}

func TestTypedGetPut(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStoreWithCleanup(0)

	u := user{ID: "u-1", Email: "x@y.io"}
	if err := Put(ctx, s, "user", u, 0); err != nil {
		t.Fatal(err)
	}
	got, err := Get[user](ctx, s, "user")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "u-1" || got.Email != "x@y.io" {
		t.Errorf("got %+v", got)
	}
}

func TestTypedGetMissing(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStoreWithCleanup(0)
	_, err := Get[user](ctx, s, "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v", err)
	}
}

func TestTypedPull(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStoreWithCleanup(0)
	_ = Put(ctx, s, "u", user{ID: "u-2"}, 0)
	got, err := Pull[user](ctx, s, "u")
	if err != nil || got.ID != "u-2" {
		t.Errorf("Pull: %+v %v", got, err)
	}
	if ok, _ := s.Has(ctx, "u"); ok {
		t.Error("Pull should delete")
	}
}

func TestTypedAdd(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStoreWithCleanup(0)
	ok, _ := Add(ctx, s, "u", user{ID: "u-1"}, 0)
	if !ok {
		t.Error("Add to empty should succeed")
	}
	ok, _ = Add(ctx, s, "u", user{ID: "u-2"}, 0)
	if ok {
		t.Error("Add to occupied should return false")
	}
	got, _ := Get[user](ctx, s, "u")
	if got.ID != "u-1" {
		t.Errorf("Add should not overwrite, got %+v", got)
	}
}

// ---------- Remember ----------------------------------------------------

func TestRemember(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStoreWithCleanup(0)

	var calls atomic.Int32
	compute := func(_ context.Context) (user, error) {
		calls.Add(1)
		return user{ID: "fresh", Email: "fresh@x"}, nil
	}

	// First call computes
	got, err := Remember(ctx, s, "user", time.Hour, compute)
	if err != nil || got.ID != "fresh" {
		t.Fatalf("first remember: %+v %v", got, err)
	}
	// Second call hits cache
	got, err = Remember(ctx, s, "user", time.Hour, compute)
	if err != nil || got.ID != "fresh" {
		t.Fatalf("second remember: %+v %v", got, err)
	}
	if calls.Load() != 1 {
		t.Errorf("compute should run once, ran %d times", calls.Load())
	}
}

func TestRemember_PropagatesError(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStoreWithCleanup(0)
	myErr := errors.New("compute failed")

	_, err := Remember(ctx, s, "k", time.Hour, func(_ context.Context) (user, error) {
		return user{}, myErr
	})
	if !errors.Is(err, myErr) {
		t.Errorf("got %v, want myErr", err)
	}
	// Failure should not be cached
	if ok, _ := s.Has(ctx, "k"); ok {
		t.Error("error result should not be cached")
	}
}

func TestRemember_RecomputesOnExpiry(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStoreWithCleanup(0)

	var calls atomic.Int32
	compute := func(_ context.Context) (string, error) {
		calls.Add(1)
		return "v", nil
	}

	_, _ = Remember(ctx, s, "k", 50*time.Millisecond, compute)
	time.Sleep(80 * time.Millisecond)
	_, _ = Remember(ctx, s, "k", 50*time.Millisecond, compute)

	if calls.Load() != 2 {
		t.Errorf("expected 2 computes (one per side of expiry), got %d", calls.Load())
	}
}

// ---------- Manager -----------------------------------------------------

func TestManager(t *testing.T) {
	a := NewMemoryStoreWithCleanup(0)
	b := NewMemoryStoreWithCleanup(0)

	mgr := NewManager("primary", a)
	mgr.Register("secondary", b)

	if mgr.Default() != a {
		t.Error("Default should return primary")
	}
	if mgr.Store("secondary") != b {
		t.Error("Store('secondary') mismatch")
	}

	mgr.SetDefault("secondary")
	if mgr.Default() != b {
		t.Error("after SetDefault, default should be b")
	}

	names := mgr.Names()
	sort.Strings(names)
	if len(names) != 2 || names[0] != "primary" || names[1] != "secondary" {
		t.Errorf("Names: %v", names)
	}
}

func TestManagerPanicsOnUnknown(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic")
		}
	}()
	mgr := NewManager("a", NewMemoryStoreWithCleanup(0))
	mgr.Store("missing")
}

// ---------- MemoryLocker -----------------------------------------------

func TestMemoryLocker_AcquireAndRelease(t *testing.T) {
	ctx := context.Background()
	locker := NewMemoryLocker()

	lock1, _ := locker.Lock(ctx, "name", time.Minute)
	got, err := lock1.Acquire(ctx)
	if err != nil || !got {
		t.Fatalf("first acquire: %v %v", got, err)
	}

	// Second lock on same name should not be acquirable.
	lock2, _ := locker.Lock(ctx, "name", time.Minute)
	got, err = lock2.Acquire(ctx)
	if err != nil || got {
		t.Errorf("second acquire: %v %v (want false, nil)", got, err)
	}

	// Release first; second can now acquire.
	_ = lock1.Release()
	got, err = lock2.Acquire(ctx)
	if err != nil || !got {
		t.Errorf("acquire after release: %v %v", got, err)
	}
}

func TestMemoryLocker_TTLExpiry(t *testing.T) {
	ctx := context.Background()
	locker := NewMemoryLocker()

	lock1, _ := locker.Lock(ctx, "name", 50*time.Millisecond)
	got, _ := lock1.Acquire(ctx)
	if !got {
		t.Fatal("first acquire should succeed")
	}

	time.Sleep(80 * time.Millisecond)

	// After TTL, a new acquirer should succeed.
	lock2, _ := locker.Lock(ctx, "name", time.Minute)
	got, _ = lock2.Acquire(ctx)
	if !got {
		t.Error("after TTL expiry, should be acquirable")
	}
}

func TestMemoryLocker_ReleaseDoesNotStealOthersLock(t *testing.T) {
	// If lock1's TTL expires and lock2 takes over, releasing lock1
	// should not affect lock2.
	ctx := context.Background()
	locker := NewMemoryLocker()

	lock1, _ := locker.Lock(ctx, "name", 30*time.Millisecond)
	_, _ = lock1.Acquire(ctx)

	time.Sleep(50 * time.Millisecond)

	lock2, _ := locker.Lock(ctx, "name", time.Minute)
	got, _ := lock2.Acquire(ctx)
	if !got {
		t.Fatal("lock2 should acquire after expiry")
	}

	// lock1 still thinks it holds the lock — Release should be a no-op
	// rather than stealing lock2's hold.
	_ = lock1.Release()

	// lock2 should still effectively hold the lock — a new attempt fails.
	lock3, _ := locker.Lock(ctx, "name", time.Minute)
	got, _ = lock3.Acquire(ctx)
	if got {
		t.Error("lock1.Release should not have evicted lock2")
	}
}

func TestMemoryLocker_Block(t *testing.T) {
	ctx := context.Background()
	locker := NewMemoryLocker()

	lock1, _ := locker.Lock(ctx, "name", time.Minute)
	_, _ = lock1.Acquire(ctx)

	// Release the first lock after a delay.
	go func() {
		time.Sleep(60 * time.Millisecond)
		_ = lock1.Release()
	}()

	// Block until we get it (within 500ms).
	lock2, _ := locker.Lock(ctx, "name", time.Minute)
	start := time.Now()
	got, err := lock2.Block(ctx, 500*time.Millisecond)
	if err != nil || !got {
		t.Errorf("Block: %v %v", got, err)
	}
	elapsed := time.Since(start)
	if elapsed < 50*time.Millisecond {
		t.Errorf("Block returned too fast (lock1 hadn't released): %v", elapsed)
	}
}

func TestMemoryLocker_BlockTimeout(t *testing.T) {
	ctx := context.Background()
	locker := NewMemoryLocker()
	lock1, _ := locker.Lock(ctx, "name", time.Minute)
	_, _ = lock1.Acquire(ctx)
	defer lock1.Release()

	lock2, _ := locker.Lock(ctx, "name", time.Minute)
	got, err := lock2.Block(ctx, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("should have timed out")
	}
}

func TestWithLock(t *testing.T) {
	ctx := context.Background()
	locker := NewMemoryLocker()

	var ran int
	err := WithLock(ctx, locker, "task", time.Minute, func(_ context.Context) error {
		ran++
		return nil
	})
	if err != nil || ran != 1 {
		t.Errorf("WithLock: ran=%d err=%v", ran, err)
	}
}

func TestWithLock_PropagatesFnError(t *testing.T) {
	ctx := context.Background()
	locker := NewMemoryLocker()
	myErr := errors.New("nope")
	err := WithLock(ctx, locker, "task", time.Minute, func(_ context.Context) error {
		return myErr
	})
	if !errors.Is(err, myErr) {
		t.Errorf("got %v", err)
	}
}

func TestWithLock_NotAcquired(t *testing.T) {
	ctx := context.Background()
	locker := NewMemoryLocker()

	// Hold the lock externally.
	holder, _ := locker.Lock(ctx, "task", time.Minute)
	_, _ = holder.Acquire(ctx)
	defer holder.Release()

	err := WithLock(ctx, locker, "task", time.Minute, func(_ context.Context) error {
		t.Error("fn should not run when lock not acquired")
		return nil
	})
	if !errors.Is(err, ErrLockNotAcquired) {
		t.Errorf("got %v, want ErrLockNotAcquired", err)
	}
}

// ---------- concurrency -------------------------------------------------

func TestMemoryStore_ConcurrentIncrement(t *testing.T) {
	// 100 goroutines × 100 increments = 10000. Final value must be 10000.
	s := NewMemoryStoreWithCleanup(0)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = s.Increment(ctx, "counter", 1)
			}
		}()
	}
	wg.Wait()

	v, err := s.Increment(ctx, "counter", 0)
	if err != nil {
		t.Fatal(err)
	}
	if v != 10000 {
		t.Errorf("got %d, want 10000", v)
	}
}
