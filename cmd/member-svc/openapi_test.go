package main

import (
	"os"
	"testing"

	"github.com/munisp/pfaengine/internal/httpsrv"
)

// documentedElsewhere lists OpenAPI operations served by the other dedicated
// services (appx-layer binaries or the api-gateway), not by member-svc.
// Each entry must name the owning binary.
var documentedElsewhere = map[string]string{
	"POST /v1/kyc/nin/verify":            "cmd/kyc",
	"POST /v1/kyc/bvn/verify":            "cmd/kyc",
	"POST /v1/kyc/liveness":              "cmd/kyc",
	"GET /v1/kyc/liveness/{id}":          "cmd/kyc",
	"POST /v1/retirees/liveness":         "cmd/kyc (IAM_ALIVE kind)",
	"POST /v1/members/{id}/rsa":          "cmd/member",
	"POST /v1/consents":                  "cmd/member",
	"GET /v1/consents":                   "cmd/member",
	"POST /v1/employers":                 "cmd/employer-portal",
	"POST /v1/employers/{id}/schedules":  "cmd/employer-portal",
	"POST /v1/contributions":             "cmd/contribution (schedule posting)",
	"GET /v1/members/{id}/contributions": "cmd/contribution",
	"GET /v1/members/{id}/statement.pdf": "cmd/statement (PDF rendering)",
	"POST /v1/statements/schedules":      "cmd/statement",
	"GET /v1/reports/{return_code}":      "cmd/reporting",
	"POST /v1/claims":                    "cmd/benefit",
	"POST /v1/claims/{id}/decision":      "cmd/benefit",
	"POST /v1/transfers":                 "cmd/transfer",
	"GET /v1/funds/{id}/nav":             "cmd/fund-pricing",
	"POST /v1/funds/{id}/nav":            "cmd/fund-pricing",
	"POST /v1/webhooks":                  "cmd/api-gateway (BFF route)",
}

// TestOpenAPIContract asserts the member-svc route table and
// api/openapi.yaml cannot drift: every member-svc route must be documented
// and every documented route must be implemented here or whitelisted above.
func TestOpenAPIContract(t *testing.T) {
	spec, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	documented, err := httpsrv.OpenAPIRoutes(spec)
	if err != nil {
		t.Fatal(err)
	}
	missing, extra := httpsrv.ContractDiff(documented, Routes(), documentedElsewhere)
	for _, m := range missing {
		t.Errorf("documented but not implemented by member-svc (whitelist with owner if served elsewhere): %s", m)
	}
	for _, e := range extra {
		t.Errorf("implemented by member-svc but not documented in api/openapi.yaml: %s", e)
	}
}
