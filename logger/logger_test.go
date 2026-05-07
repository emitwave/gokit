package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emitwave/gokit/security"
)

func TestNewJSONLogger(t *testing.T) {
	var buf bytes.Buffer
	log := New(Config{Format: "json", Output: &buf, Level: slog.LevelDebug})
	log.Info("hello", "key", "value")

	var rec map[string]any
	if err := json.NewDecoder(&buf).Decode(&rec); err != nil {
		t.Fatalf("output should be parseable JSON: %v", err)
	}
	if rec["msg"] != "hello" {
		t.Errorf("msg: got %v", rec["msg"])
	}
	if rec["key"] != "value" {
		t.Errorf("attr: got %v", rec["key"])
	}
}

func TestRedaction(t *testing.T) {
	var buf bytes.Buffer
	log := New(Config{
		Format:     "json",
		Output:     &buf,
		RedactKeys: []string{"password", "Authorization"},
	})
	log.Info("login", "user", "tunde", "password", "swordfish", "Authorization", "Bearer xyz")

	var rec map[string]any
	_ = json.NewDecoder(&buf).Decode(&rec)
	if rec["password"] != "[REDACTED]" {
		t.Errorf("password leaked: %v", rec["password"])
	}
	if rec["Authorization"] != "[REDACTED]" {
		t.Errorf("authorization leaked: %v", rec["Authorization"])
	}
	if rec["user"] != "tunde" {
		t.Errorf("non-redacted field changed: %v", rec["user"])
	}
}

func TestContextPropagation(t *testing.T) {
	var buf bytes.Buffer
	base := New(Config{Format: "json", Output: &buf})
	ctx := IntoContext(context.Background(), base)

	got := FromContext(ctx)
	if got != base {
		t.Errorf("expected to retrieve the same logger from context")
	}
}

func TestFromContextFallback(t *testing.T) {
	// FromContext on a bare context should return a non-nil logger so
	// handlers can call methods on it without a nil check.
	got := FromContext(context.Background())
	if got == nil {
		t.Fatal("FromContext should never return nil")
	}
	got.Info("just making sure this doesn't panic")
}

func TestHTTPMiddleware(t *testing.T) {
	var buf bytes.Buffer
	base := New(Config{Format: "json", Output: &buf})

	// Stack: RequestID → HTTPMiddleware → handler.
	// We need RequestID first so the logger middleware can see the ID.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		FromContext(r.Context()).Info("inside handler")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	})

	stack := security.RequestID()(HTTPMiddleware(base)(handler))

	req := httptest.NewRequest("GET", "/x", nil)
	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, req)

	// Two log lines should have been emitted: the handler's "inside
	// handler" and the middleware's "http request" summary.
	lines := splitLines(buf.String())
	if len(lines) < 2 {
		t.Fatalf("expected >=2 log lines, got %d:\n%s", len(lines), buf.String())
	}

	// Both should carry request_id, method, path.
	for i, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d not JSON: %v\n%s", i, err, line)
		}
		if rec["request_id"] == "" || rec["request_id"] == nil {
			t.Errorf("line %d missing request_id: %s", i, line)
		}
		if rec["method"] != "GET" {
			t.Errorf("line %d wrong method: %v", i, rec["method"])
		}
	}

	// The summary line should record the 201 status.
	var summary map[string]any
	_ = json.Unmarshal([]byte(lines[len(lines)-1]), &summary)
	if summary["status"] != float64(http.StatusCreated) {
		t.Errorf("status in summary: got %v, want 201", summary["status"])
	}
}

func TestHTTPMiddlewareSkipPaths(t *testing.T) {
	var buf bytes.Buffer
	base := New(Config{Format: "json", Output: &buf})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	stack := HTTPMiddleware(base, HTTPOptions{SkipPaths: []string{"/healthz"}})(handler)

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, req)

	if buf.Len() != 0 {
		t.Errorf("skipped path should not log, got: %s", buf.String())
	}
}

func TestHTTPMiddlewareLogsErrorAt500(t *testing.T) {
	var buf bytes.Buffer
	base := New(Config{Format: "json", Output: &buf})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	stack := HTTPMiddleware(base)(handler)

	req := httptest.NewRequest("GET", "/oops", nil)
	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, req)

	var summary map[string]any
	_ = json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &summary)
	if summary["level"] != "ERROR" {
		t.Errorf("500 should log at ERROR, got level %v", summary["level"])
	}
}

func splitLines(s string) []string {
	out := []string{}
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
