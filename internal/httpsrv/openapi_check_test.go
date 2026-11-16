package httpsrv

import (
	"testing"
)

const miniSpec = `
openapi: 3.0.3
info: { title: t, version: v }
paths:
  /v1/members:
    post: { responses: { '201': { description: Created } } }
  /v1/members/{id}/balance:
    get: { responses: { '200': { description: Balance } } }
`

func TestOpenAPIRoutesParsesOperations(t *testing.T) {
	routes, err := OpenAPIRoutes([]byte(miniSpec))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /v1/members/{id}/balance", "POST /v1/members"}
	if len(routes) != len(want) {
		t.Fatalf("routes = %v, want %v", routes, want)
	}
	for i := range want {
		if routes[i] != want[i] {
			t.Fatalf("routes[%d] = %q, want %q", i, routes[i], want[i])
		}
	}
}

func TestNormalizeRouteStripsAPIPrefix(t *testing.T) {
	cases := map[string]string{
		"POST /api/v1/members":  "POST /v1/members",
		"GET /v1/members":       "GET /v1/members",
		"GET /api/v1/members/":  "GET /v1/members",
		"GET /v1/members/{id}/": "GET /v1/members/{id}",
	}
	for in, want := range cases {
		if got := NormalizeRoute(in); got != want {
			t.Errorf("NormalizeRoute(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestContractDiffBothDirections(t *testing.T) {
	documented := []string{"POST /v1/members", "GET /v1/members/{id}/balance", "GET /v1/kyc/status"}
	implemented := []string{"POST /api/v1/members", "GET /api/v1/members/{id}/balance", "GET /api/v1/debug"}
	whitelist := map[string]string{"GET /v1/kyc/status": "served by cmd/kyc"}

	missing, extra := ContractDiff(documented, implemented, whitelist)
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none (kyc whitelisted)", missing)
	}
	if len(extra) != 1 || extra[0] != "GET /v1/debug" {
		t.Fatalf("extra = %v, want [GET /v1/debug]", extra)
	}

	// Without the whitelist the kyc route must surface as missing.
	missing, _ = ContractDiff(documented, implemented, nil)
	if len(missing) != 1 || missing[0] != "GET /v1/kyc/status" {
		t.Fatalf("missing = %v, want [GET /v1/kyc/status]", missing)
	}
}
