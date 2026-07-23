package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// tiktokTestCfg returns a minimal config for TikTok OAuth tests.
func tiktokTestCfg() *config.Config {
	return &config.Config{
		Auth: config.AuthConfig{
			TikTokClientID:     "test-tiktok-client-key",
			TikTokClientSecret: "test-tiktok-client-secret-32chars",
			TikTokRedirectURI:  "http://localhost:8080/tiktok/callback",
		},
	}
}

// newTestTikTokService creates a TikTokOAuthService pointed at the httptest server.
func newTestTikTokService(srv *httptest.Server) *TikTokOAuthService {
	cfg := tiktokTestCfg()
	return &TikTokOAuthService{
		cfg:        cfg,
		httpClient: testClient(srv),
	}
}

func TestTikTok_ExchangeCodeForToken(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		body        string
		wantErr     bool
		assertToken func(t *testing.T, td *tiktokTokenResponse)
	}{
		{
			name:    "Success",
			status:  http.StatusOK,
			body:    `{"access_token":"tiktok-access-token-abc","token_type":"bearer","expires_in":86400,"scope":"user.info.basic,video.publish","refresh_token":"tiktok-refresh-token-xyz"}`,
			wantErr: false,
			assertToken: func(t *testing.T, td *tiktokTokenResponse) {
				if td.AccessToken != "tiktok-access-token-abc" {
					t.Errorf("access_token: want %q, got %q", "tiktok-access-token-abc", td.AccessToken)
				}
				if td.RefreshToken != "tiktok-refresh-token-xyz" {
					t.Errorf("refresh_token: want %q, got %q", "tiktok-refresh-token-xyz", td.RefreshToken)
				}
				if td.ExpiresIn != 86400 {
					t.Errorf("expires_in: want 86400, got %d", td.ExpiresIn)
				}
			},
		},
		{
			name:    "ErrorResponse",
			status:  http.StatusBadRequest,
			body:    `{"error":"invalid_grant","error_description":"Invalid authorization code"}`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/v2/oauth/token/", func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("expected POST, got %s", r.Method)
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			svc := newTestTikTokService(srv)
			resp, err := svc.exchangeCodeForToken(context.Background(), "code")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("exchangeCodeForToken: %v", err)
			}
			if tc.assertToken != nil {
				tc.assertToken(t, resp)
			}
		})
	}
}

func TestTikTok_GetUserInfo(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		body         string
		accessToken  string
		wantErr      bool
		assertResult func(t *testing.T, profile *models.PlatformProfile)
	}{
		{
			name:        "Success",
			status:      http.StatusOK,
			body:        `{"data":{"user":{"open_id":"tiktok-open-id-456","display_name":"TikTok Creator"}}}`,
			accessToken: "tiktok-access-token",
			wantErr:     false,
			assertResult: func(t *testing.T, profile *models.PlatformProfile) {
				if profile.PlatformUserID != "tiktok-open-id-456" {
					t.Errorf("PlatformUserID: want %q, got %q", "tiktok-open-id-456", profile.PlatformUserID)
				}
				if profile.Username != "TikTok Creator" {
					t.Errorf("Username: want %q, got %q", "TikTok Creator", profile.Username)
				}
				if profile.Name != "TikTok Creator" {
					t.Errorf("Name: want %q, got %q", "TikTok Creator", profile.Name)
				}
			},
		},
		{
			name:        "ErrorResponse",
			status:      http.StatusUnauthorized,
			body:        `{"error":"invalid_token"}`,
			accessToken: "bad-token",
			wantErr:     true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/v2/user/info/", func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("expected GET, got %s", r.Method)
				}
				wantAuth := "Bearer " + tc.accessToken
				if auth := r.Header.Get("Authorization"); auth != wantAuth {
					t.Errorf("Authorization: want %q, got %q", wantAuth, auth)
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			svc := newTestTikTokService(srv)
			profile, err := svc.getUserInfo(context.Background(), tc.accessToken)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("getUserInfo: %v", err)
			}
			if tc.assertResult != nil {
				tc.assertResult(t, profile)
			}
		})
	}
}

func TestTikTok_HandleCallback(t *testing.T) {
	type tokenHandler func(w http.ResponseWriter, r *http.Request)
	type userHandler func(w http.ResponseWriter, r *http.Request)

	cases := []struct {
		name    string
		tokenH  tokenHandler
		userH   userHandler
		wantErr bool
		assert  func(t *testing.T, profile *models.PlatformProfile, tokenData *models.TokenData)
	}{
		{
			name: "Success",
			tokenH: func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"access_token":  "callback-tiktok-token",
					"token_type":    "bearer",
					"expires_in":    86400,
					"scope":         "user.info.basic,video.publish",
					"refresh_token": "callback-tiktok-refresh",
				})
			},
			userH: func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{
						"user": map[string]string{
							"open_id":      "tiktok-callback-id",
							"display_name": "Callback TikToker",
						},
					},
				})
			},
			wantErr: false,
			assert: func(t *testing.T, profile *models.PlatformProfile, tokenData *models.TokenData) {
				if profile.PlatformUserID != "tiktok-callback-id" {
					t.Errorf("PlatformUserID: want %q, got %q", "tiktok-callback-id", profile.PlatformUserID)
				}
				if profile.Username != "Callback TikToker" {
					t.Errorf("Username: want %q, got %q", "Callback TikToker", profile.Username)
				}
				if tokenData.AccessToken != "callback-tiktok-token" {
					t.Errorf("AccessToken: want %q, got %q", "callback-tiktok-token", tokenData.AccessToken)
				}
				if tokenData.RefreshToken != "callback-tiktok-refresh" {
					t.Errorf("RefreshToken: want %q, got %q", "callback-tiktok-refresh", tokenData.RefreshToken)
				}
			},
		},
		{
			name: "TokenExchangeFails",
			tokenH: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"invalid_grant"}`))
			},
			userH:   nil,
			wantErr: true,
		},
		{
			name: "UserInfoFails",
			tokenH: func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"access_token": "tok",
					"token_type":   "bearer",
					"expires_in":   86400,
					"scope":        "user.info.basic",
				})
			},
			userH: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/v2/oauth/token/", tc.tokenH)
			if tc.userH != nil {
				mux.HandleFunc("/v2/user/info/", tc.userH)
			}
			srv := httptest.NewServer(mux)
			defer srv.Close()

			svc := newTestTikTokService(srv)
			profile, tokenData, err := svc.HandleCallback(context.Background(), "state", "code")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("HandleCallback: %v", err)
			}
			if tc.assert != nil {
				tc.assert(t, profile, tokenData)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Taglio 4.2: state machine tests
// StartPublish / CheckPublishStatus / ContinuePublish / Reconcile
// ---------------------------------------------------------------------------

// validPublishPayload returns a payload that passes ValidateContent
// (video_url present, caption under 4000 runes, privacy_level set).
// Taglio 4b: privacy_level is now mandatory.
func validPublishPayload() models.PublishPayload {
	return models.PublishPayload{
		Text:         "Hello TikTok from Taglio 4.2",
		VideoURL:     "https://cdn.example.com/video.mp4",
		PrivacyLevel: "PUBLIC_TO_EVERYONE",
	}
}

func TestTikTok_StartPublish(t *testing.T) {
	cases := []struct {
		name    string
		payload models.PublishPayload
		initH   func(t *testing.T, w http.ResponseWriter, r *http.Request)
		wantErr bool
		assert  func(t *testing.T, publishID, state string, err error)
	}{
		{
			name:    "Success",
			payload: validPublishPayload(),
			initH: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if auth := r.Header.Get("Authorization"); auth != "Bearer tt-access-token" {
					t.Errorf("Authorization: want %q, got %q", "Bearer tt-access-token", auth)
				}
				json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{"publish_id": "v_pub_abc_123", "status": "PROCESSING_UPLOAD"},
				})
			},
			wantErr: false,
			assert: func(t *testing.T, publishID, state string, err error) {
				if publishID != "v_pub_abc_123" {
					t.Errorf("publishID: want %q, got %q", "v_pub_abc_123", publishID)
				}
				if state != "PROCESSING_UPLOAD" {
					t.Errorf("state: want %q, got %q", "PROCESSING_UPLOAD", state)
				}
			},
		},
		{
			name:    "NoVideoURL",
			payload: models.PublishPayload{Text: "no video"},
			initH: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				t.Error("init must not be called when video_url is missing")
				w.WriteHeader(http.StatusOK)
			},
			wantErr: true,
		},
		{
			name:    "CaptionTooLong",
			payload: models.PublishPayload{Text: strings.Repeat("a", 4001), VideoURL: "https://x.example/v.mp4"},
			initH: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				t.Error("init must not be called when caption is too long")
				w.WriteHeader(http.StatusOK)
			},
			wantErr: true,
		},
		{
			name:    "PlatformError",
			payload: validPublishPayload(),
			initH: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":{"code":"invalid_params","message":"bad title"}}`))
			},
			wantErr: true,
		},
		{
			name:    "AuthHeader",
			payload: validPublishPayload(),
			initH: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				// The helper runs with access token "user-access-tok-xyz".
				if auth := r.Header.Get("Authorization"); auth != "Bearer user-access-tok-xyz" {
					t.Errorf("Authorization: want %q, got %q", "Bearer user-access-tok-xyz", auth)
				}
				json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{"publish_id": "x", "status": "PROCESSING_UPLOAD"},
				})
			},
			wantErr: false,
		},
		{
			name:    "JSONBody",
			payload: models.PublishPayload{Text: "My Title", VideoURL: "https://cdn.example.com/abc.mp4", PrivacyLevel: "SELF_ONLY"},
			initH: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				captured, _ := io.ReadAll(r.Body)
				var parsed struct {
					SourceInfo struct {
						Source   string `json:"source"`
						VideoURL string `json:"video_url"`
					} `json:"source_info"`
					PostInfo struct {
						Title string `json:"title"`
					} `json:"post_info"`
				}
				if err := json.Unmarshal(captured, &parsed); err != nil {
					t.Fatalf("init body is not valid JSON: %v\nbody: %s", err, string(captured))
				}
				if parsed.SourceInfo.Source != "PULL_FROM_URL" {
					t.Errorf("source_info.source: want PULL_FROM_URL, got %q", parsed.SourceInfo.Source)
				}
				if parsed.SourceInfo.VideoURL != "https://cdn.example.com/abc.mp4" {
					t.Errorf("source_info.video_url: want %q, got %q", "https://cdn.example.com/abc.mp4", parsed.SourceInfo.VideoURL)
				}
				if parsed.PostInfo.Title != "My Title" {
					t.Errorf("post_info.title: want %q, got %q", "My Title", parsed.PostInfo.Title)
				}
				json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{"publish_id": "x", "status": "PROCESSING_UPLOAD"},
				})
			},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/v2/post/publish/video/init/", func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("expected POST, got %s", r.Method)
				}
				tc.initH(t, w, r)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			svc := newTestTikTokService(srv)
			accessToken := "tt-access-token"
			if tc.name == "AuthHeader" {
				accessToken = "user-access-tok-xyz"
			}
			publishID, state, err := svc.StartPublish(context.Background(), accessToken, "tt-open-id", tc.payload)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("StartPublish: %v", err)
			}
			if tc.assert != nil {
				tc.assert(t, publishID, state, err)
			}
		})
	}
}

func TestTikTok_CheckPublishStatus(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		wantErr   bool
		wantState string
	}{
		{
			name:      "Success",
			status:    http.StatusOK,
			body:      `{"data":{"status":"PUBLISH_COMPLETE"}}`,
			wantErr:   false,
			wantState: "PUBLISH_COMPLETE",
		},
		{
			name:    "HTTPError",
			status:  http.StatusBadGateway,
			body:    `{"error":{"code":"upstream_error"}}`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			mux := http.NewServeMux()
			mux.HandleFunc("/v2/post/publish/status/fetch/", func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet {
					t.Errorf("expected GET, got %s", r.Method)
				}
				if id := r.URL.Query().Get("publish_id"); id != "v_pub_abc_123" {
					t.Errorf("publish_id: want %q, got %q", "v_pub_abc_123", id)
				}
				if auth := r.Header.Get("Authorization"); auth != "Bearer tt-access-token" {
					t.Errorf("Authorization: want %q, got %q", "Bearer tt-access-token", auth)
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			svc := newTestTikTokService(srv)
			state, err := svc.CheckPublishStatus(context.Background(), "tt-access-token", "v_pub_abc_123")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckPublishStatus: %v", err)
			}
			if state != tc.wantState {
				t.Errorf("state: want %q, got %q", tc.wantState, state)
			}
			if tc.name == "Success" && calls != 1 {
				t.Errorf("expected exactly 1 HTTP call (NO polling), got %d", calls)
			}
		})
	}
}

// TestTikTok_ContinuePublish_NoOpForPullFromURL: PULL_FROM_URL flows
// don't need a ContinuePublish step — the platform fetches the video
// directly from the URL set in StartPublish. The method must return
// nil without hitting the platform.
func TestTikTok_ContinuePublish_NoOpForPullFromURL(t *testing.T) {
	mux := http.NewServeMux()
	hits := 0
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	if err := svc.ContinuePublish(context.Background(), "tt-access-token", "v_pub_abc_123"); err != nil {
		t.Fatalf("ContinuePublish (PULL_FROM_URL): %v", err)
	}
	if hits != 0 {
		t.Errorf("expected 0 HTTP calls (PULL_FROM_URL is no-op), got %d", hits)
	}
}

func TestTikTok_Reconcile(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantErr bool
		assert  func(t *testing.T, result *models.PublishResult)
	}{
		{
			name:    "PublishComplete",
			status:  http.StatusOK,
			body:    `{"data":{"status":"PUBLISH_COMPLETE"}}`,
			wantErr: false,
			assert: func(t *testing.T, result *models.PublishResult) {
				if result == nil {
					t.Fatal("result: want non-nil, got nil")
				}
				if result.PlatformMediaID != "v_pub_abc_123" {
					t.Errorf("PlatformMediaID: want v_pub_abc_123, got %q", result.PlatformMediaID)
				}
			},
		},
		{
			name:    "Failed",
			status:  http.StatusOK,
			body:    `{"data":{"status":"FAILED"}}`,
			wantErr: true,
		},
		{
			name:    "HTTPError_LeavesForRetry",
			status:  http.StatusServiceUnavailable,
			body:    `{"error":{"code":"unavailable"}}`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/v2/post/publish/status/fetch/", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			svc := newTestTikTokService(srv)
			result, err := svc.Reconcile(context.Background(), "tt-access-token", "v_pub_abc_123")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if result != nil {
					t.Errorf("result: want nil on failure, got %+v", result)
				}
				return
			}
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if tc.assert != nil {
				tc.assert(t, result)
			}
		})
	}
}

func TestTikTok_Reconcile_InFlight(t *testing.T) {
	for _, inFlightState := range []string{"PROCESSING_UPLOAD", "PENDING_PUBLISH", "IN_REVIEW"} {
		t.Run(inFlightState, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/v2/post/publish/status/fetch/", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{"status": inFlightState},
				})
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			svc := newTestTikTokService(srv)
			result, err := svc.Reconcile(context.Background(), "tt-access-token", "v_pub_abc_123")
			if err != nil {
				t.Errorf("err: want nil (in-flight is not an error), got %v", err)
			}
			if result != nil {
				t.Errorf("result: want nil (in-flight, not terminal), got %+v", result)
			}
		})
	}
}

func TestTikTok_Publish(t *testing.T) {
	cases := []struct {
		name    string
		payload models.PublishPayload
		initH   func(t *testing.T, w http.ResponseWriter, r *http.Request)
		wantErr bool
		assert  func(t *testing.T, result *models.PublishResult, initHits int)
	}{
		{
			name:    "AsyncWrapper",
			payload: validPublishPayload(),
			initH: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{
						"publish_id": "v_pub_async_456",
						"status":     "PROCESSING_UPLOAD",
					},
				})
			},
			wantErr: false,
			assert: func(t *testing.T, result *models.PublishResult, initHits int) {
				if result == nil {
					t.Fatal("result: want non-nil, got nil")
				}
				if result.PlatformMediaID != "v_pub_async_456" {
					t.Errorf("PlatformMediaID: want v_pub_async_456, got %q", result.PlatformMediaID)
				}
				if initHits != 1 {
					t.Errorf("init calls: want 1, got %d (Publish must NOT poll)", initHits)
				}
			},
		},
		{
			name:    "ValidationError_SkipsPlatform",
			payload: models.PublishPayload{Text: "no video"},
			initH: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				t.Error("init must not be called when validation fails")
				w.WriteHeader(http.StatusOK)
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			initHits := 0
			mux := http.NewServeMux()
			mux.HandleFunc("/v2/post/publish/video/init/", func(w http.ResponseWriter, r *http.Request) {
				initHits++
				tc.initH(t, w, r)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			svc := newTestTikTokService(srv)
			result, err := svc.Publish(context.Background(), "tt-access-token", "tt-open-id", tc.payload)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if initHits != 0 {
					t.Errorf("expected 0 platform calls, got %d", initHits)
				}
				return
			}
			if err != nil {
				t.Fatalf("Publish: %v", err)
			}
			if tc.assert != nil {
				tc.assert(t, result, initHits)
			}
		})
	}
}

func TestTikTok_ValidateContent(t *testing.T) {
	svc := &TikTokOAuthService{cfg: tiktokTestCfg()}
	cases := []struct {
		name    string
		payload models.PublishPayload
		wantErr bool
	}{
		{
			name:    "EmptyVideoURL",
			payload: models.PublishPayload{Text: "x"},
			wantErr: true,
		},
		{
			name:    "MissingPrivacyLevel",
			payload: models.PublishPayload{Text: "hello", VideoURL: "https://x/v.mp4"},
			wantErr: true,
		},
		{
			name:    "ValidPayload",
			payload: models.PublishPayload{Text: "hello", VideoURL: "https://x/v.mp4", PrivacyLevel: "PUBLIC_TO_EVERYONE"},
			wantErr: false,
		},
		{
			name: "CaptionTooLong",
			payload: models.PublishPayload{
				Text:         strings.Repeat("a", 4001),
				VideoURL:     "https://x/v.mp4",
				PrivacyLevel: "MUTUAL_FOLLOW_FRIENDS",
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.ValidateContent(tc.payload)
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// PULL_FROM_FILE / chunked-upload tests (Taglio 4.x addendum).
// Mirrors the snapshot tests of /v2/post/publish/video/init/, the
// chunked-PUT protocol's Content-Range header, and the
// /v2/post/publish/video/upload/complete/ call. The happy-path test
// overrides svc.chunkSize to 1024 bytes so we can exercise 3 chunks
// (1024-byte chunks on a 3072-byte video) instead of allocating
// 10MB+ payloads for unit tests.
// -----------------------------------------------------------------------------

// TestTikTok_GetLoginURL_IncludesVideoUploadScope mirrors the App
// Review submission scopes. If a future refactor drops "video.upload"
// from GetLoginURL this test fails — the OAuth consent screen would
// no longer show Upload-as-Draft (PULL_FROM_FILE) and the App Review
// submission would diverge from the runtime behaviour.
func TestTikTok_GetLoginURL_IncludesVideoUploadScope(t *testing.T) {
	svc := &TikTokOAuthService{cfg: tiktokTestCfg()}
	loginURL := svc.GetLoginURL("csrf-state-xyz")

	parsed, err := url.Parse(loginURL)
	if err != nil {
		t.Fatalf("login URL parse: %v", err)
	}
	scope := parsed.Query().Get("scope")
	wantScopes := []string{"user.info.basic", "video.publish", "video.upload"}
	for _, want := range wantScopes {
		if !strings.Contains(scope, want) {
			t.Errorf("scope %q missing %q (full scope list: %s)", scope, want, scope)
		}
	}
}

// pullFromFileMockServer builds an httptest server with the four
// endpoints PULL_FROM_FILE expects: a video source, the TikTok init
// endpoint, a chunk-upload endpoint (registered post-bind), and the
// TikTok complete endpoint. Returns the server + the chunks handler
// (for assertion on uploaded ranges) + bindable endpoints on the mux
// so tests can override per-call behaviour (e.g., inject a 4xx).
func pullFromFileMockServer(t *testing.T) (*httptest.Server, *pullFromFileHandlers) {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	h := &pullFromFileHandlers{
		mux:            mux,
		srv:            srv,
		chunksReceived: []chunkCall{},
	}
	h.bindDefaults()
	return srv, h
}

type chunkCall struct {
	rangeHeader string
	authHeader  string
	method      string
	byteCount   int64
}

type pullFromFileHandlers struct {
	mux            *http.ServeMux
	srv            *httptest.Server
	chunksReceived []chunkCall

	// OnInit is invoked by /v2/.../init/'s handler with the raw
	// request body AND the *http.Request BEFORE the response is
	// written. Tests use it to capture/assert on the JSON shape the
	// service sends to TikTok (body) and on transport-layer details
	// (Authorization header) without re-registering the mux pattern
	// (which would conflict with bindDefaults). Optional — nil is a
	// no-op.
	OnInit func(rawBody []byte, r *http.Request)

	// Pluggable behaviour (overridden per-test if needed).
	sourceVideoBytes  []byte
	sourceVideoStatus int
	initStatus        int
	initBody          []byte
	chunkStatus       int
	completeStatus    int
}

// bindDefaults registers the 4 endpoints with reasonable defaults:
//
//	/source-video               → 200 OK + 3072 zero-fills
//	/v2/.../init/               → 200 OK + JSON with upload_url mapped to /chunk-upload
//	/chunk-upload               → 200 OK + record call
//	/v2/.../upload/complete/    → 200 OK
func (h *pullFromFileHandlers) bindDefaults() {
	h.sourceVideoBytes = bytes.Repeat([]byte{0}, 3072) // 3× 1024 chunks when chunkSize=1024
	h.sourceVideoStatus = http.StatusOK
	h.initStatus = http.StatusOK
	h.chunkStatus = http.StatusOK
	h.completeStatus = http.StatusOK

	h.mux.HandleFunc("/source-video", func(w http.ResponseWriter, r *http.Request) {
		if h.sourceVideoStatus != http.StatusOK {
			w.WriteHeader(h.sourceVideoStatus)
			w.Write([]byte(`{"error":"source_unreachable"}`))
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Write(h.sourceVideoBytes)
	})
	h.mux.HandleFunc("/v2/post/publish/video/init/", h.handleInit)
	h.mux.HandleFunc("/chunk-upload", h.handleChunk)
	h.mux.HandleFunc("/v2/post/publish/video/upload/complete/", h.handleComplete)
}

func (h *pullFromFileHandlers) handleInit(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if h.OnInit != nil {
		h.OnInit(body, r)
	}
	if h.initStatus != http.StatusOK {
		w.WriteHeader(h.initStatus)
		w.Write([]byte(`{"error":{"code":"internal_error","message":"platform rejected init"}}`))
		return
	}
	if h.initBody != nil {
		w.Write(h.initBody)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"data":{"publish_id":"v_pub_file_1","upload_url":"%s/chunk-upload"}}`, h.srv.URL)
}

func (h *pullFromFileHandlers) handleChunk(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PUT" {
		http.Error(w, "want PUT", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(h.chunkStatus)
	n, _ := io.Copy(io.Discard, r.Body)
	h.chunksReceived = append(h.chunksReceived, chunkCall{
		rangeHeader: r.Header.Get("Content-Range"),
		authHeader:  r.Header.Get("Authorization"),
		method:      r.Method,
		byteCount:   n,
	})
}

func (h *pullFromFileHandlers) handleComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "want POST", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	w.WriteHeader(h.completeStatus)
	// Echo back the publish_id so the test can parse + assert.
	fmt.Fprintf(w, `{"data":{"publish_id":"%s"}}`, "v_pub_file_1")
	_ = body
}

// newTestTikTokServiceWithChunkSize mirrors newTestTikTokService but
// also sets svc.chunkSize so chunked upload tests can exercise
// small-byte videos (default chunkSize=0 → 10MB would otherwise force
// a 10MB source allocation). Same package so direct field access is
// available.
func newTestTikTokServiceWithChunkSize(srv *httptest.Server, chunkSize int) *TikTokOAuthService {
	svc := newTestTikTokService(srv)
	svc.chunkSize = chunkSize
	return svc
}

type pullFromFileCase struct {
	name        string
	chunkSize   int
	accessToken string
	setup       func(h *pullFromFileHandlers)
	assert      func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error)
}

func runPullFromFileCase(t *testing.T, tc pullFromFileCase) {
	t.Helper()
	srv, h := pullFromFileMockServer(t)
	defer srv.Close()
	if tc.setup != nil {
		tc.setup(h)
	}
	chunkSize := 1024
	if tc.chunkSize > 0 {
		chunkSize = tc.chunkSize
	}
	accessToken := tc.accessToken
	if accessToken == "" {
		accessToken = "tt-access-token"
	}
	var initBody []byte
	prevOnInit := h.OnInit
	h.OnInit = func(body []byte, r *http.Request) {
		initBody = body
		if prevOnInit != nil {
			prevOnInit(body, r)
		}
	}
	payload := models.PublishPayload{
		Text:         tc.name,
		VideoURL:     srv.URL + "/source-video",
		PrivacyLevel: "PUBLIC_TO_EVERYONE",
		Source:       models.PublishSourcePULLFromFile,
	}
	svc := newTestTikTokServiceWithChunkSize(srv, chunkSize)
	publishID, state, err := svc.StartPublish(context.Background(), accessToken, "tt-1", payload)
	tc.assert(t, h, initBody, publishID, state, err)
}

func TestTikTok_StartPublish_PULLFromFile(t *testing.T) {
	type sourceInfo struct {
		Source          string `json:"source"`
		VideoSize       int64  `json:"video_size"`
		ChunkSize       int64  `json:"chunk_size"`
		TotalChunkCount int64  `json:"total_chunk_count"`
	}

	cases := []pullFromFileCase{
		{
			name:      "HappyPath",
			chunkSize: 1024,
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err != nil {
					t.Fatalf("StartPublish: %v", err)
				}
				if publishID != "v_pub_file_1" {
					t.Errorf("publishID: want v_pub_file_1, got %q", publishID)
				}
				if state != "PROCESSING_UPLOAD" {
					t.Errorf("state: want PROCESSING_UPLOAD, got %q", state)
				}
				var parsed struct {
					SourceInfo sourceInfo `json:"source_info"`
				}
				_ = json.Unmarshal(initBody, &parsed)
				if parsed.SourceInfo.Source != "FILE_UPLOAD" {
					t.Errorf("init source: want FILE_UPLOAD, got %q", parsed.SourceInfo.Source)
				}
				if parsed.SourceInfo.VideoSize != 3072 || parsed.SourceInfo.ChunkSize != 3072 || parsed.SourceInfo.TotalChunkCount != 1 {
					t.Errorf("init body: want whole-file 3072-byte single chunk, got %+v", parsed.SourceInfo)
				}
				if len(h.chunksReceived) != 1 {
					t.Fatalf("chunks: want 1, got %d", len(h.chunksReceived))
				}
				if h.chunksReceived[0].rangeHeader != "bytes 0-3071/3072" {
					t.Errorf("chunk[0] Content-Range: want %q, got %q", "bytes 0-3071/3072", h.chunksReceived[0].rangeHeader)
				}
				if h.chunksReceived[0].byteCount != 3072 {
					t.Errorf("chunk[0] body size: want 3072, got %d", h.chunksReceived[0].byteCount)
				}
			},
		},
		{
			name:      "MultiChunk",
			chunkSize: 2 * 1024 * 1024,
			setup: func(h *pullFromFileHandlers) {
				h.sourceVideoBytes = bytes.Repeat([]byte{0}, 6*1024*1024)
			},
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err != nil {
					t.Fatalf("StartPublish: %v", err)
				}
				var parsed struct {
					SourceInfo sourceInfo `json:"source_info"`
				}
				_ = json.Unmarshal(initBody, &parsed)
				if parsed.SourceInfo.TotalChunkCount != 3 {
					t.Errorf("init total_chunk_count: want 3, got %d", parsed.SourceInfo.TotalChunkCount)
				}
				if len(h.chunksReceived) != 3 {
					t.Fatalf("chunks: want 3, got %d", len(h.chunksReceived))
				}
				wantRanges := []string{
					"bytes 0-2097151/6291456",
					"bytes 2097152-4194303/6291456",
					"bytes 4194304-6291455/6291456",
				}
				for i, want := range wantRanges {
					if h.chunksReceived[i].rangeHeader != want {
						t.Errorf("chunk[%d] Content-Range: want %q, got %q", i, want, h.chunksReceived[i].rangeHeader)
					}
					if h.chunksReceived[i].byteCount != 2*1024*1024 {
						t.Errorf("chunk[%d] body size: want %d, got %d", i, 2*1024*1024, h.chunksReceived[i].byteCount)
					}
				}
			},
		},
		{
			name:      "LastChunkPartial",
			chunkSize: 1024,
			setup: func(h *pullFromFileHandlers) {
				h.sourceVideoBytes = bytes.Repeat([]byte{0}, 1500)
			},
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err != nil {
					t.Fatalf("StartPublish: %v", err)
				}
				if len(h.chunksReceived) != 1 || h.chunksReceived[0].rangeHeader != "bytes 0-1499/1500" || h.chunksReceived[0].byteCount != 1500 {
					t.Errorf("chunk[0]: want 0-1499/1500 (1500 bytes), got %q (%d bytes)", h.chunksReceived[0].rangeHeader, h.chunksReceived[0].byteCount)
				}
			},
		},
		{
			name:      "InitFailure",
			chunkSize: 1024,
			setup: func(h *pullFromFileHandlers) {
				h.initStatus = http.StatusInternalServerError
			},
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err == nil || !strings.Contains(err.Error(), "init") {
					t.Fatalf("expected init error, got %v", err)
				}
				if len(h.chunksReceived) != 0 {
					t.Errorf("no chunks should be sent after init failure, got %d", len(h.chunksReceived))
				}
			},
		},
		{
			name:      "ChunkFailure",
			chunkSize: 1024,
			setup: func(h *pullFromFileHandlers) {
				h.chunkStatus = http.StatusBadRequest
			},
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err == nil || !strings.Contains(err.Error(), "chunk PUT") {
					t.Fatalf("expected chunk PUT error, got %v", err)
				}
			},
		},
		{
			name:      "CompleteFailure",
			chunkSize: 1024,
			setup: func(h *pullFromFileHandlers) {
				h.completeStatus = http.StatusInternalServerError
			},
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err == nil || !strings.Contains(err.Error(), "complete") {
					t.Fatalf("expected complete error, got %v", err)
				}
				if len(h.chunksReceived) != 1 {
					t.Errorf("expected 1 chunk sent before complete failed, got %d", len(h.chunksReceived))
				}
			},
		},
		{
			name:      "MissingUploadURL",
			chunkSize: 1024,
			setup: func(h *pullFromFileHandlers) {
				h.initBody = []byte(`{"data":{"publish_id":"v_pub_file_1","upload_url":""}}`)
			},
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err == nil || !strings.Contains(err.Error(), "upload_url") {
					t.Fatalf("expected upload_url error, got %v", err)
				}
			},
		},
		{
			name:      "SourceFetchFailure",
			chunkSize: 1024,
			setup: func(h *pullFromFileHandlers) {
				h.sourceVideoStatus = http.StatusNotFound
			},
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err == nil || !strings.Contains(err.Error(), "fetch video bytes") {
					t.Fatalf("expected fetch video bytes error, got %v", err)
				}
				if len(h.chunksReceived) != 0 {
					t.Error("chunks must not be sent on source-fetch failure")
				}
			},
		},
		{
			name:        "AuthHeaderOnInit",
			chunkSize:   4096,
			accessToken: "user-bearer-xyz",
			setup: func(h *pullFromFileHandlers) {
				var authSeen string
				h.OnInit = func(_ []byte, r *http.Request) {
					authSeen = r.Header.Get("Authorization")
				}
				t.Cleanup(func() {
					if authSeen != "Bearer user-bearer-xyz" {
						t.Errorf("Authorization: want %q, got %q", "Bearer user-bearer-xyz", authSeen)
					}
				})
			},
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err != nil {
					t.Fatalf("StartPublish: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runPullFromFileCase(t, tc)
		})
	}
}

// TestTikTok_StartPublish_SourceEmpty_UsesPULLFromURL is the
// regression guard for the dual-path dispatcher: an empty Source
// field MUST continue to route through the legacy PULL_FROM_URL path
// (existing callers don't set the field). If a future refactor
// changes this default the test fails.
func TestTikTok_StartPublish_SourceEmpty_UsesPULLFromURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/post/publish/video/init/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed struct {
			SourceInfo struct {
				Source   string `json:"source"`
				VideoURL string `json:"video_url"`
			} `json:"source_info"`
		}
		_ = json.Unmarshal(body, &parsed)
		if parsed.SourceInfo.Source != "PULL_FROM_URL" {
			t.Errorf("empty Source must route to PULL_FROM_URL, got %q", parsed.SourceInfo.Source)
		}
		if parsed.SourceInfo.VideoURL == "" {
			t.Error("PULL_FROM_URL init must include video_url")
		}
		w.Write([]byte(`{"data":{"publish_id":"v_pub_url_1","status":"PROCESSING_UPLOAD"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)
	_, state, err := svc.StartPublish(context.Background(), "tok", "tt-1", models.PublishPayload{
		Text:         "default path",
		VideoURL:     "https://cdn.example.com/v.mp4",
		PrivacyLevel: "PUBLIC_TO_EVERYONE",
		// Source omitted on purpose.
	})
	if err != nil {
		t.Fatalf("StartPublish(empty Source): %v", err)
	}
	if state != "PROCESSING_UPLOAD" {
		t.Errorf("state: want PROCESSING_UPLOAD, got %q", state)
	}
}

// TestTikTok_StartPublish_PULLFromFile_AuthHeaderOnInit ensures the
// init request carries the user's Bearer access token (now also true
// for uploaded sessions — same Authorization contract as the
// PULL_FROM_URL one). Regression guard against an accidental swap to
// the client_key.
func TestTikTok_StartPublish_PULLFromFile_AuthHeaderOnInit(t *testing.T) {
	srv, h := pullFromFileMockServer(t)
	defer srv.Close()

	var authSeen string
	h.OnInit = func(_ []byte, r *http.Request) {
		authSeen = r.Header.Get("Authorization")
	}

	svc := newTestTikTokServiceWithChunkSize(srv, 4096)
	if _, _, err := svc.StartPublish(context.Background(), "user-bearer-xyz", "tt-1", models.PublishPayload{
		Text: "auth header", VideoURL: srv.URL + "/source-video",
		PrivacyLevel: "PUBLIC_TO_EVERYONE", Source: models.PublishSourcePULLFromFile,
	}); err != nil {
		t.Fatalf("StartPublish: %v", err)
	}
	if authSeen != "Bearer user-bearer-xyz" {
		t.Errorf("Authorization: want %q, got %q", "Bearer user-bearer-xyz", authSeen)
	}
}
