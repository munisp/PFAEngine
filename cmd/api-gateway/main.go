// api-gateway: BFF for channels, tenant routing, rate limits, webhook
// dispatch (§4; p95 <300ms target, per-tenant keys, NFR-02).
// Tranche 8: path-prefix reverse-proxy routing to backend services with
// env-driven service discovery, aggregated /readyz across backends, and
// the shared httpsrv server (probes, request IDs, graceful shutdown).
package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/events"
	"github.com/munisp/pfaengine/internal/httpsrv"
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

// backend is an upstream service resolved from the environment.
type backend struct {
	name   string
	prefix string // path prefix routed to this service
	base   *url.URL
}

// backends resolves service URLs from env vars (one per service), falling
// back to the docker-compose defaults.
func backends() []backend {
	specs := []struct {
		name, prefix, env, def string
	}{
		{"member-svc", "/api/v1/members", "MEMBER_SERVICE_URL", "http://member-svc:8096"},
		{"member-svc", "/api/v1/contributions", "CONTRIBUTION_SERVICE_URL", "http://member-svc:8096"},
		{"member-svc", "/api/v1/recon", "RECON_SERVICE_URL", "http://member-svc:8096"},
		{"kyc", "/api/v1/kyc", "KYC_SERVICE_URL", "http://kyc:8082"},
		{"benefit", "/api/v1/benefits", "BENEFIT_SERVICE_URL", "http://benefit:8092"},
		{"transfer", "/api/v1/transfers", "TRANSFER_SERVICE_URL", "http://transfer:8093"},
	}
	out := make([]backend, 0, len(specs))
	for _, s := range specs {
		u, err := url.Parse(httpsrv.Getenv(s.env, s.def))
		if err != nil {
			continue // invalid env URL: leave route unmounted rather than crash
		}
		out = append(out, backend{name: s.name, prefix: s.prefix, base: u})
	}
	return out
}

// proxyFor builds a reverse proxy that forwards the path unchanged and
// surfaces upstream failures in the canonical error envelope.
func proxyFor(b backend) http.Handler {
	p := httputil.NewSingleHostReverseProxy(b.base)
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		httpsrv.WriteError(w, http.StatusBadGateway, "UPSTREAM_UNAVAILABLE", err)
	}
	return p
}

// allReady reports whether every distinct backend base answers /healthz.
func allReady(bs []backend) func() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	return func() bool {
		seen := map[string]bool{}
		for _, b := range bs {
			if seen[b.base.String()] {
				continue
			}
			seen[b.base.String()] = true
			resp, err := client.Get(b.base.String() + "/healthz")
			if err != nil {
				return false
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return false
			}
		}
		return true
	}
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

	// Tranche 8: path-prefix routing to backend services (/api/v1/...).
	bs := backends()
	for _, b := range bs {
		p := proxyFor(b)
		mux.Handle(b.prefix+"/", p)
		mux.Handle(b.prefix, p)
	}

	// Webhook management + dev event ingress (tenant-scoped, rate limited).
	tenantMux := http.NewServeMux()
	tenantMux.HandleFunc("POST /v1/webhooks", func(w http.ResponseWriter, r *http.Request) {
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
	tenantMux.HandleFunc("GET /v1/webhooks/deliveries", func(w http.ResponseWriter, r *http.Request) {
		appx.JSON(w, 200, disp.Deliveries())
	})
	// Dev event ingress (in production: Kafka consumers drive dispatch).
	tenantMux.HandleFunc("POST /v1/events", func(w http.ResponseWriter, r *http.Request) {
		var e events.Envelope
		if err := appx.Decode(r, &e); err != nil {
			appx.Err(w, 400, "BAD_REQUEST", err)
			return
		}
		e.TenantID = appx.TenantOf(r)
		bus.Publish(e)
		appx.JSON(w, 202, map[string]string{"status": "published"})
	})
	mux.Handle("/v1/", httpsrv.Tenant(rateLimited(limiter, tenantMux)))

	srv := httpsrv.New("api-gateway", ":"+httpsrv.Getenv("PORT", "8080"), mux,
		httpsrv.WithReady(allReady(bs)))
	log.Fatal(srv.Run())
}

func rateLimited(b *tokenBucket, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !b.Allow(httpsrv.TenantOf(r)) {
			httpsrv.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED",
				errString("rate limit exceeded"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

type errString string

func (e errString) Error() string { return string(e) }
