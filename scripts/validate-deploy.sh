#!/bin/sh
# validate-deploy.sh — Tranche 9 deploy validation (NFR-11, AR-02).
#
# Checks deploy/k8s/base.yaml and deploy/docker-compose.yml:
#   k8s:     every Deployment container has a liveness/readiness probe,
#            a non-root securityContext, resource requests AND limits, and
#            a pinned image tag (never :latest).
#   compose: every app service (build:) has a healthcheck and depends_on;
#            all 15 microservices from the README are present.
#
# POSIX sh, stdlib-only. Uses python3+PyYAML when importable; otherwise
# falls back to a minimal grep-based structural check.
# Exit 0 = PASS, 1 = violations found.

set -u
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
K8S="$ROOT/deploy/k8s/base.yaml"
COMPOSE="$ROOT/deploy/docker-compose.yml"

if [ ! -f "$K8S" ] || [ ! -f "$COMPOSE" ]; then
	echo "FAIL: manifest not found ($K8S / $COMPOSE)" >&2
	exit 1
fi

if python3 -c 'import yaml' 2>/dev/null; then
	python3 - "$K8S" "$COMPOSE" <<'PYEOF'
import sys

import yaml

k8s_path, compose_path = sys.argv[1], sys.argv[2]
violations = []

# --- Kubernetes base ---
with open(k8s_path) as f:
    docs = [d for d in yaml.safe_load_all(f) if d]

for doc in docs:
    if doc.get("kind") != "Deployment":
        continue
    name = doc.get("metadata", {}).get("name", "?")
    pod = doc.get("spec", {}).get("template", {}).get("spec", {})
    pod_sc = pod.get("securityContext", {}) or {}
    containers = pod.get("containers", [])
    if not containers:
        violations.append(f"k8s/{name}: no containers")
    for c in containers:
        cname = c.get("name", "?")
        where = f"k8s/{name}/{cname}"
        # probes
        if not (c.get("livenessProbe") or c.get("readinessProbe")):
            violations.append(f"{where}: no livenessProbe/readinessProbe")
        # non-root securityContext (container-level, or inherited from pod)
        sc = c.get("securityContext", {}) or {}
        merged = {**pod_sc, **sc}
        if merged.get("runAsNonRoot") is not True:
            violations.append(f"{where}: securityContext.runAsNonRoot is not true")
        if not (merged.get("runAsUser") or 0) > 0:
            violations.append(f"{where}: securityContext.runAsUser missing or 0 (root)")
        # resources
        res = c.get("resources", {}) or {}
        if not res.get("requests"):
            violations.append(f"{where}: missing resources.requests")
        if not res.get("limits"):
            violations.append(f"{where}: missing resources.limits")
        # pinned image tag
        image = c.get("image", "")
        if ":" not in image or image.rsplit(":", 1)[1] in ("", "latest"):
            violations.append(f"{where}: image {image!r} is not pinned to a version tag")

# --- docker-compose ---
with open(compose_path) as f:
    comp = yaml.safe_load(f)

services = comp.get("services", {}) or {}
readme_services = [  # the 15 microservices (README §"What this repo is")
    "api-gateway", "identity", "kyc", "member", "contribution",
    "reconciliation", "ledger", "statement", "reporting", "notification",
    "audit", "employer-portal", "benefit", "transfer", "fund-pricing",
]
for svc in readme_services:
    if svc not in services:
        violations.append(f"compose: README service {svc!r} missing from compose")

app_services = [n for n, s in services.items() if isinstance(s, dict) and "build" in s]
for n in sorted(app_services):
    s = services[n]
    hc = s.get("healthcheck")
    if not (isinstance(hc, dict) and hc.get("test")):
        violations.append(f"compose/{n}: app service has no healthcheck.test")
    if not s.get("depends_on"):
        violations.append(f"compose/{n}: app service has no depends_on")

if violations:
    print("deploy validation FAILED:")
    for v in violations:
        print(f"  - {v}")
    sys.exit(1)
print(f"deploy validation PASS: {len(docs)} k8s docs checked, "
      f"{len(app_services)} compose app services checked, "
      f"all {len(readme_services)} README services present")
PYEOF
	rc=$?
else
	# Minimal structural fallback (no PyYAML): grep-based spot checks.
	rc=0
	fail() { echo "  - $1" >&2; rc=1; }
	grep -q "readinessProbe" "$K8S" || fail "k8s: no readinessProbe found"
	grep -q "livenessProbe" "$K8S" || fail "k8s: no livenessProbe found"
	grep -q "runAsNonRoot: true" "$K8S" || fail "k8s: no runAsNonRoot securityContext"
	grep -q "requests:" "$K8S" || fail "k8s: no resource requests"
	grep -q "limits:" "$K8S" || fail "k8s: no resource limits"
	grep -E "image:[^#]*:latest([[:space:]]|$)" "$K8S" >/dev/null && fail "k8s: :latest image tag found"
	for svc in api-gateway identity kyc member contribution reconciliation \
		ledger statement reporting notification audit employer-portal \
		benefit transfer fund-pricing; do
		grep -q "^  $svc:" "$COMPOSE" || fail "compose: service $svc missing"
	done
	[ "$(grep -c 'healthcheck:' "$COMPOSE")" -ge 15 ] || fail "compose: fewer than 15 healthchecks"
	[ "$(grep -c 'depends_on:' "$COMPOSE")" -ge 15 ] || fail "compose: fewer than 15 depends_on"
	if [ "$rc" -eq 0 ]; then
		echo "deploy validation PASS (grep fallback — install PyYAML for full checks)"
	else
		echo "deploy validation FAILED (grep fallback)" >&2
	fi
fi
exit "$rc"
