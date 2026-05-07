// Package events provides a lightweight in-process event dispatcher with
// sync + async modes, multiple listeners per event, and type-safe
// subscription via generics.
//
// The pattern: define event types with a Name() method, register listeners
// on a Dispatcher, fire events from anywhere with access to the dispatcher.
//
//	type UserRegistered struct {
//	    UserID string
//	    Email  string
//	}
//	func (UserRegistered) Name() string { return "user.registered" }
//
//	d := events.New()
//
//	// Type-safe registration — the listener gets a typed event, no
//	// type assertions needed.
//	events.Subscribe(d, func(ctx context.Context, e UserRegistered) error {
//	    return mailer.SendWelcome(ctx, e.Email)
//	})
//
//	// Fire it
//	d.Dispatch(ctx, UserRegistered{UserID: u.ID, Email: u.Email})
//
// Listeners run in registration order. Errors are collected (one failing
// listener doesn't short-circuit the rest) and returned as a joined error.
package events

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Event is anything with a stable string name. The name routes the event
// to its listeners. Use a hierarchical convention ("user.registered",
// "order.shipped") so future wildcard subscription stays an option.
type Event interface {
	Name() string
}

// Listener is the raw, untyped listener signature. Most users go through
// Subscribe[T] for type safety, but raw Listen is useful for catch-all
// loggers, audit trails, or plugins that handle events generically.
type Listener func(ctx context.Context, event Event) error

// Dispatcher routes events to registered listeners.
//
// Safe for concurrent Dispatch calls after listeners are registered. Adding
// listeners during dispatch is safe but the new listener won't fire for
// the in-flight event.
type Dispatcher struct {
	mu        sync.RWMutex
	listeners map[string][]Listener
	wg        sync.WaitGroup // tracks in-flight async dispatches
}

// New returns an empty Dispatcher.
func New() *Dispatcher {
	return &Dispatcher{listeners: make(map[string][]Listener)}
}

// Listen registers a raw listener for events with the given name. Use
// Subscribe[T] when you have the event type at hand — it gives you a
// typed listener and avoids the type-assertion boilerplate.
func (d *Dispatcher) Listen(eventName string, l Listener) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.listeners[eventName] = append(d.listeners[eventName], l)
}

// Subscribe registers a type-safe listener. The dispatcher routes by
// the zero value's Name() so this works for any event type that
// implements the Event interface.
//
//	events.Subscribe(d, func(ctx context.Context, e UserRegistered) error {
//	    // e is typed — no assertion needed
//	    return nil
//	})
//
// This is a generic function (Go doesn't allow generic methods), which
// is why it's not a method on Dispatcher.
func Subscribe[T Event](d *Dispatcher, l func(context.Context, T) error) {
	var zero T
	d.Listen(zero.Name(), func(ctx context.Context, e Event) error {
		// The dispatcher only invokes this when the event name matches
		// T's name, so the assertion is safe in practice. We still
		// handle the false case to surface programming errors clearly.
		typed, ok := e.(T)
		if !ok {
			return fmt.Errorf("events: listener for %q got incompatible type %T", zero.Name(), e)
		}
		return l(ctx, typed)
	})
}

// Dispatch invokes all listeners for the event synchronously, in
// registration order. Errors are collected with errors.Join — every
// listener runs even if earlier ones fail. Returns nil if no listeners
// are registered (firing into the void is intentional, not an error).
func (d *Dispatcher) Dispatch(ctx context.Context, event Event) error {
	d.mu.RLock()
	ls := d.listeners[event.Name()]
	// Snapshot so listener registration during dispatch doesn't race.
	listeners := make([]Listener, len(ls))
	copy(listeners, ls)
	d.mu.RUnlock()

	if len(listeners) == 0 {
		return nil
	}

	var errs []error
	for _, l := range listeners {
		if err := l(ctx, event); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// DispatchAsync fires all listeners in goroutines and returns immediately.
// Errors are passed to errFn (if non-nil) — typically you wire this up
// to your logger so async failures still surface.
//
// Wait() blocks until all in-flight async dispatches finish. Useful in
// tests and during shutdown.
func (d *Dispatcher) DispatchAsync(ctx context.Context, event Event, errFn func(error)) {
	d.mu.RLock()
	ls := d.listeners[event.Name()]
	listeners := make([]Listener, len(ls))
	copy(listeners, ls)
	d.mu.RUnlock()

	for _, l := range listeners {
		l := l
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			defer func() {
				// A panicking listener shouldn't kill the process. Recover
				// and surface as an error if the user wired up errFn.
				if rec := recover(); rec != nil && errFn != nil {
					errFn(fmt.Errorf("events: listener panicked: %v", rec))
				}
			}()
			if err := l(ctx, event); err != nil && errFn != nil {
				errFn(err)
			}
		}()
	}
}

// Wait blocks until all in-flight async dispatches complete. Use during
// graceful shutdown to avoid losing events fired right before exit.
func (d *Dispatcher) Wait() {
	d.wg.Wait()
}

// Listeners returns the count of listeners registered for an event name.
// Mostly useful in tests to assert wiring.
func (d *Dispatcher) Listeners(eventName string) int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.listeners[eventName])
}
