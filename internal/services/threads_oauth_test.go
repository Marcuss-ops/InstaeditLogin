package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
)

// threadsTestCfg returns a minimal config for Threads OAuth tests.
func threadsTestCfg() *config.Config {
	return &config.Config{
		MetaAppID:          "test-meta-app-id",
		MetaAppSecret:      "test-meta-app-secret-must-be-32-chars-min",
		ThreadsRedirectURI: "http://localhost:8080/api/v1/auth/threads/callback",
	}
}

// newTestThreadsService creates a ThreadsOAuthService with an injected test HTTP client.
func newTestThreadsService(srv *httptest.Server) *ThreadsOAuthService {
	cfg := threadsTestCfg()
	base := NewMetaOAuthBase(cfg)
	base.httpClient = testClient(srv)
	return &ThreadsOAuthService{
		base:        base,
		redirectURI: cfg.ThreadsRedirectURI,
	}
}

// TestThreadsAuthorizationURL verifies that GetLoginURL returns a URL with:
//   - the correct Threads OAuth base URL (threads.net)
//   - the MetaAppID as client_id
//   - the Threads-specific redirect URI
//   - Threads-specific scopes (threads_basic, threads_content_publish)
func TestThreadsAuthorizationURL(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestThreadsService(srv)

	authURL := svc.GetLoginURL("th-state-42")

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("GetLoginURL returned an unparseable URL: %v\nurl: %s", err, authURL)
	}

	if parsed.Host != "threads.net" {
		t.Errorf("host: want threads.net, got %s", parsed.Host)
	}
	if parsed.Path != "/oauth/authorize" {
		t.Errorf("path: want /oauth/authorize, got %s", parsed.Path)
	}

	params := parsed.Query()

	if params.Get("client_id") != "test-meta-app-id" {
		t.Errorf("client_id: want test-meta-app-id, got %q", params.Get("client_id"))
	}

	if params.Get("redirect_uri") != "http://localhost:8080/api/v1/auth/threads/callback" {
		t.Errorf("redirect_uri: want threads callback, got %q", params.Get("redirect_uri"))
	}

	if params.Get("response_type") != "code" {
		t.Errorf("response_type: want code, got %q", params.Get("response_type"))
	}

	if params.Get("state") != "th-state-42" {
		t.Errorf("state: want th-state-42, got %q", params.Get("state"))
	}

	scopes := params.Get("scope")
	if scopes == "" {
		t.Fatal("scope is empty — Threads must request Threads scopes")
	}
}

// TestThreadsCallbackUsesCorrectRedirectURI verifies that HandleCallback
// calls the Threads token endpoint with the Threads-specific redirect URI.
func TestThreadsCallbackUsesCorrectRedirectURI(t *testing.T) {
	var capturedRedirectURI string

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		// Capture redirect_uri from the initial code exchange.
		if r.PostForm.Get("code") != "" {
			capturedRedirectURI = r.PostForm.Get("redirect_uri")
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "th-short-lived",
			"token_type":   "bearer",
			"expires_in":   3600,
		})
	})
	mux.HandleFunc("/access_token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "th-long-lived",
			"token_type":   "bearer",
			"expires_in":   5184000,
		})
	})
	mux.HandleFunc("/v1.0/me", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"id":   "th-user-id",
			"name": "Threads User",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestThreadsService(srv)

	_, _, err := svc.HandleCallback(context.Background(), "state-th", "auth-code-th")
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}

	if capturedRedirectURI != "http://localhost:8080/api/v1/auth/threads/callback" {
		t.Errorf("redirect_uri in token exchange: want threads callback, got %q", capturedRedirectURI)
	}
}

// TestThreadsRequestsThreadsScopes verifies that the scopes in the authorization
// URL are Threads-specific and do NOT contain Instagram or Facebook scopes.
func TestThreadsRequestsThreadsScopes(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestThreadsService(srv)

	authURL := svc.GetLoginURL("th-scope-test")
	parsed, _ := url.Parse(authURL)
	scopes := parsed.Query().Get("scope")

	// Threads must have Threads-specific scopes.
	if !strings.Contains(scopes, "threads_basic") {
		t.Errorf("missing threads_basic scope: %q", scopes)
	}
	if !strings.Contains(scopes, "threads_content_publish") {
		t.Errorf("missing threads_content_publish scope: %q", scopes)
	}

	// Threads must NOT have Instagram scopes.
	if strings.Contains(scopes, "instagram_basic") {
		t.Errorf("Threads URL contains Instagram scope instagram_basic: %q", scopes)
	}
	if strings.Contains(scopes, "instagram_content_publish") {
		t.Errorf("Threads URL contains Instagram scope instagram_content_publish: %q", scopes)
	}

	// Threads must NOT have Facebook Page scopes.
	if strings.Contains(scopes, "pages_manage_posts") {
		t.Errorf("Threads URL contains Facebook scope pages_manage_posts: %q", scopes)
	}
	if strings.Contains(scopes, "pages_read_engagement") {
		t.Errorf("Threads URL contains Facebook scope pages_read_engagement: %q", scopes)
	}
	if strings.Contains(scopes, "pages_show_list") {
		t.Errorf("Threads URL contains Facebook scope pages_show_list: %q", scopes)
	}
}

// TestThreadsHandleCallback_TokenDataScopes verifies that the token data
// returned by HandleCallback carries the Threads scopes.
func TestThreadsHandleCallback_TokenDataScopes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "th-short",
			"token_type":   "bearer",
			"expires_in":   3600,
		})
	})
	mux.HandleFunc("/access_token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "th-long",
			"token_type":   "bearer",
			"expires_in":   5184000,
		})
	})
	mux.HandleFunc("/v1.0/me", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": "profile", "name": "User"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestThreadsService(srv)

	_, tokenData, err := svc.HandleCallback(context.Background(), "state", "code")
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}

	if len(tokenData.Scopes) == 0 {
		t.Fatal("tokenData.Scopes is empty, expected threads scopes")
	}
	foundThreads := false
	for _, s := range tokenData.Scopes {
		if s == "threads_basic" || s == "threads_content_publish" {
			foundThreads = true
		}
	}
	if !foundThreads {
		t.Errorf("tokenData.Scopes missing threads scopes: %v", tokenData.Scopes)
	}
}

// TestThreadsDisabledWhenNoRedirectURI verifies that NewThreadsOAuthService
// returns nil when the redirect URI is not configured.
func TestThreadsDisabledWhenNoRedirectURI(t *testing.T) {
	cfg := &config.Config{
		MetaAppID:          "test-id",
		MetaAppSecret:      "test-secret-32-chars-minimum-length",
		ThreadsRedirectURI: "", // disabled
	}
	svc, err := NewThreadsOAuthService(cfg)
	if err != nil {
		t.Fatalf("NewThreadsOAuthService should return nil error when disabled, got: %v", err)
	}
	if svc != nil {
		t.Errorf("NewThreadsOAuthService should return nil service when redirect URI is empty, got: %+v", svc)
	}
}

// TestThreadsCallback_TokenExchangeFails verifies that HandleCallback
// surfaces the error when the initial code exchange fails.
func TestThreadsCallback_TokenExchangeFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"Invalid code"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestThreadsService(srv)

	_, _, err := svc.HandleCallback(context.Background(), "state", "bad-code")
	if err == nil {
		t.Fatal("expected error when code exchange fails")
	}
}

// TestThreadsRefreshOAuthToken verifies that RefreshOAuthToken calls the
// Threads-specific refresh_access_token endpoint.
func TestThreadsRefreshOAuthToken(t *testing.T) {
	var capturedGrantType string

	mux := http.NewServeMux()
	mux.HandleFunc("/refresh_access_token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		capturedGrantType = r.URL.Query().Get("grant_type")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "th-refreshed",
			"token_type":   "bearer",
			"expires_in":   5184000,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestThreadsService(srv)

	tokenData, err := svc.RefreshOAuthToken(context.Background(), "th-existing-long-lived")
	if err != nil {
		t.Fatalf("RefreshOAuthToken: %v", err)
	}
	if tokenData.AccessToken != "th-refreshed" {
		t.Errorf("AccessToken: want th-refreshed, got %q", tokenData.AccessToken)
	}
	if capturedGrantType != "th_refresh_token" {
		t.Errorf("grant_type: want th_refresh_token, got %q", capturedGrantType)
	}
}
