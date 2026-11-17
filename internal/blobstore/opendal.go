// opendal.go implements the PRIMARY blob storage backend: an HTTP client
// for storage-svc, the platform's OpenDAL microservice (see
// storage-svc/ and docs/OPENDAL.md). storage-svc owns the actual object
// store (fs, s3, gcs, azblob, memory) via Apache OpenDAL; this client is
// a thin, dependency-free (net/http only) adapter so the Go services stay
// CGO_ENABLED=0 and never link the OpenDAL Go bindings.
//
// Auth: every request carries `Authorization: Bearer <STORAGE_SVC_TOKEN>`.
// PresignGet delegates to storage-svc's POST /presign, which mints an
// HMAC-SHA256 signed URL valid against GET /obj/{key}?exp=..&sig=.. —
// backend-independent (works uniformly even for fs/memory backends).
package blobstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// opendalStore talks to storage-svc over HTTP.
type opendalStore struct {
	base  string // scheme://host[:port], no trailing slash
	token string
	hc    *http.Client
}

// NewOpenDAL returns a Store backed by storage-svc at baseURL. Fail-closed:
// an empty baseURL or token is an error — a silently unauthenticated client
// must never reach production.
func NewOpenDAL(baseURL, token string) (Store, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, errors.New("blobstore: STORAGE_SVC_URL is required for the opendal backend")
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return nil, fmt.Errorf("blobstore: STORAGE_SVC_URL %q must start with http:// or https://", base)
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("blobstore: STORAGE_SVC_TOKEN is required for the opendal backend (storage-svc is fail-closed without it)")
	}
	return &opendalStore{
		base:  base,
		token: token,
		hc:    &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// objectURL builds "base/obj/<escaped key>" after re-validating the key.
func (s *opendalStore) objectURL(key string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	return s.base + "/obj/" + escapeKey(key), nil
}

func (s *opendalStore) newRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	return req, nil
}

func (s *opendalStore) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	// Buffer the payload so the size contract is enforced before any byte
	// leaves the process (document uploads are capped at 10MB upstream).
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
	req, err := s.newRequest(ctx, http.MethodPut, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.ContentLength = int64(len(body))
	return s.do(req, "PutObject")
}

func (s *opendalStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	u, err := s.objectURL(key)
	if err != nil {
		return nil, err
	}
	req, err := s.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("blobstore: GetObject: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		return nil, svcStatusErr("GetObject", resp)
	}
	return resp.Body, nil
}

// Delete is idempotent: storage-svc maps a missing object to success, and
// a 404 is treated the same way here defensively.
func (s *opendalStore) Delete(ctx context.Context, key string) error {
	u, err := s.objectURL(key)
	if err != nil {
		return err
	}
	req, err := s.newRequest(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("blobstore: DeleteObject: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode/100 != 2 {
		return svcStatusErr("DeleteObject", resp)
	}
	return nil
}

// presignRequest / presignResponse mirror storage-svc's POST /presign API.
type presignRequest struct {
	Key     string `json:"key"`
	TTLSecs int64  `json:"ttl_secs"`
}

type presignResponse struct {
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expires_at"`
}

// PresignGet delegates to storage-svc, which mints a backend-independent
// HMAC-SHA256 signed URL (works for fs/memory too, unlike native S3
// presign). The URL is served by storage-svc itself, so it is reachable
// from anywhere storage-svc is — keep TTLs short.
func (s *opendalStore) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if ttl <= 0 || ttl > 7*24*time.Hour {
		return "", fmt.Errorf("blobstore: presign ttl must be 1s..7d, got %s", ttl)
	}
	if err := validateKey(key); err != nil {
		return "", err
	}
	payload, err := json.Marshal(presignRequest{Key: key, TTLSecs: int64(ttl / time.Second)})
	if err != nil {
		return "", err
	}
	req, err := s.newRequest(ctx, http.MethodPost, s.base+"/presign", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("blobstore: PresignGet: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", svcStatusErr("PresignGet", resp)
	}
	var out presignResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("blobstore: PresignGet: decode response: %w", err)
	}
	if out.URL == "" {
		return "", errors.New("blobstore: PresignGet: storage-svc returned an empty url")
	}
	return out.URL, nil
}

func (s *opendalStore) do(req *http.Request, op string) error {
	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("blobstore: %s: %w", op, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return svcStatusErr(op, resp)
	}
	return nil
}

// svcStatusErr summarises a failed storage-svc response without echoing
// the body: error JSON can carry request metadata, and keys/blobs must
// not leak into logs via error strings.
func svcStatusErr(op string, resp *http.Response) error {
	return fmt.Errorf("blobstore: %s: storage-svc status %s", op, resp.Status)
}
