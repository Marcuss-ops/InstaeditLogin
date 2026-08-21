package main

import (
	"strings"
	"testing"
)

func TestValidatePrivacyStatus(t *testing.T) {
	for _, value := range []string{"public", "unlisted", "private"} {
		if err := validatePrivacyStatus(value); err != nil {
			t.Errorf("validatePrivacyStatus(%q) = %v, want nil", value, err)
		}
	}
	for _, value := range []string{"", "PUBLIC", "secret"} {
		if err := validatePrivacyStatus(value); err == nil {
			t.Errorf("validatePrivacyStatus(%q) = nil, want error", value)
		}
	}
}

func TestValidateNoExtraArgs(t *testing.T) {
	if err := validateNoExtraArgs("publish", nil); err != nil {
		t.Fatalf("no extra args error = %v, want nil", err)
	}
	if err := validateNoExtraArgs("publish", []string{"unexpected"}); err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("extra args error = %v, want unexpected argument", err)
	}
}

func TestValidateRequiredPath(t *testing.T) {
	for _, path := range []string{"", "   "} {
		if err := validateRequiredPath("--file", path); err == nil {
			t.Errorf("validateRequiredPath(%q) = nil, want error", path)
		}
	}
	if err := validateRequiredPath("--file", "video.mp4"); err != nil {
		t.Fatalf("validateRequiredPath(video.mp4) = %v, want nil", err)
	}
}
