# storage-svc

PFAEngine's object-storage microservice, built directly on the
[Apache OpenDAL](https://opendal.apache.org/) Rust crate. It is the single
process that owns blob bytes; every other service talks to it over a
minimal HTTP API. See `docs/OPENDAL.md` for the architecture and the
rationale (Go services are CGO-free static binaries, so the CGO-based
OpenDAL Go bindings are not usable — this service is the bridge).

## API

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/healthz` | open | liveness probe |
| PUT | `/obj/{key}` | bearer | store raw request body under `key` |
| GET | `/obj/{key}` | bearer **or** `?exp=..&sig=..` | fetch object bytes |
| HEAD | `/obj/{key}` | bearer **or** presign | content-length via stat |
| DELETE | `/obj/{key}` | bearer | delete (idempotent) |
| POST | `/presign` | bearer | `{"key","ttl_secs"}` → `{"url","expires_at"}` |

Errors are structured JSON: `{"error":{"code":"...","message":"..."}}`.

**Presign model:** `POST /presign` returns
`{base}/obj/{key}?exp=<unix>&sig=<hmac-sha256>` where `sig` is keyed by
`STORAGE_PRESIGN_SECRET` (falling back to `STORAGE_TOKEN`) over
`"GET\n{key}\n{exp}"`. Because storage-svc mints and verifies the URLs
itself, presign works **identically on every backend** — including `fs`
and `memory`, which have no native presign. The trade-off: the URL is
redeemed against storage-svc, so clients must be able to reach it.

**Key safety:** keys are validated before touching the backend —
`..`/`.`/empty segments, absolute paths, backslashes and control
characters are rejected with `400 invalid_key`. Max 1024 bytes.

## Configuration (environment)

| Variable | Default | Notes |
|---|---|---|
| `LISTEN_ADDR` | `0.0.0.0:8110` | bind address |
| `STORAGE_BACKEND` | `fs` | `fs`, `memory`, plus `s3`/`gcs`/`azblob` when compiled in |
| `FS_ROOT` | `/var/lib/storage-svc` | root dir for the `fs` backend |
| `STORAGE_TOKEN` | — | bearer token for mutating routes; **required unless `STORAGE_ENV=dev`** (fail-closed) |
| `STORAGE_PRESIGN_SECRET` | `$STORAGE_TOKEN` | HMAC key for presigned URLs |
| `STORAGE_PUBLIC_URL` | request Host | base URL used when minting presigned URLs |
| `STORAGE_MAX_BODY_MIB` | `64` | PUT body limit |
| `S3_BUCKET`, `S3_ENDPOINT`, `S3_REGION`, `S3_ACCESS_KEY`, `S3_SECRET_KEY` | — | `s3` backend |
| `GCS_BUCKET`, `GCS_CREDENTIAL`, `GCS_ENDPOINT` | — | `gcs` backend |
| `AZBLOB_CONTAINER`, `AZBLOB_ACCOUNT_NAME`, `AZBLOB_ACCOUNT_KEY`, `AZBLOB_ENDPOINT` | — | `azblob` backend |

## Build & test

```sh
cargo test                       # unit + router tests (memory + fs backends)
cargo build --release            # fs + memory
cargo build --release --features s3          # + S3
cargo build --release --features all-backends

docker build -t pfaengine/storage-svc .      # multi-stage, FEATURES=s3 default
docker build -t pfaengine/storage-svc --build-arg FEATURES=s3,gcs .
```

## Run

```sh
STORAGE_BACKEND=fs FS_ROOT=/data STORAGE_TOKEN=... ./target/release/storage-svc
# dev, no auth (never in production):
STORAGE_ENV=dev STORAGE_BACKEND=memory ./target/release/storage-svc
```
