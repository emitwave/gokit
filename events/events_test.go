package events

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type userRegistered struct {
	UserID string
	Email  string
}

func (userRegistered) Name() string { return "user.registered" }

type orderPlaced struct {
	OrderID string
}

func (orderPlaced) Name() string { return "order.placed" }

func TestDispatchInvokesAllListeners(t *testing.T) {
	d := New()

	var hits1, hits2 atomic.Int32
	Subscribe(d, func(_ context.Context, _ userRegistered) error {
		hits1.Add(1)
		return nil
	})
	Subscribe(d, func(_ context.Context, _ userRegistered) error {
		hits2.Add(1)
		return nil
	})

	if err := d.Dispatch(context.Background(), userRegistered{UserID: "u1"}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if hits1.Load() != 1 || hits2.Load() != 1 {
		t.Errorf("listeners not invoked: %d, %d", hits1.Load(), hits2.Load())
	}
}

func TestDispatchRoutesByName(t *testing.T) {
	// A listener for orderPlaced must NOT fire on userRegistered.
	d := New()
	var orderHits atomic.Int32
	Subscribe(d, func(_ context.Context, _ orderPlaced) error {
		orderHits.Add(1)
		return nil
	})

	_ = d.Dispatch(context.Background(), userRegistered{UserID: "u1"})
	if orderHits.Load() != 0 {
		t.Error("orderPlaced listener fired on user.registered")
	}
}

func TestDispatchTypedListenerReceivesTypedEvent(t *testing.T) {
	d := New()
	got := ""
	Subscribe(d, func(_ context.Context, e userRegistered) error {
		// Direct field access — no type assertion needed.
		got = e.Email
		return nil
	})

	_ = d.Dispatch(context.Background(), userRegistered{Email: "x@y.com"})
	if got != "x@y.com" {
		t.Errorf("typed event field: got %q", got)
	}
}

func TestDispatchAggregatesErrors(t *testing.T) {
	d := New()
	errA := errors.New("A failed")
	errB := errors.New("B failed")

	Subscribe(d, func(_ context.Context, _ userRegistered) error { return errA })
	Subscribe(d, func(_ context.Context, _ userRegistered) error { return errB })
	var thirdRan bool
	Subscribe(d, func(_ context.Context, _ userRegistered) error { thirdRan = true; return nil })

	err := d.Dispatch(context.Background(), userRegistered{})
	if err == nil {
		t.Fatal("expected joined error")
	}
	if !errors.Is(err, errA) || !errors.Is(err, errB) {
		t.Errorf("expected both errors in joined result, got %v", err)
	}
	if !thirdRan {
		t.Error("third listener should still run despite earlier failures")
	}
}

func TestDispatchAsync(t *testing.T) {
	d := New()
	var hits atomic.Int32
	Subscribe(d, func(_ context.Context, _ userRegistered) error {
		hits.Add(1)
		return nil
	})

	d.DispatchAsync(context.Background(), userRegistered{}, nil)
	d.Wait()

	if hits.Load() != 1 {
		t.Errorf("async listener count: got %d", hits.Load())
	}
}

func TestDispatchAsyncRecoversFromPanic(t *testing.T) {
	d := New()
	Subscribe(d, func(_ context.Context, _ userRegistered) error {
		panic("listener went boom")
	})

	var captured error
	d.DispatchAsync(context.Background(), userRegistered{}, func(err error) {
		captured = err
	})
	d.Wait()

	if captured == nil {
		t.Fatal("expected panic to be surfaced via errFn")
	}
}

func TestNoListenersIsNotAnError(t *testing.T) {
	d := New()
	if err := d.Dispatch(context.Background(), userRegistered{}); err != nil {
		t.Errorf("dispatching with no listeners should be a no-op, got %v", err)
	}
}

func TestListenerCount(t *testing.T) {
	d := New()
	if d.Listeners("user.registered") != 0 {
		t.Errorf("empty count: got %d", d.Listeners("user.registered"))
	}
	Subscribe(d, func(_ context.Context, _ userRegistered) error { return nil })
	Subscribe(d, func(_ context.Context, _ userRegistered) error { return nil })
	if got := d.Listeners("user.registered"); got != 2 {
		t.Errorf("count: got %d, want 2", got)
	}
}

func TestRawListen(t *testing.T) {
	// Listen() (no generics) is for catch-all listeners.
	d := New()
	var name string
	d.Listen("user.registered", func(_ context.Context, e Event) error {
		name = e.Name()
		return nil
	})
	_ = d.Dispatch(context.Background(), userRegistered{})
	if name != "user.registered" {
		t.Errorf("got %q", name)
	}
}

func TestAsyncCompletesBeforeWait(t *testing.T) {
	d := New()
	Subscribe(d, func(_ context.Context, _ userRegistered) error {
		// Slow-ish listener
		time.Sleep(20 * time.Millisecond)
		return nil
	})

	start := time.Now()
	d.DispatchAsync(context.Background(), userRegistered{}, nil)
	d.Wait()

	// Wait must actually block until the listener finished — at least 20ms.
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Errorf("Wait returned too early: %v", elapsed)
	}
}
