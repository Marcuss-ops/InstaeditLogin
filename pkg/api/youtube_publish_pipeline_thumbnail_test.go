package api

// Thumbnail sub-flow tests — byte-identity across the storage→YouTube
// hop and the publishThumbnailFn failure path. Extracted from
// youtube_publish_pipeline_test.go by sub-flow.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// sha256Hex returns the canonical lowercase-hex SHA-256 of b. Used by
// the byte-identical tests so the storage→YouTube hop can be asserted
// at the cryptographic-hash layer (any single-byte mutation breaks
// the equality).
func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
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
