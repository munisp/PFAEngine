#!/bin/sh
# e2e.sh — Tranche 10 full member-lifecycle E2E harness.
# Extends smoke.sh into a full lifecycle against the REAL built binaries:
#   build + start api-gateway and member-svc
#   probes + readiness aggregation
#   tenant enforcement (400) and bad bearer token (401)
#   security headers on /api responses
#   KYC-gated onboarding
#   contribution CSV from TWO employers, one line short-paid -> exception
#   recon exception queue -> maker-checker resolve -> queue drains
#   balance reflects only auto-matched postings
#   statement payload + HMAC verify-code round-trip (good and tampered)
#   dual-control audit export (403 without/with-same approver, 200 valid chain)
#   graceful SIGTERM drain
# Prints PASS/FAIL per step; exits non-zero on any failure.
# POSIX sh, no jq required.
set -u

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=/tmp/pfa-e2e
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
ok()  { echo "ok    - $1"; }
bad() { echo "FAIL  - $1"; fails=$((fails + 1)); }

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
    STATEMENT_SERVICE_URL=$SVC AUDIT_SERVICE_URL=$SVC \
    KYC_SERVICE_URL=$SVC BENEFIT_SERVICE_URL=$SVC TRANSFER_SERVICE_URL=$SVC \
    "$BIN/api-gateway" >"$BIN/api-gateway.log" 2>&1 &
GW_PID=$!

wait_up() { # wait_up <url>
    i=0
    while [ $i -lt 50 ]; do
        if curl -sf "$1" >/dev/null 2>&1; then return 0; fi
        i=$((i + 1)); sleep 0.2
    done
    return 1
}

wait_up "$SVC/healthz" && ok "member-svc /healthz" || { bad "member-svc did not start"; cat "$BIN/member-svc.log"; exit 1; }
wait_up "$GW/healthz"  && ok "api-gateway /healthz" || { bad "api-gateway did not start"; cat "$BIN/api-gateway.log"; exit 1; }

echo "== readiness aggregation =="
code=$(curl -s -o /dev/null -w '%{http_code}' "$GW/readyz")
[ "$code" = "200" ] && ok "gateway /readyz aggregates backends (200)" || bad "gateway /readyz = $code, want 200"

echo "== security: tenant + auth + headers =="
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$GW/api/v1/members" -d '{}')
[ "$code" = "400" ] && ok "missing X-Tenant-ID rejected (400)" || bad "tenant check = $code, want 400"

code=$(curl -s -o /dev/null -w '%{http_code}' "$GW/api/v1/members/prs-x/balance" \
    -H "X-Tenant-ID: $TENANT" -H "Authorization: Bearer e2e-bogus-token")
[ "$code" = "401" ] && ok "invalid bearer token rejected (401)" || bad "bad auth = $code, want 401"

hdrs=$(curl -s -D - -o /dev/null "$GW/api/v1/members/prs-x/balance" -H "X-Tenant-ID: $TENANT")
case $hdrs in
    *"X-Content-Type-Options: nosniff"*) ok "X-Content-Type-Options: nosniff present" ;;
    *) bad "missing X-Content-Type-Options header" ;;
esac
case $hdrs in
    *"X-Frame-Options: DENY"*) ok "X-Frame-Options: DENY present" ;;
    *) bad "missing X-Frame-Options header" ;;
esac
case $hdrs in
    *"Cache-Control: no-store"*) ok "Cache-Control: no-store on /api" ;;
    *) bad "missing Cache-Control: no-store on /api" ;;
esac

echo "== onboarding (KYC-gated) =="
resp=$(curl -s -X POST "$GW/api/v1/members" \
    -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' \
    -d '{"full_name":"E2E Member","dob":"1990-01-01","phone":"+2348000000000","email":"e2e@example.com","nin":"12345678901","bvn":"10987654321"}')
MEMBER_ID=$(printf '%s' "$resp" | sed -n 's/.*"member_id":"\([^"]*\)".*/\1/p')
if [ -n "$MEMBER_ID" ]; then
    ok "member onboarded via gateway (member_id=$MEMBER_ID)"
else
    bad "onboarding failed: $resp"
fi

echo "== contribution upload: employer A (fully remitted) =="
# gross 80000000 kobo; 8%+10% split = 14400000 kobo, remitted in full.
if [ "${MEMBER_ID:-}" ]; then
    up=$(curl -s -X POST "$GW/api/v1/contributions/upload?employer=emp-alpha&period=2026-11" \
        -H "X-Tenant-ID: $TENANT" -H 'Content-Type: text/csv' \
        --data-binary "rsa_pin,gross_kobo
$MEMBER_ID,80000000
")
    case $up in
        *'"auto_matched":1'*'"posted_kobo":14400000'*)
            ok "employer A reconciled + posted: $up" ;;
        *) bad "employer A upload: $up" ;;
    esac
fi

echo "== contribution upload: employer B (short-paid -> exception) =="
if [ "${MEMBER_ID:-}" ]; then
    up=$(curl -s -X POST "$GW/api/v1/contributions/upload?employer=emp-beta&period=2026-11" \
        -H "X-Tenant-ID: $TENANT" -H 'Content-Type: text/csv' \
        --data-binary "rsa_pin,gross_kobo,voluntary_kobo,is_ppp,received_kobo
$MEMBER_ID,80000000,0,false,14000000
")
    case $up in
        *'"exceptions":1'*'"posted_lines":0'*)
            ok "employer B short-pay raised exception, nothing posted: $up" ;;
        *) bad "employer B upload: $up" ;;
    esac
fi

echo "== recon exceptions -> resolve =="
EXC_ID=""
if [ "${MEMBER_ID:-}" ]; then
    excs=$(curl -s "$GW/api/v1/recon/exceptions" -H "X-Tenant-ID: $TENANT")
    EXC_ID=$(printf '%s' "$excs" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
    case $excs in
        *'"reason":"AMOUNT_MISMATCH"'*)
            ok "exception queue shows AMOUNT_MISMATCH (id=$EXC_ID)" ;;
        *) bad "exceptions response: $excs" ;;
    esac
fi
if [ -n "$EXC_ID" ]; then
    code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
        "$GW/api/v1/recon/exceptions/$EXC_ID/resolve" \
        -H "X-Tenant-ID: $TENANT" -H "X-Actor: ops-e2e" \
        -H 'Content-Type: application/json' -d '{"resolution":"RECLASSIFY"}')
    [ "$code" = "200" ] && ok "exception resolved (maker-checker, 200)" || bad "resolve = $code, want 200"
    excs=$(curl -s "$GW/api/v1/recon/exceptions" -H "X-Tenant-ID: $TENANT")
    [ "$excs" = "[]" ] && ok "exception queue drained after resolve" || bad "queue not empty: $excs"
fi

echo "== balance (only employer A posted) =="
if [ "${MEMBER_ID:-}" ]; then
    bal=$(curl -s "$GW/api/v1/members/$MEMBER_ID/balance" -H "X-Tenant-ID: $TENANT")
    case $bal in
        *'"balance_kobo":14400000'*) ok "balance correct: $bal" ;;
        *) bad "balance response: $bal" ;;
    esac
fi

echo "== statement: payload + HMAC verify round-trip =="
if [ "${MEMBER_ID:-}" ]; then
    stmt=$(curl -s "$GW/api/v1/members/$MEMBER_ID/statement?period=2026-11" -H "X-Tenant-ID: $TENANT")
    CODE=$(printf '%s' "$stmt" | sed -n 's/.*"verify_code":"\([^"]*\)".*/\1/p')
    case $stmt in
        *'"closing_kobo":14400000'*'"verify_code":"'*)
            ok "statement payload with verify code (code=$CODE)" ;;
        *) bad "statement response: $stmt" ;;
    esac
    if [ -n "$CODE" ]; then
        v=$(curl -s -X POST "$GW/api/v1/statements/verify" \
            -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' \
            -d "{\"rsa_id\":\"$MEMBER_ID\",\"period\":\"2026-11\",\"closing_kobo\":14400000,\"code\":\"$CODE\"}")
        [ "$v" = '{"valid":true}' ] && ok "verify round-trip: good code accepted" || bad "verify good code: $v"
        v=$(curl -s -X POST "$GW/api/v1/statements/verify" \
            -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' \
            -d "{\"rsa_id\":\"$MEMBER_ID\",\"period\":\"2026-11\",\"closing_kobo\":1,\"code\":\"$CODE\"}")
        [ "$v" = '{"valid":false}' ] && ok "verify round-trip: tampered closing rejected" || bad "verify tampered: $v"
    fi
fi

echo "== audit export (dual control) =="
code=$(curl -s -o /dev/null -w '%{http_code}' "$GW/api/v1/audit/export?tenant=$TENANT" -H "X-Tenant-ID: $TENANT")
[ "$code" = "403" ] && ok "audit export without approvers rejected (403)" || bad "no-approver export = $code, want 403"
code=$(curl -s -o /dev/null -w '%{http_code}' "$GW/api/v1/audit/export?tenant=$TENANT" \
    -H "X-Tenant-ID: $TENANT" -H "X-Approver-1: ada" -H "X-Approver-2: ada")
[ "$code" = "403" ] && ok "same approver twice rejected (403)" || bad "same-approver export = $code, want 403"
exp=$(curl -s "$GW/api/v1/audit/export?tenant=$TENANT" \
    -H "X-Tenant-ID: $TENANT" -H "X-Approver-1: ada" -H "X-Approver-2: grace")
case $exp in
    *'"chain_valid":true'*'"member.onboarded"'*)
        ok "dual-control export returns verified chain ($(printf '%s' "$exp" | sed -n 's/.*"count":\([0-9]*\).*/\1/p') events)" ;;
    *) bad "audit export: $exp" ;;
esac

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
    echo "E2E: FAIL ($fails check(s) failed)"
    exit 1
fi
echo "E2E: PASS"
