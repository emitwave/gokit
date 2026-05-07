package helpers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestValue(t *testing.T) {
	got := Value(func() int { return 42 })
	if got != 42 {
		t.Errorf("got %d", got)
	}
}

func TestBlank(t *testing.T) {
	cases := []struct {
		v    any
		want bool
	}{
		{nil, true},
		{"", true},
		{"x", false},
		{[]int{}, true},
		{[]int{1}, false},
		{map[string]int{}, true},
		{map[string]int{"k": 1}, false},
		{time.Time{}, true},
		{time.Now(), false},
		{0, false}, // numeric zero is filled (matches Laravel)
		{false, true},
		{true, false},
	}
	for _, c := range cases {
		if got := Blank(c.v); got != c.want {
			t.Errorf("Blank(%v): got %v, want %v", c.v, got, c.want)
		}
	}
}

func TestFilledIsBlankNegated(t *testing.T) {
	if Filled("") {
		t.Error("empty string should not be filled")
	}
	if !Filled("x") {
		t.Error("non-empty string should be filled")
	}
}

func TestTap(t *testing.T) {
	var seen int
	out := Tap(99, func(v int) { seen = v })
	if seen != 99 || out != 99 {
		t.Errorf("Tap: seen=%d out=%d", seen, out)
	}
}

func TestWith(t *testing.T) {
	got := With(10, func(n int) string { return fmt.Sprintf("n=%d", n) })
	if got != "n=10" {
		t.Errorf("got %q", got)
	}
}

func TestWhen(t *testing.T) {
	if When(true, "a", "b") != "a" {
		t.Error("When(true): wrong branch")
	}
	if When(false, "a", "b") != "b" {
		t.Error("When(false): wrong branch")
	}
}

func TestWhenLazy(t *testing.T) {
	var trueCalled, falseCalled bool
	got := WhenLazy(true,
		func() string { trueCalled = true; return "yes" },
		func() string { falseCalled = true; return "no" },
	)
	if got != "yes" || !trueCalled || falseCalled {
		t.Errorf("WhenLazy(true): got=%q trueCalled=%v falseCalled=%v",
			got, trueCalled, falseCalled)
	}
}

func TestTransform(t *testing.T) {
	if got := Transform("hi", func(s string) int { return len(s) }); got != 2 {
		t.Errorf("filled: got %d", got)
	}
	if got := Transform("", func(s string) int { return len(s) }); got != 0 {
		t.Errorf("blank should yield zero of R: got %d", got)
	}
}

func TestRetrySucceedsEventually(t *testing.T) {
	var attempts atomic.Int32
	err := Retry(context.Background(), 5, time.Millisecond, func() error {
		if attempts.Add(1) < 3 {
			return errors.New("not yet")
		}
		return nil
	})
	if err != nil {
		t.Errorf("expected success, got %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("attempts: got %d", got)
	}
}

func TestRetryExhausts(t *testing.T) {
	err := Retry(context.Background(), 3, time.Millisecond, func() error {
		return errors.New("always fails")
	})
	if err == nil || err.Error() != "always fails" {
		t.Errorf("expected 'always fails', got %v", err)
	}
}

func TestRetryRespectsCtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done
	err := Retry(ctx, 5, 50*time.Millisecond, func() error { return errors.New("x") })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestRescueRecoversFromPanic(t *testing.T) {
	var captured error
	got := Rescue(func() string {
		panic("boom")
	}, func(err error) {
		captured = err
	})
	if got != "" {
		t.Errorf("expected zero value on panic, got %q", got)
	}
	if captured == nil || captured.Error() != "panic: boom" {
		t.Errorf("rescue err: got %v", captured)
	}
}

func TestRescuePassThroughOnSuccess(t *testing.T) {
	got := Rescue(func() int { return 7 }, nil)
	if got != 7 {
		t.Errorf("got %d", got)
	}
}

func TestThrowIf(t *testing.T) {
	myErr := errors.New("bad")
	if err := ThrowIf(true, myErr); err != myErr {
		t.Errorf("got %v", err)
	}
	if err := ThrowIf(false, myErr); err != nil {
		t.Errorf("got %v", err)
	}
}

func TestThrowUnless(t *testing.T) {
	myErr := errors.New("bad")
	if err := ThrowUnless(false, myErr); err != myErr {
		t.Errorf("got %v", err)
	}
	if err := ThrowUnless(true, myErr); err != nil {
		t.Errorf("got %v", err)
	}
}

func TestNowAndToday(t *testing.T) {
	defer SetClock(nil)
	fixed := time.Date(2026, 5, 7, 14, 30, 0, 0, time.UTC)
	SetClock(func() time.Time { return fixed })

	if !Now().Equal(fixed) {
		t.Errorf("Now did not honor SetClock")
	}
	today := Today()
	want := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	if !today.Equal(want) {
		t.Errorf("Today: got %v, want %v", today, want)
	}
}

func TestEnv(t *testing.T) {
	t.Setenv("X_GOKIT_TEST_STR", "hello")
	t.Setenv("X_GOKIT_TEST_BOOL", "true")
	t.Setenv("X_GOKIT_TEST_INT", "42")
	t.Setenv("X_GOKIT_TEST_FLOAT", "3.14")
	os.Unsetenv("X_GOKIT_TEST_MISSING")

	if got := Env("X_GOKIT_TEST_STR", "default"); got != "hello" {
		t.Errorf("string env: got %q", got)
	}
	if got := Env("X_GOKIT_TEST_BOOL", false); got != true {
		t.Errorf("bool env: got %v", got)
	}
	if got := Env("X_GOKIT_TEST_INT", 0); got != 42 {
		t.Errorf("int env: got %d", got)
	}
	if got := Env("X_GOKIT_TEST_FLOAT", 0.0); got != 3.14 {
		t.Errorf("float env: got %v", got)
	}
	// Missing var returns default
	if got := Env("X_GOKIT_TEST_MISSING", "fallback"); got != "fallback" {
		t.Errorf("missing env: got %q", got)
	}
	// Bad parse returns default
	t.Setenv("X_GOKIT_TEST_BADINT", "not-a-number")
	if got := Env("X_GOKIT_TEST_BADINT", 99); got != 99 {
		t.Errorf("bad int env: got %d (should fall back)", got)
	}
}

func TestOnceFn(t *testing.T) {
	var calls atomic.Int32
	fn := OnceFn(func() int {
		calls.Add(1)
		return 7
	})
	a, b, c := fn(), fn(), fn()
	if a != 7 || b != 7 || c != 7 {
		t.Errorf("results: %d %d %d", a, b, c)
	}
	if calls.Load() != 1 {
		t.Errorf("calls: got %d, want 1", calls.Load())
	}
}
