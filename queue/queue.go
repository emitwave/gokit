// Package queue provides background job processing with a pluggable
// backend, typed handlers, retries with exponential backoff, and delayed
// dispatch. The shape is:
//
//	q := queue.New(queue.NewMemoryBackend(), queue.Config{Workers: 4})
//	q.Register("send-welcome-email", func(ctx context.Context, payload []byte) error {
//	    var p WelcomePayload
//	    if err := json.Unmarshal(payload, &p); err != nil { return err }
//	    return mailer.Send(ctx, p)
//	})
//
//	// From any handler:
//	queue.Dispatch(ctx, q, "send-welcome-email", WelcomePayload{To: u.Email})
//
//	// Start workers (typically in main):
//	go q.Run(ctx)
//
// The MemoryBackend is process-local — fine for dev and single-instance
// deployments. For multi-instance production, implement Backend against
// Redis (BLMOVE for the worker loop) or SQS (visibility timeouts replace
// the explicit ack/nack).
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Envelope wraps a job for transport. It's what the Backend stores and
// retrieves — the user-facing API works in terms of typed payloads,
// envelopes are an implementation detail backends should serialize and
// the queue exposes for tests and inspection.
type Envelope struct {
	ID        string    // unique per-job identifier (set by Backend.Push)
	Type      string    // routing key — matches Register() name
	Key       string    // optional fairness key (e.g. tenant ID) for Config.Limiter
	Payload   []byte    // JSON-encoded payload
	Attempt   int       // 1-based; incremented on retry
	MaxRetry  int       // 0 = no retries; total attempts = MaxRetry + 1
	RunAt     time.Time // earliest time this job may execute
	EnqueuedAt time.Time
}

// Handler processes a job's payload. Return nil for success; any non-nil
// error triggers retry (until MaxRetry is exhausted, then the job goes
// to the failure path — see Backend.Fail).
//
// Handlers should respect ctx cancellation — when the queue is shutting
// down, ctx is cancelled and handlers should wrap up promptly.
type Handler func(ctx context.Context, payload []byte) error

// Backend is the storage layer for jobs. Implementations must be safe
// for concurrent calls; the worker pool fans out across goroutines.
type Backend interface {
	// Push enqueues a job. The backend is responsible for assigning ID
	// and setting EnqueuedAt — callers can leave those zero.
	Push(ctx context.Context, env *Envelope) error

	// Pop blocks until a job is available, the context is cancelled, or
	// the backend is closed. Returns ErrBackendClosed in the latter
	// two cases. Jobs whose RunAt is in the future must NOT be returned
	// — backend implementations handle the delay.
	Pop(ctx context.Context) (*Envelope, error)

	// Ack marks a job as successfully processed. After Ack, the backend
	// must not return this Envelope again.
	Ack(ctx context.Context, id string) error

	// Nack returns a job to the queue for retry. The backend should
	// honor RunAt for delayed retry scheduling. cause is informational
	// — typical backends store the most recent failure for diagnostics.
	Nack(ctx context.Context, id string, cause error) error

	// Fail marks a job as permanently failed (retries exhausted). The
	// backend should move it to a dead-letter queue or similar so the
	// envelope can be inspected post-mortem.
	Fail(ctx context.Context, env *Envelope, cause error) error
}

// ErrBackendClosed is returned by Pop when the backend is shut down.
// Workers treat this as the signal to exit cleanly.
var ErrBackendClosed = errors.New("queue: backend closed")

// ---------- payload encoding helpers ------------------------------------

// Dispatch is a generic helper that JSON-encodes the payload and pushes
// the envelope onto the queue. Use the type parameter to keep payload
// types explicit at call sites:
//
//	queue.Dispatch[WelcomePayload](ctx, q, "send-welcome", WelcomePayload{...})
//
// (Type inference makes the call site clean: queue.Dispatch(ctx, q, "x", payload))
func Dispatch[T any](ctx context.Context, q *Queue, jobType string, payload T) error {
	return DispatchWithOpts(ctx, q, jobType, payload, DispatchOptions{})
}

// DispatchAt schedules a job to run at runAt. Useful for reminders,
// follow-ups, retries with custom timing.
func DispatchAt[T any](ctx context.Context, q *Queue, jobType string, payload T, runAt time.Time) error {
	return DispatchWithOpts(ctx, q, jobType, payload, DispatchOptions{RunAt: runAt})
}

// DispatchAfter is sugar for "in N from now" delays.
func DispatchAfter[T any](ctx context.Context, q *Queue, jobType string, payload T, delay time.Duration) error {
	return DispatchWithOpts(ctx, q, jobType, payload, DispatchOptions{RunAt: time.Now().Add(delay)})
}

// DispatchKeyed enqueues like Dispatch but tags the job with a fairness
// key (typically a tenant/org ID). When the queue is configured with a
// ConcurrencyLimiter, jobs sharing a key are capped at the limiter's
// per-key concurrency so one tenant's backlog can't monopolize workers.
func DispatchKeyed[T any](ctx context.Context, q *Queue, jobType string, payload T, key string) error {
	return DispatchWithOpts(ctx, q, jobType, payload, DispatchOptions{Key: key})
}

// DispatchOptions configures a single Dispatch call.
type DispatchOptions struct {
	// RunAt is the earliest time the job may execute. Zero = now.
	RunAt time.Time

	// Key tags the job with a fairness key for per-key concurrency
	// limiting. Empty = unkeyed (never deferred by the limiter).
	Key string

	// MaxRetry overrides the queue-level default. Set to -1 to disable
	// retries for this specific job, or to a positive value to permit
	// more (or fewer) attempts.
	MaxRetry int

	// MaxRetrySet flags an explicit MaxRetry override. Without this
	// we'd treat 0 as "no retries" but couldn't tell it apart from
	// "didn't set it, use the default."
	MaxRetrySet bool
}

// DispatchWithOpts is the most general dispatch entry point.
func DispatchWithOpts[T any](ctx context.Context, q *Queue, jobType string, payload T, opts DispatchOptions) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	maxRetry := q.cfg.MaxRetry
	if opts.MaxRetrySet {
		maxRetry = opts.MaxRetry
	}
	return q.backend.Push(ctx, &Envelope{
		Type:     jobType,
		Key:      opts.Key,
		Payload:  body,
		MaxRetry: maxRetry,
		RunAt:    opts.RunAt,
	})
}
