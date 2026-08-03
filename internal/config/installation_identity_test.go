package config

import (
	"strings"
	"testing"
)

func TestLoad_ExpectedDatabaseInstallationUUID(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("EXPECTED_DATABASE_INSTALLATION_UUID", "00000000-0000-4000-8000-000000000123")
	t.Setenv("JWT_SECRET", validJWTSecret())
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	t.Setenv("METRICS_BASIC_AUTH_USER", "test-user")
	t.Setenv("METRICS_BASIC_AUTH_PASS", "test-pass")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): want nil, got %v", err)
	}
	if cfg.Database.ExpectedInstallationUUID != "00000000-0000-4000-8000-000000000123" {
		t.Fatalf("ExpectedInstallationUUID was not loaded")
	}
}

func TestLoad_ProductionRejectsMissingExpectedDatabaseInstallationUUID(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("EXPECTED_DATABASE_INSTALLATION_UUID", "")
	t.Setenv("JWT_SECRET", validJWTSecret())
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	t.Setenv("METRICS_BASIC_AUTH_USER", "test-user")
	t.Setenv("METRICS_BASIC_AUTH_PASS", "test-pass")

	_, err := Load()
	if err == nil {
		t.Fatal("Load(): want missing installation UUID error, got nil")
	}
	if !strings.Contains(err.Error(), "EXPECTED_DATABASE_INSTALLATION_UUID") {
		t.Fatalf("error must identify the missing setting, got %v", err)
	}
}

func TestLoad_ProductionRejectsInvalidExpectedDatabaseInstallationUUID(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("EXPECTED_DATABASE_INSTALLATION_UUID", "not-a-uuid")
	t.Setenv("JWT_SECRET", validJWTSecret())
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	t.Setenv("METRICS_BASIC_AUTH_USER", "test-user")
	t.Setenv("METRICS_BASIC_AUTH_PASS", "test-pass")

	_, err := Load()
	if err == nil {
		t.Fatal("Load(): want invalid installation UUID error, got nil")
	}
	if !strings.Contains(err.Error(), "EXPECTED_DATABASE_INSTALLATION_UUID") {
		t.Fatalf("error must identify the invalid setting, got %v", err)
	}
}
