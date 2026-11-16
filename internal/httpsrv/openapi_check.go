// OpenAPI contract validation (Tranche 10): parse api/openapi.yaml and
// diff the documented operations against a server's registered route table
// so the spec and the code cannot silently drift apart.
package httpsrv

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// openapiMethods are the HTTP methods OpenAPI recognises under a path item.
var openapiMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

// OpenAPIRoutes parses an OpenAPI 3.x document and returns every documented
// operation as "METHOD /path" strings (sorted, path params kept as {name}).
func OpenAPIRoutes(spec []byte) ([]string, error) {
	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("openapi: parse: %w", err)
	}
	if len(doc.Paths) == 0 {
		return nil, fmt.Errorf("openapi: no paths documented")
	}
	var out []string
	for path, item := range doc.Paths {
		for method := range item {
			if openapiMethods[strings.ToLower(method)] {
				out = append(out, strings.ToUpper(method)+" "+path)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// NormalizeRoute canonicalises a "METHOD /path" route for comparison: the
// gateway serves the documented API under an /api prefix, so a leading
// "/api" segment is stripped and trailing slashes removed.
func NormalizeRoute(route string) string {
	route = strings.TrimSpace(route)
	if i := strings.IndexByte(route, ' '); i >= 0 {
		method, path := route[:i], route[i+1:]
		path = strings.TrimSuffix(path, "/")
		path = strings.TrimPrefix(path, "/api/")
		if strings.HasPrefix(path, "api/") {
			path = strings.TrimPrefix(path, "api/")
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return method + " " + path
	}
	return route
}

// ContractDiff compares the documented OpenAPI operations against the
// implemented route table. Both directions honour the whitelist
// (route -> justification) for documented exceptions. It returns the
// documented-but-unimplemented routes (missing) and the
// implemented-but-undocumented routes (extra), both sorted.
func ContractDiff(documented, implemented []string, whitelist map[string]string) (missing, extra []string) {
	docSet := map[string]bool{}
	for _, d := range documented {
		docSet[NormalizeRoute(d)] = true
	}
	implSet := map[string]bool{}
	for _, i := range implemented {
		implSet[NormalizeRoute(i)] = true
	}
	whitelisted := map[string]bool{}
	for w := range whitelist {
		whitelisted[NormalizeRoute(w)] = true
	}
	for d := range docSet {
		if !implSet[d] && !whitelisted[d] {
			missing = append(missing, d)
		}
	}
	for i := range implSet {
		if !docSet[i] && !whitelisted[i] {
			extra = append(extra, i)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}
