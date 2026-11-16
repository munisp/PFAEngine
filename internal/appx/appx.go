// Package appx provides shared HTTP plumbing for all services:
// JSON responses, tenant extraction (all endpoints tenant-scoped, §6),
// health checks, and graceful startup.
package appx

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/munisp/pfaengine/internal/tenancy"
)

type ErrorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func Err(w http.ResponseWriter, status int, code string, err error) {
	JSON(w, status, ErrorBody{Error: err.Error(), Code: code})
}

func Decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// TenantMiddleware enforces tenant scoping via X-Tenant-ID (dev mode; the
// gateway resolves tenant from host/claims in production, NFR-10).
func TenantMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tid := r.Header.Get("X-Tenant-ID")
		if tid == "" {
			parts := strings.Split(r.Host, ".")
			if len(parts) > 2 {
				tid = parts[0]
			}
		}
		if tid == "" {
			Err(w, http.StatusBadRequest, "TENANT_REQUIRED", errors.New("X-Tenant-ID header or tenant subdomain required"))
			return
		}
		next.ServeHTTP(w, r.WithContext(tenancy.WithTenant(r.Context(), tid)))
	})
}

func TenantOf(r *http.Request) string {
	tid, _ := tenancy.FromContext(r.Context())
	return tid
}

func Health(mux *http.ServeMux, service string) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		JSON(w, 200, map[string]string{"status": "ok", "service": service})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		JSON(w, 200, map[string]string{"status": "ready", "service": service})
	})
}

// PathValue is a tiny helper since we use stdlib mux patterns.
func Path(r *http.Request, name string) string { return r.PathValue(name) }

// Serve starts the HTTP server with graceful shutdown.
func Serve(name, port string, handler http.Handler) {
	srv := &http.Server{Addr: ":" + port, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("[%s] listening on :%s", name, port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[%s] %v", name, err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Printf("[%s] stopped", name)
}

func Env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
