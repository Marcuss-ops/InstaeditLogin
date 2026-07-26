package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// mockContentPipelineStore is the in-memory fake of
// ContentPipelineStore used by the GET /pipeline handler tests.
// Tests that need to simulate errors set pipelineErr; tests that
// need to return a composite entry supply pipelineFn.
type mockContentPipelineStore struct {
	pipelineFn  func(ctx context.Context, workspaceID, postID int64) (*repository.ContentPipelineEntry, error)
	pipelineErr error
}

func (m *mockContentPipelineStore) GetPipeline(ctx context.Context, workspaceID, postID int64) (*repository.ContentPipelineEntry, error) {
	if m.pipelineFn != nil {
		return m.pipelineFn(ctx, workspaceID, postID)
	}
	if m.pipelineErr != nil {
		return nil, m.pipelineErr
	}
	return nil, repository.ErrContentPipelineNotFound
}

// newContentPipelineRig returns a Router with just enough wired
// stores to exercise handleGetContentPipeline.
func newContentPipelineRig(store ContentPipelineStore) *Router {
	return &Router{
		contentPipelineStore: store,
		editorURL:            "https://editor.instaedit.test",
	}
}

// pipelineGetRequest builds the GET /api/v1/content/{content_id}/pipeline
// request. We use httptest.NewRequest + SetPathValue so the
// handler's req.PathValue("content_id") returns the parsed value
// without spinning up the full chi.Mux + protected() middleware.
func pipelineGetRequest(t *testing.T, contentIDRaw, workspaceIDRaw string) *http.Request {
	t.Helper()
	u := "/api/v1/content/" + contentIDRaw + "/pipeline"
	if workspaceIDRaw != "" {
		u += "?workspace_id=" + workspaceIDRaw
	}
	req := httptest.NewRequest(http.MethodGet, u, nil)
	req.SetPathValue("content_id", contentIDRaw)
	return req
}

// pipelineAuthCtx wraps req so the handler's auth.IdentityFromContext
// returns the supplied shim identity. Mirrors the
// auth.WithIdentity(ctx, auth.NewUserIdentity(...)) pattern used
// by pkg/api/team_test.go and pkg/api/admin_channels_test.go.
func pipelineAuthCtx(req *http.Request, userID int64, workspaceIDs []int64) *http.Request {
	// auth.NewUserIdentity returns an auth.Identity with the
	// canonical IsUser() predicate + the WorkspaceIDs() helper
	// the handler reads. Building it via the production
	// constructor means the test contract matches what the JWT
	// middleware writes; refactor drift fails the test build.
	primaryWS := workspaceIDs[0]
	if primaryWS == 0 {
		primaryWS = workspaceIDs[0]
	}
	id := auth.Identity(auth.NewUserIdentity(userID, workspaceIDs[0], 0))
	return req.WithContext(auth.WithIdentity(req.Context(), id))
}

// TestHandleGetContentPipeline_HappyPath exercises the canonical
// Drive→Storage→YouTube→Cover→Scheduled→Published timeline:
// the post has 1 target with a YT pub row that reached
// 'youtube_uploaded' + thumbnail_status='thumbnail_ready', plus a
// media asset and an upload_job.
//
// Asserts:
//   - 200 OK
//   - content_id + workspace_id round-trip
//   - drive.file_id == upload_job.source_id
//   - storage.asset_id + storage.expires_at present
//   - targets[0].youtube_video_id == "yt-published"
//   - targets[0].thumbnail_status == "thumbnail_ready"
//   - targets[0].editor_url built from editorBaseURL + velox_project_id
//   - post_status == "published" (every target reached 'published')
func TestHandleGetContentPipeline_HappyPath(t *testing.T) {
	expireAt := time.Now().Add(7 * 24 * time.Hour).UTC().Truncate(time.Second)
	ytVideoID := "yt-published"
	editorSessionID := "edt-1234567890abcdef"
	veloxProjectID := "ve-proj-123"
	thumbnailMediaID := "asset-thumb-uuid"
	publishedAt := time.Now().UTC().Truncate(time.Second)

	store := &mockContentPipelineStore{
		pipelineFn: func(_ context.Context, _, _ int64) (*repository.ContentPipelineEntry, error) {
			return &repository.ContentPipelineEntry{
				Post: &models.Post{
					ID:          7777,
					WorkspaceID: 7,
					Title:       "Happy Path",
					Caption:     "low-effort caption",
					Status:      models.PostStatusPublished,
					IngestAfter: time.Now().UTC().Add(-1 * time.Hour),
					CreatedAt:   time.Now().UTC().Add(-2 * time.Hour),
					UpdatedAt:   time.Now().UTC(),
				},
				Targets: []*models.PostTarget{{
					ID:                100,
					PostID:            7777,
					PlatformAccountID: 42,
					Status:            models.PostStatusPublished,
					PublishedAt:       &publishedAt,
					AttemptCount:      1,
				}},
				UploadJob: &models.UploadJob{
					ID:             9001,
					SourceID:       "drive-file-abc",
					SourceType:     models.UploadJobSourceAuthenticatedDrive,
					Title:          "happy-path.mp4",
					Status:         models.UploadJobStatusPublishCompleted,
					AssetID:        ptrStringPipeline("asset-uuid-1"),
					DefaultPrivacyLevel: "private",
				},
				Asset: &models.MediaAsset{
					ID:        "asset-uuid-1",
					Status:    models.MediaAssetStatusReady,
					ExpiresAt: expireAt,
				},
				YouTubePubs: map[int64]*models.YouTubeTargetPublication{
					100: {
						ID:                      200,
						PostTargetID:            100,
						PlatformAccountID:       42,
						YouTubeVideoID:          &ytVideoID,
						YouTubeUploadStatus:     "youtube_uploaded",
						YouTubeProcessingStatus: ptrStringPipeline("processed"),
						EditorSessionID:         &editorSessionID,
						VeloxProjectID:          &veloxProjectID,
						ThumbnailMediaID:        &thumbnailMediaID,
						ThumbnailStatus:         ptrStringPipeline("thumbnail_ready"),
						DesiredPrivacy:          "private",
						PublishAt:               ptrTimePipeline(time.Now().UTC().Add(2 * time.Hour)),
						PublishedAt:             &publishedAt,
						AttemptCount:            1,
					},
				},
				Accounts: map[int64]*models.PlatformAccount{
					42: {
						ID:       42,
						Platform: "youtube",
						Username: "Happy Path Channel",
					},
				},
			}, nil
		},
	}

	r := newContentPipelineRig(store)
	req := pipelineGetRequest(t, "7777", "7")
	req = pipelineAuthCtx(req, 1, []int64{7})

	w := httptest.NewRecorder()
	r.handleGetContentPipeline(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}

	var resp contentPipelineResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, w.Body.String())
	}

	if resp.ContentID != 7777 {
		t.Errorf("content_id mismatch: want 7777, got %d", resp.ContentID)
	}
	if resp.WorkspaceID != 7 {
		t.Errorf("workspace_id mismatch: want 7, got %d", resp.WorkspaceID)
	}
	if resp.PostStatus != string(models.PostStatusPublished) {
		t.Errorf("post_status: want published, got %q", resp.PostStatus)
	}

	if resp.Drive == nil || resp.Drive.FileID != "drive-file-abc" {
		t.Errorf("drive.file_id: want drive-file-abc, got %+v", resp.Drive)
	}
	if resp.Drive != nil && resp.Drive.Name != "happy-path.mp4" {
		t.Errorf("drive.name: want happy-path.mp4, got %q", resp.Drive.Name)
	}

	if resp.Storage == nil || resp.Storage.AssetID != "asset-uuid-1" {
		t.Errorf("storage.asset_id: want asset-uuid-1, got %+v", resp.Storage)
	}
	if resp.Storage != nil && !resp.Storage.ExpiresAt.Equal(expireAt) {
		t.Errorf("storage.expires_at: want %v, got %v", expireAt, resp.Storage.ExpiresAt)
	}

	if len(resp.Targets) != 1 {
		t.Fatalf("targets fan-out: want 1, got %d", len(resp.Targets))
	}
	t0 := resp.Targets[0]
	if t0.ChannelName != "Happy Path Channel" {
		t.Errorf("target[0].channel_name: want Happy Path Channel, got %q", t0.ChannelName)
	}
	if t0.YouTubeVideoID != "yt-published" {
		t.Errorf("target[0].youtube_video_id: want yt-published, got %q", t0.YouTubeVideoID)
	}
	if t0.ThumbnailStatus != "thumbnail_ready" {
		t.Errorf("target[0].thumbnail_status: want thumbnail_ready, got %q", t0.ThumbnailStatus)
	}
	if !strings.Contains(t0.EditorURL, "ve-proj-123") {
		t.Errorf("target[0].editor_url: want contains ve-proj-123, got %q", t0.EditorURL)
	}
	if t0.PostStatus != string(models.PostStatusPublished) {
		t.Errorf("target[0].post_status: want published, got %q", t0.PostStatus)
	}
	if !strings.HasPrefix(t0.PublishedAt.Format(time.RFC3339), publishedAt.Format(time.RFC3339)) {
		t.Errorf("target[0].published_at: want %v, got %v", publishedAt, t0.PublishedAt)
	}
}

// TestHandleGetContentPipeline_NotFound asserts the 404 path: the
// repository returns ErrContentPipelineNotFound (either post
// missing or cross-workspace). The handler MUST NOT distinguish
// between those two cases to avoid an info leak.
func TestHandleGetContentPipeline_NotFound(t *testing.T) {
	store := &mockContentPipelineStore{
		pipelineErr: repository.ErrContentPipelineNotFound,
	}
	r := newContentPipelineRig(store)
	req := pipelineGetRequest(t, "1", "7")
	req = pipelineAuthCtx(req, 1, []int64{7})

	w := httptest.NewRecorder()
	r.handleGetContentPipeline(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d (%s)", w.Code, w.Body.String())
	}
}

// TestHandleGetContentPipeline_WorkspaceMismatch covers the
// 403 path: caller passes a workspace_id that isn't in their
// identity's WorkspaceIDs() — handler MUST return 403 BEFORE
// touching the repo (so a malicious caller can't probe existence
// by status code timing).
func TestHandleGetContentPipeline_WorkspaceMismatch(t *testing.T) {
	store := &mockContentPipelineStore{
		pipelineErr: repository.ErrContentPipelineNotFound,
	}
	r := newContentPipelineRig(store)
	req := pipelineGetRequest(t, "1", "999") // workspace 999 not in identity
	req = pipelineAuthCtx(req, 1, []int64{7})

	w := httptest.NewRecorder()
	r.handleGetContentPipeline(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d (%s)", w.Code, w.Body.String())
	}
}

// TestHandleGetContentPipeline_MidPipeline covers the
// Drive-done-YT-not-started case: post + targets exist but no
// media asset, no upload_job, no YT pub rows. The handler MUST
// render an empty drive block + empty storage + targets[] with
// empty youtube_* fields.
func TestHandleGetContentPipeline_MidPipeline(t *testing.T) {
	store := &mockContentPipelineStore{
		pipelineFn: func(_ context.Context, _, _ int64) (*repository.ContentPipelineEntry, error) {
			return &repository.ContentPipelineEntry{
				Post: &models.Post{
					ID:          8888,
					WorkspaceID: 7,
					Title:       "Mid Pipeline",
					Status:      models.PostStatusQueued,
					IngestAfter: time.Now().UTC(),
					CreatedAt:   time.Now().UTC(),
					UpdatedAt:   time.Now().UTC(),
				},
				Targets: []*models.PostTarget{{
					ID:                200,
					PostID:            8888,
					PlatformAccountID: 99,
					Status:            models.PostStatusQueued,
				}},
				Accounts:    map[int64]*models.PlatformAccount{99: {ID: 99, Username: "Mid Channel"}},
				YouTubePubs: map[int64]*models.YouTubeTargetPublication{},
				// UploadJob nil — Drive still running.
				// Asset nil — Storage not stamped yet.
			}, nil
		},
	}
	r := newContentPipelineRig(store)
	req := pipelineGetRequest(t, "8888", "7")
	req = pipelineAuthCtx(req, 1, []int64{7})

	w := httptest.NewRecorder()
	r.handleGetContentPipeline(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}

	var resp contentPipelineResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Drive != nil {
		t.Errorf("drive: want nil for mid-pipeline, got %+v", resp.Drive)
	}
	if resp.Storage != nil {
		t.Errorf("storage: want nil for mid-pipeline, got %+v", resp.Storage)
	}
	if len(resp.Targets) != 1 {
		t.Fatalf("targets: want 1, got %d", len(resp.Targets))
	}
	if resp.Targets[0].YouTubeVideoID != "" {
		t.Errorf("youtube_video_id: want empty for mid-pipeline, got %q", resp.Targets[0].YouTubeVideoID)
	}
	if resp.Targets[0].EditorURL != "" {
		t.Errorf("editor_url: want empty for mid-pipeline, got %q", resp.Targets[0].EditorURL)
	}
	if resp.PostStatus != string(models.PostStatusQueued) {
		t.Errorf("post_status: want queued, got %q", resp.PostStatus)
	}
}

// TestHandleGetContentPipeline_InvalidContentID covers the 400
// path: bad content_id (non-numeric, zero, negative). Each must
// return 400 BEFORE any DB lookup.
func TestHandleGetContentPipeline_InvalidContentID(t *testing.T) {
	cases := []struct {
		name      string
		pathParam string
		wantCode  int
	}{
		{"non-numeric", "abc", http.StatusBadRequest},
		{"zero", "0", http.StatusBadRequest},
		{"negative", "-1", http.StatusBadRequest},
	}
	store := &mockContentPipelineStore{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newContentPipelineRig(store)
			req := pipelineGetRequest(t, tc.pathParam, "7")
			req = pipelineAuthCtx(req, 1, []int64{7})
			w := httptest.NewRecorder()
			r.handleGetContentPipeline(w, req)
			if w.Code != tc.wantCode {
				t.Errorf("want %d, got %d (%s)", tc.wantCode, w.Code, w.Body.String())
			}
		})
	}
}

// TestHandleGetContentPipeline_MissingWorkspaceQuery covers the
// 400 path when workspace_id query is missing.
func TestHandleGetContentPipeline_MissingWorkspaceQuery(t *testing.T) {
	store := &mockContentPipelineStore{}
	r := newContentPipelineRig(store)
	req := pipelineGetRequest(t, "1", "")
	req = pipelineAuthCtx(req, 1, []int64{7})
	w := httptest.NewRecorder()
	r.handleGetContentPipeline(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d (%s)", w.Code, w.Body.String())
	}
}

// TestBuildEditorURL exercises the URL-construction pseudo-unit
// in isolation. Pure-function form keeps the handler's read-path
// behaviour testable without spinning up the full HTTP rig.
func TestBuildEditorURL(t *testing.T) {
	veID := "ve-abc-123"
	pub := &models.YouTubeTargetPublication{VeloxProjectID: &veID}

	if got := buildEditorURL("https://editor.instaedit.test", pub); !strings.Contains(got, "ve-abc-123") {
		t.Errorf("want URL containing ve-abc-123, got %q", got)
	}
	// Trailing slash on the base must NOT double up.
	if got := buildEditorURL("https://editor.instaedit.test/", pub); !strings.Contains(got, "/projects/ve-abc-123") {
		t.Errorf("trailing slash: want /projects/<id>, got %q", got)
	}
	// Empty base → empty URL (no fallback).
	if got := buildEditorURL("", pub); got != "" {
		t.Errorf("empty base: want empty URL, got %q", got)
	}
	// Nil pub → empty URL.
	if got := buildEditorURL("https://x", nil); got != "" {
		t.Errorf("nil pub: want empty URL, got %q", got)
	}
}

// TestCombinePostStatus pins the top-level status aggregator's
// ranking order. Pure-function form keeps the layout-checker away
// from mock setup.
func TestCombinePostStatus(t *testing.T) {
	cases := []struct {
		name string
		seed targetTimelineStatus
		next models.PostStatus
		want targetTimelineStatus
	}{
		{"seeded-from-unknown", statusUnknown, models.PostStatusQueued, statusQueued},
		{"next-unknown-keeps-seed", statusQueued, "", statusQueued},
		{"lower-rank-wins", statusPublished, models.PostStatusQueued, statusQueued},
		{"higher-rank-doesnt-replace", statusQueued, models.PostStatusPublished, statusQueued},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := combinePostStatus(tc.seed, tc.next); got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// --- helpers / shims ---
// Local renames to avoid compile-time collisions with the
// admin_velox_destinations_test.go / internal_velox_callback_dispatcher_test.go
// helpers in the same package (both files declare `func ptrTime` /
// `func ptrString`; using identical names would duplicate-sym at
// go vet time). The pipeline tests use the *Pipeline-prefixed names
// exclusively; the other tests keep their original helpers.

func ptrStringPipeline(s string) *string { return &s }
func ptrTimePipeline(t time.Time) *time.Time {
	tt := t.UTC()
	return &tt
}

// Compile-time assertion: the mock store implements the
// ContentPipelineStore interface so a future signature drift
// fails at go vet time (not at runtime where a production wiring
// break would silently 503).
var _ ContentPipelineStore = (*mockContentPipelineStore)(nil)
