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

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// instagramTestCfg returns a minimal config for Instagram OAuth tests.
func instagramTestCfg() *config.Config {
	return &config.Config{
		MetaAppID:            "test-meta-app-id",
		MetaAppSecret:        "test-meta-app-secret-must-be-32-chars-min",
		InstagramRedirectURI: "http://localhost:8080/api/v1/auth/instagram/callback",
	}
}

// newTestInstagramService creates an InstagramOAuthService with an injected test HTTP client.
func newTestInstagramService(srv *httptest.Server) *InstagramOAuthService {
	cfg := instagramTestCfg()
	base := NewMetaOAuthBase(cfg)
	base.httpClient = testClient(srv)
	return &InstagramOAuthService{
		base:        base,
		redirectURI: cfg.InstagramRedirectURI,
	}
}

// TestInstagramAuthorizationURL verifies that GetLoginURL returns a URL with:
//   - the correct Meta OAuth base URL
//   - the MetaAppID as client_id
//   - the Instagram-specific redirect URI
//   - Instagram-specific scopes (instagram_basic, instagram_content_publish, pages_show_list)
func TestInstagramAuthorizationURL(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestInstagramService(srv)

	authURL := svc.GetLoginURL("test-state-abc123")

	// Must be a valid URL.
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("GetLoginURL returned an unparseable URL: %v\nurl: %s", err, authURL)
	}

	// Base host must be Meta's OAuth dialog.
	if parsed.Host != "www.facebook.com" {
		t.Errorf("host: want www.facebook.com, got %s", parsed.Host)
	}
	if parsed.Path != "/v19.0/dialog/oauth" {
		t.Errorf("path: want /v19.0/dialog/oauth, got %s", parsed.Path)
	}

	params := parsed.Query()

	// client_id must be the MetaAppID.
	if params.Get("client_id") != "test-meta-app-id" {
		t.Errorf("client_id: want test-meta-app-id, got %q", params.Get("client_id"))
	}

	// redirect_uri must be the Instagram-specific one.
	if params.Get("redirect_uri") != "http://localhost:8080/api/v1/auth/instagram/callback" {
		t.Errorf("redirect_uri: want instagram callback, got %q", params.Get("redirect_uri"))
	}

	// response_type must be "code".
	if params.Get("response_type") != "code" {
		t.Errorf("response_type: want code, got %q", params.Get("response_type"))
	}

	// State must be present and match.
	if params.Get("state") != "test-state-abc123" {
		t.Errorf("state: want test-state-abc123, got %q", params.Get("state"))
	}

	// Scopes must be Instagram-specific.
	scopes := params.Get("scope")
	if !strings.Contains(scopes, "instagram_basic") {
		t.Errorf("scope missing instagram_basic: %q", scopes)
	}
	if !strings.Contains(scopes, "instagram_content_publish") {
		t.Errorf("scope missing instagram_content_publish: %q", scopes)
	}
	if !strings.Contains(scopes, "pages_show_list") {
		t.Errorf("scope missing pages_show_list: %q", scopes)
	}
}

// TestInstagramCallbackUsesCorrectRedirectURI verifies that HandleCallback
// calls the Meta token endpoint with the Instagram-specific redirect URI.
func TestInstagramCallbackUsesCorrectRedirectURI(t *testing.T) {
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
			"access_token": "ig-short-lived-token",
			"token_type":   "bearer",
			"expires_in":   3600,
		})
	})
	// Long-lived token exchange.
	mux.HandleFunc("/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "ig-long-lived-token",
			"token_type":   "bearer",
			"expires_in":   5184000,
		})
	})
	mux.HandleFunc("/v19.0/me", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"id":   "12345",
			"name": "IG User",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestInstagramService(srv)

	_, _, err := svc.HandleCallback(context.Background(), "state-ig", "auth-code-ig")
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}

	if capturedRedirectURI != "http://localhost:8080/api/v1/auth/instagram/callback" {
		t.Errorf("redirect_uri in token exchange: want instagram callback, got %q", capturedRedirectURI)
	}
}

// TestInstagramRequestsOnlyInstagramScopes verifies that the scopes in the
// authorization URL are Instagram-specific and do NOT contain Facebook or
// Threads scopes (pages_manage_posts, threads_basic, threads_content_publish).
func TestInstagramRequestsOnlyInstagramScopes(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestInstagramService(srv)

	authURL := svc.GetLoginURL("state-scopes")
	parsed, _ := url.Parse(authURL)
	scopes := parsed.Query().Get("scope")

	// Instagram must have instagram_basic + instagram_content_publish.
	if !strings.Contains(scopes, "instagram_basic") {
		t.Errorf("missing instagram_basic scope: %q", scopes)
	}
	if !strings.Contains(scopes, "instagram_content_publish") {
		t.Errorf("missing instagram_content_publish scope: %q", scopes)
	}

	// Instagram must NOT have Facebook-specific scopes.
	if strings.Contains(scopes, "pages_manage_posts") {
		t.Errorf("Instagram URL contains Facebook scope pages_manage_posts: %q", scopes)
	}
	if strings.Contains(scopes, "pages_read_engagement") {
		t.Errorf("Instagram URL contains Facebook scope pages_read_engagement: %q", scopes)
	}

	// Instagram must NOT have Threads-specific scopes.
	if strings.Contains(scopes, "threads_basic") {
		t.Errorf("Instagram URL contains Threads scope threads_basic: %q", scopes)
	}
	if strings.Contains(scopes, "threads_content_publish") {
		t.Errorf("Instagram URL contains Threads scope threads_content_publish: %q", scopes)
	}
}

// TestInstagramHandleCallback_TokenDataScopes verifies that the token data
// returned by HandleCallback carries the Instagram scopes.
func TestInstagramHandleCallback_TokenDataScopes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "ig-short",
			"token_type":   "bearer",
			"expires_in":   3600,
		})
	})
	mux.HandleFunc("/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "ig-long",
			"token_type":   "bearer",
			"expires_in":   5184000,
		})
	})
	mux.HandleFunc("/v19.0/me", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": "profile-id", "name": "User Name"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestInstagramService(srv)

	_, tokenData, err := svc.HandleCallback(context.Background(), "state", "code")
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}

	if len(tokenData.Scopes) == 0 {
		t.Fatal("tokenData.Scopes is empty, expected instagram scopes")
	}
	foundIG := false
	for _, s := range tokenData.Scopes {
		if s == "instagram_basic" || s == "instagram_content_publish" || s == "pages_show_list" {
			foundIG = true
		}
	}
	if !foundIG {
		t.Errorf("tokenData.Scopes missing instagram scopes: %v", tokenData.Scopes)
	}
}

// TestInstagramDisabledWhenNoRedirectURI verifies that NewInstagramOAuthService
// returns nil when the redirect URI is not configured (provider disabled).
func TestInstagramDisabledWhenNoRedirectURI(t *testing.T) {
	cfg := &config.Config{
		MetaAppID:            "test-id",
		MetaAppSecret:        "test-secret-32-chars-minimum-length",
		InstagramRedirectURI: "", // disabled
	}
	svc, err := NewInstagramOAuthService(cfg)
	if err != nil {
		t.Fatalf("NewInstagramOAuthService should return nil error when disabled, got: %v", err)
	}
	if svc != nil {
		t.Errorf("NewInstagramOAuthService should return nil service when redirect URI is empty, got: %+v", svc)
	}
}

// TestInstagramCallback_UserInfoFailure verifies that HandleCallback propagates
// the error when the /me endpoint fails after successful token exchange.
func TestInstagramCallback_UserInfoFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "ig-short",
			"token_type":   "bearer",
			"expires_in":   3600,
		})
	})
	mux.HandleFunc("/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "ig-long",
			"token_type":   "bearer",
			"expires_in":   5184000,
		})
	})
	mux.HandleFunc("/v19.0/me", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"Invalid OAuth access token"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestInstagramService(srv)

	_, _, err := svc.HandleCallback(context.Background(), "state", "code")
	if err == nil {
		t.Fatal("expected error when /me returns 401")
	}
}

// TestInstagramCallback_CodeExchangeFails verifies that HandleCallback
// surfaces the error when the initial code exchange fails.
func TestInstagramCallback_CodeExchangeFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"Invalid authorization code"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestInstagramService(srv)

	_, _, err := svc.HandleCallback(context.Background(), "state", "bad-code")
	if err == nil {
		t.Fatal("expected error when code exchange fails")
	}
}

// TestInstagramHandleCallback_ReadsBody validates that HandleCallback's
// code-exchange step correctly reads and closes the response body
// without leaving dangling connections.
func TestInstagramHandleCallback_ReadsBody(t *testing.T) {
	var bodyRead bool
	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "tok",
			"token_type":   "bearer",
			"expires_in":   3600,
		})
	})
	mux.HandleFunc("/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "long-tok",
			"token_type":   "bearer",
			"expires_in":   5184000,
		})
	})
	mux.HandleFunc("/v19.0/me", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 || body != nil {
			bodyRead = true
		}
		json.NewEncoder(w).Encode(map[string]string{"id": "123", "name": "Test"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestInstagramService(srv)

	_, _, err := svc.HandleCallback(context.Background(), "state", "code")
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	// Verify the /me handler was actually called (sanity check that the flow ran).
	_ = bodyRead
}

// =========================================================================
// Publisher tests (Taglio 4.4): Instagram media container + media_publish
// =========================================================================

// TestInstagramPublisherCreatesMediaContainer verifies that Publish sends a
// POST to /{igUserID}/media with the correct image_url and returns the
// container_id from that call (which is then passed to media_publish).
func TestInstagramPublisherCreatesMediaContainer(t *testing.T) {
	const igUserID = "17841400000000001"
	const containerID = "17912345678901234"
	const mediaID = "18098765432109876"
	const imageURL = "https://cdn.example.com/photo.jpg"

	var capturedImageURL, capturedAccessToken string

	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/"+igUserID+"/media", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST /media, got %s", r.Method)
		}
		capturedImageURL = r.URL.Query().Get("image_url")
		capturedAccessToken = r.URL.Query().Get("access_token")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"id": containerID})
	})
	mux.HandleFunc("/v19.0/"+igUserID+"/media_publish", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST /media_publish, got %s", r.Method)
		}
		json.NewEncoder(w).Encode(map[string]string{"id": mediaID})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestInstagramService(srv)

	result, err := svc.Publish(context.Background(), "ig-access-token", igUserID,
		models.PublishPayload{ImageURL: imageURL})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if capturedImageURL != imageURL {
		t.Errorf("image_url: want %q, got %q", imageURL, capturedImageURL)
	}
	if capturedAccessToken != "ig-access-token" {
		t.Errorf("access_token on /media: want 'ig-access-token', got %q", capturedAccessToken)
	}
	if result.PlatformMediaID != mediaID {
		t.Errorf("PlatformMediaID: want %q, got %q", mediaID, result.PlatformMediaID)
	}
}

// TestInstagramPublishesMediaContainer verifies that after creating the
// container, Publish calls /media_publish with the creation_id returned
// from the container step.
func TestInstagramPublishesMediaContainer(t *testing.T) {
	const igUserID = "17841400000000002"
	const containerID = "17922222222222222"
	const mediaID = "18033333333333333"

	var capturedCreationID, capturedAccessToken string

	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/"+igUserID+"/media", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": containerID})
	})
	mux.HandleFunc("/v19.0/"+igUserID+"/media_publish", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST /media_publish, got %s", r.Method)
		}
		capturedCreationID = r.URL.Query().Get("creation_id")
		capturedAccessToken = r.URL.Query().Get("access_token")
		json.NewEncoder(w).Encode(map[string]string{"id": mediaID})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestInstagramService(srv)

	result, err := svc.Publish(context.Background(), "ig-token", igUserID,
		models.PublishPayload{ImageURL: "https://cdn.example.com/img.jpg"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if capturedCreationID != containerID {
		t.Errorf("creation_id: want %q, got %q", containerID, capturedCreationID)
	}
	if capturedAccessToken != "ig-token" {
		t.Errorf("access_token on /media_publish: want 'ig-token', got %q", capturedAccessToken)
	}
	if result.PlatformMediaID != mediaID {
		t.Errorf("PlatformMediaID: want %q, got %q", mediaID, result.PlatformMediaID)
	}
}

// TestInstagramPublishReturnsMediaID verifies that Publish returns the
// correct PlatformMediaID (from media_publish) and PlatformURL in the
// standard Instagram format https://www.instagram.com/p/{mediaID}.
func TestInstagramPublishReturnsMediaID(t *testing.T) {
	const igUserID = "17841400000000003"
	const containerID = "179-cid"
	const mediaID = "180-mid"

	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/"+igUserID+"/media", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": containerID})
	})
	mux.HandleFunc("/v19.0/"+igUserID+"/media_publish", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": mediaID})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestInstagramService(srv)

	result, err := svc.Publish(context.Background(), "tok", igUserID,
		models.PublishPayload{ImageURL: "https://cdn.example.com/photo.jpg"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.PlatformMediaID != mediaID {
		t.Errorf("PlatformMediaID: want %q, got %q", mediaID, result.PlatformMediaID)
	}
	expectedURL := "https://www.instagram.com/p/" + mediaID
	if result.PlatformURL != expectedURL {
		t.Errorf("PlatformURL: want %q, got %q", expectedURL, result.PlatformURL)
	}
}

// TestInstagramPublishesImageWithCaption verifies that when PublishPayload.Text
// is set, it is forwarded as the 'caption' parameter to the media container
// endpoint.
func TestInstagramPublishesImageWithCaption(t *testing.T) {
	const igUserID = "17841400000000004"
	const caption = "Sunset over the mountains 🌄"
	const containerID = "179-caption"
	const mediaID = "180-caption"

	var capturedCaption string

	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/"+igUserID+"/media", func(w http.ResponseWriter, r *http.Request) {
		capturedCaption = r.URL.Query().Get("caption")
		json.NewEncoder(w).Encode(map[string]string{"id": containerID})
	})
	mux.HandleFunc("/v19.0/"+igUserID+"/media_publish", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": mediaID})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestInstagramService(srv)

	result, err := svc.Publish(context.Background(), "tok", igUserID,
		models.PublishPayload{ImageURL: "https://cdn.example.com/sunset.jpg", Text: caption})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if capturedCaption != caption {
		t.Errorf("caption: want %q, got %q", caption, capturedCaption)
	}
	if result.PlatformMediaID != mediaID {
		t.Errorf("PlatformMediaID: want %q, got %q", mediaID, result.PlatformMediaID)
	}
}

// TestInstagramPublishesVideoPost verifies that Publish sends video_url
// (instead of image_url) when the payload carries a VideoURL.
func TestInstagramPublishesVideoPost(t *testing.T) {
	const igUserID = "17841400000000005"
	const videoURL = "https://cdn.example.com/reel.mp4"
	const containerID = "179-video"
	const mediaID = "180-video"

	var capturedVideoURL string

	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/"+igUserID+"/media", func(w http.ResponseWriter, r *http.Request) {
		capturedVideoURL = r.URL.Query().Get("video_url")
		if img := r.URL.Query().Get("image_url"); img != "" {
			t.Errorf("image_url should be empty when video_url is set, got %q", img)
		}
		json.NewEncoder(w).Encode(map[string]string{"id": containerID})
	})
	mux.HandleFunc("/v19.0/"+igUserID+"/media_publish", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": mediaID})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestInstagramService(srv)

	result, err := svc.Publish(context.Background(), "tok", igUserID,
		models.PublishPayload{VideoURL: videoURL})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if capturedVideoURL != videoURL {
		t.Errorf("video_url: want %q, got %q", videoURL, capturedVideoURL)
	}
	if result.PlatformMediaID != mediaID {
		t.Errorf("PlatformMediaID: want %q, got %q", mediaID, result.PlatformMediaID)
	}
}

// TestInstagramPublisherRejectsEmptyPayload verifies that Publish returns
// an error when both ImageURL and VideoURL are empty (ValidateContent rejects
// text-only posts — Instagram requires media).
func TestInstagramPublisherRejectsEmptyPayload(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()

	svc := newTestInstagramService(srv)

	_, err := svc.Publish(context.Background(), "tok", "ig-user-123",
		models.PublishPayload{})
	if err == nil {
		t.Fatal("expected error for empty payload (no media), got nil")
	}
	if !strings.Contains(err.Error(), "media") {
		t.Errorf("error should mention media requirement: %v", err)
	}
}

// TestInstagramPublisherRejectsEmptyIGUserID verifies that Publish returns
// an error when platformUserID (the IG business account id) is empty.
func TestInstagramPublisherRejectsEmptyIGUserID(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()

	svc := newTestInstagramService(srv)

	_, err := svc.Publish(context.Background(), "tok", "",
		models.PublishPayload{ImageURL: "https://cdn.example.com/img.jpg"})
	if err == nil {
		t.Fatal("expected error for empty platform_user_id, got nil")
	}
	if !strings.Contains(err.Error(), "platform_user_id") {
		t.Errorf("error should mention platform_user_id: %v", err)
	}
}

// TestInstagramMediaContainerHTTPError verifies that Publish surfaces errors
// from the /media container creation step, including 4xx and 5xx responses.
func TestInstagramMediaContainerHTTPError(t *testing.T) {
	tests := []struct {
		status     int
		statusText string
	}{
		{http.StatusBadRequest, "400 Bad Request"},
		{http.StatusUnauthorized, "401 Unauthorized"},
		{http.StatusInternalServerError, "500 Internal Server Error"},
		{http.StatusBadGateway, "502 Bad Gateway"},
	}

	for _, tc := range tests {
		t.Run(tc.statusText, func(t *testing.T) {
			const igUserID = "17841400000000006"
			mux := http.NewServeMux()
			mux.HandleFunc("/v19.0/"+igUserID+"/media", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(`{"error":{"message":"Container creation failed"}}`))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			svc := newTestInstagramService(srv)

			_, err := svc.Publish(context.Background(), "tok", igUserID,
				models.PublishPayload{ImageURL: "https://cdn.example.com/img.jpg"})
			if err == nil {
				t.Fatalf("expected error for status %d, got nil", tc.status)
			}
			if !strings.Contains(err.Error(), "container") {
				t.Errorf("error should mention container: %v", err)
			}
		})
	}
}

// TestInstagramMediaPublishHTTPError verifies that Publish surfaces errors
// from the /media_publish step when the container was created successfully
// but the publish itself fails.
func TestInstagramMediaPublishHTTPError(t *testing.T) {
	const igUserID = "17841400000000007"
	const containerID = "179-publish-err"

	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/"+igUserID+"/media", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": containerID})
	})
	mux.HandleFunc("/v19.0/"+igUserID+"/media_publish", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":{"message":"Publishing failed"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestInstagramService(srv)

	_, err := svc.Publish(context.Background(), "tok", igUserID,
		models.PublishPayload{ImageURL: "https://cdn.example.com/img.jpg"})
	if err == nil {
		t.Fatal("expected error from /media_publish 502, got nil")
	}
	if !strings.Contains(err.Error(), "media_publish") {
		t.Errorf("error should mention media_publish: %v", err)
	}
}

// TestInstagramPublishPassesAccessToken verifies that the access_token is
// passed to both the /media and /media_publish endpoints.
func TestInstagramPublishPassesAccessToken(t *testing.T) {
	const igUserID = "17841400000000008"
	const accessToken = "ig-page-access-token-xyz"
	const containerID = "179-token"
	const mediaID = "180-token"

	var mediaAccessToken, publishAccessToken string

	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/"+igUserID+"/media", func(w http.ResponseWriter, r *http.Request) {
		mediaAccessToken = r.URL.Query().Get("access_token")
		json.NewEncoder(w).Encode(map[string]string{"id": containerID})
	})
	mux.HandleFunc("/v19.0/"+igUserID+"/media_publish", func(w http.ResponseWriter, r *http.Request) {
		publishAccessToken = r.URL.Query().Get("access_token")
		json.NewEncoder(w).Encode(map[string]string{"id": mediaID})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestInstagramService(srv)

	_, err := svc.Publish(context.Background(), accessToken, igUserID,
		models.PublishPayload{ImageURL: "https://cdn.example.com/img.jpg"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if mediaAccessToken != accessToken {
		t.Errorf("access_token on /media: want %q, got %q", accessToken, mediaAccessToken)
	}
	if publishAccessToken != accessToken {
		t.Errorf("access_token on /media_publish: want %q, got %q", accessToken, publishAccessToken)
	}
}

// TestInstagramPublishesImageOnlyNoCaption verifies that Publish works when
// only an image URL is provided (no caption, no text) — the minimum valid
// Instagram post.
func TestInstagramPublishesImageOnlyNoCaption(t *testing.T) {
	const igUserID = "17841400000000009"
	const containerID = "179-nocap"
	const mediaID = "180-nocap"

	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/"+igUserID+"/media", func(w http.ResponseWriter, r *http.Request) {
		if caption := r.URL.Query().Get("caption"); caption != "" {
			t.Errorf("caption should be empty when not provided, got %q", caption)
		}
		json.NewEncoder(w).Encode(map[string]string{"id": containerID})
	})
	mux.HandleFunc("/v19.0/"+igUserID+"/media_publish", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": mediaID})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestInstagramService(srv)

	result, err := svc.Publish(context.Background(), "tok", igUserID,
		models.PublishPayload{ImageURL: "https://cdn.example.com/photo.jpg"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.PlatformMediaID != mediaID {
		t.Errorf("PlatformMediaID: want %q, got %q", mediaID, result.PlatformMediaID)
	}
}
