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

// linkedinTestCfg returns a minimal config for LinkedIn OAuth tests.
func linkedinTestCfg() *config.Config {
	return &config.Config{
		Auth: config.AuthConfig{
			LinkedInClientID:     "test-linkedin-client-id",
			LinkedInClientSecret: "test-linkedin-client-secret-must-be-32-chars",
			LinkedInRedirectURI:  "http://localhost:8080/api/v1/auth/linkedin/callback",
		},
	}
}

// newTestLinkedInService creates a LinkedInOAuthService pointed at the httptest server.
// The cfg field type is OAuthConfig (interface), so tests wrap the
// underlying *config.Config via NewConfigAdapter — same path as
// bootstrap's production wiring.
func newTestLinkedInService(srv *httptest.Server) *LinkedInOAuthService {
	cfg := linkedinTestCfg()
	return &LinkedInOAuthService{
		cfg:        NewConfigAdapter(cfg),
		httpClient: testClient(srv),
	}
}

// ---------------------------------------------------------------------------
// exchangeCodeForToken tests
// ---------------------------------------------------------------------------

func TestLinkedIn_ExchangeCodeForToken_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/v2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type: want application/x-www-form-urlencoded, got %s", ct)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "linkedin-access-token-abc123",
			"token_type":   "bearer",
			"expires_in":   5184000,
			"scope":        "openid profile email w_member_social",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestLinkedInService(srv)

	resp, err := svc.exchangeCodeForToken(context.Background(), "auth-code-xyz")
	if err != nil {
		t.Fatalf("exchangeCodeForToken: %v", err)
	}
	if resp.AccessToken != "linkedin-access-token-abc123" {
		t.Fatalf("access_token: want %q, got %q", "linkedin-access-token-abc123", resp.AccessToken)
	}
	if resp.TokenType != "bearer" {
		t.Fatalf("token_type: want bearer, got %s", resp.TokenType)
	}
	if resp.ExpiresIn != 5184000 {
		t.Fatalf("expires_in: want 5184000, got %d", resp.ExpiresIn)
	}
}

func TestLinkedIn_ExchangeCodeForToken_ErrorResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/v2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"Invalid authorization code"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestLinkedInService(srv)

	_, err := svc.exchangeCodeForToken(context.Background(), "bad-code")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

// ---------------------------------------------------------------------------
// getUserInfo tests
// ---------------------------------------------------------------------------

func TestLinkedIn_GetUserInfo_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer linkedin-access-token" {
			t.Errorf("Authorization: want %q, got %q", "Bearer linkedin-access-token", auth)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sub":   "urn:li:person:abc123",
			"name":  "John Doe",
			"email": "john@example.com",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestLinkedInService(srv)

	profile, err := svc.getUserInfo(context.Background(), "linkedin-access-token")
	if err != nil {
		t.Fatalf("getUserInfo: %v", err)
	}
	if profile.PlatformUserID != "urn:li:person:abc123" {
		t.Fatalf("PlatformUserID: want urn:li:person:abc123, got %s", profile.PlatformUserID)
	}
	if profile.Username != "urn:li:person:abc123" {
		t.Fatalf("Username: want urn:li:person:abc123 (fallback to sub), got %s", profile.Username)
	}
	if profile.Name != "John Doe" {
		t.Fatalf("Name: want John Doe, got %s", profile.Name)
	}
	if profile.Email != "john@example.com" {
		t.Fatalf("Email: want john@example.com, got %s", profile.Email)
	}
}

func TestLinkedIn_GetUserInfo_ErrorResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/userinfo", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestLinkedInService(srv)

	_, err := svc.getUserInfo(context.Background(), "bad-token")
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

// ---------------------------------------------------------------------------
// HandleCallback tests
// ---------------------------------------------------------------------------

func TestLinkedIn_HandleCallback_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/v2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "callback-linkedin-token",
			"token_type":   "bearer",
			"expires_in":   5184000,
			"scope":        "openid profile email w_member_social",
		})
	})
	mux.HandleFunc("/v2/userinfo", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sub":   "urn:li:person:xyz789",
			"name":  "Callback User",
			"email": "callback@example.com",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestLinkedInService(srv)

	profile, tokenData, err := svc.HandleCallback(context.Background(), "linkedin_state", "auth-code-456")
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	if profile.PlatformUserID != "urn:li:person:xyz789" {
		t.Fatalf("PlatformUserID: want urn:li:person:xyz789, got %s", profile.PlatformUserID)
	}
	if tokenData.AccessToken != "callback-linkedin-token" {
		t.Fatalf("AccessToken: want callback-linkedin-token, got %s", tokenData.AccessToken)
	}
	if tokenData.TokenType != models.TokenTypeBearer {
		t.Fatalf("TokenType: want bearer, got %s", tokenData.TokenType)
	}
	if tokenData.ExpiresIn != 5184000 {
		t.Fatalf("ExpiresIn: want 5184000, got %d", tokenData.ExpiresIn)
	}
}

func TestLinkedIn_HandleCallback_TokenExchangeFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/v2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestLinkedInService(srv)

	_, _, err := svc.HandleCallback(context.Background(), "state", "bad-code")
	if err == nil {
		t.Fatal("expected error when token exchange fails")
	}
}

func TestLinkedIn_HandleCallback_UserInfoFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/v2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "tok",
			"token_type":   "bearer",
			"expires_in":   5184000,
			"scope":        "openid profile",
		})
	})
	mux.HandleFunc("/v2/userinfo", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestLinkedInService(srv)

	_, _, err := svc.HandleCallback(context.Background(), "state", "code")
	if err == nil {
		t.Fatal("expected error when user info fails")
	}
}

func TestLinkedIn_RefreshOAuthToken_NotAvailable(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestLinkedInService(srv)

	_, err := svc.RefreshOAuthToken(context.Background(), "any-token")
	if err == nil {
		t.Fatal("expected error for refresh (offline_access not requested)")
	}
}
