package main

// Gap 7: document blob handlers — the byte-moving counterparts of the
// Tranche-21 metadata-only document routes. Uploads arrive as base64 JSON,
// are SHA-256 hashed server-side (never trusting a client-supplied hash),
// streamed to the configured object store (MinIO/S3 via internal/blobstore)
// and only then recorded in docupload so Document.Ref points at a blob that
// actually exists. Downloads are short-TTL presigned GET URLs gated to the
// owning member or staff (X-Actor-Role: STAFF/ADMIN).
//
// Security invariants (NDPA — KYC documents are sensitive PII):
//   - blob bytes are never logged, audited or snapshotted;
//   - the audit record carries only {document_id, sha256, size, kind};
//   - mime is allowlisted (image/jpeg, image/png, application/pdf);
//   - the store is fail-closed: a nil store answers 503 BLOB_UNCONFIGURED,
//     never a mock success (same contract as the Gap-3/4 rails).
//
// NOTE: the shared httpsrv.MaxBody(10<<20) middleware caps the raw request
// body at 10MB; base64 inflates ~4/3, so the effective decoded-blob cap
// through this endpoint is ~7.5MB until the middleware budget is raised.

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/munisp/pfaengine/internal/blobstore"
	"github.com/munisp/pfaengine/internal/docupload"
	"github.com/munisp/pfaengine/internal/httpsrv"
)

// maxDocBlobBytes is the hard cap on a decoded document blob (10MB), aligned
// with docupload's own size validation.
const maxDocBlobBytes = 10 << 20

// docBlobTTL is the presigned-download lifetime (short — PII).
const docBlobTTL = 15 * time.Minute

// allowedDocMimes is the upload allowlist (NDPA data-minimisation: only the
// formats the KYC review queue renders).
var allowedDocMimes = map[string]bool{
	"image/jpeg":      true,
	"image/png":       true,
	"application/pdf": true,
}

func errBlobUnconfigured() error {
	return errors.New("blob store not configured (set S3_ENDPOINT/S3_BUCKET/S3_ACCESS_KEY/S3_SECRET_KEY)")
}

// newBlobID mints the object-ID segment of the blob key. It is NOT the
// docupload document ID: the key must be known before Submit allocates one,
// so the blob gets its own random ID and Document.Ref stores the full key.
func newBlobID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is fatal-quality; fall back to a time-based ID
		// rather than failing an upload for entropy quirks in sandboxes.
		return fmt.Sprintf("blb-%d", time.Now().UnixNano())
	}
	return "blb-" + hex.EncodeToString(b[:])
}

// docUploadBlob handles POST /api/v1/documents/upload — a JSON upload where
// data_b64 carries the document bytes. The server computes the SHA-256,
// stores the blob, then submits the metadata; if metadata submission fails
// the just-stored blob is deleted best-effort (no orphans).
func (d *deps) docUploadBlob(w http.ResponseWriter, r *http.Request) {
	if d.blob == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "BLOB_UNCONFIGURED", errBlobUnconfigured())
		return
	}
	var in struct {
		MemberID string `json:"member_id"`
		Kind     string `json:"kind"`
		Filename string `json:"filename"`
		Mime     string `json:"mime"`
		DataB64  string `json:"data_b64"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	in.Mime = strings.ToLower(strings.TrimSpace(in.Mime))
	if !allowedDocMimes[in.Mime] {
		httpsrv.WriteError(w, 400, "MIME_NOT_ALLOWED",
			fmt.Errorf("mime %q not allowed; want image/jpeg, image/png or application/pdf", in.Mime))
		return
	}
	// Decode with a hard cap: 10MB of blob plus one byte to detect overflow.
	data, err := io.ReadAll(io.LimitReader(
		base64.NewDecoder(base64.StdEncoding, strings.NewReader(in.DataB64)),
		maxDocBlobBytes+1))
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_BASE64", fmt.Errorf("data_b64 decode: %w", err))
		return
	}
	if len(data) == 0 {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", errors.New("data_b64 decoded to zero bytes"))
		return
	}
	if len(data) > maxDocBlobBytes {
		httpsrv.WriteError(w, http.StatusRequestEntityTooLarge, "DOC_TOO_LARGE",
			fmt.Errorf("decoded blob exceeds %d bytes", maxDocBlobBytes))
		return
	}
	tenant := httpsrv.TenantOf(r)
	sum := sha256.Sum256(data)
	key, err := blobstore.Key(tenant, newBlobID(), in.Filename)
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_FILENAME", err)
		return
	}
	if err := d.blob.Put(r.Context(), key, bytes.NewReader(data), int64(len(data)), in.Mime); err != nil {
		httpsrv.WriteError(w, http.StatusBadGateway, "BLOB_STORE_FAILED", err)
		return
	}
	doc, err := d.docupload.Submit(tenant, in.MemberID, docupload.Kind(in.Kind),
		in.Filename, in.Mime, int64(len(data)), key, hex.EncodeToString(sum[:]))
	if err != nil {
		// Metadata rejected after the blob landed: clean up best-effort so
		// unreferenced PII does not linger in the bucket.
		_ = d.blob.Delete(r.Context(), key)
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	d.auditReg(r, "document.blob_uploaded", "document", doc.ID,
		fmt.Sprintf("kind=%s size=%d sha256=%s", doc.Kind, doc.SizeBytes, doc.SHA256))
	httpsrv.WriteJSON(w, 201, doc)
}

// docDownloadURL handles GET /api/v1/documents/{id}/url — a 15-minute
// presigned GET URL for the document blob. Members may fetch only their own
// documents (member_id query param must match); staff pass
// X-Actor-Role: STAFF or ADMIN.
func (d *deps) docDownloadURL(w http.ResponseWriter, r *http.Request) {
	if d.blob == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "BLOB_UNCONFIGURED", errBlobUnconfigured())
		return
	}
	tenant := httpsrv.TenantOf(r)
	doc, ok := d.docupload.Get(tenant, r.PathValue("id"))
	if !ok {
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", errors.New("document not found"))
		return
	}
	role := strings.ToUpper(strings.TrimSpace(r.Header.Get("X-Actor-Role")))
	if role != "STAFF" && role != "ADMIN" {
		if memberID := r.URL.Query().Get("member_id"); memberID == "" || memberID != doc.MemberID {
			httpsrv.WriteError(w, http.StatusForbidden, "FORBIDDEN",
				errors.New("members may only fetch their own documents"))
			return
		}
	}
	if doc.Ref == "" {
		httpsrv.WriteError(w, http.StatusConflict, "NO_BLOB",
			errors.New("document has no stored blob (metadata-only record)"))
		return
	}
	u, err := d.blob.PresignGet(r.Context(), doc.Ref, docBlobTTL)
	if err != nil {
		httpsrv.WriteError(w, http.StatusBadGateway, "PRESIGN_FAILED", err)
		return
	}
	d.auditReg(r, "document.download_url_issued", "document", doc.ID,
		fmt.Sprintf("ttl=%s", docBlobTTL))
	httpsrv.WriteJSON(w, 200, map[string]any{
		"document_id": doc.ID,
		"url":         u,
		"expires_in":  int(docBlobTTL / time.Second),
	})
}
