package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
//  3. Else: return ErrPostMissingMediaReference so the worker routes
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
	return "", ErrPostMissingMediaReference
}

// ResolveForKey is the explicit-direct branch.
func (r *mediaDownloadResolver) ResolveForKey(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	return r.resolveByKey(ctx, bucket, key, ttl)
}

// Compile-time check: *mediaDownloadResolver must implement
// MediaDownloadResolver. Drift here is a build error, not a runtime panic.
var _ MediaDownloadResolver = (*mediaDownloadResolver)(nil)
