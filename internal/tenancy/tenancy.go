// Package tenancy implements NFR-10 / BR-05: strict tenant isolation,
// per-tenant branding and feature flags, sub-1-day tenant provisioning.
package tenancy

import (
	"context"
	"errors"
	"sync"
	"time"
)

type ctxKey struct{}

var ErrNoTenant = errors.New("tenancy: no tenant in context")

// Tenant is a licensed PFA operating on the platform.
type Tenant struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	LicenseNo    string            `json:"license_no"`    // PenCom licence number
	Schema       string            `json:"schema"`        // per-tenant PostgreSQL schema (NFR-10)
	Deployment   string            `json:"deployment"`    // shared-saas | dedicated-vpc | on-prem (REG-05)
	Jurisdiction string            `json:"jurisdiction"`  // NG, GH, KE, ZA, EG... (REG-01)
	BaseCurrency string            `json:"base_currency"` // REG-02
	Branding     map[string]string `json:"branding"`      // PLT-14 white-label
	FeatureFlags map[string]bool   `json:"feature_flags"` // NFR-20
	CreatedAt    time.Time         `json:"created_at"`
}

func WithTenant(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, tenantID)
}

func FromContext(ctx context.Context) (string, error) {
	v, ok := ctx.Value(ctxKey{}).(string)
	if !ok || v == "" {
		return "", ErrNoTenant
	}
	return v, nil
}

// Registry provisions and manages tenants (BR-05: <1 day).
type Registry struct {
	mu      sync.Mutex
	tenants map[string]*Tenant
}

func NewRegistry() *Registry { return &Registry{tenants: map[string]*Tenant{}} }

// Provision creates a tenant with schema, branding and flags — the entire
// operation is synchronous configuration, measured in seconds (BR-05).
func (r *Registry) Provision(t Tenant) (*Tenant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t.ID == "" || t.Name == "" {
		return nil, errors.New("tenancy: id and name required")
	}
	if t.Schema == "" {
		t.Schema = "tenant_" + t.ID
	}
	if t.Deployment == "" {
		t.Deployment = "shared-saas"
	}
	if t.Jurisdiction == "" {
		t.Jurisdiction = "NG"
	}
	if t.BaseCurrency == "" {
		t.BaseCurrency = "NGN"
	}
	if t.Branding == nil {
		t.Branding = map[string]string{}
	}
	if t.FeatureFlags == nil {
		t.FeatureFlags = map[string]bool{"ussd": true, "whatsapp": true, "ppp": false}
	}
	t.CreatedAt = time.Now().UTC()
	cp := t
	r.tenants[t.ID] = &cp
	return &cp, nil
}

func (r *Registry) Get(id string) (*Tenant, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tenants[id]
	return t, ok
}

// FeatureEnabled reports whether the named feature flag (e.g. "whatsapp")
// is on for the tenant. The model is opt-out: an absent flag defaults to
// enabled; only an explicit false disables the feature (NFR-20).
func (t *Tenant) FeatureEnabled(name string) bool {
	v, ok := t.FeatureFlags[name]
	return !ok || v
}

func (r *Registry) List() []Tenant {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Tenant, 0, len(r.tenants))
	for _, t := range r.tenants {
		out = append(out, *t)
	}
	return out
}

// ResidencyAttestation generates the per-tenant data-residency report (REG-05).
func (r *Registry) ResidencyAttestation(t *Tenant) map[string]string {
	region := "aws:af-south-1"
	if t.Deployment == "on-prem" {
		region = "tenant-datacentre"
	}
	return map[string]string{
		"tenant_id": t.ID, "deployment": t.Deployment, "hosting_region": region,
		"jurisdiction": t.Jurisdiction, "framework": "NDPA 2023 / PenCom",
	}
}
