//go:build integration

package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// stubQuotaGate is a scriptable YouTubeQuotaGate for the delivery-pool
// quota tests. It lets each scenario pin the three outcomes the real
// *services.YouTubeQuotaManager can produce: allowed, exhausted
// (allowed=false + retry-after), and gate-error (fail closed).
type stubQuotaGate struct {
	mu             sync.Mutex
	allowed        bool
	retryAfter     int
	reserveErr     error
	reserveCalls   int
	recordedBucket string
	recordedErrs   []string
}

func (g *stubQuotaGate) ReserveOperation(_ context.Context, _ string) (bool, int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reserveCalls++
	if g.reserveErr != nil {
		return false, 0, g.reserveErr
	}
	return g.allowed, g.retryAfter, nil
}

func (g *stubQuotaGate) RecordError(_ context.Context, bucket string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.recordedBucket = bucket
	g.recordedErrs = append(g.recordedErrs, bucket)
	return nil
}

var _ YouTubeQuotaGate = (*stubQuotaGate)(nil)

// newQuotaGateUploadWorker builds the same UploadWorker shape the
// content-package e2e test uses, but with a scriptable quota gate and
// a single (post, target) pair so the delivery processor can resolve
// everything from the fake stores.
func newQuotaGateUploadWorker(t *testing.T, provider *lifecycleYouTubeProvider, gate YouTubeQuotaGate) (*UploadWorker, *lifecycleYouTubePublicationStore, *models.Post, *models.PostTarget) {
	t.Helper()
	router := services.NewCapabilityRouter()
	router.Register(models.PlatformYouTube, provider)
	vault := &fakeVault{}
	users := &mockUserStore{findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
		return &models.PlatformAccount{ID: id, Platform: models.PlatformYouTube, PlatformUserID: fmt.Sprintf("channel-%d", id), Username: fmt.Sprintf("channel-%d", id)}, nil
	}}
	postStore := &lifecycleUploadPostStore{}
	post := &models.Post{ID: 81001, MediaURL: "https://test.invalid/media.mp4"}
	target := &models.PostTarget{ID: 81101, PostID: post.ID, PlatformAccountID: 98302}
	postStore.post = post
	postStore.targets = []*models.PostTarget{target}
	ytPubs := newLifecycleYouTubePublicationStore()
	w := NewUploadWorker(nil, nil, postStore, users, nil, router, vault, nil, nil, time.Second, nil, UploadWorkerOptions{})
	w.SetYouTubeTargetPublishStore(ytPubs)
	w.SetMediaDownloadResolver(testMediaDownloadResolver{})
	w.SetYouTubeDeliveryPostStore(&lifecycleDeliveryPostStore{postStore: postStore})
	w.SetYouTubeQuotaGate(gate)
	return w, ytPubs, post, target
}

// claimedDeliveryForQuotaTest returns a delivery row in the shape the
// delivery pool hands to processYouTubeDelivery (claimed state +
// lease owner, no upload yet) and seeds it into the fake publication
// store so MarkDelivery* lookups resolve (the store keys rows by
// post_target_id, mirroring the real UNIQUE(post_target_id)).
func claimedDeliveryForQuotaTest(t *testing.T, ytPubs *lifecycleYouTubePublicationStore, id, postTargetID int64) *models.YouTubeTargetPublication {
	t.Helper()
	workerID := "quota-gate-worker"
	row := &models.YouTubeTargetPublication{
		ID:                  id,
		PostTargetID:        postTargetID,
		PlatformAccountID:   98302,
		State:               "uploading",
		YouTubeUploadStatus: "",
		LeaseOwner:          &workerID,
		MaxAttempts:         8,
	}
	ytPubs.mu.Lock()
	ytPubs.rows[row.PostTargetID] = row
	ytPubs.mu.Unlock()
	return row
}

// TestUploadWorker_DeliveryQuotaGate_ExhaustedBucketParksInQuotaWait:
// when the gate refuses the videos.insert (video_uploads bucket
// exhausted), the delivery must be parked in quota_wait with
// next_attempt_at = now + retry-after, the retry budget must NOT be
// consumed, and the API call must never fire.
func TestUploadWorker_DeliveryQuotaGate_ExhaustedBucketParksInQuotaWait(t *testing.T) {
	provider := &lifecycleYouTubeProvider{mockProvider: &mockProvider{baseMockProvider: baseMockProvider{platform: models.PlatformYouTube}}}
	gate := &stubQuotaGate{allowed: false, retryAfter: 7200}
	w, ytPubs, _, _ := newQuotaGateUploadWorker(t, provider, gate)

	if err := w.processYouTubeDelivery(context.Background(), claimedDeliveryForQuotaTest(t, ytPubs, 1, 81101), "quota-gate-worker"); err != nil {
		t.Fatalf("processYouTubeDelivery with exhausted bucket: %v", err)
	}
	if provider.privateUploadCalls != 0 {
		t.Fatalf("videos.insert fired while bucket exhausted: calls=%d, want 0", provider.privateUploadCalls)
	}
	row, err := ytPubs.FindByPostTargetID(context.Background(), 81101)
	if err != nil || row == nil {
		t.Fatalf("delivery row lookup: %+v err=%v", row, err)
	}
	if row.State != "quota_wait" {
		t.Fatalf("state=%q, want quota_wait", row.State)
	}
	if row.ResumeState == nil || *row.ResumeState != "ready_to_upload" {
		t.Fatalf("resume_state=%v, want ready_to_upload", row.ResumeState)
	}
	if row.NextAttemptAt == nil {
		t.Fatal("next_attempt_at is nil, want ~now+2h")
	}
	expected := time.Now().Add(2 * time.Hour)
	if delta := row.NextAttemptAt.Sub(expected); delta < -30*time.Second || delta > 30*time.Second {
		t.Fatalf("next_attempt_at=%v, want ~now+2h (retryAfter=7200s), delta=%v", row.NextAttemptAt, delta)
	}
	if row.AttemptCount != 0 {
		t.Fatalf("attempt_count=%d, want 0 (quota parking must not burn the retry budget)", row.AttemptCount)
	}
	if gate.reserveCalls != 1 {
		t.Fatalf("reserve calls=%d, want 1", gate.reserveCalls)
	}
}

// TestUploadWorker_DeliveryQuotaGate_GateErrorFailsClosed: when the
// gate itself errors (DB down), the worker must NOT call YouTube and
// must route the delivery to the retry path — never to dead_letter.
func TestUploadWorker_DeliveryQuotaGate_GateErrorFailsClosed(t *testing.T) {
	provider := &lifecycleYouTubeProvider{mockProvider: &mockProvider{baseMockProvider: baseMockProvider{platform: models.PlatformYouTube}}}
	gate := &stubQuotaGate{reserveErr: errors.New("quota repo down")}
	w, ytPubs, _, _ := newQuotaGateUploadWorker(t, provider, gate)

	if err := w.processYouTubeDelivery(context.Background(), claimedDeliveryForQuotaTest(t, ytPubs, 1, 81101), "quota-gate-worker"); err == nil {
		t.Fatal("gate error must fail closed (return error), got nil")
	}
	if provider.privateUploadCalls != 0 {
		t.Fatalf("videos.insert fired while the gate could not decide: calls=%d, want 0", provider.privateUploadCalls)
	}
	row, err := ytPubs.FindByPostTargetID(context.Background(), 81101)
	if err != nil || row == nil {
		t.Fatalf("delivery row lookup: %+v err=%v", row, err)
	}
	if row.State != "retry_wait" {
		t.Fatalf("state=%q, want retry_wait (transient gate error routes to the retry path)", row.State)
	}
	if row.AttemptCount != 1 {
		t.Fatalf("attempt_count=%d, want 1", row.AttemptCount)
	}
}

// TestUploadWorker_DeliveryQuotaGate_AllowedUploadSucceeds: with quota
// available the upload runs, the delivery lands in youtube_uploaded and
// no error is recorded.
func TestUploadWorker_DeliveryQuotaGate_AllowedUploadSucceeds(t *testing.T) {
	provider := &lifecycleYouTubeProvider{mockProvider: &mockProvider{baseMockProvider: baseMockProvider{platform: models.PlatformYouTube}}}
	gate := &stubQuotaGate{allowed: true}
	w, ytPubs, _, _ := newQuotaGateUploadWorker(t, provider, gate)

	if err := w.processYouTubeDelivery(context.Background(), claimedDeliveryForQuotaTest(t, ytPubs, 1, 81101), "quota-gate-worker"); err != nil {
		t.Fatalf("processYouTubeDelivery: %v", err)
	}
	if provider.privateUploadCalls != 1 {
		t.Fatalf("videos.insert calls=%d, want 1", provider.privateUploadCalls)
	}
	if gate.reserveCalls != 1 {
		t.Fatalf("reserve calls=%d, want 1", gate.reserveCalls)
	}
	if len(gate.recordedErrs) != 0 {
		t.Fatalf("RecordError fired on success: %v", gate.recordedErrs)
	}
	row, err := ytPubs.FindByPostTargetID(context.Background(), 81101)
	if err != nil || row == nil {
		t.Fatalf("delivery row lookup: %+v err=%v", row, err)
	}
	if row.State != "youtube_uploaded" || row.YouTubeUploadStatus != "youtube_uploaded" {
		t.Fatalf("delivery row state=%q status=%q, want youtube_uploaded/youtube_uploaded", row.State, row.YouTubeUploadStatus)
	}
}

// TestUploadWorker_DeliveryQuotaGate_ApiFailureRecordsError: a real
// API failure after the reserved charge must be recorded against the
// video_uploads bucket and the delivery routed to retry_wait.
func TestUploadWorker_DeliveryQuotaGate_ApiFailureRecordsError(t *testing.T) {
	provider := &lifecycleYouTubeProvider{
		mockProvider: &mockProvider{baseMockProvider: baseMockProvider{platform: models.PlatformYouTube}},
		uploadErr:    errors.New("quota exceeded: videoUploadsFull"),
	}
	gate := &stubQuotaGate{allowed: true}
	w, ytPubs, _, _ := newQuotaGateUploadWorker(t, provider, gate)

	if err := w.processYouTubeDelivery(context.Background(), claimedDeliveryForQuotaTest(t, ytPubs, 1, 81101), "quota-gate-worker"); err == nil {
		t.Fatal("upload API failure must surface as an error, got nil")
	}
	if provider.privateUploadCalls != 1 {
		t.Fatalf("videos.insert calls=%d, want 1 (the failing call did happen)", provider.privateUploadCalls)
	}
	if gate.recordedBucket != services.YouTubeQuotaBucketVideoUploads {
		t.Fatalf("RecordError bucket=%q, want %q", gate.recordedBucket, services.YouTubeQuotaBucketVideoUploads)
	}
	row, err := ytPubs.FindByPostTargetID(context.Background(), 81101)
	if err != nil || row == nil {
		t.Fatalf("delivery row lookup: %+v err=%v", row, err)
	}
	if row.State != "retry_wait" {
		t.Fatalf("state=%q, want retry_wait", row.State)
	}
	if row.LastErrorCode == nil || *row.LastErrorCode != "upload_failed" {
		t.Fatalf("last_error_code=%v, want upload_failed", row.LastErrorCode)
	}
	if row.AttemptCount != 1 {
		t.Fatalf("attempt_count=%d, want 1", row.AttemptCount)
	}
}
