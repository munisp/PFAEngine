// Package blobstore implements Gap 7: object storage for document blobs
// (KYC passport photos, NIN slips, ID cards — sensitive PII under NDPA).
// Document metadata lives in internal/docupload; the bytes live here and
// never pass through logs, snapshots or the audit trail.
//
// Two backends implement the Store contract:
//
//   - s3Store: any S3-compatible service (MinIO in dev, AWS S3 in
//     production) via a minimal hand-rolled SigV4 signer — the repo keeps
//     dependencies lean, so no aws-sdk is pulled in. Path-style addressing
//     is used because MinIO (the dev backend) requires it.
//   - fsStore: a local-filesystem fallback (BLOB_FS_ROOT, default
//     /tmp/blobstore) for development and tests.
//
// Configuration is fail-closed via FromEnv: a partially specified S3
// config is an error naming the missing variables, never a silently
// half-configured client. When nothing is configured, BLOB_REQUIRED=true
// forces an error (production); otherwise the filesystem fallback is
// returned together with a human-readable warning string.
package blobstore

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Store is the object-storage contract for document blobs.
type Store interface {
	// Put stores size bytes from r under key with the given content type.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// Get returns the blob body; the caller must close it.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete removes the blob (best-effort semantics are the caller's choice).
	Delete(ctx context.Context, key string) error
	// PresignGet returns a time-limited URL granting GET access to key.
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// ---- key scheme ---------------------------------------------------------------

// Key builds the canonical object key "{tenant}/{docID}/{filename}" with
// path-traversal safety: every segment is validated, and any byte outside
// [A-Za-z0-9._-] is replaced with '_'. Segments containing path separators
// or ".." sequences are rejected outright (fail-closed, never silently
// rewritten into something ambiguous).
func Key(tenant, docID, filename string) (string, error) {
	segs := []string{tenant, docID, filename}
	out := make([]string, 0, 3)
	for i, s := range segs {
		v, err := sanitizeSegment(s)
		if err != nil {
			return "", fmt.Errorf("blobstore: key segment %d: %w", i, err)
		}
		out = append(out, v)
	}
	return strings.Join(out, "/"), nil
}

// sanitizeSegment validates one key segment. It rejects empty segments,
// path separators, absolute paths and ".." traversal; remaining unsafe
// bytes (spaces, unicode, …) are mapped to '_'.
func sanitizeSegment(s string) (string, error) {
	if s == "" {
		return "", errors.New("empty segment")
	}
	if strings.ContainsAny(s, "/\\") {
		return "", fmt.Errorf("segment %q contains a path separator", s)
	}
	if strings.Contains(s, "..") {
		return "", fmt.Errorf("segment %q contains '..'", s)
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	v := b.String()
	if v == "" || v == "." {
		return "", fmt.Errorf("segment %q sanitises to nothing usable", s)
	}
	return v, nil
}

// validateKey re-checks a fully joined key before any storage backend
// touches it, so callers cannot bypass Key() to smuggle traversal.
func validateKey(key string) error {
	if key == "" {
		return errors.New("blobstore: empty key")
	}
	if strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return fmt.Errorf("blobstore: key %q is absolute or contains '\\'", key)
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("blobstore: key %q has unsafe segment %q", key, seg)
		}
	}
	return nil
}

// ---- configuration ------------------------------------------------------------

// Config carries the S3 connection settings.
type Config struct {
	Endpoint  string // host[:port], no scheme
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string // default us-east-1
	UseSSL    bool
}

var s3RequiredEnv = []string{"S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY"}

// FromEnv builds a Store from the environment.
//
//   - All of S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY, S3_SECRET_KEY set:
//     an S3-compatible store (S3_REGION defaults to us-east-1; S3_USE_SSL
//     defaults to true — set it to "false" for plaintext dev MinIO).
//   - Some but not all set: error listing every missing variable
//     (fail-closed; a half-configured client is never returned).
//   - None set: BLOB_REQUIRED=true is an error (production must not
//     silently downgrade); otherwise the filesystem fallback under
//     BLOB_FS_ROOT (default /tmp/blobstore) is returned with a warning.
//
// The warning string is empty on the fully configured S3 path. Blob bytes
// are never logged; the warning mentions configuration only.
func FromEnv() (Store, string, error) {
	vals := map[string]string{}
	var missing []string
	for _, k := range s3RequiredEnv {
		v := strings.TrimSpace(os.Getenv(k))
		vals[k] = v
		if v == "" {
			missing = append(missing, k)
		}
	}
	switch {
	case len(missing) == 0:
		cfg := Config{
			Endpoint:  vals["S3_ENDPOINT"],
			Bucket:    vals["S3_BUCKET"],
			AccessKey: vals["S3_ACCESS_KEY"],
			SecretKey: vals["S3_SECRET_KEY"],
			Region:    envOr("S3_REGION", "us-east-1"),
			UseSSL:    envBool("S3_USE_SSL", true),
		}
		return NewS3(cfg), "", nil
	case len(missing) < len(s3RequiredEnv):
		return nil, "", fmt.Errorf("blobstore: partial S3 configuration, missing: %s", strings.Join(missing, ", "))
	}
	if envBool("BLOB_REQUIRED", false) {
		return nil, "", fmt.Errorf("blobstore: BLOB_REQUIRED=true but S3 is not configured, missing: %s", strings.Join(missing, ", "))
	}
	root := envOr("BLOB_FS_ROOT", "/tmp/blobstore")
	fs, err := NewFS(root)
	if err != nil {
		return nil, "", err
	}
	return fs, fmt.Sprintf("blobstore: S3 not configured (%s unset) — dev filesystem store at %s; set BLOB_REQUIRED=true to fail closed", strings.Join(missing, ", "), root), nil
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// ---- filesystem fallback --------------------------------------------------------

type fsStore struct{ root string }

// NewFS returns a Store rooted at dir (created if absent). Dev/test only:
// PresignGet returns a file:// URL that is meaningless outside the host.
func NewFS(dir string) (Store, error) {
	if dir == "" {
		return nil, errors.New("blobstore: filesystem root required")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("blobstore: create root: %w", err)
	}
	return &fsStore{root: dir}, nil
}

func (s *fsStore) path(key string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	// Clean with a forced leading slash so the result can never escape root.
	p := filepath.Join(s.root, filepath.Clean("/"+key))
	if !strings.HasPrefix(p, filepath.Clean(s.root)+string(os.PathSeparator)) {
		return "", fmt.Errorf("blobstore: key %q escapes root", key)
	}
	return p, nil
}

func (s *fsStore) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	// Write to a temp file then rename so concurrent readers never see a
	// partial blob.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, r)
	if err != nil {
		tmp.Close()
		return err
	}
	if size >= 0 && n != size {
		tmp.Close()
		return fmt.Errorf("blobstore: wrote %d bytes, expected %d", n, size)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func (s *fsStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

func (s *fsStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *fsStore) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p, err := s.path(key)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "file", Path: p}).String(), nil
}

// ---- S3-compatible backend (hand-rolled SigV4) ----------------------------------

type s3Store struct {
	cfg Config
	hc  *http.Client
	now func() time.Time // test hook
}

// NewS3 returns a Store speaking the S3 REST API with AWS Signature
// Version 4. Path-style addressing (endpoint/bucket/key) is used so the
// same client works against MinIO and AWS S3. The config is used as-is;
// build it via FromEnv for fail-closed validation.
func NewS3(cfg Config) Store {
	return &s3Store{cfg: cfg, hc: &http.Client{Timeout: 60 * time.Second}, now: time.Now}
}

func (s *s3Store) scheme() string {
	if s.cfg.UseSSL {
		return "https"
	}
	return "http"
}

// objectURL builds "scheme://endpoint/bucket/key" with the key escaped
// per-segment (S3 SigV4 canonicalisation rule).
func (s *s3Store) objectURL(key string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	return s.scheme() + "://" + s.cfg.Endpoint + "/" + s.cfg.Bucket + "/" + escapeKey(key), nil
}

// escapeKey URI-encodes each key segment (keeping '/'), leaving RFC 3986
// unreserved bytes untouched — our sanitised keys are already unreserved.
func escapeKey(key string) string {
	var b strings.Builder
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c == '/' || c == '-' || c == '_' || c == '.' || c == '~' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func (s *s3Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	// The payload hash must be known before the request is signed, and the
	// body may be needed again on redirect — buffer it (callers cap the
	// size; document uploads are limited to 10MB upstream).
	body, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("blobstore: read payload: %w", err)
	}
	if size >= 0 && int64(len(body)) != size {
		return fmt.Errorf("blobstore: read %d bytes, expected %d", len(body), size)
	}
	u, err := s.objectURL(key)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.ContentLength = int64(len(body))
	s.sign(req, sha256Hex(body), s.now())
	return s.do(req, "PutObject")
}

func (s *s3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	u, err := s.objectURL(key)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	s.sign(req, emptySHA256, s.now())
	resp, err := s.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("blobstore: GetObject: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		return nil, statusErr("GetObject", resp)
	}
	return resp.Body, nil
}

func (s *s3Store) Delete(ctx context.Context, key string) error {
	u, err := s.objectURL(key)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	s.sign(req, emptySHA256, s.now())
	return s.do(req, "DeleteObject")
}

// PresignGet mints a SigV4 query-string presigned GET URL (no network).
// The URL embeds the credential scope and expiry; anyone holding it can
// read the object until it expires, so callers must keep TTLs short.
func (s *s3Store) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if ttl <= 0 || ttl > 7*24*time.Hour {
		return "", fmt.Errorf("blobstore: presign ttl must be 1s..7d, got %s", ttl)
	}
	u, err := s.objectURL(key)
	if err != nil {
		return "", err
	}
	t := s.now().UTC()
	amzDate, dateStamp := amzDates(t)
	scope := s.scope(dateStamp)
	q := url.Values{}
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", s.cfg.AccessKey+"/"+scope)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", strconv.FormatInt(int64(ttl/time.Second), 10))
	q.Set("X-Amz-SignedHeaders", "host")
	// Canonical request: signed headers are host-only, payload unsigned.
	canonicalURI := "/" + s.cfg.Bucket + "/" + escapeKey(key)
	canonical := strings.Join([]string{
		http.MethodGet,
		canonicalURI,
		canonicalQuery(q),
		"host:" + s.cfg.Endpoint + "\n",
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")
	sig := s.signature(t, canonical)
	q.Set("X-Amz-Signature", sig)
	return u + "?" + canonicalQuery(q), nil
}

// ---- SigV4 plumbing -------------------------------------------------------------

const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func amzDates(t time.Time) (amzDate, dateStamp string) {
	return t.Format("20060102T150405Z"), t.Format("20060102")
}

func (s *s3Store) scope(dateStamp string) string {
	return dateStamp + "/" + s.cfg.Region + "/s3/aws4_request"
}

// signature derives the SigV4 signing key and signs a canonical request.
func (s *s3Store) signature(t time.Time, canonicalRequest string) string {
	amzDate, dateStamp := amzDates(t.UTC())
	scope := s.scope(dateStamp)
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))
	kDate := hmacSHA256([]byte("AWS4"+s.cfg.SecretKey), dateStamp)
	kRegion := hmacSHA256(kDate, s.cfg.Region)
	kService := hmacSHA256(kRegion, "s3")
	kSigning := hmacSHA256(kService, "aws4_request")
	return hex.EncodeToString(hmacSHA256(kSigning, stringToSign))
}

// canonicalQuery renders a query string with keys sorted and every
// name/value RFC 3986 percent-encoded (SigV4 requirement).
func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, escapeQuery(k)+"="+escapeQuery(v))
		}
	}
	return strings.Join(parts, "&")
}

func escapeQuery(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' || c == '_' || c == '.' || c == '~' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// sign applies header-based SigV4 to an outbound request. Only
// host;x-amz-content-sha256;x-amz-date are signed (Content-Type is sent
// but left unsigned to keep the signed-header set minimal and stable).
func (s *s3Store) sign(req *http.Request, payloadHash string, t time.Time) {
	t = t.UTC()
	amzDate, _ := amzDates(t)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := "host:" + req.URL.Host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"
	canonical := strings.Join([]string{
		req.Method, canonicalURI, canonicalQuery(req.URL.Query()),
		canonicalHeaders, signedHeaders, payloadHash,
	}, "\n")
	sig := s.signature(t, canonical)
	_, dateStamp := amzDates(t)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.cfg.AccessKey+"/"+s.scope(dateStamp)+
		", SignedHeaders="+signedHeaders+", Signature="+sig)
}

func (s *s3Store) do(req *http.Request, op string) error {
	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("blobstore: %s: %w", op, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return statusErr(op, resp)
	}
	return nil
}

// statusErr summarises a failed S3 response. The body is never included:
// S3 error XML can echo request metadata, and blobs/keys must not leak
// into logs via error strings.
func statusErr(op string, resp *http.Response) error {
	return fmt.Errorf("blobstore: %s: S3 status %s", op, resp.Status)
}
