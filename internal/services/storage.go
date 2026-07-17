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
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
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

// AssetURL (Taglio 3.2) returns the trusted internal S3 URL for a
// stored object. The URL uses the same scheme as the presigned upload
// URL (virtual-hosted https://{bucket}.{host}/{key} or path-style
// https://{host}/{bucket}/{key}). This is the SINGLE chokepoint
// through which publish-time URLs flow: a future contributor adding a
// new field on the publish payload cannot accidentally introduce SSRF
// because there is no public API surface for user-controlled URLs.
func (p *S3Provider) AssetURL(key string) string {
	return fmt.Sprintf("https://%s/%s", p.baseHost, p.objectKey(key))
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
		p.baseHost, p.region, "s3",
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

// SignUpload generates a SigV4 PUT URL. For presigned PUTs, the canonical
// request signs only `host` — content-type and content-length headers
// are forwarded by the client but do not participate in the signature
// (S3-compatible stores accept the upload as long as X-Amz-Signature
// validates).
func (p *S3Provider) SignUpload(ctx context.Context, userID int64, key, contentType string, sizeBytes int64, ttl time.Duration) (*UploadGrant, error) {
	_ = ctx
	_ = userID
	_ = contentType
	_ = sizeBytes
	uploadURL, err := signS3V4URL(
		p.baseHost, p.region, "s3",
		p.objectKey(key), ttl, http.MethodPut,
		p.accessKey, p.secretKey,
		time.Now(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to sign S3 URL: %w", err)
	}

	mediaURL := fmt.Sprintf("https://%s/%s", p.baseHost, p.objectKey(key))
	return &UploadGrant{
		UploadURL: uploadURL,
		MediaURL:  mediaURL,
		ExpiresAt: time.Now().Add(ttl),
	}, nil
}

// signS3V4URL is the AWS SigV4 presigned-URL signer implemented in pure
// stdlib (crypto/hmac, crypto/sha256, encoding/hex). Returns the
// fully-formed URL ready for the client to PUT.
//
// Ref: https://docs.aws.amazon.com/AmazonS3/latest/API/sigv4-query-string-auth.html
//
// Parameters:
//   - host:     bucket virtual host (e.g. "mybucket.s3.us-east-1.amazonaws.com"
//     or "mybucket.minio.example.com" for S3-compatible stores)
//   - region:   SigV4 credential-scope component
//   - service:  "s3"
//   - key:      object key (already URL-safe per BuildUploadKey)
//   - ttl:      X-Amz-Expires value in seconds
//   - method:   HTTP verb (PUT for upload, GET in theory)
//   - now:      time used for X-Amz-Date (caller injects for determinism
//     in tests; production passes time.Now())
//
// The canonical query string is BOTH the input to the SigV4 signing AND
// the query string of the returned URL — they MUST be identical for the
// signature to validate server-side. The signature is appended as
// &X-Amz-Signature={hex}.
func signS3V4URL(host, region, service, key string, ttl time.Duration, method, accessKeyID, secretAccessKey string, now time.Time) (string, error) {
	const algorithm = "AWS4-HMAC-SHA256"

	amzDate := now.UTC().Format("20060102T150405Z")
	dateStamp := now.UTC().Format("20060102")

	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, service)
	// raw credential — '/' stays for the canonical_request string-to-sign,
	// then encoded per RFC 3986 unreserved-only when placed in the query
	// string (canonicalQueryString handles that).
	credential := accessKeyID + "/" + credentialScope

	params := map[string]string{
		"X-Amz-Algorithm":     algorithm,
		"X-Amz-Credential":    credential,
		"X-Amz-Date":          amzDate,
		"X-Amz-Expires":       fmt.Sprintf("%d", int(ttl.Seconds())),
		"X-Amz-SignedHeaders": "host",
	}
	canonicalQuery := canonicalQueryString(params)

	canonicalURIPath := canonicalURI(key)

	canonicalHeaders := "host:" + host + "\n"
	signedHeaders := "host"
	payloadHash := "UNSIGNED-PAYLOAD"

	canonicalRequest := strings.Join([]string{
		method,
		canonicalURIPath,
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	hashedCanonicalRequest := sha256Hex(canonicalRequest)
	stringToSign := strings.Join([]string{
		algorithm,
		amzDate,
		credentialScope,
		hashedCanonicalRequest,
	}, "\n")

	signingKey := deriveSigningKey(secretAccessKey, dateStamp, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	// The URL query string is the canonical query + signature appended.
	// Same encoding — RFC 3986 unreserved-only — as the canonical request,
	// so the signature validates.
	finalQuery := canonicalQuery + "&X-Amz-Signature=" + signature

	return fmt.Sprintf("https://%s%s?%s", host, canonicalURIPath, finalQuery), nil
}

// canonicalURI returns the path component of a SigV4 request, RFC 3986-
// encoded per segment. Preserves a trailing "/" only when the key itself
// ends with "/" so callers can publish folder markers.
func canonicalURI(key string) string {
	if key == "" {
		return "/"
	}
	segments := strings.Split(key, "/")
	encoded := make([]string, len(segments))
	for i, seg := range segments {
		encoded[i] = uriEncodePathSegment(seg)
	}
	uri := "/" + strings.Join(encoded, "/")
	if strings.HasSuffix(key, "/") && !strings.HasSuffix(uri, "/") {
		uri += "/"
	}
	return uri
}

// canonicalQueryString builds a SigV4 canonical query string from a
// (key,value) map. Keys are sorted lexicographically. Values are URI-
// encoded per RFC 3986 unreserved-only (uriEncodeQueryComponent).
//
// Empty values produce "key=" pairs (NOT omitted) so the signed payload
// matches what AWS validators compute.
//
// Ref: https://docs.aws.amazon.com/general/latest/gr/sigv4-create-canonical-request.html
func canonicalQueryString(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sortStrings(keys)

	canonical := make([]string, 0, len(params))
	for _, k := range keys {
		canonical = append(canonical, k+"="+uriEncodeQueryComponent(params[k]))
	}
	return strings.Join(canonical, "&")
}

// sortStrings is a tiny insertion sort; n is small (≤8 params). Avoids
// importing sort just for this.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// uriEncodePathSegment applies RFC 3986 unreserved-char encoding:
// [A-Za-z0-9-_.~] pass through, everything else becomes %XX uppercase.
// Multi-byte UTF-8 runes are encoded byte-by-byte. Matches AWS SigV4
// canonical URI encoding rule:
//
//	"URI-encode each path segment per RFC 3986."
//
// Ref: https://docs.aws.amazon.com/general/latest/gr/sigv4-create-canonical-request.html
func uriEncodePathSegment(s string) string {
	var b strings.Builder
	b.Grow(len(s) * 3)
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == '~' {
			b.WriteRune(r)
		} else {
			for _, b2 := range []byte(string(r)) {
				fmt.Fprintf(&b, "%%%02X", b2)
			}
		}
	}
	return b.String()
}

// uriEncodeQueryComponent is identical to uriEncodePathSegment for
// SigV4 — both use the same RFC 3986 unreserved-only rule. Kept under a
// distinct name to surface intent at call sites.
func uriEncodeQueryComponent(s string) string { return uriEncodePathSegment(s) }

// deriveSigningKey computes the four-step HMAC-SHA256 chain per AWS
// spec:
//
//	kDate  = HMAC("AWS4"+secret, dateStamp)
//	kRegion = HMAC(kDate, region)
//	kService = HMAC(kRegion, service)
//	kSigning = HMAC(kService, "aws4_request")
//
// Ref: https://docs.aws.amazon.com/general/latest/gr/sigv4-calculate-signature.html
func deriveSigningKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// newUUID4 returns an RFC 4122 v4 UUID generated from crypto/rand. On
// the (very unlikely) OS failure of crypto/rand this returns a valid-
// shape UUID with version 4 + variant 10 bits set; we'd prefer not to
// panic since this is on a request hot-path.
func newUUID4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Don't panic — fill with time-based seed so the UUID still has
		// a valid shape. We're trading predictability for availability.
		n := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(n >> (uint(i) * 8))
		}
	}
	// RFC 4122 v4 layout: set version (4) and variant (10) bits.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// BuildUploadKey assembles the storage key for a user upload.
// Pattern: uploads/{user_id}/{uuid4}_{sanitized-filename}.
//
// The user_id prefix provides tenant isolation. The UUID4 component
// makes the key unguessable so the same filename from the same user
// never collides across uploads.
func BuildUploadKey(userID int64, filename string) string {
	return fmt.Sprintf("uploads/%d/%s_%s",
		userID, newUUID4(), sanitizeFilename(filename))
}

// sanitizeFilename reduces a client-provided filename to a safe token
// suitable for an S3 object key. Steps:
//  1. Strip any path components (path.Base) so ".." never escapes.
//  2. Replace unsafe chars (anything outside [A-Za-z0-9-_.]) with '_'.
//  3. Trim to 200 chars to keep the final key compact.
//  4. Reject empty / "." / ".." by returning "file" — defensive.
func sanitizeFilename(filename string) string {
	base := path.Base(filename)
	var b strings.Builder
	b.Grow(len(base))
	for _, r := range base {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	s := b.String()
	if s == "" || s == "." || s == ".." {
		return "file"
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
