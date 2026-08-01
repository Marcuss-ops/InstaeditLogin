package services

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// SignUpload generates a SigV4 PUT URL. For presigned PUTs, the canonical
// request signs only `host` — content-type and content-length headers
// are forwarded by the client but do not participate in the signature
// (S3-compatible stores accept the upload as long as X-Amz-Signature
// validates).
func (p *S3Provider) SignUpload(ctx context.Context, userID int64, key, contentType string, sizeBytes int64, ttl time.Duration) (*UploadGrant, error) {
	// `_ = ctx` was removed in PR-2A (YAGNI placeholder; not part of
	// the StorageProvider INTERFACE contract — the ctx param is
	// passed through but the S3 impl doesn't read it because
	// signS3V4URL doesn't take a context). The 3 blanks below STAY
	// because they ARE part of the storage contract: other impls
	// (hypothetical StorageGateway, custom CDN provider) may read
	// userID/contentType/sizeBytes for header-based validation. S3
	// SigV4 PUT URL signing canonicalises only the host header
	// (AWS SigV4 spec), so userID/contentType/sizeBytes are unused
	// in this specific impl but the interface guarantees them
	// available for forward-consumers.
	_ = userID
	_ = contentType
	_ = sizeBytes
	uploadURL, err := signS3V4URL(
		p.scheme, p.baseHost, p.region, "s3",
		p.objectKey(key), ttl, http.MethodPut,
		p.accessKey, p.secretKey,
		time.Now(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to sign S3 URL: %w", err)
	}

	mediaURL := fmt.Sprintf("%s://%s/%s", p.scheme, p.baseHost, p.objectKey(key))
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
func signS3V4URL(scheme, host, region, service, key string, ttl time.Duration, method, accessKeyID, secretAccessKey string, now time.Time) (string, error) {
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

	return fmt.Sprintf("%s://%s%s?%s", scheme, host, canonicalURIPath, finalQuery), nil
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
