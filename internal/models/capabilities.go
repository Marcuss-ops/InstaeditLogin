package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// CapabilitySet is the per-platform theoretical capability bundle.
// Loaded from config/capabilities.json at startup. Used by every
// ContentValidator implementation to gate Publish() with the same
// rules the platform documentation actually enforces -- eliminating
// "we tried to publish a 300-rune caption to Twitter and got a 400"
// surprises in production.
//
// Booleans are POSIX: false = "platform does not support" and any
// payload exercising the kind is rejected. Numerics use 0 = no
// limit / no preference. Intersect treats 0 as "no preference"
// (the non-zero other side wins); ValidatePayload treats 0 as
// "no upper bound" so caps with no configured limit accept any
// payload.
type CapabilitySet struct {
	TextOnly         bool     `json:"text_only"`
	SupportsImages   bool     `json:"supports_images"`
	SupportsVideo    bool     `json:"supports_video"`
	SupportsCarousel bool     `json:"supports_carousel"`
	SupportsStories  bool     `json:"supports_stories"`
	MaxMediaCount    int      `json:"max_media_count,omitempty"`
	MaxCaptionRunes  int      `json:"max_caption_runes,omitempty"`
	MaxVideoSeconds  int      `json:"max_video_seconds,omitempty"`
	PrivacyLevels    []string `json:"privacy_levels,omitempty"`
}

// Intersect computes the effective (tightest) capability set from
// two inputs: the theoretical matrix row + the per-account real
// capabilities (Level 2). Booleans are AND. Numeric caps use (beta)
// 0=no-preference semantics: if either side is 0, the OTHER side's
// value wins; if both are non-zero, min wins. PrivacyLevels are set
// intersection (empty input = "no preference").
//
// Central operator for the worker's L2 path:
//
//	effective := models.Intersect(theoretical, real)
//	effective.ValidatePayload(payload)
func Intersect(theory, real CapabilitySet) CapabilitySet {
	return CapabilitySet{
		TextOnly:         theory.TextOnly && real.TextOnly,
		SupportsImages:   theory.SupportsImages && real.SupportsImages,
		SupportsVideo:    theory.SupportsVideo && real.SupportsVideo,
		SupportsCarousel: theory.SupportsCarousel && real.SupportsCarousel,
		SupportsStories:  theory.SupportsStories && real.SupportsStories,
		MaxMediaCount:    minInt(theory.MaxMediaCount, real.MaxMediaCount),
		MaxCaptionRunes:  minInt(theory.MaxCaptionRunes, real.MaxCaptionRunes),
		MaxVideoSeconds:  minInt(theory.MaxVideoSeconds, real.MaxVideoSeconds),
		PrivacyLevels:    intersectStrings(theory.PrivacyLevels, real.PrivacyLevels),
	}
}

// IntersectOptional L2 worker helper: passes through the theoretical
// set when real is nil (account not yet discovered; matrix row
// alone is fine). When real is non-nil, delegates to Intersect.
func IntersectOptional(theory CapabilitySet, real *CapabilitySet) CapabilitySet {
	if real == nil {
		return theory
	}
	return Intersect(theory, *real)
}

// Value implements driver.Valuer for JSONB persistence.
func (c CapabilitySet) Value() (driver.Value, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("marshal CapabilitySet: %w", err)
	}
	return string(b), nil
}

// Scan implements sql.Scanner for JSONB hydration.
func (c *CapabilitySet) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*c = CapabilitySet{}
		return nil
	case string:
		return json.Unmarshal([]byte(v), c)
	case []byte:
		return json.Unmarshal(v, c)
	default:
		return fmt.Errorf("unsupported scan type for CapabilitySet: %T", src)
	}
}

// minInt (beta) semantics: 0 = "no preference"; non-zero side wins;
// both non-zero -> min.
func minInt(a, b int) int {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

// intersectStrings returns the common subset. Empty input =
// "no preference" -> the other input wins verbatim.
func intersectStrings(a, b []string) []string {
	if len(a) == 0 {
		return append([]string(nil), b...)
	}
	if len(b) == 0 {
		return append([]string(nil), a...)
	}
	out := make([]string, 0, len(a))
	for _, x := range a {
		for _, y := range b {
			if x == y {
				out = append(out, x)
				break
			}
		}
	}
	return out
}
