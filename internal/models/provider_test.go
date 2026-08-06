package models

import "testing"

func TestNormalizePlatformIdentifier(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{input: PlatformTwitter, want: PlatformTwitter},
		{input: PlatformX, want: PlatformTwitter},
		{input: " X ", want: PlatformTwitter},
		{input: "YouTube", want: "youtube"},
		{input: "", want: ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			if got := NormalizePlatformIdentifier(tc.input); got != tc.want {
				t.Fatalf("NormalizePlatformIdentifier(%q): want %q, got %q", tc.input, tc.want, got)
			}
		})
	}
}

func TestIsTwitterPlatform(t *testing.T) {
	if !IsTwitterPlatform(PlatformTwitter) || !IsTwitterPlatform(PlatformX) {
		t.Fatal("canonical and alias identifiers must both be recognized as Twitter")
	}
	if IsTwitterPlatform(PlatformYouTube) {
		t.Fatal("YouTube must not be recognized as Twitter")
	}
}
