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
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// facebookTestCfg returns a minimal config for Facebook OAuth tests.
func facebookTestCfg() *config.Config {
	return &config.Config{
		MetaAppID:           "test-meta-app-id",
		MetaAppSecret:       "test-meta-app-secret-must-be-32-chars-min",
		FacebookRedirectURI: "http://localhost:8080/api/v1/auth/facebook/callback",
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

// =========================================================================
// Publisher tests (Taglio 5c point 8): Facebook Page publishing
// =========================================================================

// TestFacebookPublishesTextPost verifies that Publish sends a text-only
// post to the Page's /feed endpoint and returns the remote post ID.
func TestFacebookPublishesTextPost(t *testing.T) {
	const pageID = "123456789"
	const pageAccessToken = "page-token-abc"
	const expectedPostID = "123456789_999888777"

	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/me/accounts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET /me/accounts, got %s", r.Method)
		}
		json.NewEncoder(w).Encode(models.MetaAccountsResponse{
			Data: []models.MetaPage{
				{AccessToken: pageAccessToken, ID: pageID, Name: "Test Page"},
			},
		})
	})
	var capturedMessage string
	mux.HandleFunc("/v19.0/"+pageID+"/feed", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST /feed, got %s", r.Method)
		}
		capturedMessage = r.URL.Query().Get("message")
		if at := r.URL.Query().Get("access_token"); at != pageAccessToken {
			t.Errorf("access_token: want %q, got %q", pageAccessToken, at)
		}
		json.NewEncoder(w).Encode(map[string]string{"id": expectedPostID})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestFacebookService(srv)

	result, err := svc.Publish(context.Background(), "user-token", "fb-user-id",
		models.PublishPayload{Text: "Hello from Facebook test!"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.PlatformMediaID != expectedPostID {
		t.Errorf("PlatformMediaID: want %q, got %q", expectedPostID, result.PlatformMediaID)
	}
	if capturedMessage != "Hello from Facebook test!" {
		t.Errorf("message: want %q, got %q", "Hello from Facebook test!", capturedMessage)
	}
}

// TestFacebookPublishesSingleImage verifies that Publish sends a single-image
// post to the Page's /photos endpoint with the correct url and caption.
func TestFacebookPublishesSingleImage(t *testing.T) {
	const pageID = "987654321"
	const pageAccessToken = "page-token-xyz"
	const expectedPostID = "987654321_111222333"
	const imageURL = "https://cdn.example.com/photo.jpg"
	const caption = "My beautiful photo"

	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/me/accounts", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(models.MetaAccountsResponse{
			Data: []models.MetaPage{
				{AccessToken: pageAccessToken, ID: pageID, Name: "Photo Page"},
			},
		})
	})
	var capturedURL, capturedCaption string
	mux.HandleFunc("/v19.0/"+pageID+"/photos", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST /photos, got %s", r.Method)
		}
		capturedURL = r.URL.Query().Get("url")
		capturedCaption = r.URL.Query().Get("caption")
		if at := r.URL.Query().Get("access_token"); at != pageAccessToken {
			t.Errorf("access_token: want %q, got %q", pageAccessToken, at)
		}
		json.NewEncoder(w).Encode(map[string]string{"id": expectedPostID})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestFacebookService(srv)

	result, err := svc.Publish(context.Background(), "user-token", "fb-user-id",
		models.PublishPayload{ImageURL: imageURL, Text: caption})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.PlatformMediaID != expectedPostID {
		t.Errorf("PlatformMediaID: want %q, got %q", expectedPostID, result.PlatformMediaID)
	}
	if capturedURL != imageURL {
		t.Errorf("image url: want %q, got %q", imageURL, capturedURL)
	}
	if capturedCaption != caption {
		t.Errorf("caption: want %q, got %q", caption, capturedCaption)
	}
}

// TestFacebookReturnsRemotePostID verifies that Publish returns the
// PlatformMediaID from the Graph API response.
func TestFacebookReturnsRemotePostID(t *testing.T) {
	const pageID = "111111"
	const expectedPostID = "111111_222222"

	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/me/accounts", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(models.MetaAccountsResponse{
			Data: []models.MetaPage{
				{AccessToken: "tok", ID: pageID, Name: "P"},
			},
		})
	})
	mux.HandleFunc("/v19.0/"+pageID+"/feed", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": expectedPostID})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestFacebookService(srv)

	result, err := svc.Publish(context.Background(), "user-token", "fb-user-id",
		models.PublishPayload{Text: "test"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.PlatformMediaID != expectedPostID {
		t.Errorf("PlatformMediaID: want %q, got %q", expectedPostID, result.PlatformMediaID)
	}
}

// TestFacebookReturnsRemotePostURL verifies that Publish returns the correct
// PlatformURL format: https://www.facebook.com/{post_id}.
func TestFacebookReturnsRemotePostURL(t *testing.T) {
	const pageID = "333333"
	const postID = "333333_444444"

	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/me/accounts", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(models.MetaAccountsResponse{
			Data: []models.MetaPage{
				{AccessToken: "tok", ID: pageID, Name: "P"},
			},
		})
	})
	mux.HandleFunc("/v19.0/"+pageID+"/feed", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": postID})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestFacebookService(srv)

	result, err := svc.Publish(context.Background(), "user-token", "fb-user-id",
		models.PublishPayload{Text: "url test"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	expectedURL := "https://www.facebook.com/" + postID
	if result.PlatformURL != expectedURL {
		t.Errorf("PlatformURL: want %q, got %q", expectedURL, result.PlatformURL)
	}
}

// TestFacebookRejectsEmptyPost verifies that Publish returns an error when
// both Text and ImageURL are empty. Note: Publish calls getPages first,
// then checks content — so the pages API is called once before the
// validation failure is surfaced.
func TestFacebookRejectsEmptyPost(t *testing.T) {
	mux := http.NewServeMux()
	apiHits := 0
	mux.HandleFunc("/v19.0/me/accounts", func(w http.ResponseWriter, r *http.Request) {
		apiHits++
		json.NewEncoder(w).Encode(models.MetaAccountsResponse{
			Data: []models.MetaPage{
				{AccessToken: "tok", ID: "page-1", Name: "P"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestFacebookService(srv)

	_, err := svc.Publish(context.Background(), "user-token", "fb-user-id",
		models.PublishPayload{})
	if err == nil {
		t.Fatal("expected error for empty payload, got nil")
	}
	if !strings.Contains(err.Error(), "requires") && !strings.Contains(err.Error(), "text or media") {
		t.Errorf("error message should mention missing content: %v", err)
	}
	// getPages is called before content check — 1 API call is expected.
	if apiHits != 1 {
		t.Errorf("expected 1 API call (getPages before validation), got %d", apiHits)
	}
}

// TestFacebookRejectsVideoInV1 verifies that a VideoURL-only payload
// is rejected. Publish calls getPages first, then routes to /feed
// (text) or /photos (image) — VideoURL matches neither and hits the
// else branch with "requires text or media". Video publishing to
// Pages is not implemented in v1.
func TestFacebookRejectsVideoInV1(t *testing.T) {
	mux := http.NewServeMux()
	apiHits := 0
	mux.HandleFunc("/v19.0/me/accounts", func(w http.ResponseWriter, r *http.Request) {
		apiHits++
		json.NewEncoder(w).Encode(models.MetaAccountsResponse{
			Data: []models.MetaPage{
				{AccessToken: "tok", ID: "page-1", Name: "P"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestFacebookService(srv)

	_, err := svc.Publish(context.Background(), "user-token", "fb-user-id",
		models.PublishPayload{VideoURL: "https://cdn.example.com/video.mp4"})
	if err == nil {
		t.Fatal("expected error for video-only payload in v1, got nil")
	}
	// getPages is called before content routing — 1 API call is expected.
	if apiHits != 1 {
		t.Errorf("expected 1 API call (getPages before content check), got %d", apiHits)
	}
}

// TestFacebookPublishesSingleImageOnly verifies that v1 Facebook publishing
// supports exactly one image per post. The ImageURL field is a string, not
// a []string — the API contract enforces single-image. Only one POST to
// /photos is expected.
func TestFacebookPublishesSingleImageOnly(t *testing.T) {
	// Facebook v1 publishes a single photo via /{page}/photos with one url param.
	// The ImageURL field is a string, not a []string — the API contract enforces
	// single-image. This test verifies that the single-image path is the one used.
	const imageURL = "https://cdn.example.com/single.jpg"

	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/me/accounts", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(models.MetaAccountsResponse{
			Data: []models.MetaPage{
				{AccessToken: "tok", ID: "page-1", Name: "P"},
			},
		})
	})
	var photoURLs []string
	mux.HandleFunc("/v19.0/page-1/photos", func(w http.ResponseWriter, r *http.Request) {
		photoURLs = append(photoURLs, r.URL.Query().Get("url"))
		json.NewEncoder(w).Encode(map[string]string{"id": "post-id"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestFacebookService(srv)

	_, err := svc.Publish(context.Background(), "user-token", "fb-user-id",
		models.PublishPayload{ImageURL: imageURL})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// Exactly one POST to /photos with the single image URL.
	if len(photoURLs) != 1 {
		t.Errorf("photos calls: want 1, got %d — v1 only supports single-image", len(photoURLs))
	}
	if len(photoURLs) == 1 && photoURLs[0] != imageURL {
		t.Errorf("photo url: want %q, got %q", imageURL, photoURLs[0])
	}
}

// TestFacebookRejectsPersonalProfile verifies that Publish returns an error
// when the user has no Pages (personal profile cannot publish via Graph API).
func TestFacebookRejectsPersonalProfile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/me/accounts", func(w http.ResponseWriter, r *http.Request) {
		// Return empty page list — user has no Pages.
		json.NewEncoder(w).Encode(models.MetaAccountsResponse{
			Data: []models.MetaPage{},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestFacebookService(srv)

	_, err := svc.Publish(context.Background(), "user-token", "fb-user-id",
		models.PublishPayload{Text: "trying to post to personal profile"})
	if err == nil {
		t.Fatal("expected error for personal profile (no Pages), got nil")
	}
	if !strings.Contains(err.Error(), "no Facebook Page found") {
		t.Errorf("error should mention missing Page: %v", err)
	}
}

// TestFacebookHandlesMissingPermission verifica che una risposta 403 da
// /me/accounts (permissione mancante) venga propagata come errore.
func TestFacebookHandlesMissingPermission(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/me/accounts", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":{"message":"(#10) This endpoint requires the pages_show_list permission","type":"OAuthException","code":10}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestFacebookService(srv)

	_, err := svc.Publish(context.Background(), "user-token", "fb-user-id",
		models.PublishPayload{Text: "should fail"})
	if err == nil {
		t.Fatal("expected error for 403 /me/accounts, got nil")
	}
	if !strings.Contains(err.Error(), "403") && !strings.Contains(err.Error(), "pages") {
		t.Errorf("error should reference 403 or missing permission: %v", err)
	}
}

// TestFacebookHandlesExpiredToken verifies that a 401 response from
// /me/accounts (expired/revoked token) is propagated as an error.
func TestFacebookHandlesExpiredToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/me/accounts", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"The access token has expired","type":"OAuthException","code":190}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestFacebookService(srv)

	_, err := svc.Publish(context.Background(), "expired-token", "fb-user-id",
		models.PublishPayload{Text: "should fail with 401"})
	if err == nil {
		t.Fatal("expected error for 401 /me/accounts, got nil")
	}
	if !strings.Contains(err.Error(), "401") && !strings.Contains(err.Error(), "accounts") {
		t.Errorf("error should reference 401 or accounts failure: %v", err)
	}
}

// TestFacebookHandlesRateLimit verifies that a 429 (rate limit) from
// /me/accounts is propagated as an error — the worker can retry.
func TestFacebookHandlesRateLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/me/accounts", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"(#4) Application request limit reached","type":"OAuthException","code":4}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestFacebookService(srv)

	_, err := svc.Publish(context.Background(), "user-token", "fb-user-id",
		models.PublishPayload{Text: "should hit rate limit"})
	if err == nil {
		t.Fatal("expected error for 429, got nil")
	}
	if !strings.Contains(err.Error(), "429") && !strings.Contains(err.Error(), "accounts") {
		t.Errorf("error should reference 429: %v", err)
	}
}

// TestFacebookHandlesProvider5xx verifies that 500, 502, and 503 responses
// from /me/accounts are surfaced as errors (the worker can retry).
func TestFacebookHandlesProvider5xx(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/v19.0/me/accounts", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				w.Write([]byte(`{"error":{"message":"Internal server error"}}`))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			svc := newTestFacebookService(srv)

			_, err := svc.Publish(context.Background(), "user-token", "fb-user-id",
				models.PublishPayload{Text: "test"})
			if err == nil {
				t.Fatalf("expected error for %d, got nil", status)
			}
		})
	}
}

// TestFacebookPublish_FeedHTTPError verifies that Publish surfaces errors from
// the /feed endpoint itself (pages lookup OK, publish step fails).
func TestFacebookPublish_FeedHTTPError(t *testing.T) {
	const pageID = "page-500"
	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/me/accounts", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(models.MetaAccountsResponse{
			Data: []models.MetaPage{
				{AccessToken: "tok", ID: pageID, Name: "Error Page"},
			},
		})
	})
	mux.HandleFunc("/v19.0/"+pageID+"/feed", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":{"message":"Upstream error"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestFacebookService(srv)

	_, err := svc.Publish(context.Background(), "user-token", "fb-user-id",
		models.PublishPayload{Text: "test"})
	if err == nil {
		t.Fatal("expected error from /feed 502, got nil")
	}
	if !strings.Contains(err.Error(), "facebook publish failed") {
		t.Errorf("error should be wrapped: %v", err)
	}
}

// TestFacebookPublish_PageAccessTokenPassed verifies that the Page Access Token
// (not the user token) is passed to the /feed endpoint.
func TestFacebookPublish_PageAccessTokenPassed(t *testing.T) {
	const pageAccessToken = "page-level-access-token-12345"
	const pageID = "page-token-test"

	var feedAccessToken string
	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/me/accounts", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(models.MetaAccountsResponse{
			Data: []models.MetaPage{
				{AccessToken: pageAccessToken, ID: pageID, Name: "Token Test"},
			},
		})
	})
	mux.HandleFunc("/v19.0/"+pageID+"/feed", func(w http.ResponseWriter, r *http.Request) {
		feedAccessToken = r.URL.Query().Get("access_token")
		json.NewEncoder(w).Encode(map[string]string{"id": "post-id"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestFacebookService(srv)

	_, err := svc.Publish(context.Background(), "user-level-token-different", "fb-user-id",
		models.PublishPayload{Text: "test page token"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if feedAccessToken != pageAccessToken {
		t.Errorf("feed access_token: want Page Access Token %q, got user token %q",
			pageAccessToken, feedAccessToken)
	}
}
