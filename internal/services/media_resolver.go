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
//   - ResolveForUpload(ctx, post, ttl): canonical path. Inspects
//     post.MediaAssetID first → falls back to (post.Bucket,
//     post.StorageObjectKey) when the asset-id reference is missing
//     (legacy backfill scenario).
//
//   - ResolveForKey(ctx, bucket, key, ttl): direct (bucket, key) pair
//     lookup. Useful for recovery paths that already know the storage
//     coordinates (e.g. reconciler replaying a failed publish).
//
// Default TTL: 1 hour. Long enough to bridge the platform's accept
// window for video metadata + thumbnail upload (YouTube typically
// accepts the upload within minutes), short enough that credentials
// don't linger.
type MediaDownloadResolver interface {
	ResolveForUpload(ctx context.Context, post *models.Post, ttl time.Duration) (string, error)
	ResolveForKey(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
}

// ObjectGetter is the narrow subset of StorageProvider the resolver
// needs to mint a fresh presigned GET URL. Defined here alongside the
// consumer so tests can inject a small mock without pulling in the
// full StorageProvider interface.
type ObjectGetter interface {
	GetObject(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// MediaAssetStore is the narrow subset of MediaAssetRepository used by
// the asset-id branch of ResolveForUpload. Same narrowness rationale
// as ObjectGetter above.
type MediaAssetStore interface {
	FindByID(ctx context.Context, id string) (*models.MediaAsset, error)
	// FindByUploadKey is the legacy-fallback lookup: for posts that
	// pre-date migration 080 (and therefore have ONLY post.media_url
	// set, no media_asset_id / storage_object_key), the resolver
	// parses post.media_url to recover the upload_key path segment
	// then joins back to the canonical media_assets row via
	// media_assets.upload_key. The match is UNIQUE (upload_key has
	// a UNIQUE constraint), so at most 1 row is returned.
	// (nil, nil) when no row matches.
	FindByUploadKey(ctx context.Context, key string) (*models.MediaAsset, error)
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

// resolveAssetByID performs the canonical branch.
func (r *mediaDownloadResolver) resolveAssetByID(ctx context.Context, assetID string, ttl time.Duration) (string, *models.MediaAsset, error) {
	if assetID == "" {
		return "", nil, errors.New("media resolver: empty asset id")
	}
	if r.store == nil {
		return "", nil, errors.New("media resolver: MediaAssetStore not wired")
	}
	asset, err := r.store.FindByID(ctx, assetID)
	if err != nil {
		return "", nil, fmt.Errorf("media resolver: load asset %q: %w", assetID, err)
	}
	if asset == nil {
		return "", nil, fmt.Errorf("media resolver: asset %q not found", assetID)
	}
	if asset.Status != models.MediaAssetStatusReady {
		return "", asset, fmt.Errorf("media resolver: asset %q not ready (status=%s); the worker should NOT have scheduled a publish on a non-ready asset", assetID, asset.Status)
	}
	// Expiry gate (immediately before publishing): a status='ready' row can
	// still have expires_at in the past if the row's TTL was breached and
	// the MarkExpired cleanup pass hasn't swept it yet. Return a typed
	// sentinel so the worker can write a clearer operator message instead
	// of the generic "not ready" error.
	if !asset.ExpiresAt.IsZero() && time.Now().After(asset.ExpiresAt) {
		return "", asset, fmt.Errorf("%w (asset_id=%q expires_at=%s now=%s)", ErrAssetExpired, assetID, asset.ExpiresAt.UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	}
	key := asset.UploadKey
	if key == "" {
		return "", asset, fmt.Errorf("media resolver: asset %q has empty upload_key (corrupted row)", assetID)
	}
	if ttl <= 0 {
		ttl = defaultResolverTTL
	}
	url, err := r.storage.GetObject(ctx, key, ttl)
	if err != nil {
		return "", asset, fmt.Errorf("media resolver: sign GET URL for key %q: %w", key, err)
	}
	r.logger.Debug("media resolver: fresh presigned GET URL minted (asset branch)",
		"asset_id", assetID, "key", key, "ttl_sec", int(ttl.Seconds()))
	return url, asset, nil
}

// resolveByKey performs the legacy / direct branch.
func (r *mediaDownloadResolver) resolveByKey(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	if key == "" {
		return "", errors.New("media resolver: empty key")
	}
	if ttl <= 0 {
		ttl = defaultResolverTTL
	}
	url, err := r.storage.GetObject(ctx, key, ttl)
	if err != nil {
		return "", fmt.Errorf("media resolver: sign GET URL for key %q: %w", key, err)
	}
	r.logger.Debug("media resolver: fresh presigned GET URL minted (key branch)",
		"bucket", bucket, "key", key, "ttl_sec", int(ttl.Seconds()))
	return url, nil
}

// ResolveForUpload is the canonical entry point.
//
// Decision tree:
//
//  1. If post.MediaAssetID is set: load the asset row via
//     MediaAssetStore.FindByID → mint URL via asset's UploadKey +
//     bucket.
//  2. Else if post.StorageObjectKey is set: mint URL directly via
//     (post.Bucket, post.StorageObjectKey).
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
		url, _, err := r.resolveAssetByID(ctx, *post.MediaAssetID, ttl)
		return url, err
	}
	if post.StorageObjectKey != nil && *post.StorageObjectKey != "" {
		bucket := ""
		if post.Bucket != nil {
			bucket = *post.Bucket
		}
		return r.resolveByKey(ctx, bucket, *post.StorageObjectKey, ttl)
	}
	// Legacy fallback: parse media_url to recover the upload_key, then
	// look it up in media_assets via FindByUploadKey. Runs once per
	// publish; the first DB round-trip is amortised across the whole
	// legacy row population since migration 081's SQL backfill moves
	// legacy rows into the canonical branch above.
	if post.MediaURL != "" {
		uploadKey := extractUploadKeyFromMediaURL(post.MediaURL)
		if uploadKey != "" {
			asset, err := r.store.FindByUploadKey(ctx, uploadKey)
			if err != nil {
				return "", fmt.Errorf("media resolver: legacy fallback lookup by upload_key %q: %w", uploadKey, err)
			}
			if asset != nil {
				// Recurse through resolveAssetByID so the same
				// status=ready + expires_at gate logic applies.
				url, _, err := r.resolveAssetByID(ctx, asset.ID, ttl)
				return url, err
			}
			// Legacy row whose upload_key isn't in media_assets
			// anymore (cleanup pass hard-deleted, OR hand-edited
			// media_url, OR media_url path was never canonical). Map
			// to a typed sentinel so the worker can write a clear
			// operator message.
			return "", fmt.Errorf("%w (parsed_key=%q media_url=%q)", ErrMediaURLNoMatchingAsset, uploadKey, post.MediaURL)
		}
		// Can't parse anything meaningful from media_url. Fall through
		// to ErrPostMissingMediaReference — same path the canonical
		// "no references at all" branch hits; the operator sees a
		// consistent error message.
	}
	return "", ErrPostMissingMediaReference
}

// ResolveForKey is the explicit-direct branch.
func (r *mediaDownloadResolver) ResolveForKey(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	return r.resolveByKey(ctx, bucket, key, ttl)
}

// Compile-time check: *mediaDownloadResolver must implement
// MediaDownloadResolver. Drift here is a build error, not a runtime panic.
var _ MediaDownloadResolver = (*mediaDownloadResolver)(nil)
