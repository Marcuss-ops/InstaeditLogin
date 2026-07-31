package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOAuthTokenJSONDoesNotExposeAccessToken(t *testing.T) {
	const secret = "oauth-access-token-must-not-leak"

	encoded, err := json.Marshal(&OAuthToken{
		AccessToken: secret,
		TokenType:   TokenTypeBearer,
	})
	if err != nil {
		t.Fatalf("marshal OAuthToken: %v", err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("serialized OAuthToken contains decrypted access token: %s", encoded)
	}
	if strings.Contains(string(encoded), "access_token") {
		t.Fatalf("serialized OAuthToken exposes access_token field: %s", encoded)
	}
}
