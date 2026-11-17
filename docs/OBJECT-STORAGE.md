# Gap 7 — Object Storage for Document Blobs (`internal/blobstore`)

KYC documents (passport photos, NIN slips, ID cards) are sensitive PII under
NDPA. Before Gap 7, `docupload.Submit` recorded a client-supplied `ref`
string and no bytes ever moved through the platform. Gap 7 adds a real blob
path: bytes are uploaded through member-svc, hashed server-side, stored in an
S3-compatible object store, and read back through short-lived presigned URLs.
Document **metadata** stays in `internal/docupload` (and its Gap-5 snapshot
persistence); the **bytes** live only in the object store.

## Architecture

```
member-svc
  POST /api/v1/documents/upload   JSON {member_id, kind, filename, mime, data_b64}
      |  decode (10MB cap) -> SHA-256 (server-side) -> blobstore.Put
      |  key = {tenant}/{blob-id}/{sanitised-filename}
      v
  docupload.Submit(..., ref=key, sha)     <-- metadata + ref + hash
      |
  GET /api/v1/documents/{id}/url
      |  owner check (member_id match) or staff (X-Actor-Role: STAFF/ADMIN)
      v
  blobstore.PresignGet(ref, 15min)  --> client GETs bytes directly from S3/MinIO
```

`internal/blobstore.Store`:

```go
type Store interface {
    Put(ctx, key string, r io.Reader, size int64, contentType string) error
    Get(ctx, key string) (io.ReadCloser, error)
    Delete(ctx, key string) error
    PresignGet(ctx, key string, ttl time.Duration) (string, error)
}
```

Two backends:

- **S3-compatible** (`s3Store`) — a minimal, hand-rolled AWS Signature
  Version 4 signer (header auth for Put/Get/Delete, query-string presign for
  downloads). No aws-sdk dependency is added; the repo keeps its dependency
  list lean and the S3 surface used here (three idempotent object ops +
  presign) is small and stable. Path-style addressing
  (`endpoint/bucket/key`) works against both MinIO and AWS S3.
- **Filesystem** (`fsStore`) — dev/test fallback rooted at `BLOB_FS_ROOT`
  (default `/tmp/blobstore`); writes are temp-file + rename. `PresignGet`
  returns a `file://` URL that is only meaningful on the same host.

Key scheme is `{tenant}/{docID-or-blobID}/{filename}`, sanitised
(path-traversal safe: `..`, separators, absolute paths rejected; remaining
unsafe bytes mapped to `_`). Both backends re-validate keys so callers cannot
bypass `blobstore.Key`.

> **Deviation note:** the middle key segment is a server-generated blob ID
> (`blb-<hex>`), not the `docupload` document ID — the key must be known
> before `Submit` allocates the document ID. `Document.Ref` always stores the
> full key, so lookups never need to recompute it.

## Configuration (env)

| Var | Meaning | Default |
|-----|---------|---------|
| `S3_ENDPOINT` | host[:port], no scheme | — (required for S3) |
| `S3_BUCKET` | bucket name | — (required for S3) |
| `S3_ACCESS_KEY` / `S3_SECRET_KEY` | credentials | — (required for S3) |
| `S3_REGION` | SigV4 region | `us-east-1` |
| `S3_USE_SSL` | `https` when true | `true` (set `false` for dev MinIO) |
| `BLOB_FS_ROOT` | filesystem fallback root | `/tmp/blobstore` |
| `BLOB_REQUIRED` | `true` = error instead of FS fallback | `false` |

`blobstore.FromEnv()` is **fail-closed**:

- all four `S3_*` required vars set → S3 store;
- **some but not all** set → error naming every missing variable (a
  half-configured client is never returned);
- none set → `BLOB_REQUIRED=true` is an error (production must not silently
  downgrade to local disk); otherwise the filesystem fallback is returned
  with a warning string for the boot log.

When member-svc has a nil store, both blob routes answer
`503 BLOB_UNCONFIGURED` — the same fail-closed contract as the Gap-3/4 rails
(`503 RAIL_UNCONFIGURED`).

## Dev setup (MinIO)

`deploy/docker-compose.yml` already runs MinIO (`minio:9000`, console
`:9001`, root creds `pfaengine` / `pfaengine123`). member-svc gets:

```yaml
environment:
  S3_ENDPOINT: minio:9000
  S3_BUCKET: kyc-documents
  S3_ACCESS_KEY: pfaengine
  S3_SECRET_KEY: pfaengine123
  S3_USE_SSL: "false"
```

Create the bucket once (one-off, not automated):

```
docker compose exec minio mc alias set local http://localhost:9000 pfaengine pfaengine123
docker compose exec minio mc mb local/kyc-documents
```

## Production

Point the same vars at AWS S3 (or any S3-compatible service) with
`S3_USE_SSL=true`, the real region, and an IAM pair scoped to
`s3:PutObject`/`s3:GetObject`/`s3:DeleteObject` on the one bucket. Set
`BLOB_REQUIRED=true` so a config regression fails the boot instead of
downgrading PII to a container filesystem.

## Security notes

- **No public buckets.** All reads go through presigned URLs with a 15-minute
  TTL; anyone holding a URL can read that one object until expiry, so URLs
  are issued only after the owner/staff check and are audited
  (`document.download_url_issued`).
- **Server-side SHA-256.** The hash in `Document.SHA256` is computed from the
  decoded bytes in member-svc; a client-supplied hash is never trusted.
- **Mime allowlist** (`image/jpeg`, `image/png`, `application/pdf`) and a
  10MB decoded-size cap are enforced before anything is stored.
- **Blob bytes are never logged, audited or snapshotted.** Audit records
  carry only `{document_id, kind, size, sha256}`. The Gap-5 snapshot engine
  persists metadata only (`docupload.doc`); blobs do not pass through
  Postgres.
- **Orphan cleanup**: if metadata submission fails after the blob landed, the
  blob is deleted best-effort.
- Presigned URLs bind the credential scope, exact object key and expiry into
  the signature; tampering invalidates the URL.

## Honest caveats

- **Body-size budget**: the shared `httpsrv.MaxBody(10<<20)` middleware caps
  the *raw* request body; base64 inflates ~4/3, so the effective decoded-blob
  cap through `POST /documents/upload` is ~7.5MB until that middleware budget
  is raised (or uploads move to a direct-to-S3 presigned PUT flow).
- **JSON+base64 upload** is simple but memory-bound: the blob is buffered in
  member-svc for hashing before the Put. Fine at 10MB; a multipart or
  presigned-PUT flow would stream for larger artefacts.
- The hand-rolled SigV4 signer covers single-chunk payloads and presigned
  GETs only — no multipart upload, no SSE-KMS headers, no virtual-hosted
  addressing. If those become requirements, prefer adopting `aws-sdk-go-v2`
  over extending the signer.
- The filesystem fallback is single-host and non-durable; it exists so dev
  and tests run without MinIO. `BLOB_REQUIRED=true` keeps it out of
  production.
- Bucket lifecycle/retention policy (e.g. purge rejected documents after the
  NDPA retention window) is a bucket-side setting, not implemented here.
