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

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// byProjectPublishPayload is the minimal body accepted by
// POST /api/v1/youtube/editor-sessions/by-project/{velox_project_id}/publish.
// Kept tiny so the structural assertions stay focused on what each
// test cares about (status field, idempotency, byte identity, etc).
func byProjectPublishPayload(t *testing.T, additional map[string]any) []byte {
	t.Helper()
	base := map[string]any{"privacy_status": "public"}
	for k, v := range additional {
		base[k] = v
	}
	body, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal publish payload: %v", err)
	}
	return body
}

// sha256Hex returns the canonical lowercase-hex SHA-256 of b. Used by
// the byte-identical tests so the storage→YouTube hop can be asserted
// at the cryptographic-hash layer (any single-byte mutation breaks
// the equality).
func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// TestPublishPipeline_StatusPresentInByProjectResponse closes the
// by-project gap analogue to HappyPathResponseContainsStatus (which
// covers by-id). The dark editor reads publishResult.status and
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

// TestPublishPipeline_DoublePublishProducesSingleYouTubeCall asserts
// the orchestrator's idempotency: a second POST on a session already
// in 'published' state must NOT call PublishThumbnail again. Any
// operator retry, network-replay, or double-click on Pubblica must
// bottom out at zero additional YouTube API calls.
//
// Counter is captured across both POSTs (sync/atomic.Int32 because
// the orchestrator is single-goroutine but the assertion reads after
// each POST completes, so we want a load-acquire not a stale Read).
func TestPublishPipeline_DoublePublishProducesSingleYouTubeCall(t *testing.T) {
	var publishCount atomic.Int32
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(_ context.Context, _, videoID string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			publishCount.Add(1)
			return "https://www.youtube.com/watch?v=" + videoID, nil
		},
		getVideoFn: func(_ context.Context, _, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{
				ID:           videoID,
				ChannelID:    "UC123",
				UploadStatus: "processed",
				Privacy:      "public",
			}, nil
		},
	}
	router, _ := commonPublishBackbone(t, youTubeSvc, &mockYouTubeVideoEditStore{})

	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(byProjectPublishPayload(t, nil)))
		req.Header.Set("Content-Type", "application/json")
		withBearerJWT(t, req, 1)
		w := httptest.NewRecorder()
		router.Setup().ServeHTTP(w, req)
		return w
	}

	first := post()
	if first.Code != http.StatusOK {
		t.Fatalf("first publish expected 200, got %d (body=%s)", first.Code, first.Body.String())
	}
	if got := publishCount.Load(); got != 1 {
		t.Fatalf("first publish: expected publishCount=1, got %d", got)
	}

	// Second POST: findFn (commonPublishBackbone) returns the row
	// mutated by the first POST's publish-flow; status is now
	// 'published' (the CAS fallback in mockYouTubeVideoEditStore
	// stamps it after MarkPublishedWithActualPrivacy). The
	// idempotency branch in executePublishYouTubeEditorSession
	// returns BEFORE any MarkPublishing / PublishThumbnail call.
	second := post()
	if second.Code != http.StatusOK {
		t.Fatalf("second publish expected 200 (idempotency), got %d (body=%s)", second.Code, second.Body.String())
	}
	if got := publishCount.Load(); got != 1 {
		t.Fatalf("second publish: expected publishCount STAY at 1, got %d (publish was re-invoked)", got)
	}

	// Spot-check the wire shape on the replay: status MUST equal
	// 'published' so the SPA's BroadcastChannel contract stays
	// green on the replay.
	var resp publishYouTubeEditorSessionResponse
	if err := json.Unmarshal(second.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode replay response: %v", err)
	}
	if resp.Status != "published" {
		t.Fatalf("replay response.status expected published, got %q", resp.Status)
	}
}

// TestPublishPipeline_ThumbnailBytesSentByteIdentical_JPEG_PNG walks
// the storage→YouTube hop at the cryptographic-hash layer. The httptest
// server returns a deterministic JPEG (resp. PNG) blob; the orchestrator
// fetches it via GetObject + downloadThumbnailBytes; the orchestrator's
// PublishThumbnail call hands the same bytes to the YouTube mock.
//
// SHA-256(serverBytes) == SHA-256(capturedBytes) guarantees no silent
// re-encode, format-mismatch, or buffer-copy mutation sneaks in
// anywhere between the canonical JPEG/PNG the operator uploaded and
// the bytes YouTube receives as thumbnails.set.
//
// Parametrised on the MIME type — a future WebP/HIFIC branch should
// extend the slice, not duplicate the test body.
func TestPublishPipeline_ThumbnailBytesSentByteIdentical_JPEG_PNG(t *testing.T) {
	for _, mime := range []string{"image/jpeg", "image/png"} {
		mime := mime
		t.Run(mime, func(t *testing.T) {
			jpegBytes := []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00test-jpeg-bitmap-data-identity-canon")
			pngBytes := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde\x00\x00\x00\x0cIDATx\x9cc\xf8\xff\xff?\x00\x05\xfe\x02\xfe\xdc\xcc\x59\xe7\x00\x00\x00\x00IEND\xaeB`\x82")
			body := jpegBytes
			if mime == "image/png" {
				body = pngBytes
			}
			serverHash := sha256Hex(body)

			var captured []byte
			youTubeSvc := &mockYouTubeOAuthServiceForEditor{
				publishThumbnailFn: func(_ context.Context, _, videoID string, data []byte, gotMime, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
					if gotMime != mime {
						t.Errorf("expected MimeType=%s, got %q", mime, gotMime)
					}
					captured = data
					return "https://www.youtube.com/watch?v=" + videoID, nil
				},
				getVideoFn: func(_ context.Context, _, videoID string) (*models.YouTubeVideoDetails, error) {
					return &models.YouTubeVideoDetails{
						ID:           videoID,
						ChannelID:    "UC123",
						UploadStatus: "processed",
						Privacy:      "public",
					}, nil
				},
			}

			// The asset's ContentType + SizeBytes must match the test
			// mime so executePublishYouTubeEditorSession's content-type
			// guard accepts it.
			// newMimeTestHarness registers t.Cleanup(srv.Close) itself,
			// so callers do NOT defer Close() (a second close on the
			// same httptest.Server can panic with concurrent shutdown).
			mediaStore, storage, server := newMimeTestHarness(t, body, mime, int64(len(body)))
			storage.assetURLFn = func(_ string) string { return server.URL }

			router := mustNewRouterWithDefaults(
				services.NewCapabilityRouter(),
				&mockUserStore{
					findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
						if id == 42 {
							return &models.PlatformAccount{
								ID:             42,
								UserID:         1,
								Platform:       models.PlatformYouTube,
								PlatformUserID: "UC123",
								Username:       "testchannel",
								Status:         models.AccountStatusActive,
							}, nil
						}
						return nil, nil
					},
				},
				auth.NewManager(testJWTSecret, 24),
				"https://app.instaedit.org",
				nil,
				WithWorkspaceStore(&mockWorkspaceStore{
					findByIDFn: func(id int64) (*models.Workspace, error) {
						if id == 7 {
							return &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}, nil
						}
						return nil, nil
					},
				}),
				WithYouTubeVideoEditStore(&mockYouTubeVideoEditStore{
					findFn: func(_ context.Context, id string) (*models.YouTubeVideoEdit, error) {
						if id == "session-123" {
							media := "asset-uuid-123"
							return &models.YouTubeVideoEdit{
								ID:                id,
								WorkspaceID:       7,
								PlatformAccountID: 42,
								YouTubeVideoID:    "ytvideo123",
								VeloxProjectID:    "ve-project-123",
								ThumbnailMediaID:  &media,
								DesiredPrivacy:    "public",
								Status:            "editing",
							}, nil
						}
						return nil, nil
					},
					findByProjectFn: func(_ context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
						if projectID == "ve-project-123" {
							media := "asset-uuid-123"
							return &models.YouTubeVideoEdit{
								ID:                "session-123",
								WorkspaceID:       7,
								PlatformAccountID: 42,
								YouTubeVideoID:    "ytvideo123",
								VeloxProjectID:    "ve-project-123",
								ThumbnailMediaID:  &media,
								DesiredPrivacy:    "public",
								Status:            "editing",
							}, nil
						}
						return nil, nil
					},
				}),
				WithMediaStore(mediaStore),
				WithStorageProvider(storage),
				WithYouTubeService(youTubeSvc),
				WithCredentialVault(&mockCredentialVault{
					getFn: func(_ context.Context, id int64, _ string) (*models.OAuthToken, error) {
						if id == 42 {
							return &models.OAuthToken{AccessToken: "valid-token"}, nil
						}
						return nil, nil
					},
				}),
			)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(byProjectPublishPayload(t, nil)))
			req.Header.Set("Content-Type", "application/json")
			withBearerJWT(t, req, 1)
			w := httptest.NewRecorder()
			router.Setup().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
			}
			if len(captured) == 0 {
				t.Fatalf("publishThumbnailFn was not invoked with mime=%s", mime)
			}
			capturedHash := sha256Hex(captured)
			if capturedHash != serverHash {
				t.Fatalf("byte-identity mismatch for %s: serverHash=%s capturedHash=%s (bytes differ in transit)", mime, serverHash, capturedHash)
			}
		})
	}
}

// newMimeTestHarness builds the asset download server + storage + media
// trio for the byte-identical tests. The server returns the supplied
// bytes (deterministic per-test fixture) and the media store advertises
// matching content-type / size-bytes so the orchestrator's guards accept
// the asset before the download hop.
func newMimeTestHarness(t *testing.T, body []byte, mime string, sizeBytes int64) (*mockMediaStore, *mockStorageProvider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Content-Length", strconv.FormatInt(sizeBytes, 10))
		w.Write(body)
	}))
	// Register cleanup so the httptest server tears down with the
	// subtest scope. Callers do NOT need to defer Close() themselves;
	// the harness owns the lifetime.
	t.Cleanup(srv.Close)
	media := newMockMediaStore()
	media.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:          "asset-uuid-123",
		UserID:      1,
		UploadKey:   "uploads/1/thumb." + strings.TrimPrefix(strings.TrimPrefix(mime, "image/"), "."),
		ContentType: mime,
		SizeBytes:   sizeBytes,
		Status:      models.MediaAssetStatusReady,
	}
	return media, newMockStorageProvider(), srv
}

// TestPublishPipeline_AssetLargerThan2MB_Rejected asserts that the
// orchestrator refuses a publish on an asset whose bytes exceed the
// 2 MB cap the downloadThumbnailBytes helper enforces. The orchestrator
// MUST NOT reach publishThumbnailFn for an oversize asset.
//
// We avoid stuffing 3 MB of zeros into a test binary: a synthetic
// Content-Length header well above 2 MB triggers the helper's
// pre-read size guard ("thumbnail download exceeded max size"). The
// httptest server never needs to stream the actual bytes.
func TestPublishPipeline_AssetLargerThan2MB_Rejected(t *testing.T) {
	const oversize int64 = 3 * 1024 * 1024

	var publishCalled bool
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(_ context.Context, _, videoID string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			publishCalled = true
			return "https://www.youtube.com/watch?v=" + videoID, nil
		},
		getVideoFn: func(_ context.Context, _, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{
				ID:           videoID,
				ChannelID:    "UC123",
				UploadStatus: "processed",
				Privacy:      "public",
			}, nil
		},
	}

	media := newMockMediaStore()
	media.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:          "asset-uuid-123",
		UserID:      1,
		UploadKey:   "uploads/1/thumb.jpg",
		ContentType: "image/jpeg",
		SizeBytes:   oversize,
		Status:      models.MediaAssetStatusReady,
	}

	storage := newMockStorageProvider()
	// (assetURLFn set below, after the oversize-rejection server
	// is constructed; the helper rejects the download pre-fetch so
	// the URL is exercised but never actually FETCHED.)
	_ = storage

	router := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{
			findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
				if id == 42 {
					return &models.PlatformAccount{ID: 42, UserID: 1, Platform: models.PlatformYouTube, PlatformUserID: "UC123", Username: "testchannel", Status: models.AccountStatusActive}, nil
				}
				return nil, nil
			},
		},
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				if id == 7 {
					return &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}, nil
				}
				return nil, nil
			},
		}),
		WithYouTubeVideoEditStore(&mockYouTubeVideoEditStore{
			findByProjectFn: func(_ context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
				if projectID == "ve-project-123" {
					media := "asset-uuid-123"
					return &models.YouTubeVideoEdit{
						ID:                "session-123",
						WorkspaceID:       7,
						PlatformAccountID: 42,
						YouTubeVideoID:    "ytvideo123",
						VeloxProjectID:    "ve-project-123",
						ThumbnailMediaID:  &media,
						DesiredPrivacy:    "public",
						Status:            "editing",
					}, nil
				}
				return nil, nil
			},
		}),
		WithMediaStore(media),
		WithStorageProvider(storage),
		WithYouTubeService(youTubeSvc),
		WithCredentialVault(&mockCredentialVault{
			getFn: func(_ context.Context, id int64, _ string) (*models.OAuthToken, error) {
				if id == 42 {
					// Asset download would have already been rejected before
					// the token is consulted, but the vault is wired so the
					// router + orchestrator don't 503 on missing-dep.
					return &models.OAuthToken{AccessToken: "valid-token"}, nil
				}
				return nil, nil
			},
		}),
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.FormatInt(oversize, 10))
		// The helper rejects before reading the body -> cheaper to
		// just send an empty body to satisfy net/http mechanics.
	}))
	defer srv.Close()
	storage.assetURLFn = func(_ string) string { return srv.URL }

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(byProjectPublishPayload(t, nil)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	router.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on oversize asset (cap=2MB), got %d (body=%s)", w.Code, w.Body.String())
	}
	if publishCalled {
		t.Fatalf("publishThumbnailFn MUST NOT be called when the asset exceeds the 2 MB cap")
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "exceeded max size") {
		t.Fatalf("expected body to mention the size-cap (`exceeded max size`), got %s", w.Body.String())
	}
}

// TestPublishPipeline_ThumbnailSetError_LeadsToFailed asserts the
// orchestrator's failure path on publishThumbnailFn errors:
//   - response is 502 Bad Gateway (YouTube's transient failure
//     surfaces to the operator),
//   - the row's status is stamped 'failed' via Update,
//   - the row's last_error carries the YouTube error string so the
//     dashboard can show "Perché ha fallito?".
//
// Marks a regression guard for "publish silently 502'd but row stays
// 'publishing' forever" — a future refactor of the failure branch
// must write back status='failed' before returning 502.
func TestPublishPipeline_ThumbnailSetError_LeadsToFailed(t *testing.T) {
	var capturedStatus string
	var capturedLastError string
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(_ context.Context, _, _ string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			return "", &apiError{"thumbnails.set 503 backend temporarily unavailable"}
		},
		getVideoFn: func(_ context.Context, _, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{
				ID:           videoID,
				ChannelID:    "UC123",
				UploadStatus: "processed",
				Privacy:      "public",
			}, nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{
		update: func(_ context.Context, edit *models.YouTubeVideoEdit) error {
			capturedStatus = edit.Status
			capturedLastError = edit.LastError
			return nil
		},
	}

	// commonPublishBackbone's defaults would clobber our publish fn;
	// we hand-construct so the overrides win.
	backbone := backbonePlusCustomMocks(t, youTubeSvc, editStore)
	router := backbone

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(byProjectPublishPayload(t, nil)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	router.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 on thumbnail.set failure, got %d (body=%s)", w.Code, w.Body.String())
	}
	if capturedStatus != "failed" {
		t.Fatalf("expected row.status=after-failure='failed', got %q", capturedStatus)
	}
	if !strings.Contains(capturedLastError, "thumbnails.set 503") {
		t.Fatalf("expected last_error to carry the YouTube error, got %q", capturedLastError)
	}
}

// apiError is a minimal error string carrier so the
// TestPublishPipeline_ThumbnailSetError_LeadsToFailed test injects
// a recognisable YouTube error message into the orchestrator's
// last_error column. type name `apiError` doesn't collide with
// anything in pkg/api.
type apiError struct{ msg string }

func (e *apiError) Error() string { return e.msg }

// backbonePlusCustomMocks mirrors commonPublishBackbone's wiring but
// leaves the publishThumbnailFn / update callback slots untouched
// (commonPublishBackbone's defensive `if youTubeSvc.publishThumbnailFn
// == nil` only fills the default; the editStore.update override
// fields are NOT auto-filled, but constructing inline here keeps the
// helper's responsibilities localised to the shared-feel tests).
func backbonePlusCustomMocks(t *testing.T, youTubeSvc *mockYouTubeOAuthServiceForEditor, editStore *mockYouTubeVideoEditStore) *Router {
	t.Helper()
	account := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	editStore.findFn = func(_ context.Context, id string) (*models.YouTubeVideoEdit, error) {
		if id == "session-123" {
			media := "asset-uuid-123"
			return &models.YouTubeVideoEdit{ID: id, WorkspaceID: workspace.ID, PlatformAccountID: account.ID, YouTubeVideoID: "ytvideo123", VeloxProjectID: "ve-project-123", ThumbnailMediaID: &media, DesiredPrivacy: "public", Status: "editing"}, nil
		}
		return nil, nil
	}
	editStore.findByProjectFn = func(_ context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
		if projectID == "ve-project-123" {
			media := "asset-uuid-123"
			return &models.YouTubeVideoEdit{ID: "session-123", WorkspaceID: workspace.ID, PlatformAccountID: account.ID, YouTubeVideoID: "ytvideo123", VeloxProjectID: "ve-project-123", ThumbnailMediaID: &media, DesiredPrivacy: "public", Status: "editing"}, nil
		}
		return nil, nil
	}
	if youTubeSvc.publishThumbnailFn == nil {
		youTubeSvc.publishThumbnailFn = func(_ context.Context, _, videoID string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			return "https://www.youtube.com/watch?v=" + videoID, nil
		}
	}

	thumbnailBytes := []byte("fake-thumbnail-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumbnailBytes)
	}))
	t.Cleanup(srv.Close)

	media := newMockMediaStore()
	media.assets["asset-uuid-123"] = &models.MediaAsset{
		ID: "asset-uuid-123", UserID: 1, UploadKey: "uploads/1/thumb.jpg", ContentType: "image/jpeg", SizeBytes: int64(len(thumbnailBytes)), Status: models.MediaAssetStatusReady,
	}
	storage := newMockStorageProvider()
	storage.assetURLFn = func(_ string) string { return srv.URL }

	return mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{
			findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
				if id == account.ID {
					return account, nil
				}
				return nil, nil
			},
		},
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(&mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) {
			if id == workspace.ID {
				return workspace, nil
			}
			return nil, nil
		}}),
		WithYouTubeVideoEditStore(editStore),
		WithMediaStore(media),
		WithStorageProvider(storage),
		WithYouTubeService(youTubeSvc),
		WithCredentialVault(&mockCredentialVault{
			getFn: func(_ context.Context, id int64, _ string) (*models.OAuthToken, error) {
				if id == account.ID {
					return &models.OAuthToken{AccessToken: "valid-token"}, nil
				}
				return nil, nil
			},
		}),
	)
}

// TestPublishPipeline_LocalizationsError_IsRetriable locks the
// "stage 9 / sortedTranslationKeys loop / failure mode is RETRIABLE"
// contract.
//
// First POST: upsertLocalizationsFn returns an error → 502 + the
// row stamped 'failed' (via Update). MarkPublishedWithActualPrivacy
// is NOT called yet (failure short-circuits before the final CAS).
//
// Second POST: same session, status still 'editing'/'failed' →
// orchestrator enters the loop again. The mock now returns nil →
// 200 + MarkPublishedWithActualPrivacy called → row status
// 'published'.
//
// Whichever the publish-and-status sequence, both POSTs end with
// 502 then 200 — the failure was recoverable on retry, not a
// permanent lockdown.
func TestPublishPipeline_LocalizationsError_IsRetriable(t *testing.T) {
	var upsertCalls atomic.Int32
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(_ context.Context, _, videoID string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			return "https://www.youtube.com/watch?v=" + videoID, nil
		},
		getVideoFn: func(_ context.Context, _, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{
				ID:           videoID,
				ChannelID:    "UC123",
				UploadStatus: "processed",
				Privacy:      "public",
			}, nil
		},
		upsertLocalizationsFn: func(_ context.Context, _, _, lang string, _ models.YouTubeTranslation) error {
			upsertCalls.Add(1)
			if upsertCalls.Load() == 1 {
				return &apiError{msg: "videos.update(part=localizations) 503 backend temporarily unavailable[" + lang + "]"}
			}
			return nil
		},
	}

	// Stateful mockYouTubeVideoEditStore.findFn: first POST sees
	// status='editing'; after the first publish, findFn flips to
	// status='failed' (mirroring what the orchestrator does on
	// localizations failure). The store's default MarkPublishing
	// fallback increments markPublishingAttempts which would CAS-
	// loss on call #2 -- so we inject markPublishingFn that
	// always succeeds and rewrites the status to whatever the
	// current findFn says.
	statefulRow := func(status string) *models.YouTubeVideoEdit {
		media := "asset-uuid-123"
		return &models.YouTubeVideoEdit{
			ID: "session-123", WorkspaceID: 7, PlatformAccountID: 42,
			YouTubeVideoID: "ytvideo123", VeloxProjectID: "ve-project-123",
			ThumbnailMediaID: &media, DesiredPrivacy: "public", Status: status,
		}
	}
	var currentStatus atomic.Value
	currentStatus.Store("editing")
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(_ context.Context, id string) (*models.YouTubeVideoEdit, error) {
			if id == "session-123" {
				return statefulRow(currentStatus.Load().(string)), nil
			}
			return nil, nil
		},
		findByProjectFn: func(_ context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
			if projectID == "ve-project-123" {
				return statefulRow(currentStatus.Load().(string)), nil
			}
			return nil, nil
		},
		markPublishingFn: func(_ context.Context, _ string, desiredPrivacy string, publishAt *time.Time, _ time.Duration) (*models.YouTubeVideoEdit, error) {
			row := statefulRow("publishing")
			row.DesiredPrivacy = desiredPrivacy
			row.PublishAt = publishAt
			row.UpdatedAt = time.Now().UTC()
			// MarkPublishing writes 'publishing' to the row.
			currentStatus.Store("publishing")
			return row, nil
		},
		markPublishedWithActualPrivacyFn: func(_ context.Context, id string, actualPrivacy string, syncStatus string) (*models.YouTubeVideoEdit, error) {
			row := statefulRow("published")
			row.ActualPrivacy = &actualPrivacy
			row.YouTubeSyncStatus = &syncStatus
			row.UpdatedAt = time.Now().UTC()
			currentStatus.Store("published")
			return row, nil
		},
		update: func(_ context.Context, edit *models.YouTubeVideoEdit) error {
			// The orchestrator's failure branch calls Update with
			// edit.Status='failed' BEFORE the 502 returns. Mirror
			// that into the findFn state.
			if edit.Status == "failed" {
				currentStatus.Store("failed")
			}
			return nil
		},
	}
	router := backbonePlusCustomMocks(t, youTubeSvc, editStore)

	body := byProjectPublishPayload(t, map[string]any{
		"translations": map[string]models.YouTubeTranslation{
			"it": {Title: "Titolo IT", Description: "Descrizione IT"},
		},
		"default_language": "en",
	})

	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		withBearerJWT(t, req, 1)
		w := httptest.NewRecorder()
		router.Setup().ServeHTTP(w, req)
		return w
	}

	first := post()
	if first.Code != http.StatusBadGateway {
		t.Fatalf("first publish expected 502 (localizations failure), got %d (body=%s)", first.Code, first.Body.String())
	}
	if !strings.Contains(strings.ToLower(first.Body.String()), "localizations") {
		t.Fatalf("first publish body expected to mention localizations, got %s", first.Body.String())
	}
	if currentStatus.Load() != "failed" {
		t.Fatalf("expected findFn state to flip to 'failed' after the first failure, got %q", currentStatus.Load())
	}

	// Second POST: status='failed' is not in the in-flight guard
	// (only 'publishing' is), so the orchestrator proceeds. The
	// upsertLocalizationsFn now returns nil → loop completes →
	// MarkPublishedWithActualPrivacy (mock fallback) flips
	// simulatedStatus='published'.
	second := post()
	if second.Code != http.StatusOK {
		t.Fatalf("second publish expected 200 (retry succeeded), got %d (body=%s)", second.Code, second.Body.String())
	}
	if got := upsertCalls.Load(); got != 2 {
		t.Fatalf("expected upsertLocalizationsFn called 2x across the two POSTs (1 fail + 1 success), got %d", got)
	}
	var resp publishYouTubeEditorSessionResponse
	if err := json.Unmarshal(second.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if resp.Status != "published" {
		t.Fatalf("second publish expected response.status=published, got %q", resp.Status)
	}
}
