package models

import "strings"

// NormalizePlatformIdentifier returns the canonical internal identifier for a
// provider/platform value. Twitter is the persisted and API identifier; "x"
// remains a supported legacy/input alias so existing links and clients keep
// working without creating a second provider identity.
func NormalizePlatformIdentifier(platform string) string {
	canonical := strings.ToLower(strings.TrimSpace(platform))
	if canonical == PlatformX {
		return PlatformTwitter
	}
	return canonical
}

// IsTwitterPlatform reports whether platform is either the canonical Twitter
// identifier or its legacy X alias.
func IsTwitterPlatform(platform string) bool {
	return NormalizePlatformIdentifier(platform) == PlatformTwitter
}
