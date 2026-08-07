package config

import (
	"strings"
	"testing"
)

func TestLoad_YouTubeRedirectURI_ProductionValidationWiring(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("INSTAEDITOR_URL", "https://editor.instaedit.test/dark_editor_v2")
	t.Setenv("YOUTUBE_CLIENT_ID", "youtube-client")
	t.Setenv("YOUTUBE_CLIENT_SECRET", strings.Repeat("s", 32))
	t.Setenv("YOUTUBE_REDIRECT_URI", "http://localhost:8080/api/v1/auth/youtube/callback")

	if _, err := Load(); err == nil {
		t.Fatal("production Load accepted localhost YouTube redirect while YouTube is enabled")
	} else if !strings.Contains(err.Error(), "YOUTUBE_REDIRECT_URI") {
		t.Fatalf("error should identify YOUTUBE_REDIRECT_URI, got %v", err)
	}

	t.Setenv("YOUTUBE_CLIENT_ID", "")
	t.Setenv("YOUTUBE_CLIENT_SECRET", "")
	if _, err := Load(); err != nil {
		t.Fatalf("disabled YouTube should preserve local compatibility, got %v", err)
	}
}
