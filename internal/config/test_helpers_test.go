package config

import (
	"encoding/base64"
	"strings"
)

// validMetaSecret32 is a 32-character Meta app secret fixture that
// satisfies the minimum-length guard in validate().
var validMetaSecret32 = strings.Repeat("a", 32)

// validJWTSecret returns a JWT secret string that satisfies the
// minimum-length guard in validate().
func validJWTSecret() string {
	return "this_is_a_test_secret_at_least_32_bytes_long_xx"
}

// validEncryptionKey returns a base64-encoded 32-byte AES-256 key
// fixture that satisfies the encryption-key validation in validate().
func validEncryptionKey() string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
}

// minimalValidConfig returns a Config populated with the smallest set
// of fields required to pass validate(). Tests can then mutate the
// returned struct to exercise specific validation paths.
func minimalValidConfig(jwtSecret string) *Config {
	return &Config{
		AppEnv:        "dev",
		DatabaseURL:   "postgresql://user:pass@localhost:5432/instaedit_login?sslmode=disable",
		S3Endpoint:    "https://s3.example.com",
		S3Bucket:      "instaedit-bucket",
		S3AccessKey:   "AKIAIOSFODNN7EXAMPLE",
		S3SecretKey:   "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		EncryptionKey: validEncryptionKey(),
		JWTSecret:     jwtSecret,
	}
}
