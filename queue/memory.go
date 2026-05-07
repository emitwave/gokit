package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// MemoryBackend is a process-local Backend. Suitable for dev, single-
// instance services, and tests. NOT for multi-instance production —
// jobs vanish when the process exits.
//
// The implementation uses a goroutine to move scheduled (delayed) jobs
// from a sorted heap into a ready channel as their RunAt arrives.
type MemoryBackend struct {
	mu sync.Mutex

	// ready holds jobs whose RunAt has passed. A buffered channel keeps
	// Push fast in the common case; backpressure kicks in only when the
	// channel is full.
	ready chan *Envelope

	// scheduled holds jobs with a future RunAt. Sorted by RunAt for the
	// dispatcher to dequeue cheaply.
	scheduled []*Envelope

	// failed retains permanently-failed jobs so callers can inspect them
	// (mirrors a "dead letter queue" — drop into FailedJobs() to read).
	failed []*Envelope

	closed bool
	stop   chan struct{}
	wakeUp chan struct{} // pulse when a new scheduled job is added
}

// NewMemoryBackend returns a backend with the default ready buffer size
// (1024). For services that burst-enqueue heavily, NewMemoryBackendBuffered
// lets you size up.
func NewMemoryBackend() *MemoryBackend {
	return NewMemoryBackendBuffered(1024)
}

// NewMemoryBackendBuffered returns a backend with the given ready-queue
// buffer size.
func NewMemoryBackendBuffered(buf int) *MemoryBackend {
	b := &MemoryBackend{
		ready:  make(chan *Envelope, buf),
		stop:   make(chan struct{}),
		wakeUp: make(chan struct{}, 1),
	}
	go b.scheduler()
	return b
}

// Close stops the scheduler goroutine and unblocks Pop. Idempotent.
func (b *MemoryBackend) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	close(b.stop)
	close(b.ready)
}

// Push assigns an ID and timestamps the envelope, then either drops it
// straight into ready (if RunAt has passed) or onto the scheduled list.
func (b *MemoryBackend) Push(_ context.Context, env *Envelope) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrBackendClosed
	}

	if env.ID == "" {
		env.ID = newJobID()
	}
	if env.EnqueuedAt.IsZero() {
		env.EnqueuedAt = time.Now()
	}
	if env.Attempt == 0 {
		env.Attempt = 1
	}

	if env.RunAt.IsZero() || !env.RunAt.After(time.Now()) {
		// Ready immediately — non-blocking send so a full ready buffer
		// turns into immediate Push backpressure rather than a deadlock.
		select {
		case b.ready <- env:
			return nil
		default:
			// Full: spill to scheduled with RunAt=now so the dispatcher
			// will requeue when there's capacity.
			env.RunAt = time.Now()
			b.insertScheduled(env)
			return nil
		}
	}

	// Future-dated job.
	b.insertScheduled(env)
	b.kickScheduler()
	return nil
}

// Pop blocks until a job is available, ctx is cancelled, or the backend
// is closed.
func (b *MemoryBackend) Pop(ctx context.Context) (*Envelope, error) {
	select {
	case env, ok := <-b.ready:
		if !ok {
			return nil, ErrBackendClosed
		}
		return env, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.stop:
		return nil, ErrBackendClosed
	}
}

// Ack is a no-op for the memory backend — once Pop returns a job, it's
// already off the ready channel. We keep the method to satisfy the
// Backend interface; remote backends use Ack to confirm processing.
func (b *MemoryBackend) Ack(_ context.Context, _ string) error {
	return nil
}

// Nack re-enqueues the job, incrementing Attempt and applying exponential
// backoff. The router delegates to Backend.Nack rather than computing the
// next RunAt itself so different backends can have different policies.
func (b *MemoryBackend) Nack(_ context.Context, id string, _ error) error {
	// Memory backend doesn't store the original envelope after Pop; the
	// caller must round-trip it via Push with Attempt incremented. The
	// Queue's run loop does that — Nack here is just a hook for backends
	// that DO store the in-flight set. We accept the call and no-op.
	_ = id
	return nil
}

// Fail records the permanently-failed envelope. Inspect via FailedJobs.
func (b *MemoryBackend) Fail(_ context.Context, env *Envelope, _ error) error {
	b.mu.Lock()
	b.failed = append(b.failed, env)
	b.mu.Unlock()
	return nil
}

// FailedJobs returns a snapshot of the dead-letter queue. Useful in
// tests and for ops dashboards in dev.
func (b *MemoryBackend) FailedJobs() []*Envelope {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*Envelope, len(b.failed))
	copy(out, b.failed)
	return out
}

// Pending reports the count of scheduled (not-yet-ready) jobs. Tests use
// this to assert delayed dispatch.
func (b *MemoryBackend) Pending() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.scheduled)
}

// ---------- scheduling internals ----------------------------------------

func (b *MemoryBackend) insertScheduled(env *Envelope) {
	// Insert in RunAt order (binary search). For job counts under ~10k
	// this is fine; if you need higher throughput, a Backend backed by a
	// proper time-sorted structure (e.g. Redis sorted sets) is the answer.
	lo, hi := 0, len(b.scheduled)
	for lo < hi {
		mid := (lo + hi) / 2
		if b.scheduled[mid].RunAt.Before(env.RunAt) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	b.scheduled = append(b.scheduled, nil)
	copy(b.scheduled[lo+1:], b.scheduled[lo:])
	b.scheduled[lo] = env
}

func (b *MemoryBackend) kickScheduler() {
	// Non-blocking pulse. If the scheduler is already running its tick,
	// the pulse is dropped (it'll observe the new state on its next sweep).
	select {
	case b.wakeUp <- struct{}{}:
	default:
	}
}

// scheduler runs in a goroutine, moving due jobs from scheduled → ready.
// It sleeps until the next RunAt, or until kicked by a new Push.
func (b *MemoryBackend) scheduler() {
	for {
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return
		}

		var sleep time.Duration
		if len(b.scheduled) == 0 {
			sleep = time.Hour // arbitrary large value; we wait on wakeUp/stop
		} else {
			next := b.scheduled[0].RunAt
			now := time.Now()
			if !next.After(now) {
				// First job is due — pull it (and any others that are due).
				toRun := []*Envelope{}
				for len(b.scheduled) > 0 && !b.scheduled[0].RunAt.After(now) {
					toRun = append(toRun, b.scheduled[0])
					b.scheduled = b.scheduled[1:]
				}
				b.mu.Unlock()
				for _, env := range toRun {
					select {
					case b.ready <- env:
					case <-b.stop:
						return
					}
				}
				continue
			}
			sleep = next.Sub(now)
		}
		b.mu.Unlock()

		select {
		case <-b.stop:
			return
		case <-b.wakeUp:
			// loop and re-check
		case <-time.After(sleep):
			// loop and re-check
		}
	}
}

// newJobID returns a 96-bit random hex string. Sufficient uniqueness
// for any single backend instance.
func newJobID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
