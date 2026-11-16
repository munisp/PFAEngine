// api-gateway: BFF for channels, tenant routing, rate limits, webhook
// dispatch (§4; p95 <300ms target, per-tenant keys, NFR-02).
package main

import (
	"net/http"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/events"
	"github.com/munisp/pfaengine/internal/webhk"
)

// tokenBucket is a simple per-tenant rate limiter.
type tokenBucket struct {
	mu     sync.Mutex
	tokens map[string]int
	last   map[string]time.Time
	rate   int // tokens per second
	burst  int
}

func newBucket(rate, burst int) *tokenBucket {
	return &tokenBucket{tokens: map[string]int{}, last: map[string]time.Time{}, rate: rate, burst: burst}
}

func (b *tokenBucket) Allow(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	toks, ok := b.tokens[key]
	if !ok {
		toks = b.burst
	}
	if last, ok := b.last[key]; ok {
		toks += int(now.Sub(last).Seconds()) * b.rate
		if toks > b.burst {
			toks = b.burst
		}
	}
	b.last[key] = now
	if toks <= 0 {
		return false
	}
	b.tokens[key] = toks - 1
	return true
}

func main() {
	bus := events.NewBus()
	disp := webhk.NewDispatcher(nil) // nil sender = log-only mode
	bus.Subscribe(events.TopicContributionPosted, func(e events.Envelope) { disp.Dispatch(e) })
	bus.Subscribe(events.TopicKYCVerified, func(e events.Envelope) { disp.Dispatch(e) })
	bus.Subscribe(events.TopicClaimDecided, func(e events.Envelope) { disp.Dispatch(e) })
	bus.Subscribe(events.TopicTransferStateChanged, func(e events.Envelope) { disp.Dispatch(e) })
	limiter := newBucket(50, 100)

	mux := http.NewServeMux()
	appx.Health(mux, "api-gateway")

	mux.HandleFunc("POST /v1/webhooks", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			URL    string   `json:"url"`
			Secret string   `json:"secret"`
			Events []string `json:"events"`
		}
		if err := appx.Decode(r, &in); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		ep := disp.Register(webhk.Endpoint{TenantID: appx.TenantOf(r), URL: in.URL, Secret: in.Secret, Events: in.Events})
		appx.JSON(w, 201, ep)
	})

	mux.HandleFunc("GET /v1/webhooks/deliveries", func(w http.ResponseWriter, r *http.Request) {
		appx.JSON(w, 200, disp.Deliveries())
	})

	// Dev event ingress (in production: Kafka consumers drive dispatch).
	mux.HandleFunc("POST /v1/events", func(w http.ResponseWriter, r *http.Request) {
		var e events.Envelope
		if err := appx.Decode(r, &e); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		e.TenantID = appx.TenantOf(r)
		bus.Publish(e)
		appx.JSON(w, 202, map[string]string{"status": "published"})
	})

	appx.Serve("api-gateway", appx.Env("PORT", "8080"), appx.TenantMiddleware(rateLimited(limiter, mux)))
}

func rateLimited(b *tokenBucket, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !b.Allow(appx.TenantOf(r)) {
			appx.JSON(w, 429, map[string]string{"error": "rate limit exceeded", "code": "RATE_LIMITED"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
