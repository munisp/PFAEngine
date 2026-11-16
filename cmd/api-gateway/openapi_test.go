package main

import (
	"os"
	"strings"
	"testing"

	"github.com/munisp/pfaengine/internal/httpsrv"
)

// gatewayWhitelist documents the known contract exceptions for the BFF:
// routes the gateway serves that are intentionally absent from the public
// OpenAPI document, and prefixes whose documented paths use a different
// noun.
var gatewayWhitelist = map[string]string{
	"GET /v1/webhooks/deliveries": "ops debug endpoint, not public API",
	"POST /v1/events":             "dev event ingress (production: Kafka consumers)",
	"PROXY /api/v1/benefits":      "benefit service routes documented as /v1/claims",
}

// TestGatewayContract asserts every concrete gateway route is documented or
// whitelisted, and every reverse-proxy prefix has at least one documented
// path beneath it.
func TestGatewayContract(t *testing.T) {
	spec, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	documented, err := httpsrv.OpenAPIRoutes(spec)
	if err != nil {
		t.Fatal(err)
	}

	docPaths := map[string]bool{}
	docRoutes := map[string]bool{}
	for _, d := range documented {
		docRoutes[d] = true
		parts := strings.SplitN(d, " ", 2)
		docPaths[parts[1]] = true
	}

	for _, route := range Routes() {
		if strings.HasPrefix(route, "PROXY ") {
			prefix := httpsrv.NormalizeRoute("GET " + strings.TrimPrefix(route, "PROXY "))[4:]
			if _, wl := gatewayWhitelist[route]; wl {
				continue
			}
			found := false
			for p := range docPaths {
				if p == prefix || strings.HasPrefix(p, prefix+"/") {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("proxy prefix %s has no documented path beneath it", route)
			}
			continue
		}
		if _, wl := gatewayWhitelist[route]; wl {
			continue
		}
		if !docRoutes[httpsrv.NormalizeRoute(route)] {
			t.Errorf("gateway route %s not documented in api/openapi.yaml (whitelist if intentional)", route)
		}
	}
}
