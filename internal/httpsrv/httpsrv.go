// Package httpsrv is the production HTTP service layer for PFAEngine
// (Tranche 8, NFR-02/NFR-10): a Server wrapping net/http with liveness and
// readiness probes, graceful shutdown on SIGTERM/SIGINT, a consistent JSON
// error envelope, request-ID propagation, tenant enforcement, and an
// authn-backed bearer-token middleware hook (tokens are issued only after
// password + TOTP verification in internal/authn).
package httpsrv

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/munisp/pfaengine/internal/authn"
	"github.com/munisp/pfaengine/internal/tenancy"
)

// RequestIDHeader carries the correlation ID across services.
const RequestIDHeader = "X-Request-ID"

// TenantHeader scopes every API call to a licensed PFA tenant (NFR-10).
const TenantHeader = "X-Tenant-ID"

type ctxKey struct{ name string }

var requestIDKey = ctxKey{"request-id"}

// ErrorBody is the canonical JSON error envelope: {"error","code"}.
type ErrorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// WriteJSON writes v as JSON with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes the canonical error envelope.
func WriteError(w http.ResponseWriter, status int, code string, err error) {
	WriteJSON(w, status, ErrorBody{Error: err.Error(), Code: code})
}

// Middleware is the standard net/http middleware signature.
type Middleware func(http.Handler) http.Handler

// RequestID propagates X-Request-ID (generating one when absent), stores it
// in the request context, echoes it on the response, and forwards it to
// upstreams via the request header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get(RequestIDHeader))
		if id == "" {
			id = newRequestID()
		}
		r.Header.Set(RequestIDHeader, id)
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// RequestIDOf extracts the correlation ID from a request context.
func RequestIDOf(r *http.Request) string {
	id, _ := r.Context().Value(requestIDKey).(string)
	return id
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req-unknown"
	}
	return "req-" + hex.EncodeToString(b[:])
}

// Tenant enforces tenant scoping: requests without X-Tenant-ID are rejected
// with 400 TENANT_REQUIRED; otherwise the tenant ID is stored in context.
// Mount it only around tenant-scoped routes — health probes stay public.
func Tenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tid := strings.TrimSpace(r.Header.Get(TenantHeader))
		if tid == "" {
			WriteError(w, http.StatusBadRequest, "TENANT_REQUIRED",
				errors.New(TenantHeader+" header required"))
			return
		}
		next.ServeHTTP(w, r.WithContext(tenancy.WithTenant(r.Context(), tid)))
	})
}

// TenantOf extracts the tenant ID placed by Tenant middleware.
func TenantOf(r *http.Request) string {
	tid, _ := tenancy.FromContext(r.Context())
	return tid
}

// Auth returns middleware requiring a valid authn-issued bearer token.
// authn.Login only issues tokens after password + TOTP (RFC 6238) checks,
// so a valid token implies MFA. When roles are given, RBAC is enforced.
func Auth(svc *authn.Service, roles ...authn.Role) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			token, ok := strings.CutPrefix(h, "Bearer ")
			if !ok || strings.TrimSpace(token) == "" {
				WriteError(w, http.StatusUnauthorized, "AUTH_REQUIRED",
					errors.New("bearer token required"))
				return
			}
			_, have, err := svc.VerifyToken(strings.TrimSpace(token))
			if err != nil {
				WriteError(w, http.StatusUnauthorized, "AUTH_INVALID", err)
				return
			}
			for _, need := range roles {
				if err := authn.RequireRole(have, need); err != nil {
					WriteError(w, http.StatusForbidden, "FORBIDDEN", err)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Server wraps http.Server with health probes and graceful shutdown.
type Server struct {
	name    string
	srv     *http.Server
	ready   func() bool
	timeout time.Duration
}

// Option customises a Server.
type Option func(*Server)

// WithReady sets the readiness hook consulted by /readyz (default: ready).
func WithReady(f func() bool) Option {
	return func(s *Server) {
		if f != nil {
			s.ready = f
		}
	}
}

// WithShutdownTimeout overrides the default 10s graceful-shutdown window.
func WithShutdownTimeout(d time.Duration) Option {
	return func(s *Server) {
		if d > 0 {
			s.timeout = d
		}
	}
}

// New builds a Server listening on addr. The handler is mounted under the
// request-ID middleware; /healthz (liveness, always 200) and /readyz
// (readiness via the Ready hook) are registered outside it so probes never
// require tenant or auth headers.
func New(name, addr string, handler http.Handler, opts ...Option) *Server {
	s := &Server{
		name:    name,
		ready:   func() bool { return true },
		timeout: 10 * time.Second,
	}
	for _, o := range opts {
		o(s)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": s.name})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.ready() {
			WriteJSON(w, http.StatusOK, map[string]string{"status": "ready", "service": s.name})
			return
		}
		WriteError(w, http.StatusServiceUnavailable, "NOT_READY",
			errors.New("service "+s.name+" is not ready"))
	})
	mux.Handle("/", RequestID(handler))
	s.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

// Run starts the server and blocks until SIGINT/SIGTERM, then drains
// in-flight requests within the shutdown timeout (default 10s).
func (s *Server) Run() error {
	errCh := make(chan error, 1)
	go func() {
		log.Printf("[%s] listening on %s", s.name, s.srv.Addr)
		errCh <- s.srv.ListenAndServe()
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case sig := <-stop:
		log.Printf("[%s] received %s; draining (timeout %s)", s.name, sig, s.timeout)
		ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			return err
		}
		log.Printf("[%s] stopped", s.name)
		return nil
	}
}

// Serve runs the server on an existing listener (tests, socket activation).
func (s *Server) Serve(ln net.Listener) error {
	err := s.srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown gracefully drains in-flight requests.
func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

// Getenv returns the environment variable value or def when unset.
func Getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
