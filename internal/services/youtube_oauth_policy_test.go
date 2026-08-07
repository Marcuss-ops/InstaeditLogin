package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

func TestYouTubeOAuthPolicy_ConsentAndTokenResolutionShareCanonicalScopes(t *testing.T) {
	wantScopes := []string{
		youtubeUploadOAuthScope,
		youtubeReadonlyOAuthScope,
		YouTubeForceSSLScope,
		"openid",
		"email",
		"profile",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"aud":"test-youtube-client-id","scope":"https://www.googleapis.com/auth/youtube.upload https://www.googleapis.com/auth/youtube.readonly https://www.googleapis.com/auth/youtube.force-ssl openid email profile","expires_in":300}`))
	}))
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	parsed, err := url.Parse(svc.GetLoginURL("policy-contract"))
	if err != nil {
		t.Fatalf("parse login URL: %v", err)
	}
	gotScopes := splitScopes(parsed.Query().Get("scope"))
	if !reflect.DeepEqual(gotScopes, wantScopes) {
		t.Fatalf("consent scopes: got %v, want %v", gotScopes, wantScopes)
	}

	info, err := svc.GetTokenInfo(context.Background(), "access-token")
	if err != nil {
		t.Fatalf("GetTokenInfo: %v", err)
	}
	if !info.HasUpload || !info.HasReadonly || !info.HasForceSSL {
		t.Fatalf("token flags must match canonical policy: upload=%v readonly=%v forceSSL=%v", info.HasUpload, info.HasReadonly, info.HasForceSSL)
	}
}

func TestYouTubeOAuthPolicy_ResolverAcceptsOnlyCanonicalForceSSLScope(t *testing.T) {
	if !youtubeHasScope([]string{YouTubeForceSSLScope}, YouTubeForceSSLScope) {
		t.Fatal("canonical force-ssl scope must satisfy resolver policy")
	}
	for _, alias := range []string{"youtube.force-ssl", "youtube.upload", "youtube.readonly"} {
		if youtubeHasScope([]string{alias}, YouTubeForceSSLScope) {
			t.Fatalf("short or unrelated scope %q must not satisfy canonical force-ssl policy", alias)
		}
	}
	if youtubeHasScope([]string{YouTubeForceSSLScope}, "") {
		t.Fatal("empty required scope must fail closed")
	}
}
