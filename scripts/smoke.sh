#!/bin/sh
# smoke.sh — Tranche 8 end-to-end smoke test.
# Builds api-gateway + member-svc, starts them on local ports, and checks:
#   gateway /healthz (liveness) and /readyz (backend aggregation)
#   member onboarding through the gateway (KYC-gated)
#   contribution CSV upload (parse -> recon -> ledger post)
#   member balance reflects the posting
#   recon exception queue endpoint
# Prints PASS/FAIL; exits non-zero on failure. POSIX sh, no jq required.
set -u

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=/tmp/pfa-smoke
GW_PORT=${GW_PORT:-18080}
SVC_PORT=${SVC_PORT:-18096}
GW=http://127.0.0.1:$GW_PORT
SVC=http://127.0.0.1:$SVC_PORT
TENANT=demo-pfa

# Go lives outside PATH in some environments; honour the repo bootstrap.
if ! command -v go >/dev/null 2>&1; then
    if [ -x "$HOME/sdk/go/bin/go" ]; then
        PATH="$HOME/sdk/go/bin:$PATH"
    else
        echo "FAIL  - go not found (run scripts/bootstrap-toolchain.sh)"
        exit 1
    fi
fi
export GOPROXY=${GOPROXY:-https://goproxy.cn,https://goproxy.io,direct}
export GOSUMDB=off GOFLAGS=-mod=mod GOCACHE=${GOCACHE:-/tmp/gocache}

fails=0
ok()   { echo "ok    - $1"; }
bad()  { echo "FAIL  - $1"; fails=$((fails + 1)); }

cleanup() {
    [ "${GW_PID:-}" ]  && kill "$GW_PID"  2>/dev/null
    [ "${SVC_PID:-}" ] && kill "$SVC_PID" 2>/dev/null
    wait 2>/dev/null
}
trap cleanup EXIT INT TERM

echo "== build =="
mkdir -p "$BIN"
(cd "$ROOT" && CGO_ENABLED=0 go build -o "$BIN/member-svc" ./cmd/member-svc) \
    && ok "member-svc built" || { bad "member-svc build"; exit 1; }
(cd "$ROOT" && CGO_ENABLED=0 go build -o "$BIN/api-gateway" ./cmd/api-gateway) \
    && ok "api-gateway built" || { bad "api-gateway build"; exit 1; }

echo "== start =="
PORT=$SVC_PORT "$BIN/member-svc" >"$BIN/member-svc.log" 2>&1 &
SVC_PID=$!
PORT=$GW_PORT MEMBER_SERVICE_URL=$SVC CONTRIBUTION_SERVICE_URL=$SVC RECON_SERVICE_URL=$SVC \
    KYC_SERVICE_URL=$SVC BENEFIT_SERVICE_URL=$SVC TRANSFER_SERVICE_URL=$SVC \
    "$BIN/api-gateway" >"$BIN/api-gateway.log" 2>&1 &
GW_PID=$!

wait_up() { # wait_up <url> <name>
    i=0
    while [ $i -lt 50 ]; do
        if curl -sf "$1" >/dev/null 2>&1; then return 0; fi
        i=$((i + 1)); sleep 0.2
    done
    return 1
}

wait_up "$SVC/healthz" && ok "member-svc /healthz" || { bad "member-svc did not start"; cat "$BIN/member-svc.log"; exit 1; }
wait_up "$GW/healthz"  && ok "api-gateway /healthz (liveness, no tenant needed)" || { bad "api-gateway did not start"; cat "$BIN/api-gateway.log"; exit 1; }

echo "== readiness aggregation =="
code=$(curl -s -o /dev/null -w '%{http_code}' "$GW/readyz")
[ "$code" = "200" ] && ok "gateway /readyz aggregates backends (200)" || bad "gateway /readyz = $code, want 200"

echo "== tenant enforcement =="
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$GW/api/v1/members" -d '{}')
[ "$code" = "400" ] && ok "missing X-Tenant-ID rejected (400)" || bad "tenant check = $code, want 400"

echo "== onboarding (KYC-gated) =="
resp=$(curl -s -X POST "$GW/api/v1/members" \
    -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' \
    -d '{"full_name":"Smoke Test","dob":"1990-01-01","phone":"+2348000000000","email":"smoke@example.com","nin":"12345678901","bvn":"10987654321"}')
MEMBER_ID=$(printf '%s' "$resp" | sed -n 's/.*"member_id":"\([^"]*\)".*/\1/p')
if [ -n "$MEMBER_ID" ]; then
    ok "member onboarded via gateway (member_id=$MEMBER_ID)"
else
    bad "onboarding failed: $resp"
fi

echo "== contribution upload (CSV -> recon -> ledger) =="
if [ "$MEMBER_ID" ]; then
    # gross 80000000 kobo; 8%+10% split = 14400000 kobo, remitted in full.
    up=$(curl -s -X POST "$GW/api/v1/contributions/upload?employer=emp-smoke&period=2026-11" \
        -H "X-Tenant-ID: $TENANT" -H 'Content-Type: text/csv' \
        --data-binary "rsa_pin,gross_kobo
$MEMBER_ID,80000000
")
    case $up in
        *'"auto_matched":1'*'"posted_kobo":14400000'*)
            ok "schedule reconciled + posted: $up" ;;
        *)
            bad "upload response: $up" ;;
    esac
fi

echo "== balance =="
if [ "$MEMBER_ID" ]; then
    bal=$(curl -s "$GW/api/v1/members/$MEMBER_ID/balance" -H "X-Tenant-ID: $TENANT")
    case $bal in
        *'"balance_kobo":14400000'*)
            ok "balance correct: $bal" ;;
        *)
            bad "balance response: $bal" ;;
    esac
fi

echo "== recon exceptions =="
code=$(curl -s -o /dev/null -w '%{http_code}' "$GW/api/v1/recon/exceptions" -H "X-Tenant-ID: $TENANT")
[ "$code" = "200" ] && ok "recon exceptions endpoint (200)" || bad "recon exceptions = $code"

echo "== graceful shutdown (SIGTERM) =="
kill -TERM "$SVC_PID" 2>/dev/null
sleep 0.5
if kill -0 "$SVC_PID" 2>/dev/null; then bad "member-svc did not exit on SIGTERM"; else ok "member-svc drained on SIGTERM"; fi
kill -TERM "$GW_PID" 2>/dev/null
sleep 0.5
if kill -0 "$GW_PID" 2>/dev/null; then bad "api-gateway did not exit on SIGTERM"; else ok "api-gateway drained on SIGTERM"; fi
GW_PID=; SVC_PID=

echo
if [ "$fails" -gt 0 ]; then
    echo "SMOKE: FAIL ($fails check(s) failed)"
    exit 1
fi
echo "SMOKE: PASS"
