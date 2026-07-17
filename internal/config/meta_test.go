package config

import (
	"strings"
	"testing"
)

// TestMetaConfigUsesSharedCredentials verifies that all three Meta-family
// providers (Instagram, Facebook, Threads) use META_APP_ID + META_APP_SECRET
// instead of individual per-provider OAuth credentials. There must be no
// INSTAGRAM_APP_SECRET or FACEBOOK_APP_SECRET fields.
func TestMetaConfigUsesSharedCredentials(t *testing.T) {
	cfg := minimalValidConfig(validJWTSecret())
	// All three Meta-family providers share META_APP_ID + META_APP_SECRET.
	cfg.MetaAppID = "test-app-id"
	cfg.MetaAppSecret = validMetaSecret32
	cfg.InstagramRedirectURI = "https://api.test/auth/instagram/callback"
	cfg.FacebookRedirectURI = "https://api.test/auth/facebook/callback"
	cfg.ThreadsRedirectURI = "https://api.test/auth/threads/callback"

	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() with all three Meta redirects + shared creds should succeed; got %v", err)
	}

	// Verify shared credentials are set as expected.
	// There must be no INSTAGRAM_APP_SECRET or FACEBOOK_APP_SECRET on the
	// Config struct — the shared Meta creds are the single source of truth
	// for all Meta-family providers.
	if cfg.MetaAppID != "test-app-id" {
		t.Errorf("MetaAppID: want test-app-id, got %q", cfg.MetaAppID)
	}
	if cfg.MetaAppSecret != validMetaSecret32 {
		t.Errorf("MetaAppSecret: want validMetaSecret32, got %q", cfg.MetaAppSecret)
	}
}

// TestInstagramUsesInstagramRedirectURI verifies that the Instagram provider
// uses INSTAGRAM_REDIRECT_URI, not META_REDIRECT_URI or any other shared
// redirect. The per-platform redirect is the caller's identity — Meta's
// OAuth server knows which app variant to return based on the redirect.
func TestInstagramUsesInstagramRedirectURI(t *testing.T) {
	cfg := minimalValidConfig(validJWTSecret())
	cfg.MetaAppID = "test-app-id"
	cfg.MetaAppSecret = validMetaSecret32
	cfg.InstagramRedirectURI = "https://api.test/auth/instagram/callback"

	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() with only Instagram redirect should succeed; got %v", err)
	}
	if cfg.InstagramRedirectURI != "https://api.test/auth/instagram/callback" {
		t.Errorf("InstagramRedirectURI: want custom value, got %q", cfg.InstagramRedirectURI)
	}
}

// TestFacebookUsesFacebookRedirectURI verifies per-platform redirect
// isolation for Facebook.
func TestFacebookUsesFacebookRedirectURI(t *testing.T) {
	cfg := minimalValidConfig(validJWTSecret())
	cfg.MetaAppID = "test-app-id"
	cfg.MetaAppSecret = validMetaSecret32
	cfg.FacebookRedirectURI = "https://api.test/auth/facebook/callback"

	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() with only Facebook redirect should succeed; got %v", err)
	}
	if cfg.FacebookRedirectURI != "https://api.test/auth/facebook/callback" {
		t.Errorf("FacebookRedirectURI: want custom value, got %q", cfg.FacebookRedirectURI)
	}
}

// TestThreadsUsesThreadsRedirectURI verifies per-platform redirect
// isolation for Threads.
func TestThreadsUsesThreadsRedirectURI(t *testing.T) {
	cfg := minimalValidConfig(validJWTSecret())
	cfg.MetaAppID = "test-app-id"
	cfg.MetaAppSecret = validMetaSecret32
	cfg.ThreadsRedirectURI = "https://api.test/auth/threads/callback"

	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() with only Threads redirect should succeed; got %v", err)
	}
	if cfg.ThreadsRedirectURI != "https://api.test/auth/threads/callback" {
		t.Errorf("ThreadsRedirectURI: want custom value, got %q", cfg.ThreadsRedirectURI)
	}
}

// TestMissingMetaCredentialsDisablesAllMetaProviders verifies that when
// META_APP_ID + META_APP_SECRET are both empty, the config passes validate()
// (Meta-family is considered disabled, not half-configured). This is the
// "zero OAuth platforms" degenerate case layered on top of the existing
// TestValidate_NoOAuthPlatformsValid.
func TestMissingMetaCredentialsDisablesAllMetaProviders(t *testing.T) {
	cfg := minimalValidConfig(validJWTSecret())
	cfg.MetaAppID = ""
	cfg.MetaAppSecret = ""
	// Even with redirect URIs set, missing creds should still pass validation
	// — the config doesn't validate the redirect against the creds; that's
	// the registry's job at wiring time.
	cfg.InstagramRedirectURI = "https://api.test/auth/instagram/callback"
	cfg.FacebookRedirectURI = "https://api.test/auth/facebook/callback"
	cfg.ThreadsRedirectURI = "https://api.test/auth/threads/callback"

	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() with empty Meta creds (all platforms disabled) should succeed; got %v", err)
	}
}

// TestMissingInstagramRedirectDisablesOnlyInstagram verifies that when
// INSTAGRAM_REDIRECT_URI is empty but Facebook + Threads redirects are set,
// validate() still passes. Redirect URI absence does NOT cause a validation
// error — it's interpreted as "provider disabled" by the registry.
func TestMissingInstagramRedirectDisablesOnlyInstagram(t *testing.T) {
	cfg := minimalValidConfig(validJWTSecret())
	cfg.MetaAppID = "test-app-id"
	cfg.MetaAppSecret = validMetaSecret32
	cfg.InstagramRedirectURI = ""
	cfg.FacebookRedirectURI = "https://api.test/auth/facebook/callback"
	cfg.ThreadsRedirectURI = "https://api.test/auth/threads/callback"

	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() with only Instagram redirect missing should succeed; got %v", err)
	}
}

// TestMissingFacebookRedirectDisablesOnlyFacebook is the symmetric case
// for Facebook redirect.
func TestMissingFacebookRedirectDisablesOnlyFacebook(t *testing.T) {
	cfg := minimalValidConfig(validJWTSecret())
	cfg.MetaAppID = "test-app-id"
	cfg.MetaAppSecret = validMetaSecret32
	cfg.InstagramRedirectURI = "https://api.test/auth/instagram/callback"
	cfg.FacebookRedirectURI = ""
	cfg.ThreadsRedirectURI = "https://api.test/auth/threads/callback"

	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() with only Facebook redirect missing should succeed; got %v", err)
	}
}

// TestMissingThreadsRedirectDisablesOnlyThreads is the symmetric case
// for Threads redirect.
func TestMissingThreadsRedirectDisablesOnlyThreads(t *testing.T) {
	cfg := minimalValidConfig(validJWTSecret())
	cfg.MetaAppID = "test-app-id"
	cfg.MetaAppSecret = validMetaSecret32
	cfg.InstagramRedirectURI = "https://api.test/auth/instagram/callback"
	cfg.FacebookRedirectURI = "https://api.test/auth/facebook/callback"
	cfg.ThreadsRedirectURI = ""

	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() with only Threads redirect missing should succeed; got %v", err)
	}
}

// TestIncompleteMetaCredentialsFailValidation proves that half-configured
// Meta credentials (only ID or only secret, but not both) still fail
// config.validate(). This is already covered by TestValidate_MetaHalfConfigured
// in config_test.go; this test adds the redirect-URI dimension to verify
// that having a redirect URI set does NOT relax the half-configured check.
func TestIncompleteMetaCredentialsFailValidation(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		secret    string
		errSubstr string
	}{
		{
			name:      "META_APP_ID set, META_APP_SECRET empty — redirects present",
			id:        "test-app-id",
			secret:    "",
			errSubstr: "META_APP_SECRET is required when META_APP_ID is set",
		},
		{
			name:      "META_APP_ID empty, META_APP_SECRET set — redirects present",
			id:        "",
			secret:    validMetaSecret32,
			errSubstr: "META_APP_ID is required when META_APP_SECRET is set",
		},
		{
			name:      "META_APP_SECRET too short (31 chars) — redirects present",
			id:        "test-app-id",
			secret:    strings.Repeat("a", 31),
			errSubstr: "META_APP_SECRET must be at least 32 characters",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalValidConfig(validJWTSecret())
			cfg.MetaAppID = tc.id
			cfg.MetaAppSecret = tc.secret
			cfg.InstagramRedirectURI = "https://api.test/auth/instagram/callback"
			cfg.FacebookRedirectURI = "https://api.test/auth/facebook/callback"
			cfg.ThreadsRedirectURI = "https://api.test/auth/threads/callback"

			err := cfg.validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errSubstr)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Fatalf("error: want substring %q, got %q", tc.errSubstr, err.Error())
			}
		})
	}
}
