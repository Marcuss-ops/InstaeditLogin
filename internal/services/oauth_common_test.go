package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
)

// metaAllCfg returns a config with all three Meta-family providers enabled.
func metaAllCfg() *config.Config {
	return &config.Config{
		Auth: config.AuthConfig{
			MetaAppID:            "test-meta-app-id",
			MetaAppSecret:        "test-meta-app-secret-must-be-32-chars-min",
			InstagramRedirectURI: "http://localhost:8080/api/v1/auth/instagram/callback",
			FacebookRedirectURI:  "http://localhost:8080/api/v1/auth/facebook/callback",
			ThreadsRedirectURI:   "http://localhost:8080/api/v1/auth/threads/callback",
		},
	}
}

// newMetaAllServices creates all three Meta-family services sharing one mock server.
func newMetaAllServices(srv *httptest.Server) (*InstagramOAuthService, *FacebookOAuthService, *ThreadsOAuthService) {
	cfg := metaAllCfg()

	igBase := NewMetaOAuthBase(cfg)
	igBase.httpClient = testClient(srv)
	ig := &InstagramOAuthService{base: igBase, redirectURI: cfg.Auth.InstagramRedirectURI}

	fbBase := NewMetaOAuthBase(cfg)
	fbBase.httpClient = testClient(srv)
	fb := &FacebookOAuthService{base: fbBase, redirectURI: cfg.Auth.FacebookRedirectURI}

	thBase := NewMetaOAuthBase(cfg)
	thBase.httpClient = testClient(srv)
	th := &ThreadsOAuthService{base: thBase, redirectURI: cfg.Auth.ThreadsRedirectURI}

	return ig, fb, th
}

// TestOAuthHandlesSpecialStateCharacters verifies that the service layer
// properly URL-encodes states containing special characters (newlines,
// null bytes) so the handler layer can detect tampered/invalid states.
// True state rejection (invalid signature, wrong format) is tested at the
// handler layer (pkg/api).
func TestOAuthHandlesSpecialStateCharacters(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()

	ig, _, _ := newMetaAllServices(srv)

	// A state containing newlines and special characters should still
	// produce a valid URL (state is URL-encoded).
	authURL := ig.GetLoginURL("state\nwith\x00null")
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("GetLoginURL with special-char state produced unparseable URL: %v", err)
	}

	// The state parameter must be present in the URL.
	stateParam := parsed.Query().Get("state")
	if stateParam == "" {
		t.Fatal("state parameter is empty — must be present even for edge-case values")
	}

	// The URL must be structurally valid (scheme + host + path present).
	if parsed.Scheme != "https" {
		t.Errorf("scheme: want https, got %s", parsed.Scheme)
	}
	if parsed.Host != "www.facebook.com" {
		t.Errorf("host: want www.facebook.com, got %s", parsed.Host)
	}
}

// TestOAuthStateRoundTrip verifies that the state parameter survives
// a full encode/decode cycle through the authorization URL intact.
// The handler layer uses this property to validate state expiry and
// single-use — a state that doesn't round-trip cleanly cannot be verified.
func TestOAuthStateRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()

	ig, _, _ := newMetaAllServices(srv)

	// Simulate a state that might be expired (old timestamp prefix).
	expiredState := "2020-01-01-random-nonce"
	authURL := ig.GetLoginURL(expiredState)
	parsed, _ := url.Parse(authURL)

	if parsed.Query().Get("state") != expiredState {
		t.Errorf("state round-trip: want %q, got %q", expiredState, parsed.Query().Get("state"))
	}

	// Verify the state is properly encoded and the URL is valid.
	if parsed.Scheme != "https" {
		t.Errorf("scheme: want https, got %s", parsed.Scheme)
	}
}

// TestOAuthRejectsProviderMismatch verifies that each Meta-family provider
// produces a distinct redirect URI in its authorization URL, so a state
// from one provider cannot be used with another provider's callback.
func TestOAuthRejectsProviderMismatch(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()

	ig, fb, th := newMetaAllServices(srv)

	state := "test-nonce-42"

	igURL := ig.GetLoginURL(state)
	fbURL := fb.GetLoginURL(state)
	thURL := th.GetLoginURL(state)

	igParsed, _ := url.Parse(igURL)
	fbParsed, _ := url.Parse(fbURL)
	thParsed, _ := url.Parse(thURL)

	igRedirect := igParsed.Query().Get("redirect_uri")
	fbRedirect := fbParsed.Query().Get("redirect_uri")
	thRedirect := thParsed.Query().Get("redirect_uri")

	// All three must have different redirect URIs.
	if igRedirect == fbRedirect {
		t.Errorf("Instagram and Facebook redirect URIs are identical: %q", igRedirect)
	}
	if igRedirect == thRedirect {
		t.Errorf("Instagram and Threads redirect URIs are identical: %q", igRedirect)
	}
	if fbRedirect == thRedirect {
		t.Errorf("Facebook and Threads redirect URIs are identical: %q", fbRedirect)
	}

	// Verify each contains its platform name.
	if !strings.Contains(igRedirect, "instagram") {
		t.Errorf("Instagram redirect URI should contain 'instagram': %q", igRedirect)
	}
	if !strings.Contains(fbRedirect, "facebook") {
		t.Errorf("Facebook redirect URI should contain 'facebook': %q", fbRedirect)
	}
	if !strings.Contains(thRedirect, "threads") {
		t.Errorf("Threads redirect URI should contain 'threads': %q", thRedirect)
	}

	// Verify all use the same client_id (shared Meta credentials).
	for _, u := range []*url.URL{igParsed, fbParsed, thParsed} {
		if u.Query().Get("client_id") != "test-meta-app-id" {
			t.Errorf("client_id in %s: want test-meta-app-id, got %q", u.Path, u.Query().Get("client_id"))
		}
	}
}

// TestOAuthGetLoginURLIsDeterministic verifies that GetLoginURL produces
// identical URLs for the same state across multiple calls. Deterministic
// URL generation is a precondition for the handler's state-reuse detection:
// if the same state always maps to the same URL, the handler can flag a
// second use as a replay attack.
func TestOAuthGetLoginURLIsDeterministic(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()

	ig, _, _ := newMetaAllServices(srv)

	state := "single-use-state-abc"

	// Multiple calls with the same state must produce identical URLs.
	url1 := ig.GetLoginURL(state)
	url2 := ig.GetLoginURL(state)

	if url1 != url2 {
		t.Fatalf("GetLoginURL is non-deterministic for same state:\n  call1: %s\n  call2: %s", url1, url2)
	}

	// The state must appear exactly once in the URL.
	parsed, _ := url.Parse(url1)
	if parsed.Query().Get("state") != state {
		t.Errorf("state: want %q, got %q", state, parsed.Query().Get("state"))
	}
}

// TestOAuthConcurrentStateGeneration verifies that concurrent calls to
// GetLoginURL with different states produce correct results without
// shared mutable state corruption.
func TestOAuthConcurrentStateGeneration(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()

	ig, fb, th := newMetaAllServices(srv)

	const concurrency = 10
	var wg sync.WaitGroup
	errCh := make(chan string, concurrency*3)

	for i := 0; i < concurrency; i++ {
		wg.Add(3)
		state := "state-" + string(rune('A'+i))
		go func(s string) {
			defer wg.Done()
			u, err := url.Parse(ig.GetLoginURL(s))
			if err != nil || u.Query().Get("state") != s {
				errCh <- "instagram: " + s
			}
		}(state + "-ig")
		go func(s string) {
			defer wg.Done()
			u, err := url.Parse(fb.GetLoginURL(s))
			if err != nil || u.Query().Get("state") != s {
				errCh <- "facebook: " + s
			}
		}(state + "-fb")
		go func(s string) {
			defer wg.Done()
			u, err := url.Parse(th.GetLoginURL(s))
			if err != nil || u.Query().Get("state") != s {
				errCh <- "threads: " + s
			}
		}(state + "-th")
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent state corruption: %s", err)
	}
}

// TestOAuthProviderURLsAreDistinct verifies that all three Meta providers
// produce visually distinct authorization URLs (different redirect_uri
// and/or scopes), so a human reading the URL can distinguish them.
func TestOAuthProviderURLsAreDistinct(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()

	ig, fb, th := newMetaAllServices(srv)

	state := "distinct-test"
	igURL := ig.GetLoginURL(state)
	fbURL := fb.GetLoginURL(state)
	thURL := th.GetLoginURL(state)

	// Collect the distinguishing query parameters (exclude state which is shared).
	igParsed, _ := url.Parse(igURL)
	fbParsed, _ := url.Parse(fbURL)
	thParsed, _ := url.Parse(thURL)

	igSig := igParsed.Query().Get("redirect_uri") + "|" + igParsed.Query().Get("scope")
	fbSig := fbParsed.Query().Get("redirect_uri") + "|" + fbParsed.Query().Get("scope")
	thSig := thParsed.Query().Get("redirect_uri") + "|" + thParsed.Query().Get("scope")

	if igSig == fbSig || igSig == thSig || fbSig == thSig {
		t.Errorf("Provider signatures are not distinct:\n  instagram: %s\n  facebook:  %s\n  threads:   %s", igSig, fbSig, thSig)
	}
}

// TestOAuthHandleCallback_PreservesProfileFields verifies that all three
// Meta providers populate the same profile fields (PlatformUserID, Name, etc.)
// from the /me endpoint response.
func TestOAuthHandleCallback_PreservesProfileFields(t *testing.T) {
	providers := []struct {
		name string
	}{
		{"instagram"},
		{"facebook"},
		{"threads"},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v19.0/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "short-tok",
			"token_type":   "bearer",
			"expires_in":   3600,
		})
	})
	mux.HandleFunc("/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		// This path is shared in tests because the test client rewrites all
		// hosts to the same server. Instagram/Facebook long-lived exchange hits
		// it with GET; Threads code exchange hits it with POST. Distinguish by
		// method so both flows get a sensible response.
		if r.Method == http.MethodPost {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token": "short-tok",
				"token_type":   "bearer",
				"expires_in":   3600,
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "long-tok",
			"token_type":   "bearer",
			"expires_in":   5184000,
		})
	})
	mux.HandleFunc("/access_token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "long-tok",
			"token_type":   "bearer",
			"expires_in":   5184000,
		})
	})
	mux.HandleFunc("/v19.0/me", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"id":    "profile-id-12345",
			"name":  "Test User",
			"email": "test@example.com",
		})
	})
	mux.HandleFunc("/v1.0/me", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"id":   "profile-id-12345",
			"name": "Test User",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := metaAllCfg()

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			base := NewMetaOAuthBase(cfg)
			base.httpClient = testClient(srv)

			switch p.name {
			case "instagram":
				svc := &InstagramOAuthService{base: base, redirectURI: cfg.Auth.InstagramRedirectURI}
				profile, _, err := svc.HandleCallback(context.Background(), "state", "code")
				if err != nil {
					t.Fatalf("HandleCallback: %v", err)
				}
				if profile.PlatformUserID != "profile-id-12345" {
					t.Errorf("PlatformUserID: want profile-id-12345, got %s", profile.PlatformUserID)
				}
				if profile.Name != "Test User" {
					t.Errorf("Name: want Test User, got %s", profile.Name)
				}
				if profile.Username != "Test User" {
					t.Errorf("Username: want Test User, got %s", profile.Username)
				}
			case "facebook":
				svc := &FacebookOAuthService{base: base, redirectURI: cfg.Auth.FacebookRedirectURI}
				profile, _, err := svc.HandleCallback(context.Background(), "state", "code")
				if err != nil {
					t.Fatalf("HandleCallback: %v", err)
				}
				if profile.PlatformUserID != "profile-id-12345" {
					t.Errorf("PlatformUserID: want profile-id-12345, got %s", profile.PlatformUserID)
				}
				if profile.Name != "Test User" {
					t.Errorf("Name: want Test User, got %s", profile.Name)
				}
				if profile.Username != "Test User" {
					t.Errorf("Username: want Test User, got %s", profile.Username)
				}
			case "threads":
				svc := &ThreadsOAuthService{base: base, redirectURI: cfg.Auth.ThreadsRedirectURI}
				profile, _, err := svc.HandleCallback(context.Background(), "state", "code")
				if err != nil {
					t.Fatalf("HandleCallback: %v", err)
				}
				if profile.PlatformUserID != "profile-id-12345" {
					t.Errorf("PlatformUserID: want profile-id-12345, got %s", profile.PlatformUserID)
				}
				if profile.Name != "Test User" {
					t.Errorf("Name: want Test User, got %s", profile.Name)
				}
				if profile.Username != "Test User" {
					t.Errorf("Username: want Test User, got %s", profile.Username)
				}
			}
		})
	}
}

// TestOAuthURLContainsNoSecrets verifies that no secret values (client_secret,
// MetaAppSecret) leak into the authorization URL.
func TestOAuthURLContainsNoSecrets(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()

	ig, fb, th := newMetaAllServices(srv)

	urls := []string{
		ig.GetLoginURL("s"),
		fb.GetLoginURL("s"),
		th.GetLoginURL("s"),
	}

	for i, u := range urls {
		if strings.Contains(u, "client_secret") {
			t.Errorf("URL %d contains client_secret (secret leak): %s", i, u)
		}
		if strings.Contains(u, "test-meta-app-secret-must-be-32-chars-min") {
			t.Errorf("URL %d contains MetaAppSecret (secret leak): %s", i, u)
		}
	}
}
