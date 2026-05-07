package queue

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type emailPayload struct {
	To      string
	Subject string
}

func TestDispatchAndHandle(t *testing.T) {
	backend := NewMemoryBackend()
	defer backend.Close()
	q := New(backend, Config{Workers: 1, BaseBackoff: 10 * time.Millisecond})

	got := make(chan emailPayload, 1)
	q.Register("send-email", func(_ context.Context, payload []byte) error {
		var p emailPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		got <- p
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	if err := Dispatch(ctx, q, "send-email", emailPayload{To: "x@y", Subject: "hi"}); err != nil {
		t.Fatal(err)
	}

	select {
	case p := <-got:
		if p.To != "x@y" || p.Subject != "hi" {
			t.Errorf("payload corrupted: %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not run within 2s")
	}
}

func TestRetryOnFailure(t *testing.T) {
	backend := NewMemoryBackend()
	defer backend.Close()
	q := New(backend, Config{
		Workers:     1,
		MaxRetry:    2,
		BaseBackoff: 5 * time.Millisecond,
	})

	var attempts atomic.Int32
	done := make(chan struct{})
	q.Register("flaky", func(_ context.Context, _ []byte) error {
		n := attempts.Add(1)
		if n < 3 { // fail twice, succeed on third
			return errors.New("transient")
		}
		close(done)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	_ = Dispatch(ctx, q, "flaky", struct{}{})

	select {
	case <-done:
		if attempts.Load() != 3 {
			t.Errorf("expected 3 attempts, got %d", attempts.Load())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("never succeeded; attempts=" + string(rune('0'+attempts.Load())))
	}
}

func TestDeadLetterAfterExhaustedRetries(t *testing.T) {
	backend := NewMemoryBackend()
	defer backend.Close()

	var errs []error
	var errMu sync.Mutex
	q := New(backend, Config{
		Workers:     1,
		MaxRetry:    1, // 2 attempts total
		BaseBackoff: 5 * time.Millisecond,
		ErrorHandler: func(_ *Envelope, err error, willRetry bool) {
			errMu.Lock()
			errs = append(errs, err)
			errMu.Unlock()
		},
	})

	q.Register("doomed", func(_ context.Context, _ []byte) error {
		return errors.New("always fails")
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	_ = Dispatch(ctx, q, "doomed", struct{}{})

	// Wait long enough for both attempts to run (the second is delayed by
	// 5ms backoff). Poll for the dead-letter entry.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(backend.FailedJobs()) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	failed := backend.FailedJobs()
	if len(failed) != 1 {
		t.Fatalf("expected 1 dead-lettered job, got %d", len(failed))
	}

	errMu.Lock()
	gotErrs := len(errs)
	errMu.Unlock()
	if gotErrs != 2 {
		t.Errorf("expected 2 error reports (one retried, one final), got %d", gotErrs)
	}
}

func TestDelayedJob(t *testing.T) {
	backend := NewMemoryBackend()
	defer backend.Close()
	q := New(backend, Config{Workers: 1})

	ranAt := make(chan time.Time, 1)
	q.Register("later", func(_ context.Context, _ []byte) error {
		ranAt <- time.Now()
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	scheduled := time.Now().Add(150 * time.Millisecond)
	if err := DispatchAt(ctx, q, "later", struct{}{}, scheduled); err != nil {
		t.Fatal(err)
	}

	select {
	case t0 := <-ranAt:
		if t0.Before(scheduled) {
			t.Errorf("ran %v before scheduled time", scheduled.Sub(t0))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("delayed job never ran")
	}
}

func TestUnregisteredJobTypeDeadLetters(t *testing.T) {
	backend := NewMemoryBackend()
	defer backend.Close()
	q := New(backend, Config{Workers: 1})
	// no Register() call — job type is unknown

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	_ = Dispatch(ctx, q, "unknown", struct{}{})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(backend.FailedJobs()) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("unknown job type should be dead-lettered")
}

func TestPanicInHandlerIsRetriedThenDeadLettered(t *testing.T) {
	backend := NewMemoryBackend()
	defer backend.Close()
	q := New(backend, Config{
		Workers:     1,
		MaxRetry:    0, // no retries — go straight to dead letter
		BaseBackoff: 5 * time.Millisecond,
	})
	q.Register("panic-job", func(_ context.Context, _ []byte) error {
		panic("oops")
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	_ = Dispatch(ctx, q, "panic-job", struct{}{})

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if len(backend.FailedJobs()) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("panicking job should be dead-lettered, not crash the worker")
}

func TestBackoffGrows(t *testing.T) {
	q := New(NewMemoryBackend(), Config{
		BaseBackoff: 100 * time.Millisecond,
		MaxBackoff:  10 * time.Second,
	})
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 400 * time.Millisecond},
		{4, 800 * time.Millisecond},
	}
	for _, c := range cases {
		got := q.backoff(c.attempt)
		if got != c.want {
			t.Errorf("backoff(%d): got %v, want %v", c.attempt, got, c.want)
		}
	}
}

func TestBackoffCaps(t *testing.T) {
	q := New(NewMemoryBackend(), Config{
		BaseBackoff: 1 * time.Second,
		MaxBackoff:  3 * time.Second,
	})
	// 2^9 * 1s = 512s — must be clamped to 3s.
	got := q.backoff(10)
	if got != 3*time.Second {
		t.Errorf("expected cap at 3s, got %v", got)
	}
}
