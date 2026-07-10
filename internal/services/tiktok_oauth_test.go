package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
)

// tiktokTestCfg returns a minimal config for TikTok OAuth tests.
func tiktokTestCfg() *config.Config {
	return &config.Config{
		TikTokClientKey:    "test-tiktok-client-key",
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
