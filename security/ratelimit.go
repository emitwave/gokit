package security

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimitOptions configures the RateLimit middleware.
type RateLimitOptions struct {
	// RPS is the steady-state request rate per key. Required.
	RPS float64

	// Burst is the maximum number of requests allowed at once. If 0,
	// it defaults to ceil(RPS) — enough to absorb a quick double-tap
	// without permitting sustained bursts.
	Burst int

	// KeyFunc derives the rate-limit key from a request. Defaults to
	// client IP (with proxy-aware extraction). Override to limit by
	// user ID, API key, etc.
	KeyFunc func(*http.Request) string

	// TrustForwardedFor controls whether the default KeyFunc reads
	// X-Forwarded-For / X-Real-IP. Only enable behind a trusted proxy
	// — otherwise clients can spoof these headers to evade limits.
	TrustForwardedFor bool

	// CleanupInterval is how often we evict idle entries from the in-
	// memory map. 0 → 5 minutes. Idle = haven't been seen in 2x
	// CleanupInterval.
	CleanupInterval time.Duration

	// OnLimit is called when a request is rejected. The default writes
	// a 429 with a JSON body and Retry-After header.
	OnLimit http.HandlerFunc
}

// RateLimit returns middleware that limits requests per key (default: IP)
// using a token-bucket algorithm. State is held in-memory; for multi-
// instance deployments, replace the limiter store with a shared backend
// (Redis) — the public API stays the same.
func RateLimit(opts RateLimitOptions) Middleware {
	if opts.RPS <= 0 {
		// Fail loudly during construction rather than silently letting
		// every request through. A misconfigured limiter is a security
		// regression.
		panic("security.RateLimit: RPS must be > 0")
	}
	if opts.Burst <= 0 {
		opts.Burst = int(opts.RPS) + 1
	}
	if opts.CleanupInterval <= 0 {
		opts.CleanupInterval = 5 * time.Minute
	}
	if opts.KeyFunc == nil {
		trust := opts.TrustForwardedFor
		opts.KeyFunc = func(r *http.Request) string {
			return clientIP(r, trust)
		}
	}
	if opts.OnLimit == nil {
		opts.OnLimit = defaultLimitHandler
	}

	store := newLimiterStore(rate.Limit(opts.RPS), opts.Burst, opts.CleanupInterval)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := opts.KeyFunc(r)
			lim := store.get(key)

			// Reservation lets us populate Retry-After accurately when we
			// reject — Allow() alone gives a yes/no with no timing info.
			res := lim.Reserve()
			if !res.OK() || res.Delay() > 0 {
				if res.OK() {
					retry := res.Delay()
					res.Cancel() // give the token back
					w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
				}
				w.Header().Set("X-RateLimit-Limit", strconv.FormatFloat(opts.RPS, 'f', -1, 64))
				opts.OnLimit(w, r)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func defaultLimitHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"error":"rate_limit_exceeded","message":"Too many requests."}`))
}

// ---------- limiter store ------------------------------------------------

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type limiterStore struct {
	mu      sync.Mutex
	entries map[string]*limiterEntry
	rps     rate.Limit
	burst   int
}

func newLimiterStore(rps rate.Limit, burst int, cleanupEvery time.Duration) *limiterStore {
	s := &limiterStore{
		entries: make(map[string]*limiterEntry),
		rps:     rps,
		burst:   burst,
	}
	go s.cleanupLoop(cleanupEvery)
	return s
}

func (s *limiterStore) get(key string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[key]; ok {
		e.lastSeen = time.Now()
		return e.limiter
	}
	lim := rate.NewLimiter(s.rps, s.burst)
	s.entries[key] = &limiterEntry{limiter: lim, lastSeen: time.Now()}
	return lim
}

// cleanupLoop evicts entries that haven't been seen recently. Without
// this the map grows unboundedly under per-IP keying — a small but real
// DoS amplifier.
func (s *limiterStore) cleanupLoop(every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	idleThreshold := every * 2
	for range ticker.C {
		cutoff := time.Now().Add(-idleThreshold)
		s.mu.Lock()
		for k, e := range s.entries {
			if e.lastSeen.Before(cutoff) {
				delete(s.entries, k)
			}
		}
		s.mu.Unlock()
	}
}

// ---------- IP extraction ------------------------------------------------

// clientIP extracts the client IP from r. When trustProxy is true, we
// honor X-Forwarded-For (first entry, which is the original client per
// the spec) and X-Real-IP. Otherwise we use the direct peer address —
// the only thing we can fully trust.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// XFF is a comma-separated list; first is original client.
			if idx := strings.Index(xff, ","); idx >= 0 {
				return strings.TrimSpace(xff[:idx])
			}
			return strings.TrimSpace(xff)
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			return strings.TrimSpace(xri)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr can occasionally be just an IP (e.g. unix sockets
		// or test servers); use it as-is in that case.
		return r.RemoteAddr
	}
	return host
}
