package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// MediaDownloadResolver is the single source of truth for "how do I get
// a FRESH presigned GET URL for a piece of media right before handing
// it to a per-platform publisher API?". The followup that introduced
// this package identified a real bug: a scheduled post stored the
// presigned URL on the row at create time; the URL expires; the
// worker fires 24h later; the platform API call fails with 403.
//
// All publishers (YouTube, Drive, Velox, ingest workers, dark editor
// surface) call this resolver IMMEDIATELY before the platform API
// call. The resolver constructs a fresh SigV4 presigned URL with the
// configured TTL per call — multiple calls in the same publish job
// yield different signatures because the URL time component changes
// on each invocation.
//
// Two entry points:
//
//   - ResolveForUpload(ctx, post, ttl): canonical path. Resolves the
//     post's asset id or persisted bucket/object-key mirror through an
//     ownership-scoped media_assets query, then checks readiness and expiry.
//
// Default TTL: 1 hour. Long enough to bridge the platform's accept
// window for video metadata + thumbnail upload (YouTube typically
// accepts the upload within minutes), short enough that credentials
// don't linger.
type MediaDownloadResolver interface {
	ResolveForUpload(ctx context.Context, post *models.Post, ttl time.Duration) (string, error)
}

// ObjectGetter is the narrow subset of StorageProvider the resolver
// needs to mint a fresh presigned GET URL. Defined here alongside the
// consumer so tests can inject a small mock without pulling in the
// full StorageProvider interface.
type ObjectGetter interface {
	GetObject(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// BucketAwareObjectGetter is implemented by storage providers that can sign
// an object in the bucket recorded on the media asset. ObjectGetter remains
// the compatibility fallback for legacy single-bucket providers.
type BucketAwareObjectGetter interface {
	GetObjectWithBucket(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
}

// MediaAssetStore is the narrow subset of MediaAssetRepository used by
// the asset-id branch of ResolveForUpload. Same narrowness rationale
// as ObjectGetter above.
type MediaAssetStore interface {
	// FindForPost resolves either a canonical asset id or a persisted
	// (bucket, object key) pair while enforcing that the asset uploader
	// belongs to the post workspace. It also returns the full asset row
	// so readiness and expiry are checked against database state.
	FindForPost(ctx context.Context, workspaceID int64, assetID, bucket, key string) (*models.MediaAsset, error)
	// FindByUploadKey is the legacy-fallback lookup for posts that only
	// have media_url. Implementations MUST apply the same workspace
	// ownership predicate as FindForPost.
	FindByUploadKey(ctx context.Context, workspaceID int64, key string) (*models.MediaAsset, error)
}

// ErrPostMissingMediaReference is the typed sentinel returned by
// ResolveForUpload when neither media_asset_id nor
// (bucket, storage_object_key) is set on the post row. The worker's
// publish-path error handler maps this to a deterministic
// status='failed' + error_message so dashboards can distinguish
// operator-fixable rows from transient failures.
var ErrPostMissingMediaReference = errors.New("media resolver: post has no resolvable media reference (media_asset_id and storage_object_key are both empty)")

// ErrAssetExpired is the typed sentinel returned when the resolved
// media_assets row has expires_at < NOW(). A status='ready' row can
// still be expired: the MarkExpired cleanup pass transitions
// status='pending' → 'expired' but does NOT touch already-ready rows
// (they're past their TTL but the user still owns the storage object).
// The publish path is the one place that needs to refuse service
// against an expired-ready row because the platform API will see a
// valid presigned signature even though the asset's pre-signed URL
// window has lapsed. The worker maps this to a deterministic
// status='failed' with an operator-friendly message ("media asset
// expired at time of publish; re-upload required").
var ErrAssetExpired = errors.New("media resolver: media asset expired at time of publish (re-upload required)")

// ErrMediaURLNoMatchingAsset is the typed sentinel returned by the
// legacy post.media_url fallback branch when the parsed upload_key
// does not match any row in media_assets. Two failure modes map here:
//   - the legacy post points at a row that has been hard-deleted by
//     the cleanup pass (DELETE FROM media_assets WHERE publish_at + buffer < NOW()).
//   - the legacy post's media_url was hand-edited by an operator at
//     the DB level (data corruption we cannot detect upstream).
//
// The worker maps ErrMediaURLNoMatchingAsset to "failed" with the
// operator-friendly message ("legacy post media_url no longer
// references an upload_key we host; re-upload required"). Migration
// 081 (the SQL backfill companion) prevents the first case from
// happening at the production migration cutover point.
var ErrMediaURLNoMatchingAsset = errors.New("media resolver: post media_url does not map to any media_assets row (legacy row; re-upload required)")

// ErrBucketAwareStorageRequired prevents a persisted bucket from being
// silently replaced by the provider's runtime default bucket.
var ErrBucketAwareStorageRequired = errors.New("media resolver: storage provider does not support explicit bucket resolution")

// extractUploadKeyFromMediaURL parses a posts.media_url value (a
// presigned URL the server stored at post-create time) and returns
// the upload_key path segment. The mapping is:
//   - input:  https://minio.example.com/instaedit-local/uploads/1/uuid.mp4?X-Amz-Algorithm=...
//   - output: uploads/1/uuid.mp4
//
// The presign handler's AssetURL helper builds the URL via
//
//	fmt.Sprintf("%s/%s", bucket, asset.UploadKey)
//
// so the upload_key is always the URL path AFTER the first '/'
// following the host. We split on '/' after the scheme+host; everything
// BEFORE the '?' (the query string with the SigV4 signature) is the
// path. The function is permissive (returns "" on any parse failure)
// so the caller can fall through to a typed-sentinel error path
// rather than panicking.
//
// NOTE: this is the "best-effort path" reverser. The SQL-side backfill
// in migration 081_publish_legacy_url_to_asset.sql is the AUTHORITATIVE
// source for legacy rows in production — this function is a defense-in-
// depth runtime fallback for any legacy row that escaped the migration.
func extractUploadKeyFromMediaURL(mediaURL string) string {
	mediaURL = strings.TrimSpace(mediaURL)
	if mediaURL == "" {
		return ""
	}
	// Strip scheme://host prefix. We accept either https://host/... or
	// http://host/...; the URL must be absolute (path-style S3 signed URL).
	const schemeSep = "://"
	idx := strings.Index(mediaURL, schemeSep)
	if idx == -1 {
		// No scheme: treat the entire string as a path. Useful for tests
		// that pass "uploads/1/x.mp4" verbatim.
		pathOnly := stripQuery(mediaURL)
		pathOnly = strings.TrimLeft(pathOnly, "/")
		return pathOnly
	}
	rest := mediaURL[idx+len(schemeSep):]
	slash := strings.Index(rest, "/")
	if slash == -1 {
		return ""
	}
	pathPart := rest[slash+1:]
	pathPart = stripQuery(pathPart)
	// Drop the bucket prefix: the public presign URL is "/bucket/key" so
	// the first segment is the bucket name; everything after is the
	// upload_key (which itself is "uploads/<userID>/<uuid>.<ext>").
	// Drop the FIRST leading '/' if any remains, then strip the bucket.
	pathPart = strings.TrimLeft(pathPart, "/")
	if !strings.Contains(pathPart, "/") {
		// Single-segment: not a /bucket/key shape, return as-is.
		return pathPart
	}
	firstSlash := strings.Index(pathPart, "/")
	if firstSlash == -1 {
		return pathPart
	}
	return pathPart[firstSlash+1:]
}

// stripQuery trims the query string from a path. The SigV4
// presigned URL has ?X-Amz-Algorithm=...&... as the last segment;
// everything from the first '?' is signature metadata we don't need.
func stripQuery(path string) string {
	if q := strings.Index(path, "?"); q != -1 {
		return path[:q]
	}
	return path
}

// mediaDownloadResolver is the production implementation. The resolver
// composes a MediaAssetStore lookup (canonical asset-id branch) with a
// fast (bucket, key) direct path (legacy backfill + recovery paths).
// Both branches funnel into ObjectGetter.GetObject — the actual S3
// presigned-URL signer — so signing semantics are identical to what
// the upload flow already produces. The only signing-time delta
// between calls is `now()` baked into the X-Amz-Date header.
type mediaDownloadResolver struct {
	store   MediaAssetStore
	storage ObjectGetter
	logger  *slog.Logger
}

// NewMediaDownloadResolver wires the resolver. store + storage are
// required. Pass a logger for visibility; nil defaults to slog.Default().
func NewMediaDownloadResolver(storage ObjectGetter, store MediaAssetStore, logger *slog.Logger) MediaDownloadResolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &mediaDownloadResolver{
		store:   store,
		storage: storage,
		logger:  logger,
	}
}

// defaultResolverTTL is the fallback TTL when callers pass <= 0.
const defaultResolverTTL = 1 * time.Hour

// resolveAsset validates the database row after an ownership-scoped lookup.
// The row, not post.MediaURL or caller-provided storage coordinates, is the
// source of truth for readiness, expiry, bucket, and object key.
func (r *mediaDownloadResolver) resolveAsset(ctx context.Context, asset *models.MediaAsset, ttl time.Duration) (string, *models.MediaAsset, error) {
	if asset == nil {
		return "", nil, errors.New("media resolver: media asset not found or not owned by post workspace")
	}
	if asset.Status != models.MediaAssetStatusReady {
		return "", asset, fmt.Errorf("media resolver: media asset not ready (status=%s)", asset.Status)
	}
	if !asset.ExpiresAt.IsZero() && time.Now().After(asset.ExpiresAt) {
		return "", asset, fmt.Errorf("%w (asset_id=%q expires_at=%s)", ErrAssetExpired, asset.ID, asset.ExpiresAt.UTC().Format(time.RFC3339))
	}
	if asset.UploadKey == "" {
		return "", asset, errors.New("media resolver: media asset has empty upload_key (corrupted row)")
	}
	if ttl <= 0 {
		ttl = defaultResolverTTL
	}
	url, err := r.getObjectURL(ctx, asset.Bucket, asset.UploadKey, ttl)
	if err != nil {
		return "", asset, fmt.Errorf("media resolver: sign GET URL for key %q: %w", asset.UploadKey, err)
	}
	r.logger.Debug("media resolver: fresh presigned GET URL minted",
		"asset_id", asset.ID, "key", asset.UploadKey, "ttl_sec", int(ttl.Seconds()))
	return url, asset, nil
}

func (r *mediaDownloadResolver) resolveAssetForPost(ctx context.Context, post *models.Post, assetID, bucket, key string, ttl time.Duration) (string, error) {
	if r.store == nil {
		return "", errors.New("media resolver: MediaAssetStore not wired")
	}
	if post.WorkspaceID <= 0 {
		return "", errors.New("media resolver: post has no workspace ownership context")
	}
	asset, err := r.store.FindForPost(ctx, post.WorkspaceID, assetID, bucket, key)
	if err != nil {
		return "", fmt.Errorf("media resolver: load owned asset: %w", err)
	}
	url, _, err := r.resolveAsset(ctx, asset, ttl)
	return url, err
}

func (r *mediaDownloadResolver) getObjectURL(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	if bucket != "" {
		getter, ok := r.storage.(BucketAwareObjectGetter)
		if !ok {
			return "", fmt.Errorf("%w (bucket=%q key=%q)", ErrBucketAwareStorageRequired, bucket, key)
		}
		return getter.GetObjectWithBucket(ctx, bucket, key, ttl)
	}
	return r.storage.GetObject(ctx, key, ttl)
}

// ResolveForUpload is the canonical entry point.
//
// Decision tree:
//
//  1. If post.MediaAssetID is set: resolve the asset through
//     MediaAssetStore.FindForPost using the post workspace and the
//     persisted bucket/object-key mirrors.
//  2. Else if post.StorageObjectKey is set: resolve that key through
//     the same ownership-scoped media_assets query; never sign it directly.
//  3. Else if post.MediaURL is set: parse the URL to extract upload_key,
//     then MediaAssetStore.FindByUploadKey — this is the LEGACY
//     fallback for posts created before migration 080 (which is the
//     migration that added media_asset_id + storage_object_key to
//     posts). Migration 081 (SQL backfill) is the authoritative
//     path that promotes these rows to canonical source-of-truth;
//     this fallback exists for any row that escapes the backfill.
//  4. Else: return ErrPostMissingMediaReference so the worker routes
//     to a deterministic failed status.
func (r *mediaDownloadResolver) ResolveForUpload(ctx context.Context, post *models.Post, ttl time.Duration) (string, error) {
	if post == nil {
		return "", errors.New("media resolver: post is nil")
	}
	if post.MediaAssetID != nil && *post.MediaAssetID != "" {
		bucket, key := "", ""
		if post.Bucket != nil {
			bucket = *post.Bucket
		}
		if post.StorageObjectKey != nil {
			key = *post.StorageObjectKey
		}
		return r.resolveAssetForPost(ctx, post, *post.MediaAssetID, bucket, key, ttl)
	}
	if post.StorageObjectKey != nil && *post.StorageObjectKey != "" {
		bucket := ""
		if post.Bucket != nil {
			bucket = *post.Bucket
		}
		// Even the key-only compatibility path must resolve a ready,
		// owned media_assets row; it may not mint from arbitrary input.
		return r.resolveAssetForPost(ctx, post, "", bucket, *post.StorageObjectKey, ttl)
	}
	// Legacy fallback: parse media_url to recover the upload_key, then
	// look it up in media_assets via FindByUploadKey. Runs once per
	// publish; the first DB round-trip is amortised across the whole
	// legacy row population since migration 081's SQL backfill moves
	// legacy rows into the canonical branch above.
	if post.MediaURL != "" {
		uploadKey := extractUploadKeyFromMediaURL(post.MediaURL)
		if uploadKey != "" {
			if r.store == nil {
				return "", errors.New("media resolver: MediaAssetStore not wired")
			}
			asset, err := r.store.FindByUploadKey(ctx, post.WorkspaceID, uploadKey)
			if err != nil {
				return "", fmt.Errorf("media resolver: legacy fallback lookup by upload_key %q: %w", uploadKey, err)
			}
			if asset != nil {
				url, _, err := r.resolveAsset(ctx, asset, ttl)
				return url, err
			}
			// Legacy row whose upload_key isn't in media_assets
			// anymore (cleanup pass hard-deleted, OR hand-edited
			// media_url, OR media_url path was never canonical). Map
			// to a typed sentinel so the worker can write a clear
			// operator message.
			return "", fmt.Errorf("%w (parsed_key=%q)", ErrMediaURLNoMatchingAsset, uploadKey)
		}
		// Can't parse anything meaningful from media_url. Fall through
		// to ErrPostMissingMediaReference — same path the canonical
		// "no references at all" branch hits; the operator sees a
		// consistent error message.
	}
	return "", ErrPostMissingMediaReference
}

// Compile-time check: *mediaDownloadResolver must implement
// MediaDownloadResolver. Drift here is a build error, not a runtime panic.
var _ MediaDownloadResolver = (*mediaDownloadResolver)(nil)
