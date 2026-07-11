package queue

import "time"

// PermanentError marks a handler failure as non-retryable. The queue
// fails the job immediately (Backend.Fail) regardless of remaining
// retry budget. Wrap with Permanent() rather than constructing directly.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err so the queue skips retries and fails the job
// immediately. Use for errors that will never succeed on retry —
// malformed payloads, 4xx provider rejections, invalid credentials.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// RetryAfterError carries an explicit delay for the next attempt,
// overriding the queue's exponential backoff curve. Wrap with
// RetryAfter() rather than constructing directly.
type RetryAfterError struct {
	Err   error
	Delay time.Duration
}

func (e *RetryAfterError) Error() string { return e.Err.Error() }
func (e *RetryAfterError) Unwrap() error { return e.Err }

// RetryAfter wraps err so the next attempt is scheduled after delay
// instead of the backoff curve. Use when the upstream told you when to
// come back — e.g. an HTTP 429 with a retry-after header.
func RetryAfter(err error, delay time.Duration) error {
	if err == nil {
		return nil
	}
	return &RetryAfterError{Err: err, Delay: delay}
}
