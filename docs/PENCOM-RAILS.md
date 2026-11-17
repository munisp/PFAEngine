# Gap 4 — PenCom Regulatory Rails Integration (`internal/pencomrails`)

Connector layer for the three industry rails a licensed PFA must speak:

| Rail | Purpose | Type |
|------|---------|------|
| **PENCAP** | PenCom data-recapture / enrolment (2024-2025 recapture exercise: biodata + NIN) | `PENCAPClient` |
| **RTS** | RSA Transfer System — members moving RSAs between PFAs | `RTSHub` |
| **PSSP** | Pension Service Support Provider — remittance schedule submission + status polling | `PSSPClient` |

> **Honesty flag — read before production use.** The PenCom, RTS and PSSP
> interface specifications are **industry-private bilateral documents**. Every
> wire format in this package is an **assumed layout, validate against
> PenCom/PSSP bilateral specs**. Specifically assumed:
>
> - PENCAP recapture file: pipe-delimited, `HDR|PENCAP-RECAPTURE|1.0|<count>`,
>   `REC|PIN|NIN|...` body lines, `TRAILER|<count>|<sha256-hex of body lines>`.
>   The checksum/count envelope is sound engineering regardless of final layout.
> - PENCAP response: pipe-delimited `PIN|STATUS|REASON` lines
>   (`ACCEPTED`/`REJECTED`).
> - RTS transfer file: CSV `TRANSFER_ID,PIN,FROM_PFA,TO_PFA,INITIATED_AT,STATUS`
>   (RFC3339 timestamps).
> - HTTP endpoints: `/v1/recapture`, `/v1/recapture/{batchId}/results`,
>   `/v1/transfers`, `/v1/transfers/inbound?since=…`,
>   `/v1/transfers/{id}/confirm`, `/v1/schedules`, `/v1/schedules/{ref}` —
>   all assumed paths on the rail base URLs.
> - Auth: `Authorization: Bearer <api-key>` + `X-PFA-Code: <pfa-code>` header
>   on every call (assumed scheme).
> - RSA PIN format `PEN` + 12 digits; NIN exactly 11 digits; phone E.164
>   `+234` + 10 digits. These match public NIMC/PenCom conventions but should
>   still be confirmed.

Non-negotiables that are **not** assumptions: the 12-month (365-day) transfer
eligibility rule (`CanTransfer`), the PENDING→VALIDATED→CONFIRMED|REJECTED|CANCELLED
state machine, fail-closed behaviour (missing config / non-2xx / malformed
payload / unknown status are all hard errors), and SHA-256 + record-count
integrity on recapture files.

## Files

- `recapture.go` — package doc + shared HTTP plumbing, `RecaptureRecord` +
  `Validate()`, `BuildRecaptureFile` / `ParseRecaptureFile` (checksum +
  tamper detection), `ParseRecaptureResponse`, `PENCAPClient` /
  `NewPENCAPFromEnv` (`SubmitRecapture`, `FetchResults`).
- `rts.go` — `TransferInstruction` + `Transition` state machine,
  `CanTransfer` (365-day rule), `BuildTransferFile` / `ParseTransferFile`
  (CSV), `RTSHub` / `NewRTSFromEnv` (`InitiateTransfer`, `PollInbound`,
  `Confirm`).
- `pssp.go` — `PSSPClient` / `NewPSSPFromEnv` (`SubmitSchedule`,
  `ScheduleStatus` → `ScheduleState{Status, Reason}`).
- `pencomrails_test.go` — 16 tests, all green.

## Environment variables

| Variable | Used by | Purpose |
|----------|---------|---------|
| `PENCAP_BASE_URL` | `NewPENCAPFromEnv` | PENCAP rail base URL |
| `PENCAP_PFA_CODE` | `NewPENCAPFromEnv` | This PFA's PenCom code |
| `PENCAP_API_KEY`  | `NewPENCAPFromEnv` | Bearer credential |
| `RTS_BASE_URL`    | `NewRTSFromEnv`    | RTS hub base URL |
| `RTS_PFA_CODE`    | `NewRTSFromEnv`    | This PFA's code on the RTS |
| `RTS_API_KEY`     | `NewRTSFromEnv`    | Bearer credential |
| `PSSP_BASE_URL`   | `NewPSSPFromEnv`   | PSSP base URL |
| `PSSP_PFA_CODE`   | `NewPSSPFromEnv`   | This PFA's code at the PSSP |
| `PSSP_API_KEY`    | `NewPSSPFromEnv`   | Bearer credential |

All three `New*FromEnv` constructors **fail closed** (return an error) if any
of their three variables is unset. Methods on a zero/nil client also fail
closed. Default HTTP timeout is 30 s; inject `HTTP *http.Client` to override.

## Suggested routes (for the API layer — not wired here)

| Method | Route | Handler sketch |
|--------|-------|----------------|
| POST | `/v1/pencom/recapture` | parse body → `[]RecaptureRecord` → `PENCAPClient.SubmitRecapture` → `{batch_id}` |
| GET  | `/v1/pencom/recapture/{batchId}` | `PENCAPClient.FetchResults(batchId)` → `[]RecaptureResult` |
| POST | `/v1/pencom/transfers` | check `CanTransfer(lastTransferAt, now)` → `RTSHub.InitiateTransfer(pin, toPFA)` |
| GET  | `/v1/pencom/transfers` | `RTSHub.PollInbound(since)` → `[]TransferInstruction` |
| POST | `/v1/pencom/transfers/{id}/confirm` | `RTSHub.Confirm(id)`; on success drive local `Transition(StatusConfirmed)` |
| POST | `/v1/pencom/pssp/schedules` | `json.Marshal(payroll.Schedule)` → `PSSPClient.SubmitSchedule` → `{reference}` |
| GET  | `/v1/pencom/pssp/schedules/{ref}` | `PSSPClient.ScheduleStatus(ref)` → `ScheduleState` |

## Coupling to `internal/payroll`

`internal/payroll` does **not exist yet** in this tree (another agent is
building it). The PSSP connector therefore takes `scheduleJSON []byte` and
validates it is well-formed JSON — it never imports payroll, so there is no
compile-order dependency and no duplicated local types. When payroll lands,
the route handler just does `json.Marshal(schedule)`; money stays integer
kobo inside the payroll types (`money.Kobo`), never floats.

## Deviations from the mission spec

1. `ScheduleStatus` returns `(ScheduleState{Status, Reason}, error)` instead
   of `(status string, error)` so the REJECTED reason is not dropped.
2. `InitiateTransfer` returns `(TransferInstruction, error)` (signature was
   left open in the brief) to surface the rail-assigned ID and timestamp.
3. Added `ParseRecaptureFile` (not requested) because round-trip checksum
   verification / tamper detection tests need a reader, and the receiving
   side of a bilateral exchange will need it anyway.
4. State machine: brief said PENDING→VALIDATED→CONFIRMED|REJECTED|CANCELLED;
   additionally PENDING→CANCELLED is allowed (withdraw before validation).
   CONFIRMED/REJECTED/CANCELLED are terminal.

## Verification

```
go build ./internal/pencomrails/   # ok
go vet  ./internal/pencomrails/    # ok
go test ./internal/pencomrails/    # ok — 16/16 pass
```

Tests cover: field validation (12 negative cases), recapture round-trip,
checksum tamper + count tamper detection, response parsing, full state-machine
edge matrix, 364/365/400-day eligibility boundaries, CSV round-trip + bad
header/status, httptest happy paths for all three clients (asserting Bearer +
X-PFA-Code headers), 500→error for every method, malformed-JSON→error,
unknown-status→error, and fail-closed env constructors.
