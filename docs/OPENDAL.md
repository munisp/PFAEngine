# OpenDAL as the Platform Storage Layer

PFAEngine's object storage now routes through
[Apache OpenDAL](https://opendal.apache.org/) — "one layer, all storage".
This document covers the architecture, the backend matrix, the presign
model, migration from direct-S3, and the honest caveats.

## Why OpenDAL

The platform stores two kinds of object data:

1. **Document blobs** (KYC passport photos, NIN slips, ID cards — PII
   under NDPA) written by member-svc via `internal/blobstore`.
2. **Lakehouse parquet** (bronze zone) written by `analytics/`.

Previously each had a hand-rolled, single-backend path (a bespoke SigV4
client in Go; local paths in Python). OpenDAL gives both a uniform
`write/read/stat/delete/list` contract across fs / S3 / GCS / Azure Blob /
memory with one well-tested implementation, so swapping storage targets is
a config change, not a code change.

**Why not the OpenDAL Go bindings?** The Go services are built
`CGO_ENABLED=0` as static binaries for a hardened minimal image; the
official OpenDAL Go bindings are CGO-based (they wrap the Rust core) and
were rejected. Instead we run the real OpenDAL crate in a small Rust
microservice — `storage-svc/` — and the Go side is a dependency-free
`net/http` client (`internal/blobstore/opendal.go`).

## Architecture

```
                          PFAEngine storage via OpenDAL
 ┌──────────────────────────────────────────────────────────────────┐
 │                                                                  │
 │  member-svc (Go, CGO=0)         storage-svc (Rust, real          │
 │  internal/blobstore             opendal crate)                   │
 │  ┌───────────────┐   HTTP       ┌───────────────────────────┐    │
 │  │ opendalStore  │─────────────▶│ PUT/GET/HEAD/DELETE        │    │
 │  │ (net/http +   │  bearer      │  /obj/{key}                │    │
 │  │  bearer token)│─────────────▶│ POST /presign -> HMAC URL  │    │
 │  └───────────────┘              └────────────┬──────────────┘    │
 │         ▲ presigned GET (exp+sig)            │ opendal           │
 │         │ (no bearer needed)                 ▼                   │
 │  client/browser ──────────────▶   fs | s3 | gcs | azblob | memory│
 │                                                                  │
 │  analytics/ (Python)                                             │
 │  extract_full / cdc_consumer / run.py                            │
 │       │  lakeio.py — LAKE_URI set? opendal python pkg : local FS │
 │       ▼                                                          │
 │     s3://bucket/lake | fs:///lake | memory:// | gcs:// | azblob://│
 └──────────────────────────────────────────────────────────────────┘
```

- **storage-svc** is the only process holding object-store credentials for
  blobs. member-svc holds only `STORAGE_SVC_URL` + `STORAGE_SVC_TOKEN`.
- **analytics** talks to object storage directly through the `opendal`
  Python package (no credentials pass through Go at all).

## Backend matrix

| Backend | storage-svc (Rust) | analytics lakeio (Python) | Notes |
|---|---|---|---|
| `fs` | built in (`FS_ROOT`) | `fs:///path` | dev + single-node prod |
| `memory` | built in | `memory://` | tests only |
| `s3` | `--features s3` (default in Docker) | `s3://bucket/prefix` + `S3_*` env | MinIO-compatible via `S3_ENDPOINT` |
| `gcs` | `--features gcs` | `gcs://bucket/prefix` + `GCS_CREDENTIAL` | |
| `azblob` | `--features azblob` | `azblob://container/prefix` + `AZBLOB_*` env | |

Feature gating keeps the default binary lean; asking for a backend that
was not compiled in is a hard startup error, never a silent fallback.

## Backend selection (Go side)

`internal/blobstore.FromEnv()` priority:

1. `STORAGE_SVC_URL` set → **OpenDAL backend (PRIMARY)**: HTTP client to
   storage-svc; `STORAGE_SVC_TOKEN` mandatory (fail-closed).
2. Full `S3_ENDPOINT`/`S3_BUCKET`/`S3_ACCESS_KEY`/`S3_SECRET_KEY` → the
   legacy hand-rolled SigV4 client (kept as fallback).
3. Neither → filesystem fallback (`BLOB_FS_ROOT`) + warning;
   `BLOB_REQUIRED=true` fails closed.

## Presign model

`GET` bytes are delivered to browsers/clients via presigned URLs:

- **With storage-svc (primary):** `POST /presign {key, ttl_secs}` returns
  `{base}/obj/{key}?exp=<unix>&sig=<hmac>`. `sig` = HMAC-SHA256 over
  `"GET\n{key}\n{exp}"` keyed by `STORAGE_PRESIGN_SECRET` (default:
  `STORAGE_TOKEN`). storage-svc verifies the signature itself on redeem —
  **backend-independent**: works identically for fs, memory, s3, gcs,
  azblob. The trade-off vs native S3 presign: the URL is served by
  storage-svc, so redeeming clients must be able to reach it
  (`STORAGE_PUBLIC_URL` controls the minted base).
- **Legacy S3 backend:** native SigV4 query-string presign, served
  directly by the object store (unchanged).

TTL is capped at 7 days in both implementations; keep it minutes-short in
practice (member-svc uses 15 minutes).

## Migration path from direct-S3

1. Deploy storage-svc pointing at the existing bucket:
   `STORAGE_BACKEND=s3 S3_BUCKET=docs S3_ENDPOINT=... S3_ACCESS_KEY=...
   S3_SECRET_KEY=...` (keys are flat `{tenant}/{doc}/{file}` — no data
   movement needed).
2. Add to member-svc: `STORAGE_SVC_URL=http://storage-svc:8110` and a
   shared `STORAGE_SVC_TOKEN` (must equal storage-svc's `STORAGE_TOKEN`).
3. Rollback = unset `STORAGE_SVC_URL`; FromEnv falls back to direct S3.

For the lakehouse: set `LAKE_URI=s3://bucket/lake` for the analytics
jobs, or leave unset to keep the local lake. Note silver/gold DuckDB
builds read parquet from a local path, so in `LAKE_URI` mode use
`fs://` (or replicate); bronze extraction and CDC writes work against
any backend.

## Caveats (honest)

- **Single-node service.** storage-svc is one process; there is no HA
  story yet. The stateless design makes active-passive or N+1 behind a
  load balancer straightforward, but it is not deployed that way today.
  Health: `GET /healthz`.
- **Presigned URLs are storage-svc URLs**, not native bucket URLs — the
  service is on the data path for every redeem (bandwidth cost vs native
  S3 presign).
- **No streaming**: PUT/GET buffer whole objects (bounded by
  `STORAGE_MAX_BODY_MIB`, default 64 MiB; uploads are capped at 10 MB
  upstream anyway).
- **The Rust build in this repo's dev sandbox pins several transitive
  crates down** (`Cargo.lock`) because the sandbox toolchain is Rust 1.83
  while some ecosystem crates now require ≥1.85. The committed lockfile
  is the tested configuration; the Dockerfile builds `rust:1.83` to match.
  Bumping the toolchain is fine, but regenerate the lockfile.
- **`opendal` Python package is optional**: only imported when `LAKE_URI`
  is set. The 34 pre-existing analytics tests run identically without it.

## Testing

- Rust: `cd storage-svc && cargo test` — router-level tests against the
  memory backend: put/get/head/delete round-trip, presign
  issue+validate+expiry+tamper, auth-required, traversal rejection, fs
  backend round-trip, and fail-closed on a non-compiled backend.
- Go: `go test ./internal/blobstore/` — the OpenDAL client is tested
  against an `httptest` mock of storage-svc (same routes/auth/presign
  contract), including FromEnv priority.
- Python: `python3 -m unittest discover -s analytics` — includes
  `lakeio_test.py` (memory:// round-trip, fs:// round-trip, CDC applier on
  an OpenDAL lake, local fallback unchanged).
