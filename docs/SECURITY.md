# PFAEngine — Security Overview (Tranche 11)

This document summarises the shipped security controls: the HTTP middleware
inventory, the authentication flow, the WORM audit trail with dual-control
export, and tenant isolation.

## Middleware inventory (internal/httpsrv)

| Control | Where | Behaviour |
|---|---|---|
| `RequestID` | every service (mounted by `httpsrv.New`) | Propagates/generates `X-Request-ID`, echoes it on the response, forwards to upstreams. |
| `Tenant` | api-gateway `/v1/...`, member-svc `/api/...` | Missing `X-Tenant-ID` → 400 `TENANT_REQUIRED`; otherwise the tenant is pinned into the request context. Health probes stay public. |
| `OptionalAuth` | member-svc | Dev-mode bridge: requests **without** `Authorization` pass; a presented-but-invalid bearer token → 401 `AUTH_INVALID`. Hard enforcement uses `Auth` (below). |
| `Auth` | available to all services | Requires a valid authn-issued bearer token; optional RBAC role checks (403 `FORBIDDEN`). |
| `RateLimit(rps, burst)` | api-gateway (200/400), member-svc (100/200) | Per-client-IP token bucket (stdlib only). Excess → 429 `RATE_LIMITED` in the canonical envelope + `Retry-After: 1`. The gateway also keeps a per-tenant limiter on `/v1/`. |
| `MaxBody(n)` | api-gateway, member-svc (10 MiB) | `Content-Length` above the cap → 413 `BODY_TOO_LARGE`; `http.MaxBytesReader` truncates bodies that over-read anyway. |
| `SecurityHeaders` | api-gateway, member-svc | `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY` on every response; `Cache-Control: no-store` on `/api/...` (tenant data must not be cached). |

All errors use the canonical envelope `{"error", "code"}`.

## Authentication flow

- `internal/authn` issues HMAC-SHA256-signed session tokens only after
  password **and** TOTP (RFC 6238, pure Go) verification — a valid token
  therefore implies MFA. Accounts carry RBAC roles
  (`TENANT_ADMIN`, `OPS`, `COMPLIANCE`, `MEMBER`, `EMPLOYER`, `AUDITOR`).
- `cmd/identity` exposes login; `httpsrv.Auth` verifies tokens at the edge of
  any service that mounts it. member-svc currently mounts `OptionalAuth`
  (401 on invalid presented tokens) while the identity chain is wired into
  the gateway; the E2E harness asserts the 401 path against the real binary.
- Statement verification codes are HMAC-SHA256 over
  `tenant|rsa|period|closing_kobo` (`internal/statement`), so a tampered
  statement or closing balance fails the round-trip
  (`POST /api/v1/statements/verify`).

## WORM audit trail and dual control

- `internal/audit` is an append-only, hash-chained event store:
  `H(n) = sha256(H(n-1) | record)`; there is no update or delete path (WORM,
  SC-02/NFR-05/FR-306). `Verify()` recomputes the whole chain (nightly job).
- Export (`GET /api/v1/audit/export`) is dual-controlled: `X-Approver-1` and
  `X-Approver-2` must be present and **distinct**, else 403
  `DUAL_CONTROL_REQUIRED`. The response includes the tenant's event slice
  plus the live chain-integrity result (`chain_valid`).
- Maker-checker is enforced on recon exception resolution
  (`POST /api/v1/recon/exceptions/{id}/resolve`, resolver identity via
  `X-Actor`, double-resolve → 409), on claims decisions, and on returns.

## Tenant isolation (NFR-10)

- Every API route is tenant-scoped: the tenant middleware rejects unscoped
  requests, and handlers read the tenant only from the request context
  (`httpsrv.TenantOf`), never from client-supplied body fields.
- Domain stores key every record by tenant (members, KYC tokens, recon
  exceptions, audit events, ledger accounts); cross-tenant reads are not
  representable in the repository interfaces.
- `GET /api/v1/audit/export?tenant=...` rejects a query tenant that
  disagrees with the header tenant (403 `TENANT_MISMATCH`).
- PII minimisation: NIN/BVN are verified via the provider facade and
  persisted hash-only (tokenized); KYC records never store raw identity
  numbers.

## Webhook signature verification (WhatsApp, INT-07)

- `POST /webhooks/whatsapp` (notification-service) accepts a delivery only
  when `X-Hub-Signature-256` recomputes as
  `"sha256=" + hex(HMAC-SHA256(body, WHATSAPP_APP_SECRET))` under a
  constant-time comparison (`vendors.Client.VerifyWebhookSignature`); any
  mismatch, malformed prefix, or missing header → 401 `BAD_SIGNATURE`. An
  unset app secret fails closed — verification can never pass.
- The GET subscription handshake requires `hub.mode=subscribe` and an exact
  `hub.verify_token` match before the challenge is echoed; an unset
  configured token also fails closed (403).
- Webhook routes bypass the tenant-header middleware (Meta sends none) but
  are gated by the tenant's `whatsapp` feature flag (403 when explicitly
  disabled), and bodies are capped at 1 MiB via `http.MaxBytesReader`.
- Outbound sends authenticate to the Graph API with a Bearer token;
  secrets arrive only via environment variables, never config files.

## API contract validation

`internal/httpsrv/openapi_check.go` parses `api/openapi.yaml` and diffs the
documented operations against the exported route tables (`Routes()`) of
member-svc and the api-gateway. The tests fail on any
documented-but-unimplemented or implemented-but-undocumented route; each
whitelisted exception names the owning binary. This keeps the public
contract and the shipped surface from drifting.

## Verification

- `sh scripts/e2e.sh` — full lifecycle E2E incl. 400/401/403/409 security
  probes, statement HMAC round-trip, and dual-control audit export.
- `sh scripts/smoke.sh` — Tranche 8 smoke (still green).
- `go test ./...` — unit + handler + contract tests (see
  `internal/httpsrv/middleware_test.go` for 429/413/header coverage).
