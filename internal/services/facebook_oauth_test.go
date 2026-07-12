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

// facebookTestCfg returns a minimal config for Facebook OAuth tests.
func facebookTestCfg() *config.Config {
	return &config.Config{
		MetaAppID:            "test-meta-app-id",
		MetaAppSecret:        "test-meta-app-secret-must-be-32-chars-min",
		FacebookRedirectURI:  "http://localhost:8080/api/v1/auth/facebook/callback",
	}
}

// newTestFacebookService creates a FacebookOAuthService with an injected test HTTP client.
func newTestFacebookService(srv *httptest.Server) *FacebookOAuthService {
	cfg := facebookTestCfg()
	base := NewMetaOAuthBase(cfg)
	base.httpClient = testClient(srv)
	return &FacebookOAuthService{
		base:        base,
		redirectURI: cfg.FacebookRedirectURI,
	}
}

// TestFacebookAuthorizationURL verifies that GetLoginURL returns a URL with:
//   - the correct Meta OAuth base URL
//   - the MetaAppID as client_id
//   - the Facebook-specific redirect URI
//   - Page-management scopes (pages_manage_posts, pages_read_engagement, pages_show_list)
func TestFacebookAuthorizationURL(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestFacebookService(srv)

	authURL := svc.GetLoginURL("fb-state-xyz")

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("GetLoginURL returned an unparseable URL: %v\nurl: %s", err, authURL)
	}

	if parsed.Host != "www.facebook.com" {
		t.Errorf("host: want www.facebook.com, got %s", parsed.Host)
	}
	if parsed.Path != "/v19.0/dialog/oauth" {
		t.Errorf("path: want /v19.0/dialog/oauth, got %s", parsed.Path)
	}

	params := parsed.Query()

	if params.Get("client_id") != "test-meta-app-id" {
		t.Errorf("client_id: want test-meta-app-id, got %q", params.Get("client_id"))
	}

	if params.Get("redirect_uri") != "http://localhost:8080/api/v1/auth/facebook/callback" {
		t.Errorf("redirect_uri: want facebook callback, got %q", params.Get("redirect_uri"))
	}

	if params.Get("response_type") != "code" {
		t.Errorf("response_type: want code, got %q", params.Get("response_type"))
	}

	if params.Get("state") != "fb-state-xyz" {
		t.Errorf("state: want fb-state-xyz, got %q", params.Get("state"))
	}

	scopes := params.Get("scope")
	if scopes == "" {
		t.Fatal("scope is empty — Facebook must request Page scopes")
	}
}

// TestFacebookCallbackUsesCorrectRedirectURI verifies that HandleCallback
// calls the Meta token endpoint with the Facebook-specific redirect URI.
func TestFacebookCallbackUsesCorrectRedirectURI(t *testing.T) {
	var capturedRedirectURI string

	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		// Only capture redirect_uri from the initial code exchange
		// (the second call — ExchangeForLongLivedToken — also hits
		// this path but without a "code" param; skip overwriting).
		if r.URL.Query().Get("code") != "" {
			capturedRedirectURI = r.URL.Query().Get("redirect_uri")
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "fb-short-lived",
			"token_type":   "bearer",
			"expires_in":   3600,
		})
	})
	mux.HandleFunc("/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "fb-long-lived",
			"token_type":   "bearer",
			"expires_in":   5184000,
		})
	})
	mux.HandleFunc("/v19.0/me", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"id":   "fb-user-id",
			"name": "FB User",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestFacebookService(srv)

	_, _, err := svc.HandleCallback(context.Background(), "state-fb", "auth-code-fb")
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}

	if capturedRedirectURI != "http://localhost:8080/api/v1/auth/facebook/callback" {
		t.Errorf("redirect_uri in token exchange: want facebook callback, got %q", capturedRedirectURI)
	}
}

// TestFacebookRequestsPageScopes verifies that the scopes in the authorization
// URL are Page-specific and do NOT contain Instagram or Threads scopes.
func TestFacebookRequestsPageScopes(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestFacebookService(srv)

	authURL := svc.GetLoginURL("fb-scope-test")
	parsed, _ := url.Parse(authURL)
	scopes := parsed.Query().Get("scope")

	// Facebook must have Page management scopes.
	if !strings.Contains(scopes, "pages_manage_posts") {
		t.Errorf("missing pages_manage_posts scope: %q", scopes)
	}
	if !strings.Contains(scopes, "pages_read_engagement") {
		t.Errorf("missing pages_read_engagement scope: %q", scopes)
	}
	if !strings.Contains(scopes, "pages_show_list") {
		t.Errorf("missing pages_show_list scope: %q", scopes)
	}

	// Facebook must NOT have Instagram scopes.
	if strings.Contains(scopes, "instagram_basic") {
		t.Errorf("Facebook URL contains Instagram scope instagram_basic: %q", scopes)
	}
	if strings.Contains(scopes, "instagram_content_publish") {
		t.Errorf("Facebook URL contains Instagram scope instagram_content_publish: %q", scopes)
	}

	// Facebook must NOT have Threads scopes.
	if strings.Contains(scopes, "threads_basic") {
		t.Errorf("Facebook URL contains Threads scope threads_basic: %q", scopes)
	}
	if strings.Contains(scopes, "threads_content_publish") {
		t.Errorf("Facebook URL contains Threads scope threads_content_publish: %q", scopes)
	}
}

// TestFacebookHandleCallback_TokenDataScopes verifies that the token data
// returned by HandleCallback carries the Facebook scopes.
func TestFacebookHandleCallback_TokenDataScopes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "fb-short",
			"token_type":   "bearer",
			"expires_in":   3600,
		})
	})
	mux.HandleFunc("/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "fb-long",
			"token_type":   "bearer",
			"expires_in":   5184000,
		})
	})
	mux.HandleFunc("/v19.0/me", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": "profile", "name": "User"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestFacebookService(srv)

	_, tokenData, err := svc.HandleCallback(context.Background(), "state", "code")
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}

	if len(tokenData.Scopes) == 0 {
		t.Fatal("tokenData.Scopes is empty, expected facebook scopes")
	}
	foundPages := false
	for _, s := range tokenData.Scopes {
		if s == "pages_manage_posts" || s == "pages_read_engagement" || s == "pages_show_list" {
			foundPages = true
		}
	}
	if !foundPages {
		t.Errorf("tokenData.Scopes missing facebook page scopes: %v", tokenData.Scopes)
	}
}

// TestFacebookDisabledWhenNoRedirectURI verifies that NewFacebookOAuthService
// returns nil when the redirect URI is not configured.
func TestFacebookDisabledWhenNoRedirectURI(t *testing.T) {
	cfg := &config.Config{
		MetaAppID:           "test-id",
		MetaAppSecret:       "test-secret-32-chars-minimum-length",
		FacebookRedirectURI: "", // disabled
	}
	svc, err := NewFacebookOAuthService(cfg)
	if err != nil {
		t.Fatalf("NewFacebookOAuthService should return nil error when disabled, got: %v", err)
	}
	if svc != nil {
		t.Errorf("NewFacebookOAuthService should return nil service when redirect URI is empty, got: %+v", svc)
	}
}
