package services

import (
	"fmt"
	"strings"
)

// tikTokTitleMaxRunes is TikTok's documented per-post title/caption limit.
const tikTokTitleMaxRunes = 4000

func truncateTikTokTitle(s string) string {
	runes := []rune(s)
	if len(runes) <= tikTokTitleMaxRunes {
		return s
	}
	return string(runes[:tikTokTitleMaxRunes])
}

func normalizeTikTokPrivacyLevel(level string) string {
	// Taglio 4b: ValidateContent already rejected empty/unrecognized
	// values, so this switch always matches. No default fallback.
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "PUBLIC_TO_EVERYONE":
		return "PUBLIC_TO_EVERYONE"
	case "MUTUAL_FOLLOW_FRIENDS":
		return "MUTUAL_FOLLOW_FRIENDS"
	case "SELF_ONLY":
		return "SELF_ONLY"
	default:
		return ""
	}
}

// validateTikTokPrivacyLevel returns an error if level is not one of the
// three TikTok-recognized privacy values. Used by ValidateContent.
func validateTikTokPrivacyLevel(level string) error {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "PUBLIC_TO_EVERYONE", "MUTUAL_FOLLOW_FRIENDS", "SELF_ONLY":
		return nil
	default:
		return fmt.Errorf("tiktok privacy_level must be one of PUBLIC_TO_EVERYONE, MUTUAL_FOLLOW_FRIENDS, SELF_ONLY (got %q)", level)
	}
}

func modeIsDisabled(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "no_comments", "no_duet", "disabled", "off", "false", "0":
		return true
	default:
		return false
	}
}
