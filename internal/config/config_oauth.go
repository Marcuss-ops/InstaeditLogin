package config

import (
	"fmt"
)

// validateVelox enforces that the InstaEdit→Velox control pair is
// configured consistently when either side is present. The two
// secrets are checked only when their related feature is enabled,
// so dev setups can wire the internal Velox routes (which need
// VELOX_API_TOKEN) without also exposing the BFF control routes.
func (c *Config) validateVelox() error {
	hasControl := c.Velox.VeloxControlURL != "" || c.Velox.VeloxControlJWTSecret != ""
	if hasControl {
		if c.Velox.VeloxControlURL == "" {
			return fmt.Errorf("VELOX_CONTROL_URL is required when VELOX_CONTROL_JWT_SECRET is set")
		}
		if c.Velox.VeloxControlJWTSecret == "" {
			return fmt.Errorf("VELOX_CONTROL_JWT_SECRET is required when VELOX_CONTROL_URL is set")
		}
		if len(c.Velox.VeloxControlJWTSecret) < 32 {
			return fmt.Errorf("VELOX_CONTROL_JWT_SECRET must be at least 32 bytes (got %d)", len(c.Velox.VeloxControlJWTSecret))
		}
	}
	return nil
}

func (c *Config) validateOptionalPlatform(name, id, secret string) error {
	if id == "" && secret == "" {
		return nil
	}
	if id == "" {
		return fmt.Errorf("%s_CLIENT_ID is required when %s_CLIENT_SECRET is set (or unset both to disable the platform)", name, name)
	}
	if secret == "" {
		return fmt.Errorf("%s_CLIENT_SECRET is required when %s_CLIENT_ID is set (or unset both to disable the platform)", name, name)
	}
	if len(secret) < secretMinChars {
		return fmt.Errorf("%s_CLIENT_SECRET must be at least %d characters (got %d)", name, secretMinChars, len(secret))
	}
	return nil
}
