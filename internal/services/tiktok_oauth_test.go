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
		TikTokClientID:     "test-tiktok-client-key",
		TikTokClientSecret: "test-tiktok-client-secret-32chars",
		TikTokRedirectURI:  "http://localhost:8080/tiktok/callback",
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

func TestTikTok_ExchangeCodeForToken_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/oauth/token/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "tiktok-access-token-abc",
			"token_type":    "bearer",
			"expires_in":    86400,
			"scope":         "user.info.basic,video.publish",
			"refresh_token": "tiktok-refresh-token-xyz",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	resp, err := svc.exchangeCodeForToken(context.Background(), "auth-code-tiktok")
	if err != nil {
		t.Fatalf("exchangeCodeForToken: %v", err)
	}
	if resp.AccessToken != "tiktok-access-token-abc" {
		t.Fatalf("access_token: want %q, got %q", "tiktok-access-token-abc", resp.AccessToken)
	}
	if resp.RefreshToken != "tiktok-refresh-token-xyz" {
		t.Fatalf("refresh_token: want %q, got %q", "tiktok-refresh-token-xyz", resp.RefreshToken)
	}
	if resp.ExpiresIn != 86400 {
		t.Fatalf("expires_in: want 86400, got %d", resp.ExpiresIn)
	}
}

func TestTikTok_ExchangeCodeForToken_ErrorResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/oauth/token/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"Invalid authorization code"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	_, err := svc.exchangeCodeForToken(context.Background(), "bad-code")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

func TestTikTok_GetUserInfo_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/user/info/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer tiktok-access-token" {
			t.Errorf("Authorization: want %q, got %q", "Bearer tiktok-access-token", auth)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"user": map[string]string{
					"open_id":      "tiktok-open-id-456",
					"display_name": "TikTok Creator",
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	profile, err := svc.getUserInfo(context.Background(), "tiktok-access-token")
	if err != nil {
		t.Fatalf("getUserInfo: %v", err)
	}
	if profile.PlatformUserID != "tiktok-open-id-456" {
		t.Fatalf("PlatformUserID: want %q, got %q", "tiktok-open-id-456", profile.PlatformUserID)
	}
	if profile.Username != "TikTok Creator" {
		t.Fatalf("Username: want %q, got %q", "TikTok Creator", profile.Username)
	}
	if profile.Name != "TikTok Creator" {
		t.Fatalf("Name: want %q, got %q", "TikTok Creator", profile.Name)
	}
}

func TestTikTok_GetUserInfo_ErrorResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/user/info/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid_token"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	_, err := svc.getUserInfo(context.Background(), "bad-token")
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestTikTok_HandleCallback_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/oauth/token/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "callback-tiktok-token",
			"token_type":    "bearer",
			"expires_in":    86400,
			"scope":         "user.info.basic,video.publish",
			"refresh_token": "callback-tiktok-refresh",
		})
	})
	mux.HandleFunc("/v2/user/info/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"user": map[string]string{
					"open_id":      "tiktok-callback-id",
					"display_name": "Callback TikToker",
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	profile, tokenData, err := svc.HandleCallback(context.Background(), "ignored-state", "auth-code-tiktok-cb")
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	if profile.PlatformUserID != "tiktok-callback-id" {
		t.Fatalf("PlatformUserID: want %q, got %q", "tiktok-callback-id", profile.PlatformUserID)
	}
	if profile.Username != "Callback TikToker" {
		t.Fatalf("Username: want %q, got %q", "Callback TikToker", profile.Username)
	}
	if tokenData.AccessToken != "callback-tiktok-token" {
		t.Fatalf("AccessToken: want %q, got %q", "callback-tiktok-token", tokenData.AccessToken)
	}
	if tokenData.RefreshToken != "callback-tiktok-refresh" {
		t.Fatalf("RefreshToken: want %q, got %q", "callback-tiktok-refresh", tokenData.RefreshToken)
	}
}

func TestTikTok_HandleCallback_TokenExchangeFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/oauth/token/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	_, _, err := svc.HandleCallback(context.Background(), "state", "bad-code")
	if err == nil {
		t.Fatal("expected error when token exchange fails")
	}
}

func TestTikTok_HandleCallback_UserInfoFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/oauth/token/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "tok",
			"token_type":   "bearer",
			"expires_in":   86400,
			"scope":        "user.info.basic",
		})
	})
	mux.HandleFunc("/v2/user/info/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	_, _, err := svc.HandleCallback(context.Background(), "state", "code")
	if err == nil {
		t.Fatal("expected error when user info fails")
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

// TestTikTok_StartPublish_Success: init endpoint returns a publish_id
// and the initial state (PROCESSING_UPLOAD). No polling — returns
// immediately.
func TestTikTok_StartPublish_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/post/publish/video/init/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer tt-access-token" {
			t.Errorf("Authorization: want %q, got %q", "Bearer tt-access-token", auth)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"publish_id": "v_pub_abc_123",
				"status":     "PROCESSING_UPLOAD",
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	publishID, state, err := svc.StartPublish(context.Background(), "tt-access-token", "tt-open-id", validPublishPayload())
	if err != nil {
		t.Fatalf("StartPublish: %v", err)
	}
	if publishID != "v_pub_abc_123" {
		t.Errorf("publishID: want %q, got %q", "v_pub_abc_123", publishID)
	}
	if state != "PROCESSING_UPLOAD" {
		t.Errorf("state: want %q, got %q", "PROCESSING_UPLOAD", state)
	}
}

// TestTikTok_StartPublish_NoVideoURL: ValidateContent fails before the
// HTTP call — must NOT hit the platform.
func TestTikTok_StartPublish_NoVideoURL(t *testing.T) {
	mux := http.NewServeMux()
	hits := 0
	mux.HandleFunc("/v2/post/publish/video/init/", func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	_, _, err := svc.StartPublish(context.Background(), "tt-access-token", "tt-open-id",
		models.PublishPayload{Text: "no video"})
	if err == nil {
		t.Fatal("expected error from missing video_url, got nil")
	}
	if hits != 0 {
		t.Errorf("expected 0 platform calls (validation must short-circuit), got %d", hits)
	}
}

// TestTikTok_StartPublish_CaptionTooLong: ValidateContent fails on
// caption > 4000 runes — must NOT hit the platform.
func TestTikTok_StartPublish_CaptionTooLong(t *testing.T) {
	mux := http.NewServeMux()
	hits := 0
	mux.HandleFunc("/v2/post/publish/video/init/", func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	// 4001 runes (just over the limit).
	tooLong := strings.Repeat("a", 4001)
	_, _, err := svc.StartPublish(context.Background(), "tt-access-token", "tt-open-id",
		models.PublishPayload{Text: tooLong, VideoURL: "https://x.example/v.mp4"})
	if err == nil {
		t.Fatal("expected error from caption too long, got nil")
	}
	if hits != 0 {
		t.Errorf("expected 0 platform calls (validation must short-circuit), got %d", hits)
	}
}

// TestTikTok_StartPublish_PlatformError: init returns 4xx — error
// surfaces to the worker.
func TestTikTok_StartPublish_PlatformError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/post/publish/video/init/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"code":"invalid_params","message":"bad title"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	_, _, err := svc.StartPublish(context.Background(), "tt-access-token", "tt-open-id", validPublishPayload())
	if err == nil {
		t.Fatal("expected error from 400 response, got nil")
	}
}

// TestTikTok_StartPublish_AuthHeader: the init request must carry the
// Bearer access token in the Authorization header (not a refresh token,
// not a client_key). A regression that swaps these would silently
// 401 the request.
func TestTikTok_StartPublish_AuthHeader(t *testing.T) {
	mux := http.NewServeMux()
	var authSeen string
	mux.HandleFunc("/v2/post/publish/video/init/", func(w http.ResponseWriter, r *http.Request) {
		authSeen = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"publish_id": "x", "status": "PROCESSING_UPLOAD",
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)
	_, _, err := svc.StartPublish(context.Background(), "user-access-tok-xyz", "tt-1", validPublishPayload())
	if err != nil {
		t.Fatalf("StartPublish: %v", err)
	}
	if authSeen != "Bearer user-access-tok-xyz" {
		t.Errorf("Authorization: want %q, got %q", "Bearer user-access-tok-xyz", authSeen)
	}
}

// TestTikTok_StartPublish_JSONBody: the init body must be valid JSON
// with source_info.source="PULL_FROM_URL" and the video_url from the
// payload. A regression that breaks the JSON shape would 400 the
// request.
func TestTikTok_StartPublish_JSONBody(t *testing.T) {
	var captured []byte
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/post/publish/video/init/", func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"publish_id": "x", "status": "PROCESSING_UPLOAD",
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)
	_, _, err := svc.StartPublish(context.Background(), "tok", "tt-1",
		models.PublishPayload{Text: "My Title", VideoURL: "https://cdn.example.com/abc.mp4", PrivacyLevel: "SELF_ONLY"})
	if err != nil {
		t.Fatalf("StartPublish: %v", err)
	}
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
}

// TestTikTok_CheckPublishStatus_Success: single GET to the status
// endpoint returns the current state. No polling — returns immediately.
func TestTikTok_CheckPublishStatus_Success(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
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
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{"status": "PUBLISH_COMPLETE"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	state, err := svc.CheckPublishStatus(context.Background(), "tt-access-token", "v_pub_abc_123")
	if err != nil {
		t.Fatalf("CheckPublishStatus: %v", err)
	}
	if state != "PUBLISH_COMPLETE" {
		t.Errorf("state: want %q, got %q", "PUBLISH_COMPLETE", state)
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 HTTP call (NO polling), got %d", calls)
	}
}

// TestTikTok_CheckPublishStatus_HTTPError: status endpoint returns 5xx
// — error surfaces. The reconciler must NOT mark the target failed
// on a transient 5xx (it just leaves the target alone to retry).
func TestTikTok_CheckPublishStatus_HTTPError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/post/publish/status/fetch/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":{"code":"upstream_error"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	_, err := svc.CheckPublishStatus(context.Background(), "tt-access-token", "v_pub_abc_123")
	if err == nil {
		t.Fatal("expected error from 502 response, got nil")
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

// TestTikTok_Reconcile_PublishComplete: when the status endpoint
// reports PUBLISH_COMPLETE, Reconcile returns a *PublishResult with
// the publish_id and a nil error — the worker will transition the
// target to 'published'.
func TestTikTok_Reconcile_PublishComplete(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/post/publish/status/fetch/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{"status": "PUBLISH_COMPLETE"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	result, err := svc.Reconcile(context.Background(), "tt-access-token", "v_pub_abc_123")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result == nil {
		t.Fatal("result: want non-nil (PUBLISH_COMPLETE is a terminal success), got nil")
	}
	if result.PlatformMediaID != "v_pub_abc_123" {
		t.Errorf("PlatformMediaID: want v_pub_abc_123 (publish_id becomes media_id on success), got %q", result.PlatformMediaID)
	}
}

// TestTikTok_Reconcile_Failed: when the status endpoint reports FAILED,
// Reconcile returns (nil, err) — the worker will mark the target failed.
func TestTikTok_Reconcile_Failed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/post/publish/status/fetch/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{"status": "FAILED"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	result, err := svc.Reconcile(context.Background(), "tt-access-token", "v_pub_abc_123")
	if err == nil {
		t.Fatal("err: want non-nil (FAILED is a terminal failure), got nil")
	}
	if result != nil {
		t.Errorf("result: want nil on failure, got %+v", result)
	}
}

// TestTikTok_Reconcile_InFlight: when the status endpoint reports
// PROCESSING_UPLOAD (or PENDING_PUBLISH / IN_REVIEW), Reconcile
// returns (nil, nil) — the worker leaves the target alone and checks
// again on the next tick.
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

// TestTikTok_Reconcile_HTTPError_LeavesForRetry: when the status
// endpoint returns a transient 5xx, Reconcile returns the error.
// The worker logs it as a warning and leaves the target alone to
// retry on the next tick (failing a target on a transient 5xx
// would be too aggressive — TikTok's SLO is loose).
func TestTikTok_Reconcile_HTTPError_LeavesForRetry(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/post/publish/status/fetch/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":{"code":"unavailable"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	_, err := svc.Reconcile(context.Background(), "tt-access-token", "v_pub_abc_123")
	if err == nil {
		t.Fatal("err: want non-nil from 503, got nil")
	}
}

// TestTikTok_Publish_AsyncWrapper: the Publisher.Publish entry point
// must be a thin wrapper that calls StartPublish and returns
// immediately with the publish_id. No polling. The reconciler
// drives the state machine on subsequent ticks.
func TestTikTok_Publish_AsyncWrapper(t *testing.T) {
	mux := http.NewServeMux()
	initHits := 0
	mux.HandleFunc("/v2/post/publish/video/init/", func(w http.ResponseWriter, r *http.Request) {
		initHits++
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"publish_id": "v_pub_async_456",
				"status":     "PROCESSING_UPLOAD",
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	result, err := svc.Publish(context.Background(), "tt-access-token", "tt-open-id", validPublishPayload())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result == nil {
		t.Fatal("result: want non-nil, got nil")
	}
	if result.PlatformMediaID != "v_pub_async_456" {
		t.Errorf("PlatformMediaID: want v_pub_async_456 (publish_id becomes media_id), got %q", result.PlatformMediaID)
	}
	// CRITICAL: only the init endpoint was called — no status/fetch
	// calls inside Publish. The whole point of Taglio 4.2 is removing
	// the synchronous polling loop from the request path.
	if initHits != 1 {
		t.Errorf("init calls: want 1, got %d (Publish must NOT poll)", initHits)
	}
}

// TestTikTok_Publish_ValidationError_SkipsPlatform: Publish delegates
// to StartPublish, which calls ValidateContent. A missing video_url
// must short-circuit before any HTTP call.
func TestTikTok_Publish_ValidationError_SkipsPlatform(t *testing.T) {
	mux := http.NewServeMux()
	hits := 0
	mux.HandleFunc("/v2/post/publish/video/init/", func(w http.ResponseWriter, r *http.Request) {
		hits++
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)

	_, err := svc.Publish(context.Background(), "tt-access-token", "tt-open-id",
		models.PublishPayload{Text: "no video"})
	if err == nil {
		t.Fatal("expected error from missing video_url, got nil")
	}
	if hits != 0 {
		t.Errorf("expected 0 platform calls, got %d", hits)
	}
}

// TestTikTok_ValidateContent: exercises the dedicated validation
// helper. This is the test that the other Publish / StartPublish
// tests rely on — kept here as an explicit, granular check.
func TestTikTok_ValidateContent(t *testing.T) {
	svc := &TikTokOAuthService{cfg: tiktokTestCfg()}

	// Empty VideoURL → error.
	if err := svc.ValidateContent(models.PublishPayload{Text: "x"}); err == nil {
		t.Error("expected error for empty video_url, got nil")
	}
	// Missing privacy_level → error (Taglio 4b).
	if err := svc.ValidateContent(models.PublishPayload{Text: "hello", VideoURL: "https://x/v.mp4"}); err == nil {
		t.Error("expected error for missing privacy_level (Taglio 4b), got nil")
	}
	// Full valid payload → no error.
	if err := svc.ValidateContent(models.PublishPayload{Text: "hello", VideoURL: "https://x/v.mp4", PrivacyLevel: "PUBLIC_TO_EVERYONE"}); err != nil {
		t.Errorf("unexpected error for valid payload: %v", err)
	}
	// Caption over limit → error.
	if err := svc.ValidateContent(models.PublishPayload{
		Text:         strings.Repeat("a", 4001),
		VideoURL:     "https://x/v.mp4",
		PrivacyLevel: "MUTUAL_FOLLOW_FRIENDS",
	}); err == nil {
		t.Error("expected error for caption > 4000 runes, got nil")
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

// TestTikTok_StartPublish_PULLFromFile_HappyPath drives the full
// chunked-upload chain on a 3072-byte video. Since it is < 5MB TikTok
// requires a single chunk whose size equals the whole file. Asserts:
//   - init body has source=FILE_UPLOAD, video_size=3072, chunk_size=3072, total_chunk_count=1
//   - exactly 1 chunk PUT happens with the correct Content-Range
//   - complete endpoint is invoked with publish_id in the body
//   - returned publish_id + initial state match expectations
func TestTikTok_StartPublish_PULLFromFile_HappyPath(t *testing.T) {
	srv, h := pullFromFileMockServer(t)
	defer srv.Close()
	var initBodyParsed struct {
		SourceInfo struct {
			Source          string `json:"source"`
			VideoSize       int64  `json:"video_size"`
			ChunkSize       int64  `json:"chunk_size"`
			TotalChunkCount int64  `json:"total_chunk_count"`
		} `json:"source_info"`
	}
	h.OnInit = func(rawBody []byte, _ *http.Request) {
		_ = json.Unmarshal(rawBody, &initBodyParsed)
	}

	svc := newTestTikTokServiceWithChunkSize(srv, 1024)
	payload := models.PublishPayload{
		Text:         "PULL_FROM_FILE happy path",
		VideoURL:     srv.URL + "/source-video",
		PrivacyLevel: "PUBLIC_TO_EVERYONE",
		Source:       models.PublishSourcePULLFromFile,
	}
	publishID, state, err := svc.StartPublish(context.Background(), "tt-access-token", "tt-open-id", payload)
	if err != nil {
		t.Fatalf("StartPublish(PULL_FROM_FILE): %v", err)
	}
	if publishID != "v_pub_file_1" {
		t.Errorf("publishID: want v_pub_file_1, got %q", publishID)
	}
	if state != "PROCESSING_UPLOAD" {
		t.Errorf("state: want PROCESSING_UPLOAD, got %q", state)
	}

	// init body assertions: TikTok's FILE_UPLOAD source value + a single
	// whole-file chunk (video < 5MB).
	if initBodyParsed.SourceInfo.Source != "FILE_UPLOAD" {
		t.Errorf("init source: want FILE_UPLOAD, got %q", initBodyParsed.SourceInfo.Source)
	}
	if initBodyParsed.SourceInfo.VideoSize != 3072 {
		t.Errorf("init video_size: want 3072, got %d", initBodyParsed.SourceInfo.VideoSize)
	}
	if initBodyParsed.SourceInfo.ChunkSize != 3072 {
		t.Errorf("init chunk_size: want 3072 (whole-file single chunk), got %d", initBodyParsed.SourceInfo.ChunkSize)
	}
	if initBodyParsed.SourceInfo.TotalChunkCount != 1 {
		t.Errorf("init total_chunk_count: want 1, got %d", initBodyParsed.SourceInfo.TotalChunkCount)
	}

	// chunk assertions: 1 PUT covering the whole file.
	if len(h.chunksReceived) != 1 {
		t.Fatalf("chunks: want 1, got %d", len(h.chunksReceived))
	}
	if h.chunksReceived[0].rangeHeader != "bytes 0-3071/3072" {
		t.Errorf("chunk[0] Content-Range: want %q, got %q", "bytes 0-3071/3072", h.chunksReceived[0].rangeHeader)
	}
	if h.chunksReceived[0].byteCount != 3072 {
		t.Errorf("chunk[0] body size: want 3072, got %d", h.chunksReceived[0].byteCount)
	}
	if h.chunksReceived[0].method != "PUT" {
		t.Errorf("chunk[0] method: want PUT, got %q", h.chunksReceived[0].method)
	}
}

// TestTikTok_StartPublish_PULLFromFile_MultiChunk verifies the chunk math
// for a > 5MB video with an injected 2MB chunk size → 3 chunks. Small
// files are forced to a single chunk, so multi-chunk coverage needs a
// file above TikTok's 5MB single-chunk threshold.
func TestTikTok_StartPublish_PULLFromFile_MultiChunk(t *testing.T) {
	srv, h := pullFromFileMockServer(t)
	defer srv.Close()
	const total = 6 * 1024 * 1024 // 6MB
	h.sourceVideoBytes = bytes.Repeat([]byte{0}, total)
	const chunkSize = 2 * 1024 * 1024 // 2MB

	var initBodyParsed struct {
		SourceInfo struct {
			Source          string `json:"source"`
			VideoSize       int64  `json:"video_size"`
			ChunkSize       int64  `json:"chunk_size"`
			TotalChunkCount int64  `json:"total_chunk_count"`
		} `json:"source_info"`
	}
	h.OnInit = func(rawBody []byte, _ *http.Request) {
		_ = json.Unmarshal(rawBody, &initBodyParsed)
	}

	svc := newTestTikTokServiceWithChunkSize(srv, chunkSize)
	payload := models.PublishPayload{
		Text:         "multi chunk",
		VideoURL:     srv.URL + "/source-video",
		PrivacyLevel: "PUBLIC_TO_EVERYONE",
		Source:       models.PublishSourcePULLFromFile,
	}
	if _, _, err := svc.StartPublish(context.Background(), "tok", "tt-1", payload); err != nil {
		t.Fatalf("StartPublish: %v", err)
	}

	if initBodyParsed.SourceInfo.Source != "FILE_UPLOAD" {
		t.Errorf("init source: want FILE_UPLOAD, got %q", initBodyParsed.SourceInfo.Source)
	}
	if initBodyParsed.SourceInfo.ChunkSize != chunkSize {
		t.Errorf("init chunk_size: want %d, got %d", chunkSize, initBodyParsed.SourceInfo.ChunkSize)
	}
	if initBodyParsed.SourceInfo.TotalChunkCount != 3 {
		t.Errorf("init total_chunk_count: want 3, got %d", initBodyParsed.SourceInfo.TotalChunkCount)
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
		if h.chunksReceived[i].byteCount != chunkSize {
			t.Errorf("chunk[%d] body size: want %d, got %d", i, chunkSize, h.chunksReceived[i].byteCount)
		}
	}
}

// TestTikTok_StartPublish_PULLFromFile_LastChunkPartial verifies the
// final chunk is correctly sized when total is not chunk-aligned. For a
// < 5MB video TikTok requires a single whole-file chunk.
func TestTikTok_StartPublish_PULLFromFile_LastChunkPartial(t *testing.T) {
	srv, h := pullFromFileMockServer(t)
	defer srv.Close()
	h.sourceVideoBytes = bytes.Repeat([]byte{0}, 1500)

	svc := newTestTikTokServiceWithChunkSize(srv, 1024)
	payload := models.PublishPayload{
		Text:         "partial",
		VideoURL:     srv.URL + "/source-video",
		PrivacyLevel: "SELF_ONLY",
		Source:       models.PublishSourcePULLFromFile,
	}
	if _, _, err := svc.StartPublish(context.Background(), "tok", "tt-1", payload); err != nil {
		t.Fatalf("StartPublish: %v", err)
	}

	if len(h.chunksReceived) != 1 {
		t.Fatalf("chunks: want 1 (whole-file single chunk), got %d", len(h.chunksReceived))
	}
	if h.chunksReceived[0].rangeHeader != "bytes 0-1499/1500" || h.chunksReceived[0].byteCount != 1500 {
		t.Errorf("chunk[0]: want 0-1499/1500 (1500 bytes), got %q (%d bytes)", h.chunksReceived[0].rangeHeader, h.chunksReceived[0].byteCount)
	}
}

// TestTikTok_StartPublish_PULLFromFile_InitFailure: init endpoint
// returns 500 → StartPublish surfaces the error before any chunk goes
// out. Verifies no chunk PUTs and no complete call happened.
func TestTikTok_StartPublish_PULLFromFile_InitFailure(t *testing.T) {
	srv, h := pullFromFileMockServer(t)
	defer srv.Close()
	h.initStatus = http.StatusInternalServerError

	svc := newTestTikTokServiceWithChunkSize(srv, 1024)
	payload := models.PublishPayload{
		Text:         "init fail",
		VideoURL:     srv.URL + "/source-video",
		PrivacyLevel: "PUBLIC_TO_EVERYONE",
		Source:       models.PublishSourcePULLFromFile,
	}
	_, _, err := svc.StartPublish(context.Background(), "tok", "tt-1", payload)
	if err == nil {
		t.Fatal("expected error from init 500, got nil")
	}
	if !strings.Contains(err.Error(), "init") {
		t.Errorf("error should mention init: %v", err)
	}
	if len(h.chunksReceived) != 0 {
		t.Errorf("no chunks should be sent after init failure, got %d", len(h.chunksReceived))
	}
}

// TestTikTok_StartPublish_PULLFromFile_ChunkFailure: chunk PUT
// returns 4xx → StartPublish surfaces the error. Verifies that the
// chain aborts cleanly (subsequent chunks not sent).
func TestTikTok_StartPublish_PULLFromFile_ChunkFailure(t *testing.T) {
	srv, h := pullFromFileMockServer(t)
	defer srv.Close()
	h.chunkStatus = http.StatusBadRequest

	svc := newTestTikTokServiceWithChunkSize(srv, 1024)
	payload := models.PublishPayload{
		Text:         "chunk fail",
		VideoURL:     srv.URL + "/source-video",
		PrivacyLevel: "PUBLIC_TO_EVERYONE",
		Source:       models.PublishSourcePULLFromFile,
	}
	_, _, err := svc.StartPublish(context.Background(), "tok", "tt-1", payload)
	if err == nil {
		t.Fatal("expected error from chunk 400, got nil")
	}
	if !strings.Contains(err.Error(), "chunk PUT") {
		t.Errorf("error should mention chunk PUT: %v", err)
	}
}

// TestTikTok_StartPublish_PULLFromFile_CompleteFailure: chunks go
// out OK but the final complete POST returns 500. Surfaces a
// complete-related error.
func TestTikTok_StartPublish_PULLFromFile_CompleteFailure(t *testing.T) {
	srv, h := pullFromFileMockServer(t)
	defer srv.Close()
	h.completeStatus = http.StatusInternalServerError

	svc := newTestTikTokServiceWithChunkSize(srv, 1024)
	payload := models.PublishPayload{
		Text:         "complete fail",
		VideoURL:     srv.URL + "/source-video",
		PrivacyLevel: "PUBLIC_TO_EVERYONE",
		Source:       models.PublishSourcePULLFromFile,
	}
	_, _, err := svc.StartPublish(context.Background(), "tok", "tt-1", payload)
	if err == nil {
		t.Fatal("expected error from complete 500, got nil")
	}
	if !strings.Contains(err.Error(), "complete") {
		t.Errorf("error should mention complete: %v", err)
	}
	// Chunks DID go out (then complete failed afterward). The default
	// 3072-byte video is a single whole-file chunk.
	if len(h.chunksReceived) != 1 {
		t.Errorf("expected 1 chunk sent before complete failed, got %d", len(h.chunksReceived))
	}
}

// TestTikTok_StartPublish_PULLFromFile_MissingUploadURL surfaces a
// clear error when TikTok's init response lacks the upload_url field
// (a regression guard against a future init-response-shape change).
func TestTikTok_StartPublish_PULLFromFile_MissingUploadURL(t *testing.T) {
	srv, h := pullFromFileMockServer(t)
	defer srv.Close()
	h.initBody = []byte(`{"data":{"publish_id":"v_pub_file_1","upload_url":""}}`)

	svc := newTestTikTokServiceWithChunkSize(srv, 1024)
	_, _, err := svc.StartPublish(context.Background(), "tok", "tt-1", models.PublishPayload{
		Text: "no upload url", VideoURL: srv.URL + "/source-video",
		PrivacyLevel: "PUBLIC_TO_EVERYONE", Source: models.PublishSourcePULLFromFile,
	})
	if err == nil {
		t.Fatal("expected error from missing upload_url, got nil")
	}
	if !strings.Contains(err.Error(), "upload_url") {
		t.Errorf("error should mention upload_url: %v", err)
	}
}

// TestTikTok_StartPublish_PULLFromFile_SourceFetchFailure guards
// the entry-point: if the source VideoURL returns non-200, fail fast
// before init is called.
func TestTikTok_StartPublish_PULLFromFile_SourceFetchFailure(t *testing.T) {
	srv, h := pullFromFileMockServer(t)
	defer srv.Close()
	h.sourceVideoStatus = http.StatusNotFound

	svc := newTestTikTokServiceWithChunkSize(srv, 1024)
	_, _, err := svc.StartPublish(context.Background(), "tok", "tt-1", models.PublishPayload{
		Text: "no source", VideoURL: srv.URL + "/source-video",
		PrivacyLevel: "PUBLIC_TO_EVERYONE", Source: models.PublishSourcePULLFromFile,
	})
	if err == nil {
		t.Fatal("expected error from source 404, got nil")
	}
	if !strings.Contains(err.Error(), "fetch video bytes") {
		t.Errorf("error should mention fetch: %v", err)
	}
	if len(h.chunksReceived) != 0 {
		t.Error("chunks must not be sent on source-fetch failure")
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
