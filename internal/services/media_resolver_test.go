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
	Key string
	TTL time.Duration
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
				UploadKey: "uploads/1/uuid.mp4",
				Status:    models.MediaAssetStatusReady,
			},
		},
	}
	getter := &fakeObjectGetter{nextURL: "https://presigned.example.com/video.mp4?signed=abc"}
	resolver := NewMediaDownloadResolver(getter, store, nil)

	post := &models.Post{
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
				UploadKey: "uploads/2/x.mp4",
				Status:    models.MediaAssetStatusPending,
			},
		},
	}
	getter := &fakeObjectGetter{nextURL: "https://example.com/x"}
	resolver := NewMediaDownloadResolver(getter, store, nil)

	post := &models.Post{MediaAssetID: ptrToString("asset-pending")}
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

	post := &models.Post{MediaAssetID: ptrToString("asset-does-not-exist")}
	_, err := resolver.ResolveForUpload(context.Background(), post, time.Minute)
	if err == nil {
		t.Fatal("expected error for missing asset, got nil")
	}
	if len(getter.calls) != 0 {
		t.Errorf("ObjectGetter.GetObject must not be called when asset is missing; got %d call(s)", len(getter.calls))
	}
}

// TestMediaDownloadResolver_ResolveForUpload_StorageObjectKey verifies
// the legacy / direct branch: when post.MediaAssetID is empty BUT
// (post.Bucket, post.StorageObjectKey) are set, the resolver falls back
// to ResolveByKey without consulting MediaAssetStore.
func TestMediaDownloadResolver_ResolveForUpload_StorageObjectKey(t *testing.T) {
	// store configured but NOT expected to be queried.
	store := &fakeMediaAssetStore{
		assets: map[string]*models.MediaAsset{},
		err:    errors.New("store should not be called"),
	}
	getter := &fakeObjectGetter{nextURL: "https://presigned.example.com/legacy.mp4"}
	resolver := NewMediaDownloadResolver(getter, store, nil)

	post := &models.Post{
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

// TestMediaDownloadResolver_ResolveForKey verifies the direct entry
// point: caller supplies (bucket, key) explicitly. Resolver forwards to
// ObjectGetter.GetObject without consulting MediaAssetStore.
func TestMediaDownloadResolver_ResolveForKey(t *testing.T) {
	store := &fakeMediaAssetStore{err: errors.New("store should not be called")}
	getter := &fakeObjectGetter{nextURL: "https://presigned.example.com/direct.mp4"}
	resolver := NewMediaDownloadResolver(getter, store, nil)

	url, err := resolver.ResolveForKey(context.Background(), "instaedit-local", "uploads/1/direct.mp4", 2*time.Hour)
	if err != nil {
		t.Fatalf("ResolveForKey returned unexpected error: %v", err)
	}
	if url != "https://presigned.example.com/direct.mp4" {
		t.Errorf("returned URL = %q, want %q", url, "https://presigned.example.com/direct.mp4")
	}
	if len(getter.calls) != 1 || getter.calls[0].Key != "uploads/1/direct.mp4" || getter.calls[0].TTL != 2*time.Hour {
		t.Errorf("ObjectGetter.GetObject invoked wrong: %+v", getter.calls)
	}
}

// ptrToString is a tiny constructor helper for *string literals in
// test fixtures. Keeps test bodies concise without pulling in a
// generic helper file.
func ptrToString(s string) *string {
	return &s
}
