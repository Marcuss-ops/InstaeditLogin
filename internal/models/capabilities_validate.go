package models

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ValidatePayload runs the GENERIC cross-platform checks a CapabilitySet
// can express:
//
//   - text-only vs media URLs (text_only flag rejects any media)
//   - media-URL/bool flags (supports_images/supports_video vs payload)
//   - max caption rune count (text length detects the platform's published
//     hard cap; trim spaces first so an all-space caption doesn't pass
//     a 1-rune minimum)
//   - privacy level allowlist (PrivacyLevels nil/absent means "no opt"
//
// Platform-specific extras (TikTok's 4000-rune title cap, YouTube's
// VALID_PRIVACY enum, LinkedIn's PUBLIC/CONNECTIONS visibility, etc.)
// stay in each provider's ValidateContent as a SECOND tier — they encode
// platform contracts that are NOT cleanly expressible as a CapabilitySet
// bit field.
//
// nil-Receiver safe: passing a nil *CapabilitySet returns nil (the
// validator was called on the zero set; treat as "no rule set, accept
// everything"). The router always passes a populated CapabilitySet so
// the nil path is only relevant for tests / defensive callers.
func (c *CapabilitySet) ValidatePayload(payload PublishPayload) error {
	if c == nil {
		return nil
	}
	// Text-only platforms (Twitter classic via a future flag; not active
	// in any current platform's matrix) reject any media URL.
	if c.TextOnly {
		if payload.ImageURL != "" || payload.VideoURL != "" {
			return fmt.Errorf("text-only platform forbids media URLs (image_url=%q video_url=%q)",
				payload.ImageURL, payload.VideoURL)
		}
	}
	if payload.ImageURL != "" && !c.SupportsImages {
		return fmt.Errorf("platform does not support images (image_url=%q)", payload.ImageURL)
	}
	if payload.VideoURL != "" && !c.SupportsVideo {
		return fmt.Errorf("platform does not support video (video_url=%q)", payload.VideoURL)
	}
	trimmed := strings.TrimSpace(payload.Text)
	if c.MaxCaptionRunes > 0 && utf8.RuneCountInString(trimmed) > c.MaxCaptionRunes {
		return fmt.Errorf("caption exceeds %d-rune limit (got %d runes)",
			c.MaxCaptionRunes, utf8.RuneCountInString(trimmed))
	}
	if len(c.PrivacyLevels) > 0 && payload.PrivacyLevel != "" {
		if !containsString(c.PrivacyLevels, payload.PrivacyLevel) {
			return fmt.Errorf("privacy_level %q not in allowed set %v", payload.PrivacyLevel, c.PrivacyLevels)
		}
	}
	return nil
}

// containsString returns true if s is an exact match for any element of
// arr. Used by ValidatePayload to enforce the privacy-level allowlist
// without map allocations (the slice is at most a handful of strings).
func containsString(arr []string, s string) bool {
	for _, v := range arr {
		if v == s {
			return true
		}
	}
	return false
}
