package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// ---------- Chain --------------------------------------------------------

func TestChainOrder(t *testing.T) {
	var order []string
	mw := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+":before")
				next.ServeHTTP(w, r)
				order = append(order, name+":after")
			})
		}
	}

	stack := Chain(mw("A"), mw("B"), mw("C"))
	srv := httptest.NewServer(stack(okHandler()))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	want := []string{"A:before", "B:before", "C:before", "C:after", "B:after", "A:after"}
	if len(order) != len(want) {
		t.Fatalf("got %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("step %d: got %q, want %q", i, order[i], want[i])
		}
	}
}

// ---------- Headers ------------------------------------------------------

func TestHeadersDefaults(t *testing.T) {
	srv := httptest.NewServer(Headers()(okHandler()))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	wants := map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
		"Referrer-Policy":              "strict-origin-when-cross-origin",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
	}
	for k, want := range wants {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s: got %q, want %q", k, got, want)
		}
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("CSP should be set by default")
	}
}

func TestHeadersOverride(t *testing.T) {
	srv := httptest.NewServer(Headers(HeaderOptions{
		FrameOptions: "SAMEORIGIN",
	})(okHandler()))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options: got %q", got)
	}
}

func TestHeadersOff(t *testing.T) {
	srv := httptest.NewServer(Headers(HeaderOptions{
		FrameOptions: "off",
	})(okHandler()))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Frame-Options"); got != "" {
		t.Errorf("X-Frame-Options should be off, got %q", got)
	}
}

// ---------- CORS ---------------------------------------------------------

func TestCORSAllowedOrigin(t *testing.T) {
	srv := httptest.NewServer(CORS(CORSOptions{
		AllowedOrigins:   []string{"https://app.emitwave.com"},
		AllowCredentials: true,
	})(okHandler()))
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("Origin", "https://app.emitwave.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://app.emitwave.com" {
		t.Errorf("ACAO: got %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("ACAC: got %q", got)
	}
}

func TestCORSDisallowedOrigin(t *testing.T) {
	srv := httptest.NewServer(CORS(CORSOptions{
		AllowedOrigins: []string{"https://app.emitwave.com"},
	})(okHandler()))
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("disallowed origin should not get ACAO header, got %q", got)
	}
}

func TestCORSPreflight(t *testing.T) {
	srv := httptest.NewServer(CORS(CORSOptions{
		AllowedOrigins: []string{"https://app.emitwave.com"},
		MaxAge:         600,
	})(okHandler()))
	defer srv.Close()

	req, _ := http.NewRequest("OPTIONS", srv.URL, nil)
	req.Header.Set("Origin", "https://app.emitwave.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("preflight status: got %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Max-Age"); got != "600" {
		t.Errorf("max-age: got %q", got)
	}
	if !strings.Contains(resp.Header.Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("ACAM should include POST: %q", resp.Header.Get("Access-Control-Allow-Methods"))
	}
}

// ---------- RequestID ----------------------------------------------------

func TestRequestIDGenerated(t *testing.T) {
	var captured string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = RequestIDFromContext(r.Context())
	})
	srv := httptest.NewServer(RequestID()(handler))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if captured == "" {
		t.Error("expected request ID in context")
	}
	if resp.Header.Get("X-Request-ID") != captured {
		t.Error("response header should match context value")
	}
}

func TestRequestIDFromClient(t *testing.T) {
	var captured string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = RequestIDFromContext(r.Context())
	})
	srv := httptest.NewServer(RequestID()(handler))
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("X-Request-ID", "client-supplied-id")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if captured != "client-supplied-id" {
		t.Errorf("expected client-supplied id, got %q", captured)
	}
}

// ---------- Recovery -----------------------------------------------------

type fakeLogger struct {
	called atomic.Int32
	last   string
}

func (l *fakeLogger) Error(msg string, _ ...any) {
	l.called.Add(1)
	l.last = msg
}

func TestRecovery(t *testing.T) {
	logger := &fakeLogger{}
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	srv := httptest.NewServer(Recovery(RecoveryOptions{Logger: logger})(panicHandler))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", resp.StatusCode)
	}
	if logger.called.Load() != 1 {
		t.Errorf("logger called %d times, want 1", logger.called.Load())
	}
}

// ---------- RateLimit ----------------------------------------------------

func TestRateLimitAllowsBurst(t *testing.T) {
	mw := RateLimit(RateLimitOptions{RPS: 1, Burst: 3})
	srv := httptest.NewServer(mw(okHandler()))
	defer srv.Close()

	// 3 requests within the burst window should succeed.
	for i := 0; i < 3; i++ {
		resp, err := http.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("request %d: got %d, want 200", i, resp.StatusCode)
		}
	}
}

func TestRateLimitRejects(t *testing.T) {
	mw := RateLimit(RateLimitOptions{RPS: 1, Burst: 1})
	srv := httptest.NewServer(mw(okHandler()))
	defer srv.Close()

	// First request consumes the only burst token.
	resp, _ := http.Get(srv.URL)
	resp.Body.Close()

	// Second immediate request should be rejected.
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("got %d, want 429", resp.StatusCode)
	}

	// After ~1 second a token should refill.
	time.Sleep(1100 * time.Millisecond)
	resp, _ = http.Get(srv.URL)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("after refill: got %d, want 200", resp.StatusCode)
	}
}
