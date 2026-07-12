package models

import (
	"fmt"
	"unicode/utf8"
)

// ValidatePayload is the matrix-level rule check, called by every
// per-platform ContentValidator BEFORE the platform-specific guard.
//
// Provider-specific mandatory-content rules (Twitter requires text,
// Instagram requires media, etc.) live in the per-provider guard and
// run AFTER this helper. This helper only knows the matrix shape:
// caption rune cap, media-kind legality, media-count cap.
//
// Numeric caps use 0=no-limit. Bool supports_* gate media kinds.
// MaxMediaCount counts ImageURL + VideoURL as discrete items today.
func (c *CapabilitySet) ValidatePayload(payload PublishPayload) error {
	// 1. Caption rune cap (0=no cap).
	if c.MaxCaptionRunes > 0 {
		runes := utf8.RuneCountInString(payload.Text)
		if runes > c.MaxCaptionRunes {
			return fmt.Errorf("payload caption is %d runes, exceeds matrix cap %d", runes, c.MaxCaptionRunes)
		}
	}
	// 2. Media-kind legality.
	if payload.ImageURL != "" && !c.SupportsImages {
		return fmt.Errorf("payload carries an image but platform does not support images")
	}
	if payload.VideoURL != "" && !c.SupportsVideo {
		return fmt.Errorf("payload carries a video but platform does not support videos")
	}
	// 3. Media-count cap.
	if c.MaxMediaCount > 0 {
		count := 0
		if payload.ImageURL != "" {
			count++
		}
		if payload.VideoURL != "" {
			count++
		}
		if count > c.MaxMediaCount {
			return fmt.Errorf("payload has %d media items, exceeds matrix cap %d", count, c.MaxMediaCount)
		}
	}
	return nil
}
