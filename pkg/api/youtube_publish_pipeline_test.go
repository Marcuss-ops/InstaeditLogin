package api

// pipeline-Go-test-suite-for-publish:
//
// The 14 cases listed in the plan map cleanly onto existing or new
// tests in pkg/api. This file adds only the ones that are NOT already
// covered by the existing commonPublishBackbone-driven suite:
//
//   * TestPublishPipeline_StatusPresentInByProjectResponse
//       Closes the by-project "status" wire-key gap analogous to
//       HappyPathResponseContainsStatus (which closes the by-id gap).
//
//   * TestPublishPipeline_DoublePublishProducesSingleYouTubeCall
//       Idempotency: a second POST on a session already in
//       'published' state must NOT call PublishThumbnail again.
//       Counters publishThumbnailFn invocations across two POSTs;
//       asserts the counter == 1.
//
//   * TestPublishPipeline_ThumbnailBytesSentByteIdentical_JPEG_PNG
//       SHA-256 of the bytes served by the storage HTTP server ==
//       SHA-256 of the bytes the YouTube mock received. Two flavours
//       (image/jpeg and image/png). Catches any silent re-encode /
//       format-mismatch / proxy mutation in the storage→YouTube hop.
//
//   * TestPublishPipeline_AssetLargerThan2MB_Rejected
//       The downloadThumbnailBytes helper caps at 2 MB and the
//       orchestrator maps a download error to 500; this test makes
//       the storage server send a Content-Length > 2 MB body so
//       the orchestrator refuses before any YouTube call. The mock
//       publishThumbnailFn MUST NOT be called.
//
//   * TestPublishPipeline_ThumbnailSetError_LeadsToFailed
//       publishThumbnailFn returns an error. Orchestrator stamps
//       status='failed' on the row (via Update) with last_error
//       carrying the YouTube error message, then returns 502 to
//       the operator. Hook on the mockYouTubeVideoEditStore.update
//       callback to capture the failed row.
//
//   * TestPublishPipeline_LocalizationsError_IsRetriable
//       First POST: upsertLocalizationsFn returns an error → 502 +
//       status='failed' stamped. Second POST: same session, mock
//       upsertLocalizationsFn now returns nil → 200 + status=
//       'published'. Demonstrates the failure-mode-is-retriable
//       contract for stage 9 of the orchestrator
//       (sortedTranslationKeys loop).
//
// All tests reuse the commonPublishBackbone helper from
// youtube_publish_actual_privacy_test.go (workspace + auth-token
// vault + media store + storage provider wiring).
//
// File layout (split by sub-flow):
//   - youtube_publish_pipeline_test.go            — package doc + status test
//   - youtube_publish_pipeline_shared_test.go     — byProjectPublishPayload,
//     apiError, backbonePlusCustomMocks (shared fixtures)
//   - youtube_publish_pipeline_thumbnail_test.go  — byte-identity +
//     thumbnail-set-error tests, newMimeTestHarness, sha256Hex
//   - youtube_publish_pipeline_assetsize_test.go  — 2 MB cap rejection
//   - youtube_publish_pipeline_localization_test.go — retriable
//     localizations contract
//   - youtube_publish_pipeline_cas_test.go        — idempotency/dedup +
//     PATCH by-project CAS guards, patchByProjectPayload, newPatchRouter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestPublishPipeline_StatusPresentInByProjectResponse closes the
// by-project gap analogue to HappyPathResponseContainsStatus (which
// covers by-id). InstaEditor reads publishResult.status and
// broadcasts it on BroadcastChannel('instaedit-publish') — a missing
// JSON key on the by-project pathway silently breaks the live card
// update for every session published via that endpoint.
//
// Two assertion layers mirror HappyPathResponseContainsStatus:
//  1. Decoded struct: publishYouTubeEditorSessionResponse.Status must
//     equal "published" — catches handler-vs-DTO drift.
//  2. Raw wire body: the response bytes must contain the literal
//     substring `"status":"published"` — catches JSON-tag drift on
//     the struct.
func TestPublishPipeline_StatusPresentInByProjectResponse(t *testing.T) {
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		getVideoFn: func(ctx context.Context, _, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{
				ID:           videoID,
				ChannelID:    "UC123",
				UploadStatus: "processed",
				Privacy:      "public",
			}, nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{
		markPublishedWithActualPrivacyFn: func(_ context.Context, id string, actualPrivacy string, syncStatus string) (*models.YouTubeVideoEdit, error) {
			row := &models.YouTubeVideoEdit{
				ID:                id,
				WorkspaceID:       7,
				YouTubeVideoID:    "ytvideo123",
				VeloxProjectID:    "ve-project-123",
				Status:            "published",
				DesiredPrivacy:    "public",
				ActualPrivacy:     &actualPrivacy,
				YouTubeSyncStatus: &syncStatus,
			}
			return row, nil
		},
	}
	router, _ := commonPublishBackbone(t, youTubeSvc, editStore)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(byProjectPublishPayload(t, nil)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	router.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	// Layer 1: decoded struct catches handler-vs-DTO drift.
	var resp publishYouTubeEditorSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "published" {
		t.Fatalf("expected response.status=published, got %q (full resp: %+v)", resp.Status, resp)
	}

	// Layer 2: wire-key substring catches JSON-tag drift on the struct.
	if !strings.Contains(w.Body.String(), `"status":"published"`) {
		t.Fatalf("expected raw wire body to contain literal `\"status\":\"published\"`, got %s", w.Body.String())
	}
}
