package capabilities

import (
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestMatrix_For_KnownPlatform(t *testing.T) {
	m := &Matrix{
		Platforms: map[string]models.CapabilitySet{
			models.PlatformTwitter: {TextOnly: true, MaxCaptionRunes: 280, SupportsImages: true},
		},
	}
	got := m.For(models.PlatformTwitter)
	if !got.TextOnly {
		t.Error("Twitter row: TextOnly should be true")
	}
	if got.MaxCaptionRunes != 280 {
		t.Errorf("Twitter row: MaxCaptionRunes want 280, got %d", got.MaxCaptionRunes)
	}
	if !got.SupportsImages {
		t.Error("Twitter row: SupportsImages should be true")
	}
}

func TestMatrix_For_UnknownPlatform_FailClosed(t *testing.T) {
	m := &Matrix{}
	got := m.For("some-future-platform-not-in-json")
	if !got.TextOnly {
		t.Error("unknown platform: TextOnly must be true (fail-closed)")
	}
	if got.SupportsImages || got.SupportsVideo || got.SupportsCarousel || got.SupportsStories {
		t.Error("unknown platform: all Supports* must be false (fail-closed)")
	}
	if got.MaxCaptionRunes != 0 || got.MaxVideoSeconds != 0 || got.MaxMediaCount != 0 {
		t.Errorf("unknown platform: numeric caps must be 0; got %+v", got)
	}
}

func TestIntersect_ZeroMeansNoPreference(t *testing.T) {
	// Scenario B: theory=0, real=N -> N wins.
	got := models.Intersect(models.CapabilitySet{MaxCaptionRunes: 0}, models.CapabilitySet{MaxCaptionRunes: 5000})
	if got.MaxCaptionRunes != 5000 {
		t.Errorf("(beta) theory=0 real=5000 -> want 5000, got %d", got.MaxCaptionRunes)
	}
	// Scenario C: theory=N, real=0 -> N wins.
	got = models.Intersect(models.CapabilitySet{MaxCaptionRunes: 4000}, models.CapabilitySet{MaxCaptionRunes: 0})
	if got.MaxCaptionRunes != 4000 {
		t.Errorf("(beta) theory=4000 real=0 -> want 4000, got %d", got.MaxCaptionRunes)
	}
	// Scenario A: both non-zero -> min wins.
	got = models.Intersect(models.CapabilitySet{MaxCaptionRunes: 4000}, models.CapabilitySet{MaxCaptionRunes: 1000})
	if got.MaxCaptionRunes != 1000 {
		t.Errorf("both non-zero: min wins; want 1000, got %d", got.MaxCaptionRunes)
	}
}

func TestValidatePayload_CaptionRuneCap(t *testing.T) {
	c := models.CapabilitySet{MaxCaptionRunes: 5}
	if err := c.ValidatePayload(models.PublishPayload{Text: "hi"}); err != nil {
		t.Errorf("5-rune cap accept 2-rune text: %v", err)
	}
	if err := c.ValidatePayload(models.PublishPayload{Text: "this is way too long for the cap"}); err == nil {
		t.Error("5-rune cap should reject long text")
	}
	// 0=no-cap.
	open := models.CapabilitySet{MaxCaptionRunes: 0}
	if err := open.ValidatePayload(models.PublishPayload{Text: "any length at all is fine here"}); err != nil {
		t.Errorf("0=no-cap should accept any text, got: %v", err)
	}
}

func TestValidatePayload_MediaKindLegality(t *testing.T) {
	noImg := models.CapabilitySet{SupportsVideo: true}
	if err := noImg.ValidatePayload(models.PublishPayload{ImageURL: "http://x"}); err == nil {
		t.Error("image on no-image platform: rejected expected")
	}
	noVid := models.CapabilitySet{SupportsImages: true}
	if err := noVid.ValidatePayload(models.PublishPayload{VideoURL: "http://x"}); err == nil {
		t.Error("video on no-video platform: rejected expected")
	}
}

func TestValidatePayload_MediaCountCap(t *testing.T) {
	c := models.CapabilitySet{MaxMediaCount: 1, SupportsImages: true, SupportsVideo: true}
	two := models.PublishPayload{ImageURL: "http://x", VideoURL: "http://y"}
	if err := c.ValidatePayload(two); err == nil {
		t.Error("MaxMediaCount=1: image+video (count=2) rejected expected")
	}
	if err := c.ValidatePayload(models.PublishPayload{ImageURL: "http://x"}); err != nil {
		t.Errorf("MaxMediaCount=1: image alone accepted expected, got: %v", err)
	}
}
