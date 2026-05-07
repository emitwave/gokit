// Package helpers ports the most useful generic helper functions from
// Laravel's helpers list to idiomatic Go. Subpackages cover specific
// domains:
//
//   - helpers/arr  — Arr::* (slice and map utilities with generics)
//   - helpers/num  — Number::* (formatting, parsing, units, locales)
//   - helpers/data — data_* path-based access on nested structures
//
// This root package collects the scalar / control-flow helpers that
// don't fit cleanly into any of those: Value, Blank, Filled, Tap, With,
// When, Retry, Rescue, ThrowIf, ThrowUnless, Transform, Now, Today, Env,
// Dump, Once.
//
// Several Laravel helpers are intentionally NOT ported here because they
// map to existing parts of this kit or to Go's standard library:
//
//   - auth, policy, bcrypt, encrypt, decrypt → use crypto/* + your auth pkg
//   - request, response, redirect, back      → router and response packages
//   - session                                → session package
//   - logger, info                           → logger package
//   - validator                              → validation package
//   - dispatch, dispatch_sync                → queue package
//   - event                                  → events package
//   - cache, broadcast, view, fake           → out of scope (separate packages)
//   - app, resolve, config, env (the facade) → not Go-idiomatic (no container)
//   - app_path/base_path/storage_path/...    → Laravel-specific (use os.Executable, runtime.Caller)
//   - csrf_field, csrf_token                 → would need a dedicated CSRF pkg
//   - class_uses_recursive, trait_uses_*     → PHP-specific
//
// Read each function's doc for the Laravel equivalent.
package helpers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"sync"
	"time"
)

// ---------- Value, Blank, Filled ----------------------------------------

// Value returns the result of fn. It's the generic-typed counterpart to
// Laravel's value() — useful when you want to lazily compute something
// only at call time.
//
//	default := helpers.Value(func() string { return computeDefault() })
func Value[T any](fn func() T) T { return fn() }

// Blank reports whether v is "blank" — nil, the zero value of its type
// (for primitives), an empty string, or an empty slice/map. Mirrors
// Laravel's blank() helper.
func Blank(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		return rv.Len() == 0
	case reflect.Slice, reflect.Map, reflect.Array, reflect.Chan:
		return rv.Len() == 0
	case reflect.Ptr, reflect.Interface:
		return rv.IsNil()
	case reflect.Bool:
		return !rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		// Numeric zero is NOT blank in Laravel — only nil/empty/empty-string.
		// Matching that behavior here: 0 is "filled" because it's a real value.
		return false
	case reflect.Float32, reflect.Float64:
		return false
	case reflect.Struct:
		// Special case: time.Time zero is treated as blank, matching
		// Laravel's Carbon-aware behavior.
		if t, ok := v.(time.Time); ok {
			return t.IsZero()
		}
		return false
	}
	return false
}

// Filled is the negation of Blank.
func Filled(v any) bool { return !Blank(v) }

// ---------- Tap, With, When ---------------------------------------------

// Tap calls fn with v, then returns v unchanged. Useful for inline side
// effects in fluent chains.
//
//	user := helpers.Tap(NewUser(), func(u *User) {
//	    u.SetDefaults()
//	    u.LogCreation()
//	})
func Tap[T any](v T, fn func(T)) T {
	fn(v)
	return v
}

// With calls fn with v and returns fn's result. Sugar for "transform
// this value through this function" without requiring a chain.
func With[T, R any](v T, fn func(T) R) R { return fn(v) }

// When returns onTrue if cond is true, otherwise onFalse. The unused
// branch is NOT evaluated — both args are values you've already computed
// (use lazy variants below if you need lazy evaluation).
func When[T any](cond bool, onTrue, onFalse T) T {
	if cond {
		return onTrue
	}
	return onFalse
}

// WhenLazy is When with lazy evaluation: only the chosen branch's
// function is called. Use when constructing the unused branch is
// expensive or has side effects.
func WhenLazy[T any](cond bool, onTrue, onFalse func() T) T {
	if cond {
		return onTrue()
	}
	return onFalse()
}

// ---------- Transform ----------------------------------------------------

// Transform applies fn to v if v is non-zero (per Blank), otherwise
// returns the zero value of R. Mirrors Laravel's transform() helper.
func Transform[T, R any](v T, fn func(T) R) R {
	if Blank(v) {
		var zero R
		return zero
	}
	return fn(v)
}

// ---------- Retry, Rescue ------------------------------------------------

// Retry calls fn up to attempts times, with sleep between tries. Returns
// the first nil error or the last error after exhausting attempts.
// Respects ctx cancellation — a cancelled ctx aborts immediately.
//
//	err := helpers.Retry(ctx, 3, 200*time.Millisecond, func() error {
//	    return doFlakyThing()
//	})
func Retry(ctx context.Context, attempts int, sleep time.Duration, fn func() error) error {
	var lastErr error
	for i := 0; i < attempts; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i < attempts-1 && sleep > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleep):
			}
		}
	}
	return lastErr
}

// Rescue runs fn and catches any panic, converting it to an error and
// (optionally) calling rescue with that error. The return value is fn's
// result on success, or the zero value of T on panic.
//
//	user := helpers.Rescue(func() *User { return mustLoadUser() }, func(err error) {
//	    log.Error("user load failed", "err", err)
//	})
func Rescue[T any](fn func() T, rescue func(error)) (result T) {
	defer func() {
		if r := recover(); r != nil {
			err, ok := r.(error)
			if !ok {
				err = fmt.Errorf("panic: %v", r)
			}
			if rescue != nil {
				rescue(err)
			}
			// result is already the zero value of T at this point.
		}
	}()
	return fn()
}

// ---------- ThrowIf, ThrowUnless ----------------------------------------

// ThrowIf returns err if cond is true, else returns nil. Sugar for
// `if cond { return err }` patterns.
//
//	if err := helpers.ThrowIf(amount < 0, errors.New("amount must be non-negative")); err != nil {
//	    return err
//	}
func ThrowIf(cond bool, err error) error {
	if cond {
		return err
	}
	return nil
}

// ThrowUnless is ThrowIf with the negated condition.
func ThrowUnless(cond bool, err error) error {
	return ThrowIf(!cond, err)
}

// ---------- Now, Today --------------------------------------------------

// Now returns the current time. The package-level clock is replaceable
// via SetClock for tests so any helpers building on Now (Today, expiry
// calculations) can be controlled deterministically.
func Now() time.Time { return clockNow() }

// Today returns the start of the current day in the local timezone.
func Today() time.Time {
	t := Now()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

var (
	clockMu  sync.RWMutex
	clockNow = time.Now
)

// SetClock replaces the time source used by Now and Today. Pass nil to
// restore time.Now. Intended for tests:
//
//	defer helpers.SetClock(nil)
//	helpers.SetClock(func() time.Time { return fixedTime })
func SetClock(fn func() time.Time) {
	clockMu.Lock()
	defer clockMu.Unlock()
	if fn == nil {
		clockNow = time.Now
	} else {
		clockNow = fn
	}
}

// ---------- Env ----------------------------------------------------------

// Env returns the environment variable named key, parsed into T. If the
// variable is unset OR fails to parse, returns def. Supported types:
// string, bool, int, int64, float64. For other types, use os.Getenv
// directly.
//
//	debug := helpers.Env("DEBUG", false)
//	port  := helpers.Env("PORT", 8080)
//	name  := helpers.Env("APP_NAME", "emitwave")
func Env[T string | bool | int | int64 | float64](key string, def T) T {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def
	}
	// Type-switch via any() — Go 1.21+ idiom for branching on a generic
	// type parameter's underlying type.
	var zero T
	switch any(zero).(type) {
	case string:
		return any(raw).(T)
	case bool:
		switch raw {
		case "1", "true", "TRUE", "True", "yes", "YES", "on", "ON":
			return any(true).(T)
		case "0", "false", "FALSE", "False", "no", "NO", "off", "OFF":
			return any(false).(T)
		}
		return def
	case int:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return def
		}
		return any(n).(T)
	case int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return def
		}
		return any(n).(T)
	case float64:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return def
		}
		return any(n).(T)
	}
	return def
}

// ---------- Dump --------------------------------------------------------

// Dump pretty-prints v to stderr for debugging. No equivalent of dd()
// (which dumps and exits) — use os.Exit yourself if you really want
// that, but in Go we strongly prefer returning errors.
func Dump(v ...any) {
	for i, item := range v {
		fmt.Fprintf(os.Stderr, "[%d] (%T) %+v\n", i, item, item)
	}
}

// ---------- Once --------------------------------------------------------

// OnceFn returns a function that calls fn exactly once and caches the
// result. Subsequent calls return the cached value. Equivalent to
// Laravel's once() but typed via generics.
//
//	getConfig := helpers.OnceFn(func() *Config { return loadConfig() })
//	cfg := getConfig() // first call: loadConfig runs
//	cfg = getConfig()  // returns the cached *Config
func OnceFn[T any](fn func() T) func() T {
	var (
		once sync.Once
		val  T
	)
	return func() T {
		once.Do(func() { val = fn() })
		return val
	}
}

// ---------- minor helpers used across subpackages -----------------------

// errOrPanic is a tiny convenience used internally where we want either
// a returned error or a clear panic for programmer errors.
var errProgrammer = errors.New("helpers: programmer error")
