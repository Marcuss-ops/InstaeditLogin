package services

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestYouTubeLoginURL_IncludesRequiredScopes verifies that GetLoginURL
// requests the YouTube scopes required by the publish pipeline (upload,
// readonly) along with the operator-identity scopes (openid, email,
// profile). `yt-analytics.readonly` is intentionally absent from the
// requested scope set (least-privilege; docs/OAUTH-PRODUCTION.md Step 3
// "Code-side guard"): `videos.insert` accepts `youtube.upload` alone,
// and re-introducing the analytics scope would re-open Google's brand
// verification queue with zero functional gain. A negative assertion
// in the test body confirms the analytics scope is NOT requested.
func TestYouTubeLoginURL_IncludesRequiredScopes(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	authURL := svc.GetLoginURL("yt-state")

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("GetLoginURL returned unparseable URL: %v\nurl: %s", err, authURL)
	}

	params := parsed.Query()
	scopes := params.Get("scope")

	for _, want := range []string{
		"https://www.googleapis.com/auth/youtube.upload",
		"https://www.googleapis.com/auth/youtube.readonly",
		"https://www.googleapis.com/auth/youtube.force-ssl",
		"openid",
		"email",
		"profile",
	} {
		if !containsScope(scopes, want) {
			t.Errorf("scope missing %q, got: %s", want, scopes)
		}
	}

	// Negative assertion on the analytics scope: least-privilege
	// policy (docs/OAUTH-PRODUCTION.md Step 3 "Code-side guard").
	// Re-introducing the analytics scope would re-open Google's
	// brand verification queue without delivering any functional
	// gain to the publish pipeline (`videos.insert` accepts
	// `youtube.upload` alone).
	const forbiddenAnalyticsScope = "https://www.googleapis.com/auth/yt-analytics.readonly"
	if containsScope(scopes, forbiddenAnalyticsScope) {
		t.Errorf("scope list MUST NOT contain %q (least-privilege + brand-verification cost); got: %s",
			forbiddenAnalyticsScope, scopes)
	}

	if params.Get("access_type") != "offline" {
		t.Errorf("access_type: want offline, got %q", params.Get("access_type"))
	}
	if params.Get("include_granted_scopes") != "true" {
		t.Errorf("include_granted_scopes: want true, got %q", params.Get("include_granted_scopes"))
	}
	if prompt := params.Get("prompt"); prompt != "" {
		t.Errorf("normal login must not force prompt=consent, got %q", prompt)
	}
}

// TestYouTubeLoginURL_ExplicitConsentAndAccountSelection verifies that
// explicitly requested OAuth options are encoded as the expected Google
// prompt values. The API routing test covers when add/reconnect select them.
func TestYouTubeLoginURL_ExplicitConsentAndAccountSelection(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	authURL := svc.GetLoginURLWithOptions("state", OAuthLoginOptions{
		ForceConsent:  true,
		SelectAccount: true,
	})

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("unparseable URL: %v", err)
	}

	prompt := parsed.Query().Get("prompt")
	if !containsPrompt(prompt, "consent") {
		t.Errorf("prompt missing consent, got: %s", prompt)
	}
	if !containsPrompt(prompt, "select_account") {
		t.Errorf("prompt missing select_account, got: %s", prompt)
	}
}

// TestYouTubeLoginURL_ReconnectModeForcesConsent verifies that the
// reconnect mode forces consent but does not select_account.
func TestYouTubeLoginURL_ReconnectModeForcesConsent(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	authURL := svc.GetLoginURLWithOptions("state", OAuthLoginOptions{
		ForceConsent: true,
	})

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("unparseable URL: %v", err)
	}

	prompt := parsed.Query().Get("prompt")
	if !containsPrompt(prompt, "consent") {
		t.Errorf("prompt missing consent, got: %s", prompt)
	}
	if containsPrompt(prompt, "select_account") {
		t.Errorf("prompt should NOT contain select_account in reconnect mode, got: %s", prompt)
	}
}

// TestYouTubePreferredTokenTypes verifies that YouTube declares its
// canonical token types for account validation.
func TestYouTubePreferredTokenTypes(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	types := svc.PreferredTokenTypes()
	if len(types) == 0 {
		t.Fatal("expected at least one preferred token type")
	}
	if types[0] != models.TokenTypeBearer {
		t.Errorf("first token type: want %q, got %q", models.TokenTypeBearer, types[0])
	}
}

// TestYouTubeLoginURL_LoginHint verifies that login_hint is set when provided.
func TestYouTubeLoginURL_LoginHint(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	authURL := svc.GetLoginURLWithOptions("state", OAuthLoginOptions{
		LoginHint: "user@example.com",
	})

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("unparseable URL: %v", err)
	}

	if got := parsed.Query().Get("login_hint"); got != "user@example.com" {
		t.Errorf("login_hint: want user@example.com, got %q", got)
	}
}

// TestYouTubeRefresh_InvalidGrantResponse_ClassifiedTyped is the P1
// regression test: Google's REAL 400 + {"error":"invalid_grant"} response
// must surface as credentials.ErrInvalidGrant through the actual
// postTokenRequest path. Previously the body was discarded and only
// "token exchange failed (status 400)" surfaced, defeating every
// downstream classification (vault status, RenewYouTubeToken, /validate).
func TestYouTubeRefresh_InvalidGrantResponse_ClassifiedTyped(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	_, err := svc.RefreshOAuthToken(context.Background(), "stale-refresh-token")
	if err == nil {
		t.Fatal("RefreshOAuthToken: want error for invalid_grant response, got nil")
	}
	if !errors.Is(err, credentials.ErrInvalidGrant) {
		t.Errorf("RefreshOAuthToken: want wrapped credentials.ErrInvalidGrant, got %v", err)
	}
	var tokenErr *credentials.OAuthTokenError
	if !errors.As(err, &tokenErr) {
		t.Fatalf("RefreshOAuthToken: want OAuthTokenError, got %T: %v", err, err)
	}
	if tokenErr.StatusCode != http.StatusBadRequest || tokenErr.Code != "invalid_grant" {
		t.Errorf("OAuthTokenError metadata: got status=%d code=%q", tokenErr.StatusCode, tokenErr.Code)
	}
	if strings.Contains(err.Error(), "Token has been expired or revoked") {
		t.Errorf("RefreshOAuthToken leaked error_description: %v", err)
	}
}

// TestYouTubeRefresh_Other400_NotClassifiedInvalidGrant proves the typed
// classification fires ONLY for invalid_grant: a 400 with any other RFC
// 6749 error code must keep the plain error shape (no sentinel, status
// retained) so infrastructure failures stay distinguishable.
func TestYouTubeRefresh_Other400_NotClassifiedInvalidGrant(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_client","error_description":"unauthorized client"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	_, err := svc.RefreshOAuthToken(context.Background(), "stale-refresh-token")
	if err == nil {
		t.Fatal("RefreshOAuthToken: want error for 400 response, got nil")
	}
	if errors.Is(err, credentials.ErrInvalidGrant) {
		t.Errorf("RefreshOAuthToken: invalid_client must NOT be classified as ErrInvalidGrant, got %v", err)
	}
	if !strings.Contains(err.Error(), "status 400") {
		t.Errorf("error must still carry the HTTP status; got %v", err)
	}
}

// TestYouTubeRefresh_PreservesOldRefreshToken verifies that when Google
// does not return a new refresh token, the old one is preserved.
func TestYouTubeRefresh_PreservesOldRefreshToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		// Google sometimes omits refresh_token on refresh.
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "new-access-token",
			"token_type":   "bearer",
			"expires_in":   3600,
			"scope":        "youtube.upload youtube.readonly youtube.force-ssl",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	result, err := svc.RefreshOAuthToken(context.Background(), "old-refresh-token")
	if err != nil {
		t.Fatalf("RefreshOAuthToken failed: %v", err)
	}

	if result.RefreshToken != "old-refresh-token" {
		t.Errorf("refresh token: want old-refresh-token preserved, got %q", result.RefreshToken)
	}
	if result.AccessToken != "new-access-token" {
		t.Errorf("access token: want new-access-token, got %q", result.AccessToken)
	}
}
