package config

import (
	"strings"
	"testing"
)

func TestDBPoolFieldSpec_ResolvesDefaultsAndOverrides(t *testing.T) {
	t.Setenv("DB_TEST_MAX_OPEN_CONNS", "17")
	t.Setenv("DB_TEST_MAX_IDLE_CONNS", "")
	t.Setenv("DB_TEST_CONN_MAX_LIFETIME_SECONDS", "901")
	t.Setenv("DB_TEST_CONN_MAX_IDLE_TIME_SECONDS", "73")

	got := newDBPoolFieldSpec("DB_TEST", DBPoolProfile{
		MaxOpenConns: 15, MaxIdleConns: 7,
		ConnMaxLifetimeSeconds: 1800, ConnMaxIdleTimeSeconds: 300,
	}).resolve()
	want := DBPoolProfile{MaxOpenConns: 17, MaxIdleConns: 7, ConnMaxLifetimeSeconds: 901, ConnMaxIdleTimeSeconds: 73}
	if got != want {
		t.Fatalf("resolved profile: got %+v, want %+v", got, want)
	}
}

func TestDBPoolFieldSpec_InvalidIntegerUsesFallback(t *testing.T) {
	t.Setenv("DB_TEST_MAX_OPEN_CONNS", "not-an-integer")
	t.Setenv("DB_TEST_MAX_IDLE_CONNS", "8")

	got := newDBPoolFieldSpec("DB_TEST", DBPoolProfile{MaxOpenConns: 15, MaxIdleConns: 7}).resolve()
	if got.MaxOpenConns != 15 || got.MaxIdleConns != 8 {
		t.Fatalf("invalid integer fallback: got %+v", got)
	}
}

func TestYouTubeOAuthClientFieldSpec_ResolvesBothSlotsWithoutSecretLeak(t *testing.T) {
	t.Setenv("YOUTUBE_OAUTH_CLIENT_TEST_ID", "client-id")
	t.Setenv("YOUTUBE_OAUTH_CLIENT_TEST_SECRET", strings.Repeat("s", 32))
	t.Setenv("YOUTUBE_OAUTH_CLIENT_TEST_REDIRECT_URI", "http://localhost/callback")

	got := newYouTubeOAuthClientFieldSpec("TEST").resolve()
	if got.ClientID != "client-id" || got.RedirectURI != "http://localhost/callback" {
		t.Fatalf("resolved client metadata: id=%q redirect=%q", got.ClientID, got.RedirectURI)
	}
	if got.ClientSecret != strings.Repeat("s", 32) {
		t.Fatalf("resolved client secret length: got %d, want 32", len(got.ClientSecret))
	}

	t.Setenv("YOUTUBE_OAUTH_CLIENT_TEST_SECRET", "")
	if got := newYouTubeOAuthClientFieldSpec("TEST").resolve(); got.ClientSecret != "" {
		t.Fatalf("empty env override must win over fallback: got secret length %d", len(got.ClientSecret))
	}
}

func TestYouTubeOAuthClientFieldSpec_ResolvesSlotsIndependently(t *testing.T) {
	t.Setenv("YOUTUBE_OAUTH_CLIENT_A_ID", "client-a")
	t.Setenv("YOUTUBE_OAUTH_CLIENT_B_ID", "client-b")

	a := newYouTubeOAuthClientFieldSpec("A").resolve()
	b := newYouTubeOAuthClientFieldSpec("B").resolve()
	if a.ClientID != "client-a" || b.ClientID != "client-b" {
		t.Fatalf("slot-specific IDs: A=%q B=%q", a.ClientID, b.ClientID)
	}
}

func TestYouTubeOAuthClientFieldSpec_UnsetDefaultsEmpty(t *testing.T) {
	for _, key := range []string{
		"YOUTUBE_OAUTH_CLIENT_TEST_ID",
		"YOUTUBE_OAUTH_CLIENT_TEST_SECRET",
		"YOUTUBE_OAUTH_CLIENT_TEST_REDIRECT_URI",
	} {
		t.Setenv(key, "")
	}
	got := newYouTubeOAuthClientFieldSpec("TEST").resolve()
	if got.ClientID != "" || got.RedirectURI != "" || got.ClientSecret != "" {
		t.Fatalf("unset client should resolve to empty metadata and secret length 0: id=%q redirect=%q secret_len=%d", got.ClientID, got.RedirectURI, len(got.ClientSecret))
	}
}
