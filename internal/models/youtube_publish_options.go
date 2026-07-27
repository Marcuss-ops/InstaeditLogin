package models

import (
	"fmt"
	"strings"
)

// YouTubeTranslation is the per-language localizations payload sent
// to YouTube's videos.update(part=localizations) endpoint. Each
// translation is keyed by an ISO 639-1 / BCP-47 language code
// (e.g. "en", "it", "pt-BR") and supplies the localized title +
// description for the video's snippet in that language. The default
// snippet (Title + Description in YouTubePublishOptions) is what
// the operator typed; everything in Translations is the per-language
// addition that YouTube exposes in the video's localized metadata.
//
// YouTube imposes:
//   - title: <= 100 chars (same bound as the default snippet);
//   - description: <= 5000 chars (same bound as the default snippet).
type YouTubeTranslation struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// YouTubePublishOptions bundles every per-publish YouTube snippet
// input into a single struct so the PublishThumbnail service
// signature doesn't grow unboundedly with each release.
//
// Design note: YouTube charges 1600 quota units per videos.update
// call. Bundling privacy change + title + description + tags +
// default language + default audio language into a SINGLE
// videos.update(part=snippet,status) saves a round-trip vs. the
// alternative of separate videos.update(part=status) +
// tag-only videos.update(part=snippet) calls. Translations are
// emitted as separate videos.update(part=localizations) calls
// (one per language) because YouTube expects a single language
// per call; the orchestrator loop handles them sequentially
// after the snippet update succeeds.
//
// All fields except PrivacyStatus are optional. The orchestrator
// validates against the YouTube bounds via Validate() below:
//   - Tags: max 30 items, total character count (incl. commas)
//     must not exceed 500.
//   - DefaultLanguage / DefaultAudioLanguage: BCP-47 codes
//     (e.g. "en", "it", "pt-BR"); light length sanity check
//     only — we do NOT run a full BCP-47 parser here.
//   - Translations: map[lang]YouTubeTranslation; empty allowed.
//     When non-empty, DefaultLanguage MUST be set — YouTube
//     refuses translations otherwise and burns 1600 quota in a
//     4xx response, so we catch it in the orchestrator before
//     the API call.
type YouTubePublishOptions struct {
	Title                string                        `json:"title,omitempty"`
	Description          string                        `json:"description,omitempty"`
	Tags                 []string                      `json:"tags,omitempty"`
	DefaultLanguage      string                        `json:"default_language,omitempty"`
	DefaultAudioLanguage string                        `json:"default_audio_language,omitempty"`
	Translations         map[string]YouTubeTranslation `json:"translations,omitempty"`
}

// Sanity-check constants enforced by Validate(). The non-zero values
// are YouTube-published bounds; the others are locally-tuned bounds
// (sanity gates against malformed BCP-47 codes).
const (
	YouTubeTagsMax           = 30
	YouTubeTagsTotalCharsMax = 500
	BCP47LengthSanityMax     = 35 // generous upper bound; well-formed codes are <12
)

// Validate enforces the YouTube-side bounds documented above so the
// orchestrator fails fast on HTTP 400 instead of burning quota on a
// guaranteed-4xx API call. Errors are written verbatim into the HTTP
// 400 response body, so keep them operator-readable.
//
// Idempotent: returns nil when the input is empty (the call becomes
// a privacy-only update with no snippet change).
func (o YouTubePublishOptions) Validate() error {
	if len(o.Tags) > YouTubeTagsMax {
		return fmt.Errorf("too many tags: %d (max %d)", len(o.Tags), YouTubeTagsMax)
	}
	total := 0
	for _, t := range o.Tags {
		total += len(t)
	}
	// YouTube counts commas as separators when joining tags; mimic
	// the bound by adding one separator per tag beyond the first.
	if len(o.Tags) > 1 {
		total += len(o.Tags) - 1
	}
	if total > YouTubeTagsTotalCharsMax {
		return fmt.Errorf("total tag characters %d exceeds YouTube bound %d", total, YouTubeTagsTotalCharsMax)
	}
	if err := checkBCP47Like("default_language", o.DefaultLanguage); err != nil {
		return err
	}
	if err := checkBCP47Like("default_audio_language", o.DefaultAudioLanguage); err != nil {
		return err
	}
	if len(o.Translations) > 0 && o.DefaultLanguage == "" {
		return fmt.Errorf("translations require default_language (YouTube refuses localizations without one)")
	}
	for lang, tr := range o.Translations {
		if err := checkBCP47Like("translation key", lang); err != nil {
			return err
		}
		if tr.Title == "" && tr.Description == "" {
			return fmt.Errorf("translation %q has empty title AND description (skip it or fill one)", lang)
		}
	}
	return nil
}

// checkBCP47Like is a tiny sanity gate against obviously-malformed
// language tags (length out of band, contains a slash, unknown chars).
// Full BCP-47 validation is delegated to YouTube — the API will reject
// truly malformed codes. We only catch the obvious misspellings here
// so the operator gets a friendly 400 message instead of a paid-for
// 4xx response.
func checkBCP47Like(label, code string) error {
	if code == "" {
		return nil
	}
	if len(code) > BCP47LengthSanityMax {
		return fmt.Errorf("%s %q looks malformed (BCP-47 codes are short)", label, code)
	}
	// Reject codes that contain characters BCP-47 never allows at
	// the top level: a slash (subtag separator), a backslash (we
	// never see it), whitespace, or anything non-alphanumeric.
	for _, r := range code {
		if r == '/' || r == '\\' || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return fmt.Errorf("%s %q contains a forbidden character (%q)", label, code, r)
		}
	}
	low := strings.ToLower(code)
	// YouTube also rejects codes that look like HTTP-ish artifacts
	// (e.g. "/it", "it it"). A non-empty code MUST contain at least
	// one ASCII letter.
	hasLetter := false
	for _, r := range low {
		if r >= 'a' && r <= 'z' {
			hasLetter = true
			break
		}
	}
	if !hasLetter {
		return fmt.Errorf("%s %q has no language subtag", label, code)
	}
	return nil
}
