package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
