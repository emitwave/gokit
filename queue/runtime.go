package queue

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

// Config tunes a Queue. Zero values get production-reasonable defaults.
type Config struct {
	// Workers is the number of goroutines pulling and processing jobs.
	// Default: 4. Tune based on whether your handlers are CPU-bound or
	// I/O-bound.
	Workers int

	// MaxRetry is the default retry budget (in addition to the first
	// attempt). 0 = no retries; 5 means up to 6 total attempts. Default: 3.
	// Override per-dispatch via DispatchOptions.MaxRetry.
	MaxRetry int

	// BaseBackoff is the first retry delay. Default: 1s. Subsequent
	// retries use exponential backoff (2x each time, capped at MaxBackoff).
	BaseBackoff time.Duration

	// MaxBackoff caps the exponential growth so retry intervals don't
	// run away on long-failing jobs. Default: 5 minutes.
	MaxBackoff time.Duration

	// ErrorHandler is called for every failed attempt. Wire it to your
	// logger / error tracker. Default: no-op (failures are silent
	// otherwise — strongly recommend setting this).
	ErrorHandler func(env *Envelope, err error, willRetry bool)
}

// Queue is the high-level API: register handlers, dispatch jobs, run
// workers. Backends provide storage; Queue provides routing + retry.
type Queue struct {
	backend  Backend
	cfg      Config
	mu       sync.RWMutex
	handlers map[string]Handler

	// stopOnce guards Stop being called concurrently or repeatedly.
	stopOnce sync.Once
	stopped  chan struct{}
}

// New constructs a Queue. The backend is required; pass NewMemoryBackend()
// for dev or your own implementation for production.
func New(backend Backend, cfg Config) *Queue {
	if backend == nil {
		panic("queue: backend is required")
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.MaxRetry < 0 {
		cfg.MaxRetry = 0
	} else if cfg.MaxRetry == 0 {
		cfg.MaxRetry = 3
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 5 * time.Minute
	}

	return &Queue{
		backend:  backend,
		cfg:      cfg,
		handlers: make(map[string]Handler),
		stopped:  make(chan struct{}),
	}
}

// Register binds a handler to a job type. Re-registering the same type
// replaces the prior handler — useful in tests and hot reloads, but
// avoid in normal startup wiring (the second registration silently
// shadows the first).
func (q *Queue) Register(jobType string, h Handler) {
	q.mu.Lock()
	q.handlers[jobType] = h
	q.mu.Unlock()
}

// Run starts the worker pool. Blocks until ctx is cancelled or Stop is
// called. Typically called as `go q.Run(ctx)` from main.
func (q *Queue) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for i := 0; i < q.cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q.worker(ctx)
		}()
	}
	wg.Wait()
	return ctx.Err()
}

// Stop signals workers to drain and exit. Call before shutdown so in-
// flight handlers can finish.
func (q *Queue) Stop() {
	q.stopOnce.Do(func() { close(q.stopped) })
}

// worker is the per-goroutine loop: Pop → Handler → Ack/Retry/Fail.
func (q *Queue) worker(ctx context.Context) {
	for {
		// Bail early if Stop was called or ctx is done.
		select {
		case <-ctx.Done():
			return
		case <-q.stopped:
			return
		default:
		}

		env, err := q.backend.Pop(ctx)
		if err != nil {
			// Pop failures (closed backend, ctx cancel) terminate the
			// worker. Real production backends should retry transient
			// failures internally before surfacing them here.
			return
		}
		if env == nil {
			continue
		}

		q.handle(ctx, env)
	}
}

// handle runs a single job through its handler, applying retry logic on
// failure.
func (q *Queue) handle(ctx context.Context, env *Envelope) {
	q.mu.RLock()
	h, ok := q.handlers[env.Type]
	q.mu.RUnlock()
	if !ok {
		// Unregistered job type — fail it permanently rather than retry
		// indefinitely. This catches deploy ordering bugs (worker came up
		// before the handler was wired).
		err := fmt.Errorf("queue: no handler registered for job type %q", env.Type)
		q.reportError(env, err, false)
		_ = q.backend.Fail(ctx, env, err)
		return
	}

	// Recover from handler panics so a bug in one job doesn't kill the
	// worker. Treat the panic as a regular failure.
	var handlerErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				handlerErr = fmt.Errorf("queue: handler panicked: %v", r)
			}
		}()
		handlerErr = h(ctx, env.Payload)
	}()

	if handlerErr == nil {
		_ = q.backend.Ack(ctx, env.ID)
		return
	}

	// Retry path.
	if env.Attempt > env.MaxRetry {
		// Exhausted — into the dead-letter pile.
		q.reportError(env, handlerErr, false)
		_ = q.backend.Fail(ctx, env, handlerErr)
		return
	}

	q.reportError(env, handlerErr, true)
	_ = q.backend.Nack(ctx, env.ID, handlerErr)

	// Re-push with backoff. We do this rather than expecting the backend
	// to handle retries because the backoff policy belongs to the queue,
	// not the storage layer — different teams want different curves.
	retry := *env
	retry.ID = ""
	retry.Attempt = env.Attempt + 1
	retry.RunAt = time.Now().Add(q.backoff(env.Attempt))
	_ = q.backend.Push(ctx, &retry)
}

// backoff computes the delay before the (attempt+1)-th try. Exponential
// with cap.
func (q *Queue) backoff(attempt int) time.Duration {
	// attempt is the just-failed attempt number (1-based). Delay before
	// the next try grows as 2^(attempt-1) * BaseBackoff.
	mult := math.Pow(2, float64(attempt-1))
	d := time.Duration(float64(q.cfg.BaseBackoff) * mult)
	if d > q.cfg.MaxBackoff {
		d = q.cfg.MaxBackoff
	}
	return d
}

func (q *Queue) reportError(env *Envelope, err error, willRetry bool) {
	if q.cfg.ErrorHandler != nil {
		q.cfg.ErrorHandler(env, err, willRetry)
	}
}

// HandlersCount returns the number of registered handlers. Tests use it
// to assert wiring.
func (q *Queue) HandlersCount() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.handlers)
}
