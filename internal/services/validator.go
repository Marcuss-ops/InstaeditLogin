package services

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ValidateYouTubeSnippet returns an error if the supplied title or
// description exceed YouTube's documented snippet limits (title 100
// characters, description 5000 characters). It counts runes, not
// bytes, and trims surrounding whitespace before measuring.
//
// Defined in a neutral, non-platform-specific file so both the YouTube
// service and the NVIDIA metadata generator can call it without
// importing each other — essential for the future extraction of YouTube
// into its own package.
func ValidateYouTubeSnippet(title, description string) error {
	const maxTitleLen = 100
	const maxDescriptionLen = 5000
	if utf8.RuneCountInString(strings.TrimSpace(title)) > maxTitleLen {
		return fmt.Errorf("title exceeds %d characters", maxTitleLen)
	}
	if utf8.RuneCountInString(strings.TrimSpace(description)) > maxDescriptionLen {
		return fmt.Errorf("description exceeds %d characters", maxDescriptionLen)
	}
	return nil
}
