package services

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
)

func TestGoogleDriveOAuthService_Name(t *testing.T) {
	svc, err := NewGoogleDriveOAuthService(&config.Config{
		GoogleDriveClientID:     "client-id",
		GoogleDriveClientSecret: "client-secret-01234567890123456789012345678901",
		GoogleDriveRedirectURI:  "http://localhost/callback",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc == nil {
		t.Fatal("expected service to be non-nil")
	}
	if got := svc.Name(); got != "google-drive" {
		t.Fatalf("expected name google-drive, got %s", got)
	}
}

func TestGoogleDriveOAuthService_DisabledWhenNoClientID(t *testing.T) {
	svc, err := NewGoogleDriveOAuthService(&config.Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc != nil {
		t.Fatal("expected service to be nil when client id is empty")
	}
}

func TestGoogleDriveOAuthService_GetLoginURL(t *testing.T) {
	svc, _ := NewGoogleDriveOAuthService(&config.Config{
		GoogleDriveClientID:     "client-id",
		GoogleDriveClientSecret: "client-secret-01234567890123456789012345678901",
		GoogleDriveRedirectURI:  "http://localhost/callback",
	})
	url := svc.GetLoginURL("my-state")
	if !strings.Contains(url, "accounts.google.com/o/oauth2/v2/auth") {
		t.Fatalf("expected google oauth host, got %s", url)
	}
	if !strings.Contains(url, "scope=https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fdrive.readonly") {
		t.Fatalf("expected drive.readonly scope, got %s", url)
	}
	if !strings.Contains(url, "state=my-state") {
		t.Fatalf("expected state parameter, got %s", url)
	}
}
