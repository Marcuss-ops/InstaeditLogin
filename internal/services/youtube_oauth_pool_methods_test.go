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
)

// poolTestClient returns a fully-configured pool client with
// credentials distinct from the legacy single client so tests can prove
// the pool client (not the legacy config) is used.
func poolTestClient() *YouTubeOAuthClientConfig {
	return &YouTubeOAuthClientConfig{
		Key:          "youtube_pool_a",
		ClientID:     "pool-a-client-id",
		ClientSecret: "pool-a-client-secret-at-least-32-chars!!",
		RedirectURI:  "https://instaedit.example.com/oauth/youtube/callback?client=a",
	}
}

// TestYouTubeLoginURL_PoolClient_UsesClientCredentials verifies that
// GetLoginURLWithPoolClient builds the authorize URL against the pool
// client's client_id + redirect_uri (NOT the legacy single client) —
// the consent URL and the callback exchange must share one client.
func TestYouTubeLoginURL_PoolClient_UsesClientCredentials(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	poolA := poolTestClient()

	authURL := svc.GetLoginURLWithPoolClient("signed-state", OAuthLoginOptions{
		ForceConsent:  true,
		SelectAccount: true,
	}, poolA)

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("GetLoginURLWithPoolClient returned unparseable URL: %v", err)
	}
	params := parsed.Query()

	if got := params.Get("client_id"); got != poolA.ClientID {
		t.Errorf("client_id: want %q, got %q", poolA.ClientID, got)
	}
	if got := params.Get("redirect_uri"); got != poolA.RedirectURI {
		t.Errorf("redirect_uri: want %q, got %q", poolA.RedirectURI, got)
	}
	if params.Get("client_id") == svc.cfg.Auth.YouTubeClientID {
		t.Error("client_id must NOT be the legacy single-client id when a pool client is passed")
	}
	if !containsPrompt(params.Get("prompt"), "consent") || !containsPrompt(params.Get("prompt"), "select_account") {
		t.Errorf("prompt must carry consent + select_account, got %q", params.Get("prompt"))
	}
	if params.Get("access_type") != "offline" {
		t.Errorf("access_type: want offline, got %q", params.Get("access_type"))
	}
}

// TestYouTubeLoginURL_PoolClient_NilFallsBackToLegacy pins the nil-client
// contract: GetLoginURLWithPoolClient(state, opts, nil) is byte-identical
// to GetLoginURLWithOptions(state, opts).
func TestYouTubeLoginURL_PoolClient_NilFallsBackToLegacy(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	withOpts := svc.GetLoginURLWithOptions("state", OAuthLoginOptions{ForceConsent: true})
	withNilClient := svc.GetLoginURLWithPoolClient("state", OAuthLoginOptions{ForceConsent: true}, nil)
	if withOpts != withNilClient {
		t.Errorf("nil pool client must equal legacy URL:\n legacy=%s\n pool=%s", withOpts, withNilClient)
	}
}

// TestYouTube_HandleCallbackWithClient_UsesPoolClientCredentials is the
// core "callback uses the state's client" test: the token endpoint must
// receive the pool client's client_id, client_secret and redirect_uri —
// not the legacy single-client credentials.
func TestYouTube_HandleCallbackWithClient_UsesPoolClientCredentials(t *testing.T) {
	poolA := poolTestClient()

	var gotClientID, gotSecret, gotRedirect string
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		gotClientID = form.Get("client_id")
		gotSecret = form.Get("client_secret")
		gotRedirect = form.Get("redirect_uri")
		if form.Get("grant_type") != "authorization_code" {
			t.Errorf("grant_type: want authorization_code, got %q", form.Get("grant_type"))
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "pool-a-access-token",
			"token_type":    "bearer",
			"expires_in":    3600,
			"scope":         "youtube.upload youtube.readonly youtube.force-ssl",
			"refresh_token": "pool-a-refresh-token",
		})
	})
	mux.HandleFunc("/oauth2/v2/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer pool-a-access-token" {
			t.Errorf("userinfo Authorization: want Bearer pool-a-access-token, got %q", got)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "google-subject-123", "name": "Pool A User", "email": "a@example.com",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestYouTubeService(srv)

	profile, tokenData, err := svc.HandleCallbackWithClient(context.Background(), "signed-state", "auth-code-1", poolA)
	if err != nil {
		t.Fatalf("HandleCallbackWithClient: %v", err)
	}

	if gotClientID != poolA.ClientID {
		t.Errorf("token exchange client_id: want %q, got %q", poolA.ClientID, gotClientID)
	}
	if gotSecret != poolA.ClientSecret {
		t.Errorf("token exchange client_secret: want %q, got %q (never log/echo it)", poolA.ClientSecret, gotSecret)
	}
	if gotRedirect != poolA.RedirectURI {
		t.Errorf("token exchange redirect_uri: want %q, got %q", poolA.RedirectURI, gotRedirect)
	}
	if profile.ProviderSubjectID != "google-subject-123" {
		t.Errorf("ProviderSubjectID: want google-subject-123, got %q", profile.ProviderSubjectID)
	}
	if tokenData.AccessToken != "pool-a-access-token" {
		t.Errorf("AccessToken: want pool-a-access-token, got %q", tokenData.AccessToken)
	}
	if tokenData.RefreshToken != "pool-a-refresh-token" {
		t.Errorf("RefreshToken: want pool-a-refresh-token, got %q", tokenData.RefreshToken)
	}
}

// TestYouTube_HandleCallbackWithClient_DoesNotLeakSecretInError pins that
// a failing pool-client exchange never echoes the client_secret in the
// returned error (the shared postTokenRequest + ParseOAuthTokenError
// redaction path is reused, and the wrapped error only carries the
// provider's stable OAuth error code).
func TestYouTube_HandleCallbackWithClient_DoesNotLeakSecretInError(t *testing.T) {
	poolA := poolTestClient()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"Code was already redeemed."}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestYouTubeService(srv)

	_, _, err := svc.HandleCallbackWithClient(context.Background(), "state", "bad-code", poolA)
	if err == nil {
		t.Fatal("HandleCallbackWithClient: want error, got nil")
	}
	if strings.Contains(err.Error(), poolA.ClientSecret) {
		t.Errorf("error leaked the pool client_secret: %v", err)
	}
}
