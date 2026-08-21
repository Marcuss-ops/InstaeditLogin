package auth

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestParseFullKey_RejectsMalformedSecretBeforeLookup(t *testing.T) {
	validSecret := strings.Repeat("a", KeySecretChars)
	valid := KeyPrefixTest + validSecret
	if _, secret, err := ParseFullKey(valid); err != nil || secret != validSecret {
		t.Fatalf("valid key parse = (%q, %q, %v)", models.ApiKeyEnvironmentTest, secret, err)
	}

	cases := []string{
		KeyPrefixTest,
		KeyPrefixTest + strings.Repeat("a", KeySecretChars-1),
		KeyPrefixTest + strings.Repeat("a", KeySecretChars+1),
		KeyPrefixTest + strings.Repeat("A", KeySecretChars),
		KeyPrefixTest + strings.Repeat("0", KeySecretChars),
	}
	for _, raw := range cases {
		if _, _, err := ParseFullKey(raw); err == nil {
			t.Errorf("ParseFullKey(%q) = nil error, want malformed key", raw)
		}
	}
}

func TestApiKeyEnvironmentForAppEnv(t *testing.T) {
	if got := ApiKeyEnvironmentForAppEnv("production"); got != models.ApiKeyEnvironmentLive {
		t.Errorf("production environment = %q, want live", got)
	}
	for _, appEnv := range []string{"dev", "staging", "", "PRODUCTION ", " production"} {
		want := models.ApiKeyEnvironmentTest
		if strings.TrimSpace(strings.ToLower(appEnv)) == "production" {
			want = models.ApiKeyEnvironmentLive
		}
		if got := ApiKeyEnvironmentForAppEnv(appEnv); got != want {
			t.Errorf("app env %q = %q, want %q", appEnv, got, want)
		}
	}
}
