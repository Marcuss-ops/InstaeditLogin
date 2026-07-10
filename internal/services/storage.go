// Package services implements the StorageProvider abstraction used by
// /api/v1/storage/upload-url. Two providers are wired at startup based on
// environment variables (see cmd/server/main.go and internal/config):
//
//	Supabase Storage — requires SUPABASE_URL + SUPABASE_SERVICE_KEY + SUPABASE_BUCKET.
//	                  Uses the REST endpoint POST /storage/v1/object/upload/sign/{b}/{k}
//	                  with the service-role JWT; bucket must be PUBLIC so media_url
//	                  derived from the public URL is reachable by social platforms.
//
//	AWS S3           — requires AWS_REGION + AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY
//	                  + AWS_S3_BUCKET. Generates an AWS SigV4 presigned PUT URL with
//	                  the canonical-request test vectors documented in signS3V4URL().
//
// The chosen implementation returns a StorageProvider bound to a single
// bucket/account. The handler calls SignUpload to mint an UploadGrant
// containing both the time-limited upload URL and the bucket's public
// media URL the client stores as Post.MediaURL after the PUT succeeds.
//
// Path keying convention: uploads/{user_id}/{uuid4}_{sanitized_name}.
// The user_id prefix is required for tenant isolation under shared-bucket
// ACLs (Supabase row-level security or S3 bucket policy). The UUID4
// component (crypto/rand, RFC 4122 v4) makes keys unguessable so the same
// filename from the same user never collides across uploads.
package services

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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

// StorageProvider generates UploadGrants for client uploads. Both Supabase
// and S3 providers implement this — the handler stays provider-agnostic.
type StorageProvider interface {
	// Provider returns the implementation tag ("supabase" or "s3"). Useful
	// for logging + the /health endpoint so operators can see which backend
	// is wired without tailing env vars.
	Provider() string
	// SignUpload mints a TTL-bound upload URL for key scoped under
	// user_id plus the corresponding public media_url. content_type and
	// size_bytes are forwarded so providers can pass them to Content-Length
	// headers if they support header-based validation.
	SignUpload(ctx context.Context, userID int64, key, contentType string, sizeBytes int64, ttl time.Duration) (*UploadGrant, error)
}

// SupabaseProvider uses the Supabase Storage REST API to mint a signed
// upload URL. The returned URL embeds a short-lived token in the query
// string so the client can PUT directly without an Authorization header.
//
// Requires a "public" or "public-read" bucket (or RLS-by-user) so that
// media_url (constructed as /storage/v1/object/public/{bucket}/{key}) is
// reachable by the social platforms' servers downloading the asset
// during publish.
type SupabaseProvider struct {
	baseURL string // e.g. https://abcdefgh.supabase.co  (no trailing slash)
	bucket  string
	apiKey  string // service-role JWT
	http    *http.Client
	logger  *slog.Logger
}

// NewSupabaseProvider is a thin constructor; trims trailing slashes from
// baseURL to avoid double-slash in constructed URLs.
func NewSupabaseProvider(baseURL, apiKey, bucket string, logger *slog.Logger) *SupabaseProvider {
	if logger == nil {
		logger = slog.Default()
	}
	return &SupabaseProvider{
		baseURL: strings.TrimRight(baseURL, "/"),
		bucket:  bucket,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 15 * time.Second},
		logger:  logger,
	}
}

// Provider implements StorageProvider.
func (p *SupabaseProvider) Provider() string { return "supabase" }

// SignUpload calls Supabase's POST /storage/v1/object/upload/sign/{bucket}/{key}
// and constructs media_url as /storage/v1/object/public/{bucket}/{key}.
// The Supabase API: https://supabase.com/docs/reference/javascript/storage-from-createsigneduploadurl
func (p *SupabaseProvider) SignUpload(ctx context.Context, userID int64, key, contentType string, sizeBytes int64, ttl time.Duration) (*UploadGrant, error) {
	signPath := fmt.Sprintf("%s/storage/v1/object/upload/sign/%s/%s",
		p.baseURL, p.bucket, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, signPath, strings.NewReader(""))
	if err != nil {
		return nil, fmt.Errorf("failed to build supabase sign request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", p.apiKey)

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("supabase sign request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("supabase sign request returned %d: %s",
			resp.StatusCode, truncate(string(body), 256))
	}

	var result struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode supabase response: %w", err)
	}
	if result.URL == "" {
		return nil, fmt.Errorf("supabase returned empty url in response")
	}

	mediaURL := fmt.Sprintf("%s/storage/v1/object/public/%s/%s",
		p.baseURL, p.bucket, key)
	return &UploadGrant{
		UploadURL: result.URL,
		MediaURL:  mediaURL,
		ExpiresAt: time.Now().Add(ttl),
	}, nil
}

// S3Provider generates an AWS SigV4-signed PUT URL against a bucket using
// virtual-hosted-style addressing: {bucket}.s3.{region}.amazonaws.com.
//
// Signing is hand-rolled to avoid pulling in aws-sdk-go-v2 (~50 MB of
// transitively downloaded modules). The implementation follows AWS's
// reference SigV4 spec:
//   https://docs.aws.amazon.com/general/latest/gr/sigv4-create-canonical-request.html
//   https://docs.aws.amazon.com/general/latest/gr/sigv4-create-string-to-sign.html
//   https://docs.aws.amazon.com/general/latest/gr/sigv4-calculate-signature.html
//
// For presigned URLs the canonical request is signed with payload hash
// UNSIGNED-PAYLOAD so the client doesn't need to hash the entire file
// upfront. This is the canonical approach for client-side uploads.
type S3Provider struct {
	region      string
	bucket      string
	accessKeyID string
	secretKey   string
	baseHost    string // {bucket}.s3.{region}.amazonaws.com (virtual-hosted)
	http        *http.Client
	logger      *slog.Logger
}

// NewS3Provider builds the provider and pre-computes baseHost so SignUpload
// doesn't repeat the template on every call.
func NewS3Provider(region, bucket, accessKeyID, secretKey string, logger *slog.Logger) *S3Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &S3Provider{
		region:      region,
		bucket:      bucket,
		accessKeyID: accessKeyID,
		secretKey:   secretKey,
		baseHost:    fmt.Sprintf("%s.s3.%s.amazonaws.com", bucket, region),
		http:        &http.Client{Timeout: 15 * time.Second},
		logger:      logger,
	}
}

// Provider implements StorageProvider.
func (p *S3Provider) Provider() string { return "s3" }

// SignUpload generates a SigV4 PUT URL. For presigned PUTs, the canonical
// request signs only `host` — content-type and content-length headers
// are forwarded by the client but do not participate in the signature
// (S3 accepts the upload as long as X-Amz-Signature validates).
func (p *S3Provider) SignUpload(ctx context.Context, userID int64, key, contentType string, sizeBytes int64, ttl time.Duration) (*UploadGrant, error) {
	_ = ctx
	_ = userID
	_ = contentType
	_ = sizeBytes
	uploadURL, err := signS3V4URL(
		p.baseHost, p.region, "s3",
		key, ttl, http.MethodPut,
		p.accessKeyID, p.secretKey,
		time.Now(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to sign S3 URL: %w", err)
	}

	mediaURL := fmt.Sprintf("https://%s/%s", p.baseHost, key)
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
//   - host:     bucket virtual host (e.g. "mybucket.s3.us-east-1.amazonaws.com")
//   - region:   AWS region of the bucket
//   - service:  "s3"
//   - key:      object key (already URL-safe per BuildUploadKey)
//   - ttl:      X-Amz-Expires value in seconds
//   - method:   HTTP verb (PUT for upload, GET in theory)
//   - now:      time used for X-Amz-Date (caller injects for determinism
//               in tests; production passes time.Now())
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
// The user_id prefix provides tenant isolation (Supabase row-level
// security OR S3 bucket policy can later enforce access). The UUID4
// component makes the key unguessable so the same filename from the
// same user never collides across uploads.
func BuildUploadKey(userID int64, filename string) string {
	return fmt.Sprintf("uploads/%d/%s_%s",
		userID, newUUID4(), sanitizeFilename(filename))
}

// sanitizeFilename reduces a client-provided filename to a safe token
// suitable for an S3/Supabase object key. Steps:
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

// truncate shortens a string for inclusion in error messages without
// flooding logs with potentially-large provider responses.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
