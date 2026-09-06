//go:build integration

package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

// lifecycleYouTubeProvider is a single fake provider for both phases of the
// real pipeline: UploadVideoAsPrivate (preparation/upload phase) and Publish
// (public publish phase). It embeds the shared OAuth/publish test double so
// the worker exercises the same CapabilityRouter and CredentialVault seams as
// production.
type lifecycleYouTubeProvider struct {
	*mockProvider
	mu                 sync.Mutex
	privateUploadCalls int
	privateVideoIDs    []string
	// uploadErr — when non-nil, UploadVideoAsPrivate fails with this
	// error AFTER incrementing the call counter (a real API failure
	// that already burned the reserved quota charge).
	uploadErr error
}

func (p *lifecycleYouTubeProvider) RefreshOAuthToken(_ context.Context, _ string) (*models.TokenData, error) {
	return &models.TokenData{AccessToken: "fake-youtube-access", TokenType: "Bearer"}, nil
}

func (p *lifecycleYouTubeProvider) UploadVideoAsPrivate(_ context.Context, _ string, _ *models.Post, _ string, _ *time.Time) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.privateUploadCalls++
	if p.uploadErr != nil {
		return "", p.uploadErr
	}
	videoID := fmt.Sprintf("private-video-%d", p.privateUploadCalls)
	p.privateVideoIDs = append(p.privateVideoIDs, videoID)
	return videoID, nil
}

var (
	_ services.OAuthProvider         = (*lifecycleYouTubeProvider)(nil)
	_ services.Publisher             = (*lifecycleYouTubeProvider)(nil)
	_ services.UploadChannelUploader = (*lifecycleYouTubeProvider)(nil)
)

// lifecycleUploadPostStore is the narrow post surface consumed by
// UploadWorker.processPublishJob. It is intentionally in-memory: the test is
// DB-backed through Inbox/Package/Schedule/UploadJob, while this fake isolates
// the provider fan-out and exposes exact create/trigger counters.
type lifecycleUploadPostStore struct {
	mu            sync.Mutex
	post          *models.Post
	targets       []*models.PostTarget
	createCalls   int
	publishCalls  int
	statusUpdates int
}

func (s *lifecycleUploadPostStore) Create(post *models.Post, targets []*models.PostTarget) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createCalls++
	if post.ID == 0 {
		post.ID = 74001
	}
	for i, target := range targets {
		if target.ID == 0 {
			target.ID = int64(74100 + i)
		}
		target.PostID = post.ID
	}
	s.post = post
	s.targets = append([]*models.PostTarget(nil), targets...)
	return nil
}

func (s *lifecycleUploadPostStore) PublishPost(_ int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishCalls++
	return nil
}

func (s *lifecycleUploadPostStore) SetTargetStatus(_ context.Context, _ int64, _ models.PostStatus, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusUpdates++
	return nil
}

// lifecycleYouTubePublicationStore models the durable per-target YouTube
// publication row. Its map is keyed by post_target_id, so a repeated upload
// phase can only find the existing uploaded row and skip videos.insert.
type lifecycleYouTubePublicationStore struct {
	mu            sync.Mutex
	rows          map[int64]*models.YouTubeTargetPublication
	createCalls   int
	uploadedCalls int
}

func newLifecycleYouTubePublicationStore() *lifecycleYouTubePublicationStore {
	return &lifecycleYouTubePublicationStore{rows: make(map[int64]*models.YouTubeTargetPublication)}
}

func (s *lifecycleYouTubePublicationStore) Create(_ context.Context, pub *models.YouTubeTargetPublication) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.rows[pub.PostTargetID]; exists {
		return errors.New("duplicate youtube target publication")
	}
	s.createCalls++
	copy := *pub
	copy.ID = int64(75000 + s.createCalls)
	s.rows[pub.PostTargetID] = &copy
	*pub = copy
	return nil
}

func (s *lifecycleYouTubePublicationStore) FindByPostTargetID(_ context.Context, targetID int64) (*models.YouTubeTargetPublication, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.rows[targetID]
	if row == nil {
		return nil, nil
	}
	copy := *row
	return &copy, nil
}

// MarkPublished (YouTubeTargetPublicationLookup) stamps published_at on the
// Phase-2 reused video row. Upsert-shaped: no-op when the id is unknown so
// the caller's non-fatal warn path is exercised like production.
func (s *lifecycleYouTubePublicationStore) MarkPublished(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.rows {
		if row.ID == id {
			now := time.Now()
			row.PublishedAt = &now
			return nil
		}
	}
	return nil
}

// ClearYouTubeUpload (YouTubeTargetPublicationLookup) nullifies the Phase-1
// video stamp — used by orphan-video recovery. Not exercised by the happy
// path in this test but required by the interface.
func (s *lifecycleYouTubePublicationStore) ClearYouTubeUpload(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.rows {
		if row.ID == id {
			row.YouTubeVideoID = nil
			row.YouTubeUploadStatus = "upload_session_initiated"
			return nil
		}
	}
	return nil
}

func (s *lifecycleYouTubePublicationStore) MarkYouTubeUploaded(_ context.Context, id int64, videoID string) error {
	return s.MarkYouTubeUploadedAtomic(context.Background(), id, videoID)
}

func (s *lifecycleYouTubePublicationStore) MarkYouTubeUploadedAtomic(_ context.Context, id int64, videoID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.rows {
		if row.ID == id {
			if row.YouTubeUploadStatus != "youtube_uploaded" {
				s.uploadedCalls++
			}
			row.YouTubeVideoID = &videoID
			row.YouTubeUploadStatus = "youtube_uploaded"
			return nil
		}
	}
	return fmt.Errorf("publication id %d not found", id)
}

func (s *lifecycleYouTubePublicationStore) IncrementAttempt(_ context.Context, id int64, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.rows {
		if row.ID == id {
			row.AttemptCount++
			row.LastError = message
			return nil
		}
	}
	return fmt.Errorf("publication id %d not found", id)
}

func (s *lifecycleYouTubePublicationStore) Update(_ context.Context, pub *models.YouTubeTargetPublication) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.rows[pub.PostTargetID]
	if row == nil {
		return fmt.Errorf("publication target %d not found", pub.PostTargetID)
	}
	copy := *pub
	s.rows[pub.PostTargetID] = &copy
	return nil
}

// delivery-queue fake surface (migration 125). The claim simulates the
// FOR UPDATE SKIP LOCKED + lease-CAS UPDATE: rows in a claimable state
// flip to 'uploading' with a lease owner; the processor transitions
// them via MarkDeliveryUploaded / MarkDeliveryFailed /
// MarkDeliveryBlockedAuth.
func (s *lifecycleYouTubePublicationStore) ClaimReadyDeliveries(_ context.Context, workerID string, limit int, _ time.Duration) ([]*models.YouTubeTargetPublication, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*models.YouTubeTargetPublication
	for _, row := range s.rows {
		if row.State != "ready_to_upload" && row.State != "preflight" {
			continue
		}
		copy := *row
		copy.State = "uploading"
		copy.LeaseOwner = &workerID
		expires := time.Now().Add(time.Minute)
		copy.LeaseExpiresAt = &expires
		s.rows[row.PostTargetID] = &copy
		out = append(out, &copy)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *lifecycleYouTubePublicationStore) HeartbeatDelivery(_ context.Context, _ int64, _ string, _ time.Duration) (bool, error) {
	return true, nil
}

func (s *lifecycleYouTubePublicationStore) ReleaseDeliveryLease(_ context.Context, id int64, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.rows {
		if row.ID == id {
			row.LeaseOwner = nil
			row.LeaseExpiresAt = nil
			row.HeartbeatAt = nil
			return nil
		}
	}
	return fmt.Errorf("publication id %d not found", id)
}

func (s *lifecycleYouTubePublicationStore) MarkDeliveryUploaded(_ context.Context, id int64, _ string, videoID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.rows {
		if row.ID == id {
			if row.YouTubeUploadStatus != "youtube_uploaded" {
				s.uploadedCalls++
			}
			row.YouTubeVideoID = &videoID
			row.YouTubeUploadStatus = "youtube_uploaded"
			row.State = "youtube_uploaded"
			row.AttemptCount++
			row.LeaseOwner = nil
			row.LeaseExpiresAt = nil
			row.HeartbeatAt = nil
			return nil
		}
	}
	return fmt.Errorf("publication id %d not found", id)
}

func (s *lifecycleYouTubePublicationStore) MarkDeliveryFailed(_ context.Context, id int64, _ string, errorCode string, message string, nextAttemptAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.rows {
		if row.ID == id {
			row.AttemptCount++
			row.LastError = message
			code := errorCode
			row.LastErrorCode = &code
			row.NextAttemptAt = &nextAttemptAt
			if row.AttemptCount >= row.MaxAttempts {
				row.State = "dead_letter"
			} else {
				row.State = "retry_wait"
			}
			row.LeaseOwner = nil
			row.LeaseExpiresAt = nil
			row.HeartbeatAt = nil
			return nil
		}
	}
	return fmt.Errorf("publication id %d not found", id)
}

func (s *lifecycleYouTubePublicationStore) MarkDeliveryQuotaWait(_ context.Context, id int64, _ string, nextAttemptAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.rows {
		if row.ID == id {
			row.State = "quota_wait"
			resume := "ready_to_upload"
			row.ResumeState = &resume
			row.NextAttemptAt = &nextAttemptAt
			code := "quota_exceeded"
			row.LastErrorCode = &code
			row.LeaseOwner = nil
			row.LeaseExpiresAt = nil
			row.HeartbeatAt = nil
			return nil
		}
	}
	return fmt.Errorf("publication id %d not found", id)
}

func (s *lifecycleYouTubePublicationStore) MarkDeliveryBlockedAuth(_ context.Context, id int64, _ string, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.rows {
		if row.ID == id {
			row.AttemptCount++
			row.LastError = reason
			row.State = "failed"
			row.LeaseOwner = nil
			row.LeaseExpiresAt = nil
			row.HeartbeatAt = nil
			return nil
		}
	}
	return fmt.Errorf("publication id %d not found", id)
}

func (s *lifecycleYouTubePublicationStore) ReclaimExpiredDeliveryLeases(_ context.Context, _ int) (int64, error) {
	return 0, nil
}

// lifecycleDeliveryPostStore resolves the post + post_target rows a
// claimed delivery needs, mirroring *repository.PostRepository's
// lookups. It reads LIVE from the lifecycleUploadPostStore because the
// post/targets are only populated when processPublishJob calls
// Create (the delivery pool runs after materialization).
type lifecycleDeliveryPostStore struct {
	postStore *lifecycleUploadPostStore
}

func (s *lifecycleDeliveryPostStore) FindByID(id int64) (*models.Post, error) {
	if s.postStore.post != nil && s.postStore.post.ID == id {
		return s.postStore.post, nil
	}
	return nil, nil
}

func (s *lifecycleDeliveryPostStore) FindTargetByID(id int64) (*models.PostTarget, error) {
	for _, t := range s.postStore.targets {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, nil
}

var _ UploadDeliveryPostStore = (*lifecycleDeliveryPostStore)(nil)

// lifecycleTestStorage is a tiny S3-compatible fake. It reuses the worker's
// normal presigned PUT flow and returns the same verified size/mime that Drive
// advertised, so artifact verification still runs before MarkIngested.
type lifecycleTestStorage struct {
	server      *httptest.Server
	contentType string
	sizeBytes   int64
	mu          sync.Mutex
	putCalls    int
}

func newLifecycleTestStorage(t *testing.T, contentType string, sizeBytes int64) *lifecycleTestStorage {
	t.Helper()
	storage := &lifecycleTestStorage{contentType: contentType, sizeBytes: sizeBytes}
	storage.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		storage.mu.Lock()
		storage.putCalls++
		storage.mu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(storage.server.Close)
	return storage
}

func (s *lifecycleTestStorage) SignUpload(_ context.Context, _ int64, _, _ string, _ int64, _ time.Duration) (*services.UploadGrant, error) {
	return &services.UploadGrant{UploadURL: s.server.URL + "/upload"}, nil
}
func (s *lifecycleTestStorage) VerifyUpload(_ context.Context, _ string) (string, int64, error) {
	return s.contentType, s.sizeBytes, nil
}
func (s *lifecycleTestStorage) AssetURL(key string) string { return "https://fake-storage.test/" + key }
func (s *lifecycleTestStorage) Provider() string           { return "fake-s3" }
func (s *lifecycleTestStorage) Bucket() string             { return "fake-bucket" }
func (s *lifecycleTestStorage) GetObject(_ context.Context, key string, _ time.Duration) (string, error) {
	return s.AssetURL(key), nil
}
func (s *lifecycleTestStorage) Upload(_ context.Context, _ io.Reader, _, _ string, sizeBytes int64) (int64, error) {
	return sizeBytes, nil
}

var _ services.StorageProvider = (*lifecycleTestStorage)(nil)
var _ UploadPostStore = (*lifecycleUploadPostStore)(nil)
var _ UploadYouTubeTargetPubStore = (*lifecycleYouTubePublicationStore)(nil)

func lifecycleSHA(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestContentPackageLifecycleE2E_DriveSchedulePreparationPublish_Idempotent(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t, postgres.WithDatabase("instaedit_content_lifecycle_e2e"))
	defer cleanup()
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}

	const userID int64 = 98300
	const workspaceID int64 = 98300
	const driveAccountID int64 = 98301
	const youtubeAccountIT int64 = 98302
	const youtubeAccountEN int64 = 98303
	const youtubeAccountES int64 = 98304
	accountIDs := []int64{driveAccountID, youtubeAccountIT, youtubeAccountEN, youtubeAccountES}
	if _, err := db.Exec(`INSERT INTO users (id,email,name) VALUES ($1,$2,$3)`, userID, "lifecycle@example.test", "Lifecycle"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces (id,name,owner_id) VALUES ($1,$2,$3)`, workspaceID, "Lifecycle Workspace", userID); err != nil {
		t.Fatal(err)
	}
	for _, accountID := range accountIDs {
		platform := "youtube"
		if accountID == driveAccountID {
			platform = "google_drive"
		}
		if _, err := db.Exec(`INSERT INTO platform_accounts (id,user_id,workspace_id,platform,platform_user_id,username) VALUES ($1,$2,$3,$4,$5,$6)`, accountID, userID, workspaceID, platform, "channel-"+strconv.FormatInt(accountID, 10), "channel-"+strconv.FormatInt(accountID, 10)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO workspace_channels (workspace_id,platform_account_id,enabled) VALUES ($1,$2,true)`, workspaceID, accountID); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	packageRepo := repository.NewContentPackageRepository(db)
	uploadRepo := repository.NewUploadJobRepository(db)

	cover := "cover-lifecycle"
	driveAccountIDPtr := driveAccountID
	revision := &models.ContentMetadataRevision{SourceLanguage: "it", Title: "Titolo lifecycle", Description: "Descrizione lifecycle", Tags: json.RawMessage(`[]`), CreatedBy: userID}
	pkg := &models.ContentPackage{WorkspaceID: workspaceID, CreatedBy: userID, SourceType: "google_drive", DriveAccountID: &driveAccountIDPtr, DriveFileID: "drive-lifecycle-1", SourceFilename: "lifecycle.mp4", SourceFingerprint: "lifecycle-sha", SourceLanguage: "it", CurrentCoverMediaID: &cover}
	if err := packageRepo.CreatePackage(ctx, pkg, revision); err != nil {
		t.Fatalf("create package: %v", err)
	}
	if pkg.ID == 0 {
		t.Fatal("create did not create a content package")
	}

	targets := []*models.ContentPackageTarget{
		{ContentPackageID: pkg.ID, PlatformAccountID: youtubeAccountIT, Language: "it", PrivacyStatus: "public", Enabled: true},
		{ContentPackageID: pkg.ID, PlatformAccountID: youtubeAccountEN, Language: "en", PrivacyStatus: "public", Enabled: true},
		{ContentPackageID: pkg.ID, PlatformAccountID: youtubeAccountES, Language: "es", PrivacyStatus: "public", Enabled: true},
	}
	if _, err := packageRepo.ReplaceTargets(ctx, pkg.ID, pkg.Version, targets); err != nil {
		t.Fatalf("replace targets: %v", err)
	}
	pkg.Version++
	if err := packageRepo.CreateTranslationBundle(ctx, &models.TranslationBundle{ContentPackageID: pkg.ID, SourceMetadataRevisionID: revision.ID, Provider: "fake-nvidia", Status: "completed", RequestedLanguages: []string{"en", "es"}}, []*models.TranslationEntry{
		{Language: "en", Title: "Lifecycle EN", Description: "Lifecycle description EN", Tags: json.RawMessage(`[]`), Origin: "nvidia"},
		{Language: "es", Title: "Lifecycle ES", Description: "Lifecycle description ES", Tags: json.RawMessage(`[]`), Origin: "nvidia"},
	}); err != nil {
		t.Fatalf("translations: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	schedule := &models.ContentSchedule{ContentPackageID: pkg.ID, ScheduledAt: now.Add(-time.Minute), PrepareAt: now.Add(-2 * time.Minute), Timezone: "Europe/Rome"}
	if err := packageRepo.UpsertSchedule(ctx, schedule, pkg.Version); err != nil {
		t.Fatalf("schedule: %v", err)
	}

	provider := &lifecycleYouTubeProvider{mockProvider: &mockProvider{
		baseMockProvider: baseMockProvider{platform: models.PlatformYouTube},
		publishFn: func(_ context.Context, _, platformUserID string, _ models.PublishPayload) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: "public-" + platformUserID}, nil
		},
	}}
	router := services.NewCapabilityRouter()
	router.Register(models.PlatformYouTube, provider)
	vault := &fakeVault{}
	users := &mockUserStore{findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
		if id == driveAccountID {
			return &models.PlatformAccount{ID: id, Platform: models.PlatformGoogleDrive, PlatformUserID: "drive-channel"}, nil
		}
		return &models.PlatformAccount{ID: id, Platform: models.PlatformYouTube, PlatformUserID: "channel-" + strconv.FormatInt(id, 10), Username: "channel-" + strconv.FormatInt(id, 10)}, nil
	}}

	prep := NewContentPreparationWorker(packageRepo, uploadRepo, "lifecycle-preparation", ContentPreparationWorkerOptions{Interval: time.Hour, LeaseTTL: time.Minute, BatchSize: 10}, nil)
	if err := prep.RunOnce(ctx); err != nil {
		t.Fatalf("preparation: %v", err)
	}
	preparedSchedule, err := packageRepo.FindSchedule(ctx, pkg.ID)
	if err != nil || preparedSchedule == nil || preparedSchedule.Status != "ready_to_publish" {
		t.Fatalf("schedule after preparation: %+v err=%v", preparedSchedule, err)
	}
	job, err := uploadRepo.FindByScheduleID(ctx, schedule.ID)
	if err != nil || job == nil {
		t.Fatalf("prepared upload job: %+v err=%v", job, err)
	}
	if job.Status != models.UploadJobStatusPending {
		t.Fatalf("prepared upload job status=%q, want pending", job.Status)
	}

	payload := []byte("fake-drive-video-lifecycle")
	driveImporter := &fakeImporter{
		metadataResp: &services.GoogleDriveFile{ID: pkg.DriveFileID, Name: pkg.SourceFilename, Size: strconv.Itoa(len(payload)), MimeType: "video/mp4", SHA256Checksum: lifecycleSHA(payload)},
		downloadResp: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytesReader(payload)), Header: http.Header{"Content-Type": []string{"video/mp4"}}},
	}
	driveSource, err := NewAuthenticatedDriveSource(driveImporter, vault)
	if err != nil {
		t.Fatalf("drive source: %v", err)
	}
	registry := NewArtifactSourceRegistry()
	if err := registry.Register(driveSource); err != nil {
		t.Fatalf("source registry: %v", err)
	}
	storage := newLifecycleTestStorage(t, "video/mp4", int64(len(payload)))
	mediaStore := &fakeMediaStore{}
	uploadPostStore := &lifecycleUploadPostStore{}
	ytPubs := newLifecycleYouTubePublicationStore()
	uploadWorker := NewUploadWorker(uploadRepo, mediaStore, uploadPostStore, users, storage, router, vault, registry, nil, time.Second, nil, UploadWorkerOptions{VideoRetentionBufferDays: 7})
	uploadWorker.SetMediaDownloadResolver(testMediaDownloadResolver{})
	uploadWorker.SetYouTubeTargetPublishStore(ytPubs)
	uploadWorker.SetYouTubeDeliveryPostStore(&lifecycleDeliveryPostStore{postStore: uploadPostStore})

	claimedIngest, err := uploadRepo.ClaimBatch(ctx, "lifecycle-ingest", 1, time.Minute)
	if err != nil || len(claimedIngest) != 1 {
		t.Fatalf("ingest claim: jobs=%d err=%v", len(claimedIngest), err)
	}
	if err := uploadWorker.processIngestJob(ctx, claimedIngest[0], "lifecycle-ingest"); err != nil {
		t.Fatalf("Drive ingest: %v", err)
	}
	if got := driveImporter.downloadCallCount(); got != 1 {
		t.Fatalf("Drive download calls=%d, want 1", got)
	}
	if got := storage.putCallCount(); got != 1 {
		t.Fatalf("storage PUT calls=%d, want 1", got)
	}
	if second, err := uploadRepo.ClaimBatch(ctx, "lifecycle-ingest-2", 1, time.Minute); err != nil || len(second) != 0 {
		t.Fatalf("second ingest claim duplicated work: jobs=%d err=%v", len(second), err)
	}

	claimedPublish, err := uploadRepo.ClaimBatchForPublish(ctx, "lifecycle-publish", 1, time.Minute)
	if err != nil || len(claimedPublish) != 1 {
		t.Fatalf("publish claim: jobs=%d err=%v", len(claimedPublish), err)
	}
	if err := uploadWorker.processPublishJob(ctx, claimedPublish[0], "lifecycle-publish"); err != nil {
		t.Fatalf("private upload preparation: %v", err)
	}
	// The job claim only MATERIALIZES the delivery rows now — the heavy
	// videos.insert happens in the delivery pool per (video, channel)
	// row. Assert the fan-out split: 3 rows enqueued, 0 uploads yet.
	if provider.privateUploadCalls != 0 {
		t.Fatalf("private YouTube uploads during materialization=%d, want 0 (uploads moved to the delivery pool)", provider.privateUploadCalls)
	}
	if ytPubs.createCalls != 3 || ytPubs.uploadedCalls != 0 {
		t.Fatalf("YouTube publication rows create=%d uploaded=%d, want 3/0 after materialization", ytPubs.createCalls, ytPubs.uploadedCalls)
	}
	// Fan-out phase: the delivery pool claims the 3 (video, channel)
	// rows and uploads each independently. A slow channel cannot block
	// its siblings — each row is its own claim + lease + retry budget.
	deliveries, err := ytPubs.ClaimReadyDeliveries(ctx, "lifecycle-delivery", 10, time.Minute)
	if err != nil || len(deliveries) != 3 {
		t.Fatalf("delivery claim: rows=%d err=%v, want 3", len(deliveries), err)
	}
	for _, delivery := range deliveries {
		if err := uploadWorker.processYouTubeDelivery(ctx, delivery, "lifecycle-delivery"); err != nil {
			t.Fatalf("per-delivery private upload id=%d: %v", delivery.ID, err)
		}
	}
	if provider.privateUploadCalls != 3 {
		t.Fatalf("private YouTube uploads=%d, want 3 (one per delivery row)", provider.privateUploadCalls)
	}
	if ytPubs.uploadedCalls != 3 {
		t.Fatalf("YouTube publication rows uploaded=%d, want 3", ytPubs.uploadedCalls)
	}
	// Exercise the idempotent retry branch after the durable rows are
	// youtube_uploaded: re-processing a delivery must find the row and
	// skip videos.insert — a retry must not create a second
	// provider-side video for any channel.
	for _, target := range uploadPostStore.targets {
		row, err := ytPubs.FindByPostTargetID(ctx, target.ID)
		if err != nil || row == nil || row.YouTubeUploadStatus != "youtube_uploaded" {
			t.Fatalf("delivery row after fan-out target=%d: %+v err=%v, want youtube_uploaded", target.ID, row, err)
		}
		if err := uploadWorker.processYouTubeDelivery(ctx, row, "lifecycle-delivery-2"); err != nil {
			t.Fatalf("idempotent delivery retry id=%d: %v", row.ID, err)
		}
	}
	if provider.privateUploadCalls != 3 || ytPubs.createCalls != 3 || ytPubs.uploadedCalls != 3 {
		t.Fatalf("delivery retry duplicated provider state: private=%d creates=%d uploaded=%d, want 3/3/3", provider.privateUploadCalls, ytPubs.createCalls, ytPubs.uploadedCalls)
	}
	if uploadPostStore.createCalls != 1 || uploadPostStore.publishCalls != 1 {
		t.Fatalf("post handoff create=%d publish-trigger=%d, want 1/1", uploadPostStore.createCalls, uploadPostStore.publishCalls)
	}
	if next, err := uploadRepo.ClaimBatchForPublish(ctx, "lifecycle-publish-2", 1, time.Minute); err != nil || len(next) != 0 {
		t.Fatalf("second publish claim duplicated preparation: jobs=%d err=%v", len(next), err)
	}

	postStore := &mockPostStore{}
	postStore.findByIDFn = func(int64) (*models.Post, error) { return uploadPostStore.post, nil }
	claimedTargets := make(map[int64]bool)
	postStore.claimFn = func(id int64) (bool, error) {
		if claimedTargets[id] {
			return false, nil
		}
		claimedTargets[id] = true
		return true, nil
	}
	publishedTargets := 0
	postStore.updateStatusFn = func(target *models.PostTarget) error {
		if target.Status == models.PostStatusPublished {
			publishedTargets++
		}
		return nil
	}
	publishWorker := NewPublishWorker(postStore, users, router, vault, testMediaDownloadResolver{}, "lifecycle-public-publish", nil, time.Second, nil)
	// Posts materialized from an upload job carry UploadJobID, so the
	// publish worker's Phase-2 path (not the synchronous publisher.Publish)
	// owns the public transition: FindByPostTargetID finds the row stamped
	// youtube_uploaded by the delivery pool, UpdateVideoPrivacy flips the
	// video public, and markYouTubeTargetPublished stamps the target.
	publishWorker.SetYouTubeTargetPublicationStore(ytPubs)
	for _, target := range uploadPostStore.targets {
		if err := publishWorker.publishTarget(ctx, target); err != nil {
			t.Fatalf("public publish target %d: %v", target.ID, err)
		}
	}
	// Two-phase contract: NO extra videos.insert (publishCalls stays 0) —
	// the public transition is the Phase-2 privacy update over the Phase-1
	// private video, one per delivery row.
	if provider.updateVideoPrivacyCalls != 3 || publishedTargets != 3 {
		t.Fatalf("Phase-2 privacy updates=%d target terminal updates=%d, want 3/3", provider.updateVideoPrivacyCalls, publishedTargets)
	}
	// Re-run every already-published target as a worker retry. The claim
	// CAS loses before provider dispatch, so no extra provider call occurs.
	for _, target := range uploadPostStore.targets {
		if err := publishWorker.publishTarget(ctx, target); err != nil {
			t.Fatalf("idempotent retry target %d: %v", target.ID, err)
		}
	}
	if provider.privateUploadCalls != 3 || provider.updateVideoPrivacyCalls != 3 || provider.publishCalls != 0 {
		t.Fatalf("provider calls changed after retries: private=%d privacy_updates=%d public=%d, want 3/3/0", provider.privateUploadCalls, provider.updateVideoPrivacyCalls, provider.publishCalls)
	}

	jobAfter, err := uploadRepo.FindByScheduleID(ctx, schedule.ID)
	if err != nil || jobAfter == nil || jobAfter.Status != models.UploadJobStatusPublishCompleted {
		t.Fatalf("final upload job: %+v err=%v, want publish_completed", jobAfter, err)
	}
}

// bytesReader avoids sharing a mutable bytes.Buffer between the fake Drive
// response and the worker's HTTP-independent source path.
func bytesReader(data []byte) io.ReadCloser {
	copy := append([]byte(nil), data...)
	return &lifecycleReader{data: copy}
}

type lifecycleReader struct {
	data []byte
	pos  int
}

func (r *lifecycleReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
func (r *lifecycleReader) Close() error { return nil }

func (f *fakeImporter) downloadCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.downloadCalls
}

func (s *lifecycleTestStorage) putCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.putCalls
}
