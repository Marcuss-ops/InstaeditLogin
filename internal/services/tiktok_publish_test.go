package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

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
