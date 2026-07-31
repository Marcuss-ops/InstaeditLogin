package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// fakeObjectGetter records every GetObject call so tests can verify
// the resolver invoked the signer with the right key + ttl key. Tests
// can override the return or, by setting err, force an error path.
type fakeObjectGetter struct {
	calls   []fakeGetObjectCall
	nextURL string
	nextErr error
	mu      noopMu
}

type fakeGetObjectCall struct {
	Bucket string
	Key    string
	TTL    time.Duration
}

// noopMu is a stand-in mutex to satisfy the conventional sync.Mutex
// field without importing sync; tests are single-goroutine so any
// locking is purely cosmetic.
type noopMu struct{}

func (f *fakeObjectGetter) GetObject(_ context.Context, key string, ttl time.Duration) (string, error) {
	f.calls = append(f.calls, fakeGetObjectCall{Key: key, TTL: ttl})
	if f.nextErr != nil {
		return "", f.nextErr
	}
	return f.nextURL, nil
}

func (f *fakeObjectGetter) GetObjectWithBucket(_ context.Context, bucket, key string, ttl time.Duration) (string, error) {
	f.calls = append(f.calls, fakeGetObjectCall{Bucket: bucket, Key: key, TTL: ttl})
	if f.nextErr != nil {
		return "", f.nextErr
	}
	return f.nextURL, nil
}

// fakeMediaAssetStore is a minimal in-memory map. Returns a per-test
// configured asset for the requested id; nil or errors are surfaces
// that test code controls.
type fakeMediaAssetStore struct {
	assets map[string]*models.MediaAsset
	err    error
}

func (f *fakeMediaAssetStore) FindByID(_ context.Context, id string) (*models.MediaAsset, error) {
	if f.err != nil {
		return nil, f.err
	}
	asset, ok := f.assets[id]
	if !ok {
		return nil, nil
	}
	return asset, nil
}

// FindByUploadKey iterates through the configured assets map and
// returns the first match on asset.UploadKey (the production repo is
// a UNIQUE constraint query so at most 1 row exists; the fake is a
// map so an explicit scan is the only sensible shape).
func (f *fakeMediaAssetStore) FindForPost(_ context.Context, workspaceID int64, assetID, bucket, key string) (*models.MediaAsset, error) {
	if f.err != nil {
		return nil, f.err
	}
	for _, asset := range f.assets {
		if asset == nil || asset.UserID != workspaceID {
			continue
		}
		if assetID != "" && asset.ID != assetID {
			continue
		}
		if assetID == "" && key != "" && asset.UploadKey != key {
			continue
		}
		if bucket != "" && asset.Bucket != bucket {
			continue
		}
		return asset, nil
	}
	return nil, nil
}

func (f *fakeMediaAssetStore) FindByUploadKey(_ context.Context, workspaceID int64, key string) (*models.MediaAsset, error) {
	if f.err != nil {
		return nil, f.err
	}
	for _, asset := range f.assets {
		if asset != nil && asset.UserID == workspaceID && asset.UploadKey == key {
			return asset, nil
		}
	}
	return nil, nil
}

// TestMediaDownloadResolver_ResolveForUpload_AssetIDSuccess verifies
// the canonical branch: when post.MediaAssetID is set AND the asset
// exists in the store AND the asset is in `ready` status, the
// resolver calls ObjectGetter.GetObject exactly once with the asset's
// upload_key, and returns the presigned URL.
func TestMediaDownloadResolver_ResolveForUpload_AssetIDSuccess(t *testing.T) {
	store := &fakeMediaAssetStore{
		assets: map[string]*models.MediaAsset{
			"asset-001": {
				ID:        "asset-001",
				UserID:    1,
				UploadKey: "uploads/1/uuid.mp4",
				Bucket:    "instaedit-media",
				Status:    models.MediaAssetStatusReady,
			},
		},
	}
	getter := &fakeObjectGetter{nextURL: "https://presigned.example.com/video.mp4?signed=abc"}
	resolver := NewMediaDownloadResolver(getter, store, nil)

	post := &models.Post{
		WorkspaceID:  1,
		MediaAssetID: ptrToString("asset-001"),
	}
	url, err := resolver.ResolveForUpload(context.Background(), post, 30*time.Minute)
	if err != nil {
		t.Fatalf("ResolveForUpload returned unexpected error: %v", err)
	}
	if url != "https://presigned.example.com/video.mp4?signed=abc" {
		t.Errorf("returned URL = %q, want %q", url, "https://presigned.example.com/video.mp4?signed=abc")
	}
	if len(getter.calls) != 1 {
		t.Fatalf("ObjectGetter.GetObject call count = %d, want 1", len(getter.calls))
	}
	if getter.calls[0].Key != "uploads/1/uuid.mp4" {
		t.Errorf("ObjectGetter.GetObject key = %q, want %q", getter.calls[0].Key, "uploads/1/uuid.mp4")
	}
	if getter.calls[0].Bucket != "instaedit-media" {
		t.Errorf("ObjectGetter.GetObject bucket = %q, want %q", getter.calls[0].Bucket, "instaedit-media")
	}
	if getter.calls[0].TTL != 30*time.Minute {
		t.Errorf("ObjectGetter.GetObject ttl = %v, want 30m", getter.calls[0].TTL)
	}
}

// TestMediaDownloadResolver_ResolveForUpload_AssetNotReady verifies
// the resolver fails fast when the asset row exists but is NOT in
// `ready` status (e.g. still pending, expired). ObjectGetter.GetObject
// MUST NOT be called.
func TestMediaDownloadResolver_ResolveForUpload_AssetNotReady(t *testing.T) {
	store := &fakeMediaAssetStore{
		assets: map[string]*models.MediaAsset{
			"asset-pending": {
				ID:        "asset-pending",
				UserID:    1,
				UploadKey: "uploads/2/x.mp4",
				Status:    models.MediaAssetStatusPending,
			},
		},
	}
	getter := &fakeObjectGetter{nextURL: "https://example.com/x"}
	resolver := NewMediaDownloadResolver(getter, store, nil)

	post := &models.Post{WorkspaceID: 1, MediaAssetID: ptrToString("asset-pending")}
	_, err := resolver.ResolveForUpload(context.Background(), post, time.Minute)
	if err == nil {
		t.Fatal("expected error for non-ready asset, got nil")
	}
	if len(getter.calls) != 0 {
		t.Errorf("ObjectGetter.GetObject must not be called when asset is non-ready; got %d call(s)", len(getter.calls))
	}
}

// TestMediaDownloadResolver_ResolveForUpload_AssetMissing verifies the
// resolver fails when the asset id is set but the row does NOT exist.
// This is a different failure mode from "asset not ready" — the operator
// needs to investigate whether the asset id is correct (more likely) or
// whether a media asset re-upload is required.
func TestMediaDownloadResolver_ResolveForUpload_AssetMissing(t *testing.T) {
	store := &fakeMediaAssetStore{assets: map[string]*models.MediaAsset{}}
	getter := &fakeObjectGetter{nextURL: "https://example.com/x"}
	resolver := NewMediaDownloadResolver(getter, store, nil)

	post := &models.Post{WorkspaceID: 1, MediaAssetID: ptrToString("asset-does-not-exist")}
	_, err := resolver.ResolveForUpload(context.Background(), post, time.Minute)
	if err == nil {
		t.Fatal("expected error for missing asset, got nil")
	}
	if len(getter.calls) != 0 {
		t.Errorf("ObjectGetter.GetObject must not be called when asset is missing; got %d call(s)", len(getter.calls))
	}
}

// TestMediaDownloadResolver_ResolveForUpload_AssetExpired verifies the
// resolver fails fast when the resolved asset row has expires_at < NOW(),
// even if status='ready' (MarkExpired only transitions status='pending'
// rows; a row that reached 'ready' and then expired will show up to the
// resolver as status=ready + expires_at in the past). The error MUST
// wrap the typed sentinel ErrAssetExpired so the worker can emit a clear
// "re-upload required" message instead of the generic "not ready".
func TestMediaDownloadResolver_ResolveForUpload_AssetExpired(t *testing.T) {
	expired := time.Now().Add(-5 * time.Minute)
	store := &fakeMediaAssetStore{
		assets: map[string]*models.MediaAsset{
			"asset-expired": {
				ID:        "asset-expired",
				UserID:    1,
				UploadKey: "uploads/7/x.mp4",
				// status=ready is what an asset row that never got swept
				// by MarkExpired looks like at the resolver. The resolver
				// must NOT trust status alone — the expires_at gate is
				// the second invariant. Today the production code would
				// pass status=ready + a stale presigned URL to YouTube,
				// which would 403 mid-upload. This test pins the gate so
				// the worker falls into a deterministic "re-upload
				// required" failed status instead.
				Status:    models.MediaAssetStatusReady,
				ExpiresAt: expired,
			},
		},
	}
	getter := &fakeObjectGetter{nextURL: "https://example.com/x"}
	resolver := NewMediaDownloadResolver(getter, store, nil)

	post := &models.Post{WorkspaceID: 1, MediaAssetID: ptrToString("asset-expired")}
	_, err := resolver.ResolveForUpload(context.Background(), post, time.Minute)
	if !errors.Is(err, ErrAssetExpired) {
		t.Fatalf("err = %v, want errors.Is(err, ErrAssetExpired)", err)
	}
	if len(getter.calls) != 0 {
		t.Errorf("ObjectGetter.GetObject must not be called when asset expired; got %d call(s)", len(getter.calls))
	}
}

// TestMediaDownloadResolver_ResolveForUpload_StorageObjectKey verifies
// the legacy / direct branch: when post.MediaAssetID is empty BUT
// (post.Bucket, post.StorageObjectKey) are set, the resolver falls back
// to ResolveByKey without consulting MediaAssetStore.
func TestMediaDownloadResolver_ResolveForUpload_StorageObjectKey(t *testing.T) {
	store := &fakeMediaAssetStore{
		assets: map[string]*models.MediaAsset{
			"asset-key": {ID: "asset-key", UserID: 1, UploadKey: "uploads/1/legacy-x.mp4", Bucket: "instaedit-local", Status: models.MediaAssetStatusReady},
		},
	}
	getter := &fakeObjectGetter{nextURL: "https://presigned.example.com/legacy.mp4"}
	resolver := NewMediaDownloadResolver(getter, store, nil)

	post := &models.Post{
		WorkspaceID:      1,
		Bucket:           ptrToString("instaedit-local"),
		StorageObjectKey: ptrToString("uploads/1/legacy-x.mp4"),
	}
	// MediaAssetID is nil (legacy).
	url, err := resolver.ResolveForUpload(context.Background(), post, time.Minute)
	if err != nil {
		t.Fatalf("ResolveForUpload returned unexpected error: %v", err)
	}
	if url != "https://presigned.example.com/legacy.mp4" {
		t.Errorf("returned URL = %q, want %q", url, "https://presigned.example.com/legacy.mp4")
	}
	if len(getter.calls) != 1 {
		t.Fatalf("ObjectGetter.GetObject call count = %d, want 1", len(getter.calls))
	}
	if getter.calls[0].Key != "uploads/1/legacy-x.mp4" {
		t.Errorf("ObjectGetter.GetObject key = %q, want %q", getter.calls[0].Key, "uploads/1/legacy-x.mp4")
	}
}

// TestMediaDownloadResolver_ResolveForUpload_LegacyMediaURLFallback
// verifies the LEGACY branch: posts that pre-date migration 080 have
// ONLY post.MediaURL set (no MediaAssetID, no StorageObjectKey). The
// resolver MUST parse post.MediaURL to recover the upload_key, then
// call MediaAssetStore.FindByUploadKey to load the canonical asset
// row, then run the same status=ready + expires_at gates applied to
// the asset-id branch. No fresh presigned URL may be minted without
// confirming the row exists AND is ready AND not expired.
func TestMediaDownloadResolver_ResolveForUpload_LegacyMediaURLFallback(t *testing.T) {
	store := &fakeMediaAssetStore{
		assets: map[string]*models.MediaAsset{
			"asset-legacy": {
				ID:        "asset-legacy",
				UserID:    1,
				UploadKey: "uploads/1/legacy.mp4",
				Status:    models.MediaAssetStatusReady,
				ExpiresAt: time.Now().Add(1 * time.Hour),
			},
		},
	}
	getter := &fakeObjectGetter{nextURL: "https://presigned.example.com/legacy.mp4"}
	resolver := NewMediaDownloadResolver(getter, store, nil)

	// The media_url embeds the upload_key AFTER the bucket segment.
	// Resolver.extractUploadKeyFromMediaURL splits scheme://host/ then
	// strips the FIRST path component (the bucket name); the residual
	// is the upload_key.
	post := &models.Post{
		WorkspaceID: 1,
		MediaURL:    "https://minio.example.com/instaedit-local/uploads/1/legacy.mp4?X-Amz-Algorithm=AWS4-HMAC-SHA256",
		// MediaAssetID is nil (legacy).
		// StorageObjectKey is nil (legacy).
	}
	url, err := resolver.ResolveForUpload(context.Background(), post, time.Minute)
	if err != nil {
		t.Fatalf("ResolveForUpload returned unexpected error: %v", err)
	}
	if url != "https://presigned.example.com/legacy.mp4" {
		t.Errorf("url = %q, want %q", url, "https://presigned.example.com/legacy.mp4")
	}
	if len(getter.calls) != 1 {
		t.Fatalf("ObjectGetter.GetObject call count = %d, want 1", len(getter.calls))
	}
	if getter.calls[0].Key != "uploads/1/legacy.mp4" {
		t.Errorf("ObjectGetter.GetObject key = %q, want %q", getter.calls[0].Key, "uploads/1/legacy.mp4")
	}
	if getter.calls[0].TTL != time.Minute {
		t.Errorf("ObjectGetter.GetObject ttl = %v, want 1m", getter.calls[0].TTL)
	}
}

// TestMediaDownloadResolver_ResolveForUpload_LegacyMediaURLNoMatchingAsset
// verifies the legacy URL fallback returns ErrMediaURLNoMatchingAsset
// when the parsed upload_key does not correspond to any media_assets
// row (e.g. cleanup pass hard-deleted, operator hand-edited the URL,
// or media_url was never canonical). No presigned URL may be minted
// in this branch — defending against the bug class where the worker
// would otherwise pass a bogus URL to YouTube and receive a 403
// mid-upload.
func TestMediaDownloadResolver_ResolveForUpload_LegacyMediaURLNoMatchingAsset(t *testing.T) {
	store := &fakeMediaAssetStore{assets: map[string]*models.MediaAsset{}}
	getter := &fakeObjectGetter{nextURL: "https://example.com/never-called"}
	resolver := NewMediaDownloadResolver(getter, store, nil)

	post := &models.Post{
		MediaURL: "https://minio.example.com/instaedit-local/uploads/999/orphan.mp4",
	}
	_, err := resolver.ResolveForUpload(context.Background(), post, time.Minute)
	if !errors.Is(err, ErrMediaURLNoMatchingAsset) {
		t.Fatalf("err = %v, want errors.Is(err, ErrMediaURLNoMatchingAsset)", err)
	}
	if len(getter.calls) != 0 {
		t.Errorf("ObjectGetter.GetObject must not be called when no matching asset; got %d call(s)", len(getter.calls))
	}
}

// TestMediaDownloadResolver_ResolveForUpload_LegacyMediaURLExpired
// verifies the legacy branch also honours the expires_at gate after
// the FindByUploadKey lookup (defense for the scenario where the
// asset row is status=ready but expires_at is past — the cleanup
// pass only sweeps status='pending' rows so a ready row whose TTL
// lapsed slips past MarkExpired).
func TestMediaDownloadResolver_ResolveForUpload_LegacyMediaURLExpired(t *testing.T) {
	store := &fakeMediaAssetStore{
		assets: map[string]*models.MediaAsset{
			"asset-leg-expired": {
				ID:        "asset-leg-expired",
				UserID:    1,
				UploadKey: "uploads/2/x.mp4",
				Status:    models.MediaAssetStatusReady,
				ExpiresAt: time.Now().Add(-1 * time.Minute), // past TTL
			},
		},
	}
	getter := &fakeObjectGetter{nextURL: "https://example.com/x"}
	resolver := NewMediaDownloadResolver(getter, store, nil)

	post := &models.Post{
		WorkspaceID: 1,
		MediaURL:    "https://minio.example.com/instaedit-local/uploads/2/x.mp4",
	}
	_, err := resolver.ResolveForUpload(context.Background(), post, time.Minute)
	if !errors.Is(err, ErrAssetExpired) {
		t.Fatalf("err = %v, want errors.Is(err, ErrAssetExpired)", err)
	}
	if len(getter.calls) != 0 {
		t.Errorf("ObjectGetter.GetObject must not be called on expired asset; got %d call(s)", len(getter.calls))
	}
}

// TestMediaDownloadResolver_ResolveForUpload_NoReference verifies the
// resolver returns ErrPostMissingMediaReference when neither field is
// set. Worker uses errors.Is on this sentinel to route the target to
// status='failed' with a deterministic message.
func TestMediaDownloadResolver_ResolveForUpload_NoReference(t *testing.T) {
	store := &fakeMediaAssetStore{}
	getter := &fakeObjectGetter{nextURL: "https://example.com/should-not-be-called"}
	resolver := NewMediaDownloadResolver(getter, store, nil)

	post := &models.Post{} // no MediaAssetID, no StorageObjectKey
	_, err := resolver.ResolveForUpload(context.Background(), post, time.Minute)
	if !errors.Is(err, ErrPostMissingMediaReference) {
		t.Errorf("err = %v, want errors.Is(err, ErrPostMissingMediaReference)", err)
	}
	if len(getter.calls) != 0 {
		t.Errorf("ObjectGetter.GetObject must not be called when post has no reference; got %d call(s)", len(getter.calls))
	}
}

// ptrToString is a tiny constructor helper for *string literals in
// test fixtures. Keeps test bodies concise without pulling in a
// generic helper file.
func ptrToString(s string) *string {
	return &s
}
