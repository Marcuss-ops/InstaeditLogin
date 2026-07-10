package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
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

// oauth1TestCfg returns a config with OAuth 1.0a credentials so Publish()
// uses the static OAuth 1.0a signer instead of the OAuth 2.0 Bearer path.
func oauth1TestCfg() *config.Config {
	return &config.Config{
		TwitterClientID:          "unused",
		TwitterAPIKey:            "test-api-key",
		TwitterAPIKeySecret:      "test-api-secret",
		TwitterAccessToken:       "test-access-token",
		TwitterAccessTokenSecret: "test-access-secret",
	}
}

func newOAuth1TestService(srv *httptest.Server) *TwitterOAuthService {
	cfg := oauth1TestCfg()
	return &TwitterOAuthService{
		cfg:        cfg,
		httpClient: testClient(srv),
	}
}

func TestTwitter_Publish_OAuth1_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/2/tweets", func(w http.ResponseWriter, r *http.Request) {
		// Verify the OAuth 1.0a Authorization header is present.
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "OAuth ") {
			t.Errorf("expected OAuth 1.0a header, got: %s", auth)
		}
		// Verify it contains the expected OAuth params.
		for _, want := range []string{
			`oauth_consumer_key="test-api-key"`,
			`oauth_signature_method="HMAC-SHA1"`,
			`oauth_token="test-access-token"`,
			`oauth_version="1.0"`,
		} {
			if !strings.Contains(auth, want) {
				t.Errorf("OAuth header missing %s, got: %s", want, auth)
			}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"id":   "1234567890",
				"text": "hello from oauth1 test",
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newOAuth1TestService(srv)

	result, err := svc.Publish(context.Background(), "ignored-oauth2-token", "pf-123",
		models.PublishPayload{Text: "hello from oauth1 test"})
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}
	if result.PlatformMediaID != "1234567890" {
		t.Fatalf("PlatformMediaID: want 1234567890, got %s", result.PlatformMediaID)
	}
}

func TestTwitter_Publish_OAuth1_NoCredentials_FallsBackToBearer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/2/tweets", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer my-oauth2-token" {
			t.Errorf("expected Bearer token, got: %s", auth)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{"id": "999", "text": "ok"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTwitterService(srv) // no OAuth 1.0a credentials

	_, err := svc.Publish(context.Background(), "my-oauth2-token", "pf-123",
		models.PublishPayload{Text: "bearer test"})
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}
}

// TestTwitter_Publish_OAuth1_RealAPI performs a live smoke test against the
// real Twitter API using the OAuth 1.0a credentials from the environment.
// Skipped when TWITTER_ACCESS_TOKEN is not set (CI / local without secrets).
func TestTwitter_Publish_OAuth1_RealAPI(t *testing.T) {
	apiKey := os.Getenv("TWITTER_API_KEY")
	apiKeySecret := os.Getenv("TWITTER_API_KEY_SECRET")
	accessToken := os.Getenv("TWITTER_ACCESS_TOKEN")
	accessTokenSecret := os.Getenv("TWITTER_ACCESS_TOKEN_SECRET")

	if accessToken == "" || apiKey == "" {
		t.Skip("TWITTER_ACCESS_TOKEN + TWITTER_API_KEY not set; skipping live smoke test")
	}

	cfg := &config.Config{
		TwitterAPIKey:            apiKey,
		TwitterAPIKeySecret:      apiKeySecret,
		TwitterAccessToken:       accessToken,
		TwitterAccessTokenSecret: accessTokenSecret,
	}
	svc := &TwitterOAuthService{
		cfg:        cfg,
		httpClient: NewHTTPClient(),
	}

	result, err := svc.Publish(context.Background(), "ignored", "pf-123",
		models.PublishPayload{Text: fmt.Sprintf("InstaEdit OAuth 1.0a smoke test — %s", time.Now().Format(time.RFC3339))})
	if err != nil {
		t.Fatalf("Real API publish failed: %v", err)
	}
	t.Logf("Tweet published! ID: %s, URL: %s", result.PlatformMediaID, result.PlatformURL)
}
