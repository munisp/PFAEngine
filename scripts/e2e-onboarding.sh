#!/bin/sh
# e2e-onboarding.sh — Tranche 13 unified onboarding framework E2E (FR-520).
# Builds and starts member-svc, employer-portal and identity, then drives
# full onboarding lifecycles against the real binaries:
#   MEMBER   — case -> 5 ordered steps (KYC facade-backed) -> auto-ACTIVE
#              -> member record exists and balance endpoint works;
#              out-of-order step and consent-less consent step rejected.
#   EMPLOYER — case -> rc_validate/GLIP(3x)/mandate/pssp steps ->
#              APPROVAL_PENDING; same-approver approve rejected (4xx,
#              maker-checker); distinct approver -> compliance COMPLIANT.
#   STAFF    — case (disabled account) -> login blocked -> rbac + TOTP
#              enroll steps -> distinct-approver approval -> TOTP login works.
# Prints PASS/FAIL per step; exits non-zero on any failure.
# POSIX sh, no jq required; python3 used only to compute the RFC 6238 TOTP.
set -u

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=/tmp/pfa-onb-e2e
SVC_PORT=${SVC_PORT:-18096}
EMP_PORT=${EMP_PORT:-18191}
ID_PORT=${ID_PORT:-18181}
SVC=http://127.0.0.1:$SVC_PORT
EMP=http://127.0.0.1:$EMP_PORT
IDS=http://127.0.0.1:$ID_PORT
TENANT=demo-pfa

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
    [ "${SVC_PID:-}" ] && kill "$SVC_PID" 2>/dev/null
    [ "${EMP_PID:-}" ] && kill "$EMP_PID" 2>/dev/null
    [ "${ID_PID:-}" ]  && kill "$ID_PID" 2>/dev/null
    wait 2>/dev/null
}
trap cleanup EXIT INT TERM

json_field() { # json_field <json> <field>
    printf '%s' "$1" | sed -n "s/.*\"$2\":\"\([^\"]*\)\".*/\1/p"
}

echo "== build =="
mkdir -p "$BIN"
for svc in member-svc employer-portal identity; do
    (cd "$ROOT" && CGO_ENABLED=0 go build -o "$BIN/$svc" ./cmd/"$svc") \
        && ok "$svc built" || { bad "$svc build"; exit 1; }
done

echo "== start =="
PORT=$SVC_PORT "$BIN/member-svc" >"$BIN/member-svc.log" 2>&1 &
SVC_PID=$!
PORT=$EMP_PORT "$BIN/employer-portal" >"$BIN/employer-portal.log" 2>&1 &
EMP_PID=$!
PORT=$ID_PORT "$BIN/identity" >"$BIN/identity.log" 2>&1 &
ID_PID=$!

wait_up() {
    i=0
    while [ $i -lt 50 ]; do
        if curl -sf "$1" >/dev/null 2>&1; then return 0; fi
        i=$((i + 1)); sleep 0.2
    done
    return 1
}
wait_up "$SVC/healthz" && ok "member-svc /healthz" || { bad "member-svc start"; cat "$BIN/member-svc.log"; exit 1; }
wait_up "$EMP/healthz" && ok "employer-portal /healthz" || { bad "employer-portal start"; cat "$BIN/employer-portal.log"; exit 1; }
wait_up "$IDS/healthz" && ok "identity /healthz" || { bad "identity start"; cat "$BIN/identity.log"; exit 1; }

echo "== MEMBER pipeline: case -> 5 steps -> ACTIVE -> member exists =="
resp=$(curl -s -X POST "$SVC/api/v1/onboarding" \
    -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' \
    -d '{"stakeholder":"MEMBER","full_name":"E2E Onboarded","dob":"1990-01-01","phone":"+2348000001234","email":"e2e-onb@example.com","nin":"12345678901","bvn":"10987654321"}')
CASE=$(json_field "$resp" id)
if [ -n "$CASE" ]; then
    ok "onboarding case created (id=$CASE, status=$(json_field "$resp" status))"
else
    bad "create case: $resp"
fi

if [ -n "${CASE:-}" ]; then
    code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
        "$SVC/api/v1/onboarding/$CASE/steps/rsa_open" \
        -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' -d '{}')
    [ "$code" = "409" ] && ok "out-of-order step rejected (409)" || bad "out-of-order step = $code, want 409"

    for step in nin_verify bvn_validate; do
        code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
            "$SVC/api/v1/onboarding/$CASE/steps/$step" \
            -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' -d '{}')
        [ "$code" = "200" ] && ok "step $step completed" || bad "step $step = $code, want 200"
    done

    code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
        "$SVC/api/v1/onboarding/$CASE/steps/consent_record" \
        -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' -d '{}')
    [ "$code" = "400" ] && ok "consent_record without consent rejected (400)" || bad "consent-less consent = $code, want 400"
    code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
        "$SVC/api/v1/onboarding/$CASE/steps/consent_record" \
        -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' -d '{"consent":true}')
    [ "$code" = "200" ] && ok "step consent_record completed" || bad "consent_record = $code, want 200"

    code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
        "$SVC/api/v1/onboarding/$CASE/steps/liveness_enroll" \
        -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' -d '{"selfie_ref":"spoof-e2e"}')
    [ "$code" = "422" ] && ok "liveness spoof rejected (422)" || bad "liveness spoof = $code, want 422"
    code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
        "$SVC/api/v1/onboarding/$CASE/steps/liveness_enroll" \
        -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' -d '{"selfie_ref":"e2e-selfie"}')
    [ "$code" = "200" ] && ok "step liveness_enroll completed" || bad "liveness_enroll = $code, want 200"

    resp=$(curl -s -X POST "$SVC/api/v1/onboarding/$CASE/steps/rsa_open" \
        -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' -d '{}')
    MEMBER_ID=$(json_field "$resp" ref_id)
    case $resp in
        *'"status":"ACTIVE"'*)
            [ -n "$MEMBER_ID" ] && ok "case ACTIVE after rsa_open; member activated (ref_id=$MEMBER_ID)" \
                || bad "case ACTIVE but no ref_id: $resp" ;;
        *) bad "rsa_open response: $resp" ;;
    esac

    if [ -n "${MEMBER_ID:-}" ]; then
        bal=$(curl -s "$SVC/api/v1/members/$MEMBER_ID/balance" -H "X-Tenant-ID: $TENANT")
        case $bal in
            *'"balance_kobo":0'*) ok "member balance endpoint works: $bal" ;;
            *) bad "balance response: $bal" ;;
        esac
    fi
fi

echo "== EMPLOYER pipeline: case -> GLIP 3x -> maker-checker approval =="
resp=$(curl -s -X POST "$EMP/v1/employers" \
    -H "X-Tenant-ID: $TENANT" -H "X-Actor: maker-1" -H 'Content-Type: application/json' \
    -d '{"name":"E2E Employers Ltd","rc_number":"RC123456","contact":"hr@e2e.ng","remittance_terms":"monthly"}')
EMP_CASE=$(json_field "$resp" case_id)
EMP_ID=$(json_field "$resp" "id")
if [ -n "$EMP_CASE" ] && [ -n "$EMP_ID" ]; then
    ok "employer case opened (employer=$EMP_ID case=$EMP_CASE status=$(json_field "$resp" onboarding_status))"
else
    bad "employer create: $resp"
fi

if [ -n "${EMP_CASE:-}" ]; then
    code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
        "$EMP/v1/employers/$EMP_ID/steps/rc_validate" \
        -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' -d '{}')
    [ "$code" = "200" ] && ok "step rc_validate completed" || bad "rc_validate = $code, want 200"

    # GLIP below the statutory 3x emolument must be rejected.
    code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
        "$EMP/v1/employers/$EMP_ID/steps/glip_evidence" \
        -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' \
        -d '{"insurer":"Leadway","policy_no":"GLIP-001","cover_multiple_x100":200,"expiry":"2030-01-01T00:00:00Z","annual_emolument_kobo":100000000,"cover_amount_kobo":200000000}')
    [ "$code" = "422" ] && ok "GLIP below 3x rejected (422)" || bad "GLIP 2x = $code, want 422"
    code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
        "$EMP/v1/employers/$EMP_ID/steps/glip_evidence" \
        -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' \
        -d '{"insurer":"Leadway","policy_no":"GLIP-001","cover_multiple_x100":300,"expiry":"2030-01-01T00:00:00Z","annual_emolument_kobo":100000000,"cover_amount_kobo":300000000}')
    [ "$code" = "200" ] && ok "step glip_evidence completed (3x cover)" || bad "glip_evidence = $code, want 200"

    for step in mandate_form pssp_link; do
        code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
            "$EMP/v1/employers/$EMP_ID/steps/$step" \
            -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' -d '{}')
        [ "$code" = "200" ] && ok "step $step completed" || bad "$step = $code, want 200"
    done

    # Maker-checker: the submitter cannot approve their own case.
    code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
        "$EMP/v1/employers/$EMP_ID/approve" \
        -H "X-Tenant-ID: $TENANT" -H "X-Actor: maker-1")
    case $code in
        4*) ok "same-approver approve rejected ($code, maker-checker)" ;;
        *)  bad "same-approver approve = $code, want 4xx" ;;
    esac

    resp=$(curl -s -X POST "$EMP/v1/employers/$EMP_ID/approve" \
        -H "X-Tenant-ID: $TENANT" -H "X-Actor: checker-1")
    case $resp in
        *'"status":"ACTIVE"'*) ok "distinct approver activated the case" ;;
        *) bad "approve response: $resp" ;;
    esac

    comp=$(curl -s "$EMP/v1/employers/$EMP_ID/compliance" -H "X-Tenant-ID: $TENANT")
    case $comp in
        *'"compliance_state":"COMPLIANT"'*'"onboarding_status":"ACTIVE"'*)
            ok "compliance endpoint shows COMPLIANT/ACTIVE: $comp" ;;
        *) bad "compliance response: $comp" ;;
    esac
fi

echo "== STAFF pipeline: TOTP enroll -> approve -> login works =="
resp=$(curl -s -X POST "$IDS/v1/staff" \
    -H "X-Tenant-ID: $TENANT" -H "X-Actor: admin-1" -H 'Content-Type: application/json' \
    -d '{"email":"staff-e2e@example.com","password":"s3cret-pass","roles":["OPS"]}')
STAFF_CASE=$(json_field "$resp" case_id)
if [ -n "$STAFF_CASE" ]; then
    ok "staff case opened (case=$STAFF_CASE)"
else
    bad "staff create: $resp"
fi

if [ -n "${STAFF_CASE:-}" ]; then
    code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$IDS/v1/login" \
        -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' \
        -d '{"email":"staff-e2e@example.com","password":"s3cret-pass"}')
    [ "$code" = "403" ] && ok "login blocked before approval (403 disabled)" || bad "pre-approval login = $code, want 403"

    code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
        "$IDS/v1/onboarding/$STAFF_CASE/steps/rbac_role_assign" \
        -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' -d '{}')
    [ "$code" = "200" ] && ok "step rbac_role_assign completed" || bad "rbac_role_assign = $code, want 200"

    resp=$(curl -s -X POST "$IDS/v1/onboarding/$STAFF_CASE/steps/totp_enroll" \
        -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' -d '{}')
    TOTP_SECRET=$(json_field "$resp" totp_secret)
    [ -n "$TOTP_SECRET" ] && ok "step totp_enroll completed (secret issued)" || bad "totp_enroll: $resp"

    code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
        "$IDS/v1/onboarding/$STAFF_CASE/approve" \
        -H "X-Tenant-ID: $TENANT" -H "X-Actor: admin-1")
    case $code in
        4*) ok "same-approver approve rejected ($code, maker-checker)" ;;
        *)  bad "same-approver approve = $code, want 4xx" ;;
    esac

    resp=$(curl -s -X POST "$IDS/v1/onboarding/$STAFF_CASE/approve" \
        -H "X-Tenant-ID: $TENANT" -H "X-Actor: admin-2")
    case $resp in
        *'"status":"ACTIVE"'*) ok "distinct approver activated staff case" ;;
        *) bad "staff approve: $resp" ;;
    esac

    if [ -n "${TOTP_SECRET:-}" ]; then
        TOTP=$(python3 - "$TOTP_SECRET" <<'PYEOF'
import sys, hmac, hashlib, struct, base64, time
secret = sys.argv[1]
pad = "=" * ((8 - len(secret) % 8) % 8)
key = base64.b32decode((secret + pad).upper())
msg = struct.pack(">Q", int(time.time()) // 30)
digest = hmac.new(key, msg, hashlib.sha1).digest()
off = digest[-1] & 0x0F
code = (struct.unpack(">I", digest[off:off + 4])[0] & 0x7FFFFFFF) % 1000000
print("%06d" % code)
PYEOF
)
        resp=$(curl -s -X POST "$IDS/v1/login" \
            -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' \
            -d "{\"email\":\"staff-e2e@example.com\",\"password\":\"s3cret-pass\",\"totp\":\"$TOTP\"}")
        case $resp in
            *'"token":"'*) ok "staff login with TOTP works after approval" ;;
            *)
                # Retry once in case the 30s TOTP window rolled over.
                TOTP=$(python3 - "$TOTP_SECRET" <<'PYEOF'
import sys, hmac, hashlib, struct, base64, time
secret = sys.argv[1]
pad = "=" * ((8 - len(secret) % 8) % 8)
key = base64.b32decode((secret + pad).upper())
msg = struct.pack(">Q", int(time.time()) // 30)
digest = hmac.new(key, msg, hashlib.sha1).digest()
off = digest[-1] & 0x0F
code = (struct.unpack(">I", digest[off:off + 4])[0] & 0x7FFFFFFF) % 1000000
print("%06d" % code)
PYEOF
)
                resp=$(curl -s -X POST "$IDS/v1/login" \
                    -H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json' \
                    -d "{\"email\":\"staff-e2e@example.com\",\"password\":\"s3cret-pass\",\"totp\":\"$TOTP\"}")
                case $resp in
                    *'"token":"'*) ok "staff login with TOTP works after approval (retry)" ;;
                    *) bad "staff login: $resp" ;;
                esac ;;
        esac
    fi
fi

echo
if [ "$fails" -gt 0 ]; then
    echo "E2E-ONBOARDING: FAIL ($fails check(s) failed)"
    exit 1
fi
echo "E2E-ONBOARDING: PASS"
