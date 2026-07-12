// Package capabilities loads + serves the theoretical capability
// matrix (Taglio 5.0 LEVEL 1) -- the per-platform rules that decide
// whether a publish payload is acceptable before the per-platform
// Publish call round-trip.
//
// The matrix is loaded once at startup from config/capabilities.json
// (a small, hand-edited file). Missing file -> falls back to
// WithDefaults(platform) on every lookup so the server boots in dev
// mode without an explicit matrix file.
//
// Level 2 (real per-account caps) reuses this same Matrix type:
// the worker computes effective = Intersect(theoretical, real) and
// pipes it into PrePublishCheckWithEffective. The matrix itself does
// not grow -- Level 2 is a discoverer + repository-cache + worker-side
// intersect.
package capabilities

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// Matrix holds default and per-platform theoretical capabilities.
// Loaded once at startup from config/capabilities.json.
type Matrix struct {
	Defaults  models.CapabilitySet            `json:"_defaults"`
	Platforms map[string]models.CapabilitySet `json:"-"`
}

// rawMatrix is the on-disk JSON shape: a flat map keyed by platform
// name (or "_defaults").
type rawMatrix map[string]models.CapabilitySet

// LoadFromFile reads the capability matrix JSON from disk.
func LoadFromFile(path string) (*Matrix, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("capabilities: read %s: %w", path, err)
	}
	var raw rawMatrix
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("capabilities: parse %s: %w", path, err)
	}
	m := &Matrix{Platforms: make(map[string]models.CapabilitySet, len(raw))}
	for k, v := range raw {
		if k == "_defaults" {
			m.Defaults = v
			continue
		}
		m.Platforms[k] = v
	}
	return m, nil
}

// For returns capabilities for a platform name. KNOWN platforms with
// an entry in the JSON resolve to that row verbatim. UNKNOWN
// platforms log a slog.Warn (so config typos / new-platform-not-
// yet-added-in-JSON are visible) and fall back to WithDefaults(platform)
// which is fail-closed for unrecognized platforms.
func (m *Matrix) For(platform string) models.CapabilitySet {
	if c, ok := m.Platforms[platform]; ok {
		return c
	}
	slog.Warn("capabilities: unknown platform lookup, falling back to closed defaults", "platform", platform)
	return WithDefaults(platform)
}

// WithDefaults provides hardcoded per-platform fallbacks. The
// default branch (unknown platform) is FAIL-CLOSED: rejects all
// payloads until the operator adds the platform to capabilities.json.
// Accepting everything silently is far worse than the operator
// seeing a publish failure with a clear error.
func WithDefaults(platform string) models.CapabilitySet {
	switch platform {
	case models.PlatformTwitter:
		return models.CapabilitySet{
			TextOnly:        true,
			SupportsImages:  true,
			SupportsVideo:   true,
			MaxMediaCount:   4,
			MaxCaptionRunes: 280,
		}
	case models.PlatformInstagram:
		return models.CapabilitySet{
			SupportsImages:   true,
			SupportsVideo:    true,
			SupportsCarousel: true,
			SupportsStories:  true,
			MaxMediaCount:    10,
			MaxCaptionRunes:  2200,
		}
	case models.PlatformFacebook:
		return models.CapabilitySet{
			SupportsImages:  true,
			SupportsVideo:   true,
			SupportsStories: true,
			MaxMediaCount:   1,
			MaxCaptionRunes: 63206,
		}
	case models.PlatformThreads:
		return models.CapabilitySet{
			SupportsImages:  true,
			SupportsVideo:   true,
			MaxCaptionRunes: 500,
		}
	case models.PlatformYoutube:
		return models.CapabilitySet{
			SupportsVideo:   true,
			MaxVideoSeconds: 60,
			MaxCaptionRunes: 5000,
		}
	case models.PlatformLinkedin:
		return models.CapabilitySet{
			SupportsImages:  true,
			SupportsVideo:   true,
			MaxMediaCount:   9,
			MaxCaptionRunes: 3000,
			PrivacyLevels:   []string{"PUBLIC", "CONNECTIONS_ONLY"},
		}
	case models.PlatformTiktok:
		return models.CapabilitySet{
			SupportsVideo:   true,
			MaxVideoSeconds: 600,
			MaxCaptionRunes: 4000,
			PrivacyLevels:   []string{"PUBLIC_TO_EVERYONE", "MUTUAL_FOLLOW_FRIENDS", "SELF_ONLY"},
		}
	default:
		// FAIL-CLOSED: TextOnly only, no media, no privacy levels.
		return models.CapabilitySet{TextOnly: true}
	}
}
