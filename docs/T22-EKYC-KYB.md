# T22 — Next-generation eKYC / KYB engine

## Stack (all license-clean, commercial-use-safe)

| Component | Choice | License | Role |
|---|---|---|---|
| OCR | PaddleOCR 3.x (PP-OCRv5 mobile) | Apache-2.0 | ID document text extraction |
| Document layout | Docling | MIT | PDF layout/tables parsing |
| VLM (optional) | ollama MiniCPM-V / Qwen2.5-VL via `OLLAMA_URL` | model-dependent | Second-pass field extraction, **always cross-validated against OCR text** — VLM digits are never accepted unless they also appear in the PaddleOCR output |
| Face detection | YuNet (OpenCV Zoo) | Apache-2.0 | Largest-face detection, alignment |
| Face match | SFace 128-d embeddings, cosine ≥ 0.50 | Apache-2.0 | Selfie vs ID portrait |
| Liveness | MiniFASNetV1SE + V2 dual-scale ensemble (Silent-Face-Anti-Spoofing) | Apache-2.0 | Passive anti-spoof, 80×80 crops at scales 2.7/4.0, softmax averaged |

**Deliberately excluded:** insightface `buffalo_l` weights (non-commercial
licence — hard blocker). `FaceOnLive`/`openkyc` repos are commercial bait
or abandoned; the flow design borrows from Ballerine's case-management
orchestration instead (Apache-2.0 concepts, no code).

Weights are vendored in `ml/kyc_service/weights/` (see its README for
provenance + sha). PaddleOCR downloads its own weights on first run.

## Architecture

```
member-pwa / staff ──► member-svc (internal/ekyc)
                          │  verification session state machine:
                          │  DOCUMENT_CAPTURE → DOC_ANALYSIS →
                          │  IDENTITY_CHECK → BIOMETRIC → DECIDED
                          ▼
                    kyc-service sidecar (ml/kyc_service, :8105)
                    /ocr /parse /extract /face/compare /liveness /healthz
                          │
        NIN/BVN cross-check ──► internal/kyc (tokenized, dup detection)
        manual review ────────► casemgmt KYC_REVIEW case (auto-opened)
```

### Fail-closed contract
Every sidecar error, timeout or absence (`KYC_SERVICE_URL` unset) degrades
the session to **MANUAL_REVIEW** — never to auto-approve, and unavailability
flags alone never produce a REJECT. Only negative evidence (liveness fail,
clear face mismatch, sanctions hit) rejects.

### Risk scoring (internal/ekyc)
Points accumulate per flag: `doc_intel_unavailable` 40, `ocr_low_confidence` 25,
`document_expired` 30, `nin_unverified` 50, `bvn_unverified` 20,
`duplicate_identity` 45, `name_mismatch` 30, `dob_mismatch` 25,
`face_borderline` 30, `liveness_borderline` 40, `liveness_fail` / `face_clear_mismatch` 100 (hard reject).
Decision: risk < 25 → AUTO_APPROVE · 25–59 → MANUAL_REVIEW (opens KYC_REVIEW case) · ≥ 60 with negative evidence → REJECT.

Staff override (`POST /ekyc/sessions/{id}/override`) approves/rejects a
manual-review session and resolves the linked case.

## KYB (`internal/kyb`)
- CAC RC validation (`RC` + 5–7 digits, space/case-normalised), TIN (8–13 digits).
- Directors + UBO registry with the **25% beneficial-ownership rule** (no UBO ≥ 25% → manual review; ownership > 100% flagged).
- Every director and UBO is sanctions/PEP-screened via `internal/aml` (Tier 3). Sanction hit → immediate REJECT; PEP → +40.
- Corporate documents required (`missing_corporate_docs` → review).
- Routes: `POST/GET /api/v1/kyb/businesses`, `GET /kyb/businesses/{id}`, `POST /kyb/businesses/{id}/review`.

## Deployment
`deploy/kyc-service.Dockerfile` + `kyc-service` service in
`deploy/docker-compose.yml` (member-svc gets `KYC_SERVICE_URL=http://kyc-service:8105`,
sidecar gets optional `OLLAMA_URL` for the VLM pass).

## UI
- member-pwa **KYC Center** (`Kyc.tsx`): 4-step wizard (document → identity → selfie → decision) + my-documents.
- staff-portal **KYC / KYB ops** (`KycOps.tsx`): sidecar health banner with per-model badges, session queue with risk scores + history timeline, approve/reject overrides; KYB submit + review queue.

## Tests
- `internal/ekyc` — 7 tests: happy-path auto-approve, fail-closed manual review, spoof reject, face-mismatch reject, duplicate-identity routing + case, override resolving case, state-machine enforcement, name fuzzy match.
- `internal/kyb` — 7 tests: verified path, RC/TIN validation, UBO 25% rule → case, sanctions reject, review resolving case, >100% ownership.
- `cmd/member-svc/regulatory_test.go` — e2e over HTTP incl. fail-closed eKYC flow and KYB flow.
- `ml/kyc_service/tests/test_extract.py` — 11 unittest cases for regex extraction + date normalisation (no heavy deps).

## Honest caveats
- Passive liveness (MiniFASNet) defeats print/replay attacks but is **weak against 3D masks and deepfakes** — for high-assurance flows (large withdrawals) pair with the existing challenge-response liveness in `internal/kyc`.
- OCR quality on laminated NIN slips under poor lighting varies; low confidence routes to manual review by design.
- Sessions are in-memory; add a Postgres write-through repo (pattern from T20 `store.CaseRepo`) before production.
- VLM pass requires an operator to pull the model into ollama (`ollama pull minicpm-v`); without `OLLAMA_URL` the sidecar runs regex-only, which is the supported baseline.
