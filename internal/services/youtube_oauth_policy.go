package services

import "strings"

// Canonical YouTube OAuth scopes used by both the consent request and
// credential validation. Keeping the individual scopes here prevents the
// OAuth transport and resolver from maintaining separate policy literals.
const (
	youtubeUploadOAuthScope   = "https://www.googleapis.com/auth/youtube.upload"
	youtubeReadonlyOAuthScope = "https://www.googleapis.com/auth/youtube.readonly"

	// YouTubeForceSSLScope is required for thumbnails, metadata/privacy writes,
	// and YouTube Live operations. It remains exported for existing resolver
	// and integration-test callers.
	YouTubeForceSSLScope = "https://www.googleapis.com/auth/youtube.force-ssl"

	// youtubeOAuthScopes is the sole consent-screen scope string. Analytics
	// scopes are intentionally excluded under the least-privilege policy.
	youtubeOAuthScopes = youtubeUploadOAuthScope + " " + youtubeReadonlyOAuthScope + " " + YouTubeForceSSLScope + " openid email profile"
)

// youtubeHasScope performs the canonical full-string comparison used for
// grants and refreshed tokens. Short aliases must not satisfy a policy check.
func youtubeHasScope(scopes []string, required string) bool {
	if required == "" {
		return false
	}
	for _, scope := range scopes {
		if scope == required {
			return true
		}
	}
	return false
}

// youtubeScopeFlags derives the tokeninfo capability flags from the same
// canonical scope constants used to build the consent URL.
func youtubeScopeFlags(raw string) (hasUpload, hasReadonly, hasForceSSL bool) {
	for _, scope := range strings.Fields(raw) {
		switch scope {
		case youtubeUploadOAuthScope:
			hasUpload = true
		case youtubeReadonlyOAuthScope:
			hasReadonly = true
		case YouTubeForceSSLScope:
			hasForceSSL = true
		}
	}
	return hasUpload, hasReadonly, hasForceSSL
}
