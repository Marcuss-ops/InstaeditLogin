package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

func nvidiaMetadataPublishPayload(t *testing.T) []byte {
	t.Helper()
	payload := map[string]any{
		"title":                  "Come automatizzare la pubblicazione YouTube nel 2026",
		"description":            "In questo video vediamo come creare, modificare e pubblicare contenuti YouTube attraverso un flusso automatizzato con InstaEdit e NVIDIA AI.",
		"privacy_status":         "unlisted",
		"tags":                   []string{"youtube automation", "video editing", "instaedit", "content creation"},
		"default_language":       "it",
		"default_audio_language": "it",
		"translations": map[string]models.YouTubeTranslation{
			"en":    {Title: "How to Automate YouTube Publishing in 2026", Description: "This video explains how to create, edit and publish YouTube content through an automated workflow with InstaEdit and NVIDIA AI."},
			"es":    {Title: "Cómo automatizar la publicación en YouTube en 2026", Description: "Este video explica cómo crear, editar y publicar contenido de YouTube mediante un flujo automatizado con InstaEdit y NVIDIA AI."},
			"pt-BR": {Title: "Como automatizar publicações no YouTube em 2026", Description: "Este vídeo mostra como criar, editar e publicar conteúdo no YouTube por meio de um fluxo automatizado com InstaEdit e NVIDIA AI."},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal nvidia metadata publish payload: %v", err)
	}
	return b
}

// TestNVIDIAMetadataPublish_FullFlow_YouTubeMetadataPreserved is the
// canonical end-to-end test for the NVIDIA metadata publish flow.
//
// It POSTs the full contract fixture as the publish payload against
// the by-project endpoint and asserts that EVERY metadata field
// reaches the YouTube mock intact — title, description, tags,
// default_language, default_audio_language, translations (en, es,
// pt-BR), privacy_status.
//
// The test uses the same commonPublishBackbone as every other
// pipeline test so a future refactor of the router wiring cannot
// silently break the metadata contract.

func customBackboneForNegative(t *testing.T, youTubeSvc *mockYouTubeOAuthServiceForEditor, editStore *mockYouTubeVideoEditStore) *Router {
	t.Helper()
	account := &models.PlatformAccount{
		ID: 42, UserID: 1, Platform: models.PlatformYouTube,
		PlatformUserID: "UC123", Username: "testchannel", Status: models.AccountStatusActive,
	}
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}

	media := newMockMediaStore()
	media.assets["asset-uuid-123"] = &models.MediaAsset{
		ID: "asset-uuid-123", UserID: 1, UploadKey: "uploads/1/thumb.jpg",
		ContentType: "image/jpeg", SizeBytes: 1024, Status: models.MediaAssetStatusReady,
	}

	thumbnailBytes := []byte("fake-thumbnail-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumbnailBytes)
	}))
	t.Cleanup(srv.Close)

	storage := newMockStorageProvider()
	storage.assetURLFn = func(_ string) string { return srv.URL }

	if youTubeSvc.publishThumbnailFn == nil {
		youTubeSvc.publishThumbnailFn = func(_ context.Context, _, videoID string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			return "https://www.youtube.com/watch?v=" + videoID, nil
		}
	}

	// Set defaults for find fns if not provided by the test.
	if editStore.findFn == nil {
		editStore.findFn = func(_ context.Context, id string) (*models.YouTubeVideoEdit, error) {
			if id == "session-123" {
				media := "asset-uuid-123"
				return &models.YouTubeVideoEdit{
					ID: id, WorkspaceID: 7, PlatformAccountID: 42,
					YouTubeVideoID: "ytvideo123", VeloxProjectID: "ve-project-123",
					ThumbnailMediaID: &media, DesiredPrivacy: "unlisted", Status: "editing",
				}, nil
			}
			return nil, nil
		}
	}
	if editStore.findByProjectFn == nil {
		editStore.findByProjectFn = func(_ context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
			if projectID == "ve-project-123" {
				media := "asset-uuid-123"
				return &models.YouTubeVideoEdit{
					ID: "session-123", WorkspaceID: 7, PlatformAccountID: 42,
					YouTubeVideoID: "ytvideo123", VeloxProjectID: "ve-project-123",
					ThumbnailMediaID: &media, DesiredPrivacy: "unlisted", Status: "editing",
				}, nil
			}
			return nil, nil
		}
	}

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
		WithWorkspaceStore(&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				if id == workspace.ID {
					return workspace, nil
				}
				return nil, nil
			},
		}),
		WithYouTubeVideoEditStore(editStore),
		WithMediaStore(media),
		WithStorageProvider(storage),
		WithYouTubeService(youTubeSvc),
		WithCredentialVault(&mockCredentialVault{
			getFn: func(_ context.Context, id int64, _ string) (*models.OAuthToken, error) {
				if id == account.ID {
					return &models.OAuthToken{AccessToken: "valid-token"}, nil
				}
				return nil, errors.New("token not found")
			},
		}),
	)
}

// TestNVIDIAMetadataPublish_Negative_SimultaneousPublishReturns409 asserts
// that two nearly-simultaneous publish requests result in one success (200)
// and one conflict (409) — the CAS (Compare-And-Swap) on MarkPublishing
// prevents double YouTube calls.
