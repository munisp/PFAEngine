// Security hardening middleware (Tranche 10, NFR-02/NFR-10): per-client-IP
// token-bucket rate limiting, request body size caps, and security response
// headers. Standard library only.
package httpsrv

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/authn"
)

// ipBucket is one client's token bucket.
type ipBucket struct {
	tokens float64
	last   time.Time
}

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*ipBucket
	rps     float64
	burst   float64
}

// allow consumes one token for key, refilling at rps tokens/second up to
// burst.
func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, seen := rl.buckets[key]
	if !seen {
		b = &ipBucket{tokens: rl.burst, last: now}
		rl.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * rl.rps
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	// Bound memory: sweep idle buckets once the map grows large.
	if len(rl.buckets) > 10000 {
		cutoff := now.Add(-10 * time.Minute)
		for k, v := range rl.buckets {
			if v.last.Before(cutoff) {
				delete(rl.buckets, k)
			}
		}
	}
	return true
}

// RateLimit returns middleware applying a per-client-IP token bucket
// (rps tokens/second, burst capacity). Excess requests get 429 RATE_LIMITED
// in the canonical envelope with a Retry-After header.
func RateLimit(rps, burst int) Middleware {
	if rps <= 0 {
		rps = 1
	}
	if burst <= 0 {
		burst = rps
	}
	rl := &rateLimiter{buckets: map[string]*ipBucket{}, rps: float64(rps), burst: float64(burst)}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}
			if ip == "" {
				ip = "unknown"
			}
			if !rl.allow(ip) {
				w.Header().Set("Retry-After", "1")
				WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED",
					errors.New("rate limit exceeded"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MaxBody returns middleware capping request bodies at n bytes. Requests
// declaring a larger Content-Length are rejected up front with
// 413 BODY_TOO_LARGE; bodies that over-read anyway (chunked/lied length)
// are truncated by MaxBytesReader and surface as a handler-side 400.
func MaxBody(n int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > n {
				WriteError(w, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE",
					errors.New("request body exceeds limit"))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders sets hardening response headers on every response:
// X-Content-Type-Options: nosniff, X-Frame-Options: DENY, and
// Cache-Control: no-store on /api responses (tenant data must not be cached).
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// OptionalAuth validates a bearer token when one is presented: a malformed
// or invalid token is rejected with 401, while requests without an
// Authorization header pass through. This is the dev-mode bridge until the
// identity service is wired into the gateway chain; use Auth for hard
// enforcement.
func OptionalAuth(svc *authn.Service) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := strings.TrimSpace(r.Header.Get("Authorization"))
			if h == "" {
				next.ServeHTTP(w, r)
				return
			}
			token, ok := strings.CutPrefix(h, "Bearer ")
			if !ok || strings.TrimSpace(token) == "" {
				WriteError(w, http.StatusUnauthorized, "AUTH_INVALID",
					errors.New("malformed Authorization header"))
				return
			}
			if _, _, err := svc.VerifyToken(strings.TrimSpace(token)); err != nil {
				WriteError(w, http.StatusUnauthorized, "AUTH_INVALID", err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
