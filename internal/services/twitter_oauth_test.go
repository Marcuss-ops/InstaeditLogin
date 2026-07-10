package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
)

// urlRewriteTransport rewrites all request URLs to point at the given target
// server. This lets httptest.Server intercept requests the service code makes
// to absolute production URLs (https://api.twitter.com, etc.).
type urlRewriteTransport struct {
	target *url.URL
	next   http.RoundTripper
}

func (rt *urlRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = rt.target.Scheme
	req.URL.Host = rt.target.Host
	return rt.next.RoundTrip(req)
}

// testClient creates an *http.Client that routes all requests through the
// httptest server regardless of the original URL host.
func testClient(srv *httptest.Server) *http.Client {
	u, _ := url.Parse(srv.URL)
	return &http.Client{
		Transport: &urlRewriteTransport{target: u, next: http.DefaultTransport},
	}
}

// twitterTestCfg returns a minimal config that passes validate() so we can
// construct a real TwitterOAuthService without hitting a real database.
func twitterTestCfg() *config.Config {
	return &config.Config{
		TwitterClientID:     "test-client-id",
		TwitterClientSecret: "test-client-secret-must-be-at-least-32-chars-long",
		TwitterRedirectURI:  "http://localhost:8080/callback",
	}
}

// newTestTwitterService creates a TwitterOAuthService whose httpClient is
// pointed at the httptest server. TokenHelper is nil because these tests
// only exercise the OAuth flow, not token persistence.
func newTestTwitterService(srv *httptest.Server) *TwitterOAuthService {
	cfg := twitterTestCfg()
	return &TwitterOAuthService{
		cfg:        cfg,
		httpClient: testClient(srv),
	}
}

func TestTwitter_ExchangeCodeForToken_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/2/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "twitter-access-token-abc",
			"token_type":    "bearer",
			"expires_in":    7200,
			"scope":         "tweet.read tweet.write users.read offline.access",
			"refresh_token": "twitter-refresh-token-xyz",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTwitterService(srv)

	resp, err := svc.exchangeCodeForToken(context.Background(), "auth-code-123", "pkce-verifier")
	if err != nil {
		t.Fatalf("exchangeCodeForToken: %v", err)
	}
	if resp.AccessToken != "twitter-access-token-abc" {
		t.Fatalf("access_token: want %q, got %q", "twitter-access-token-abc", resp.AccessToken)
	}
	if resp.RefreshToken != "twitter-refresh-token-xyz" {
		t.Fatalf("refresh_token: want %q, got %q", "twitter-refresh-token-xyz", resp.RefreshToken)
	}
	if resp.ExpiresIn != 7200 {
		t.Fatalf("expires_in: want 7200, got %d", resp.ExpiresIn)
	}
}

func TestTwitter_ExchangeCodeForToken_ErrorResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/2/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"Invalid authorization code"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTwitterService(srv)

	_, err := svc.exchangeCodeForToken(context.Background(), "bad-code", "verifier")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

func TestTwitter_GetUserInfo_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/2/users/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer twitter-access-token" {
			t.Errorf("Authorization: want %q, got %q", "Bearer twitter-access-token", auth)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]string{
				"id":       "123456789",
				"name":     "Test User",
				"username": "testuser",
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTwitterService(srv)

	profile, err := svc.getUserInfo(context.Background(), "twitter-access-token")
	if err != nil {
		t.Fatalf("getUserInfo: %v", err)
	}
	if profile.PlatformUserID != "123456789" {
		t.Fatalf("PlatformUserID: want %q, got %q", "123456789", profile.PlatformUserID)
	}
	if profile.Username != "testuser" {
		t.Fatalf("Username: want %q, got %q", "testuser", profile.Username)
	}
	if profile.Name != "Test User" {
		t.Fatalf("Name: want %q, got %q", "Test User", profile.Name)
	}
}

func TestTwitter_GetUserInfo_ErrorResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/2/users/me", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTwitterService(srv)

	_, err := svc.getUserInfo(context.Background(), "bad-token")
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestTwitter_HandleCallback_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/2/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "callback-access-token",
			"token_type":    "bearer",
			"expires_in":    7200,
			"scope":         "tweet.read tweet.write users.read offline.access",
			"refresh_token": "callback-refresh-token",
		})
	})
	mux.HandleFunc("/2/users/me", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]string{
				"id":       "987654321",
				"name":     "Callback User",
				"username": "callbackuser",
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTwitterService(srv)

	// State must contain a verifier after a dot (format set by GetLoginURL).
	state := "twitter_default.test-verifier-123"
	profile, tokenData, err := svc.HandleCallback(context.Background(), state, "auth-code-456")
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	if profile.PlatformUserID != "987654321" {
		t.Fatalf("PlatformUserID: want %q, got %q", "987654321", profile.PlatformUserID)
	}
	if tokenData.AccessToken != "callback-access-token" {
		t.Fatalf("AccessToken: want %q, got %q", "callback-access-token", tokenData.AccessToken)
	}
	if tokenData.RefreshToken != "callback-refresh-token" {
		t.Fatalf("RefreshToken: want %q, got %q", "callback-refresh-token", tokenData.RefreshToken)
	}
}

func TestTwitter_HandleCallback_MissingVerifier(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestTwitterService(srv)

	// State without a dot → no verifier extractable.
	_, _, err := svc.HandleCallback(context.Background(), "no-dot-state", "code")
	if err == nil {
		t.Fatal("expected error for state without verifier")
	}
}

func TestTwitter_HandleCallback_TokenExchangeFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/2/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTwitterService(srv)

	_, _, err := svc.HandleCallback(context.Background(), "state.verifier", "bad-code")
	if err == nil {
		t.Fatal("expected error when token exchange fails")
	}
}

func TestTwitter_HandleCallback_UserInfoFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/2/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "tok",
			"token_type":   "bearer",
			"expires_in":   7200,
			"scope":        "tweet.read",
		})
	})
	mux.HandleFunc("/2/users/me", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTwitterService(srv)

	_, _, err := svc.HandleCallback(context.Background(), "state.verifier", "code")
	if err == nil {
		t.Fatal("expected error when user info fails")
	}
}
