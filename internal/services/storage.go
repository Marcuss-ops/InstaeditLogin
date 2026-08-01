// Package services implements the StorageProvider abstraction used by
// /api/v1/storage/upload-url. A single S3-compatible provider is wired
// at startup based on environment variables (see cmd/server/main.go
// and internal/config):
//
//	S3-compatible — requires S3_ENDPOINT + S3_BUCKET + S3_ACCESS_KEY +
//	                S3_SECRET_KEY. Optional S3_REGION (default "us-east-1").
//	                Uses the standard AWS SigV4 presigned-URL algorithm
//	                (signS3V4URL). Works with AWS S3, MinIO, Cloudflare R2,
//	                Backblaze B2, Wasabi, and any other S3-compatible store.
//
// The chosen implementation returns a StorageProvider bound to a single
// bucket. The handler calls SignUpload to mint an UploadGrant containing
// both the time-limited upload URL and the bucket's public media URL
// the client stores as Post.MediaURL after the PUT succeeds.
//
// Path keying convention: uploads/{user_id}/{uuid4}_{sanitized_name}.
// The user_id prefix is required for tenant isolation under shared-bucket
// ACLs. The UUID4 component (crypto/rand, RFC 4122 v4) makes keys
// unguessable so the same filename from the same user never collides
// across uploads.
//
// Taglio 3.1: SupabaseProvider was removed. Storage is now exclusively
// S3-compatible; main.go panics at startup if any of the four required
// env vars is missing.
package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// UploadGrant is the response shape for /api/v1/storage/upload-url. The
// upload_url accepts a PUT for `ExpiresAt - now` window; after that it
// expires and the client must re-request. media_url is what the client
// stores as Post.MediaURL once the PUT succeeds.
type UploadGrant struct {
	UploadURL string    `json:"upload_url"`
	MediaURL  string    `json:"media_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// StorageProvider generates UploadGrants for client uploads. The
// handler stays provider-agnostic — it only knows the interface.
// BucketProvider exposes the bucket bound to a storage provider. It is
// optional so existing test doubles and single-bucket providers remain
// compatible with StorageProvider.
type BucketProvider interface {
	Bucket() string
}

type StorageProvider interface {
	// Provider returns the implementation tag ("s3"). Useful for logging
	// + the /health endpoint so operators can see which backend is
	// wired without tailing env vars.
	Provider() string
	// SignUpload mints a TTL-bound upload URL for key scoped under
	// user_id plus the corresponding public media_url. content_type and
	// size_bytes are forwarded so providers can pass them to Content-Length
	// headers if they support header-based validation.
	SignUpload(ctx context.Context, userID int64, key, contentType string, sizeBytes int64, ttl time.Duration) (*UploadGrant, error)
	// VerifyUpload (Taglio 3.2) HEADs the object at key and returns
	// the server-reported content-type + size. The /complete handler
	// calls this to commit a media asset: the asset is marked `ready`
	// only if the S3 server confirms the object exists with the
	// expected size + content-type. Returns an error on 404 or any
	// non-2xx.
	VerifyUpload(ctx context.Context, key string) (contentType string, sizeBytes int64, err error)
	// AssetURL (Taglio 3.2) returns the trusted internal URL the
	// publish flow passes to per-platform providers. The URL is
	// always built from this provider's bucket + the asset's
	// upload_key — never from a user-controlled string. This is the
	// single chokepoint that prevents SSRF: even if a future
	// contributor accidentally exposes a "url" field somewhere, the
	// only path the platform API ever sees is AssetURL(key).
	AssetURL(key string) string
	// GetObject returns a presigned GET URL for the object at key.
	// Used by server-side flows that need to read back uploaded bytes.
	GetObject(ctx context.Context, key string, ttl time.Duration) (string, error)
	// Upload (P1 Velox integration) streams `body` directly into the
	// bucket at `key`. Used by the worker's server-side ingest paths
	// — primarily VeloxArtifactStream drain after the size+SHA
	// validation pass — replacing the historical
	// PresignedURL-then-PUT-from-the-client pattern that doesn't
	// fit a server-to-server flow.
	//
	// Contract:
	//   * sizeBytes MUST equal the body length on success (Content-Length
	//     header is set accordingly). A short body yields a truncated
	//     upload; S3 will reject it on its own MD5/size check, but we
	//     surface a clear error regardless.
	//   * contentType is forwarded as the Content-Type header.
	//     Pass "" to skip the header (S3 will infer from the object
	//     extension or default to application/octet-stream).
	//   * SIGV4 signing reuses the same signer with UNSIGNED-PAYLOAD
	//     (same approach as SignUpload / VerifyUpload). The tradeoff:
	//     S3 disables payload-integrity verification on UNSIGNED-PAYLOAD
	//     requests. That's acceptable here because (a) the body has
	//     already been SHA-256 validated by the upstream source (the
	//     VeloxArtifactStream), and (b) the request itself is
	//     authenticated via X-Amz-Signature — a network attacker
	//     cannot MITM without also forging the signature.
	//   * Time-bound: the caller MUST pass a ctx with a deadline. The
	//     struct's 15s http.Timeout is a fail-safe only; large uploads
	//     (>15s of streamed bytes) need a generous ctx timeout.
	//
	// Returns the number of bytes uploaded (== sizeBytes on success) or
	// a non-nil error on transport failure, sign failure, body read
	// failure, or non-2xx response. Errors are wrapped with `fmt.Errorf`
	// + `%w` so callers can `errors.Is` for specific sentinels if
	// desired (none defined yet).
	Upload(ctx context.Context, body io.Reader, key, contentType string, sizeBytes int64) (int64, error)
}

// S3Provider generates an AWS SigV4-signed PUT URL against an arbitrary
// S3-compatible endpoint. The address style is virtual-hosted
// (https://{bucket}.{endpoint-host}/{key}), which works for AWS S3 and
// most S3-compatible stores (MinIO, R2, B2, Wasabi). For stores that
// only support path-style (e.g. older MinIO without DNS), the
// S3_ENDPOINT should be set to the bucket subdomain directly
// (e.g. "https://mybucket.minio.example.com") — the signer still works.
//
// Signing is hand-rolled to avoid pulling in aws-sdk-go-v2 (~50 MB of
// transitively downloaded modules). The implementation follows the
// AWS SigV4 reference spec and is identical for every S3-compatible
// backend (only the endpoint host + region change):
//
//	https://docs.aws.amazon.com/general/latest/gr/sigv4-create-canonical-request.html
//	https://docs.aws.amazon.com/general/latest/gr/sigv4-create-string-to-sign.html
//	https://docs.aws.amazon.com/general/latest/gr/sigv4-calculate-signature.html
//
// For presigned URLs the canonical request is signed with payload hash
// UNSIGNED-PAYLOAD so the client doesn't need to hash the entire file
// upfront. This is the canonical approach for client-side uploads.
type S3Provider struct {
	endpoint  string // e.g. "https://s3.us-east-1.amazonaws.com" (no trailing slash, no bucket)
	scheme    string // endpoint scheme; local MinIO commonly uses "http"
	bucket    string
	region    string // SigV4 credential-scope component; default "us-east-1"
	accessKey string
	secretKey string
	baseHost  string // path-style: endpoint host; virtual-hosted: "{bucket}.{endpoint-host}"
	pathStyle bool   // when true, objects live at /{bucket}/{key} (not {bucket}.host/{key})
	mediaBase string // "{endpoint}/{bucket}" — pre-computed for MediaURL
	http      *http.Client
	logger    *slog.Logger
}

// NewS3Provider builds the provider. endpoint MUST be the bare host URL
// (no bucket, no trailing slash, no path) — e.g.
// "https://s3.us-east-1.amazonaws.com" or "https://minio.example.com".
// region is the SigV4 credential-scope component; pass "" to default
// to "us-east-1" (acceptable for AWS S3, MinIO, R2, B2, Wasabi).
// pathStyle selects the addressing scheme: virtual-hosted
// ({bucket}.{host}/{key}, the default for AWS S3) or path-style
// ({host}/{bucket}/{key}, required when the S3 host is a single
// fixed origin — e.g. a Cloudflare quick tunnel — that cannot serve
// per-bucket subdomains).
//
// Returns an error (NOT nil) when the endpoint is malformed: an empty
// string, a missing scheme, a non-http(s) scheme, or a missing host.
// This is fail-loud — a typo'd endpoint would otherwise produce a
// syntactically valid signed URL pointing at a dead host, surfacing as
// a confusing 403 from S3 instead of a clear Go-side error.
func NewS3Provider(endpoint, bucket, region, accessKey, secretKey string, pathStyle bool, logger *slog.Logger) (*S3Provider, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if region == "" {
		region = "us-east-1"
	}
	// Parse the endpoint. Fail loud on malformed input instead of
	// silently passing it through to the signer (which would produce
	// a syntactically valid URL pointing at a dead host).
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil {
		return nil, fmt.Errorf("S3 endpoint %q is not a valid URL: %w", endpoint, err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("S3 endpoint %q must use http or https scheme (got %q)", endpoint, u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("S3 endpoint %q has no host (expected format: https://s3.us-east-1.amazonaws.com)", endpoint)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("S3 endpoint %q must be a bare host (no path/query/fragment/userinfo)", endpoint)
	}
	host := u.Scheme + "://" + u.Host
	hostOnly := u.Host
	baseHost := hostOnly
	if !pathStyle {
		baseHost = bucket + "." + hostOnly
	}
	return &S3Provider{
		endpoint:  host,
		scheme:    u.Scheme,
		bucket:    bucket,
		region:    region,
		accessKey: accessKey,
		secretKey: secretKey,
		baseHost:  baseHost,
		pathStyle: pathStyle,
		mediaBase: host + "/" + bucket,
		http:      &http.Client{Timeout: 15 * time.Second},
		logger:    logger,
	}, nil
}

// objectKey returns the full object key used in the signed URL path.
// Path-style prefixes the bucket; virtual-hosted does not (the bucket
// lives in the host).
func (p *S3Provider) objectKey(key string) string {
	if p.pathStyle {
		return p.bucket + "/" + key
	}
	return key
}

// Provider implements StorageProvider.
func (p *S3Provider) Provider() string { return "s3" }

// Bucket implements BucketProvider. The value is the default bucket used
// for new assets; published asset rows persist this value explicitly.
func (p *S3Provider) Bucket() string { return p.bucket }

func (p *S3Provider) hostForBucket(bucket string) string {
	if bucket == "" || bucket == p.bucket {
		return p.baseHost
	}
	if p.pathStyle {
		return strings.TrimPrefix(p.endpoint, p.scheme+"://")
	}
	return bucket + "." + strings.TrimPrefix(p.endpoint, p.scheme+"://")
}

func (p *S3Provider) objectKeyForBucket(bucket, key string) string {
	if p.pathStyle {
		return bucket + "/" + key
	}
	return key
}

// Upload (P1 Velox integration) streams body directly into the bucket
// at key. See the StorageProvider.Upload interface comment for the
// full contract. Implementation choices:
//
//  1. SignS3V4URL with method=PUT, TTL=5m — same signer as
//     SignUpload / VerifyUpload. Reusing it avoids a 200-line second
//     copy of the SigV4 algorithm. The 5-minute TTL is more than
//     enough for server-side workloads (vs. client presigned URLs
//     which might sit for hours).
//
//  2. http.Request body = `body` (the io.Reader the caller passed).
//     Streaming: we DO NOT buffer. The Go HTTP client will write
//     the body to the connection as fast as S3 reads it.
//
//  3. Content-Length header set from sizeBytes. S3 MUST know the
//     body length in advance for PUT (you cannot PUT without
//     Content-Length OR chunked transfer encoding — and our SigV4
//     signer uses UNSIGNED-PAYLOAD, NOT the AWS streaming v4
//     algorithm that supports chunked transfer). So sizeBytes must
//     match the actual body length, otherwise S3 will reject the
//     upload.
//
//  4. Returns sizeBytes on success — NOT len(io.Copy(...)) — because
//     we don't have a separate "how many bytes did the body yield"
//     counter and the contract guarantees the body yielded exactly
//     sizeBytes (caller's responsibility).
//
//  5. Any non-2xx response returns an error wrapped with the
//     status code + key so on-call can grep the logs.
func (p *S3Provider) Upload(ctx context.Context, body io.Reader, key, contentType string, sizeBytes int64) (int64, error) {
	if body == nil {
		return 0, errors.New("storage: Upload: body is nil")
	}
	if sizeBytes <= 0 {
		return 0, fmt.Errorf("storage: Upload: sizeBytes must be > 0 (got %d)", sizeBytes)
	}
	signedURL, signErr := signS3V4URL(
		p.scheme, p.baseHost, p.region, "s3",
		p.objectKey(key), 5*time.Minute, http.MethodPut,
		p.accessKey, p.secretKey,
		time.Now(),
	)
	if signErr != nil {
		return 0, fmt.Errorf("storage: Upload: sign PUT URL for key %q: %w", key, signErr)
	}
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodPut, signedURL, body)
	if reqErr != nil {
		return 0, fmt.Errorf("storage: Upload: build PUT request for key %q: %w", key, reqErr)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if sizeBytes > 0 {
		req.ContentLength = sizeBytes
	}
	resp, doErr := p.http.Do(req)
	if doErr != nil {
		return 0, fmt.Errorf("storage: Upload: PUT %s failed: %w", key, doErr)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("storage: Upload: S3 PUT returned status %d for key %q", resp.StatusCode, key)
	}
	return sizeBytes, nil
}

// AssetURL (Taglio 3.2) returns the trusted internal S3 URL for a
// stored object. The URL uses the same scheme as the presigned upload
// URL (virtual-hosted https://{bucket}.{host}/{key} or path-style
// https://{host}/{bucket}/{key}). This is the SINGLE chokepoint
// through which publish-time URLs flow: a future contributor adding a
// new field on the publish payload cannot accidentally introduce SSRF
// because there is no public API surface for user-controlled URLs.
func (p *S3Provider) AssetURL(key string) string {
	return fmt.Sprintf("%s://%s/%s", p.scheme, p.baseHost, p.objectKey(key))
}

// VerifyUpload (Taglio 3.2) performs a SigV4-signed HEAD against the
// S3 object at key. Returns the server-reported content-type and
// content-length, or an error if the object doesn't exist or S3
// returns a non-2xx. Used by the /complete handler to commit a
// media asset.
//
// The presigned-URL signer (signS3V4URL) is reused with method=HEAD
// and a 5-minute TTL; HEAD is idempotent and the TTL is just the
// URL-expiry window. The signature is computed with
// UNSIGNED-PAYLOAD (the same as PUT presigns) because S3 supports it
// for HEAD too, and reusing the signer avoids a second copy of the
// SigV4 algorithm.
func (p *S3Provider) VerifyUpload(ctx context.Context, key string) (contentType string, sizeBytes int64, err error) {
	signedURL, signErr := signS3V4URL(
		p.scheme, p.baseHost, p.region, "s3",
		p.objectKey(key), 5*time.Minute, http.MethodHead,
		p.accessKey, p.secretKey,
		time.Now(),
	)
	if signErr != nil {
		return "", 0, fmt.Errorf("failed to sign HEAD URL: %w", signErr)
	}
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodHead, signedURL, nil)
	if reqErr != nil {
		return "", 0, fmt.Errorf("failed to build HEAD request: %w", reqErr)
	}
	resp, doErr := p.http.Do(req)
	if doErr != nil {
		return "", 0, fmt.Errorf("HEAD request failed: %w", doErr)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", 0, fmt.Errorf("object not found in S3: %s", key)
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("S3 HEAD returned status %d for key %s", resp.StatusCode, key)
	}
	return resp.Header.Get("Content-Type"), resp.ContentLength, nil
}

// GetObject returns a presigned GET URL for the default bucket object.
func (p *S3Provider) GetObject(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return p.GetObjectWithBucket(ctx, p.bucket, key, ttl)
}

// GetObjectWithBucket returns a fresh presigned GET URL for the explicit
// bucket/object pair persisted on a media asset. The provider still uses
// the same credentials and endpoint, but does not silently substitute its
// default bucket when a canonical asset names another bucket.
func (p *S3Provider) GetObjectWithBucket(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	if bucket == "" {
		return "", errors.New("storage: bucket is required")
	}
	if key == "" {
		return "", errors.New("storage: object key is required")
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return signS3V4URL(
		p.scheme, p.hostForBucket(bucket), p.region, "s3",
		p.objectKeyForBucket(bucket, key), ttl, http.MethodGet,
		p.accessKey, p.secretKey,
		time.Now(),
	)
}
