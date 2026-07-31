package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func hasExtendedSnippet(opts models.YouTubePublishOptions) bool {
	return len(opts.Tags) > 0 || opts.DefaultLanguage != "" || opts.DefaultAudioLanguage != ""
}

// updateVideoWithExtendedSnippet issues a single
// videos.update(part=snippet,status) call carrying:
//   - status.privacyStatus + (optional) status.publishAt
//   - snippet.title + snippet.description (when supplied)
//   - snippet.tags[] (when supplied)
//   - snippet.defaultLanguage + snippet.defaultAudioLanguage
//     (when supplied)
//
// YouTube charges 1600 quota units per videos.update call, so
// folding tags + default languages into the SAME call as the
// privacy change saves one round-trip vs. running a separate
// snippet-only update after the status update. The payload
// shape mirrors the existing UpdateVideoPrivacy path so a
// downstream reader that already parses that payload can accept
// the new keys without a refactor.
//
// Returns the same typed errors as UpdateVideoPrivacy
// (YouTubeAPIError, snippet-validation, etc.) so callers'
// failure-path handling stays unchanged.
func (s *YouTubeOAuthService) updateVideoWithExtendedSnippet(ctx context.Context, accessToken, videoID, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) error {
	privacyStatus = strings.ToLower(strings.TrimSpace(privacyStatus))
	switch privacyStatus {
	case "public", "unlisted", "private":
		// ok
	default:
		return fmt.Errorf("youtube update video: invalid privacy status %q", privacyStatus)
	}
	if err := ValidateYouTubeSnippet(opts.Title, opts.Description); err != nil {
		return fmt.Errorf("youtube update video: %w", err)
	}

	// status object — always present.
	status := map[string]interface{}{
		"privacyStatus": privacyStatus,
	}
	if publishAt != nil && !publishAt.IsZero() {
		if privacyStatus != "private" {
			return fmt.Errorf("youtube update video: publishAt requires privacyStatus=private")
		}
		status["publishAt"] = publishAt.UTC().Format(time.RFC3339)
	}

	// snippet object — only added when at least one snippet field
	// is non-empty. Without this gate YouTube would 4xx on an
	// empty snippet.
	snippet := make(map[string]interface{})
	if opts.Title != "" {
		snippet["title"] = opts.Title
	}
	if opts.Description != "" {
		snippet["description"] = opts.Description
	}
	if len(opts.Tags) > 0 {
		// Defensive copy so a calling test that re-uses opts
		// after the call still sees consistent state.
		tagsCopy := make([]string, len(opts.Tags))
		copy(tagsCopy, opts.Tags)
		snippet["tags"] = tagsCopy
	}
	if opts.DefaultLanguage != "" {
		snippet["defaultLanguage"] = opts.DefaultLanguage
	}
	if opts.DefaultAudioLanguage != "" {
		snippet["defaultAudioLanguage"] = opts.DefaultAudioLanguage
	}

	payload := map[string]interface{}{
		"id":     videoID,
		"status": status,
	}
	if len(snippet) > 0 {
		payload["snippet"] = snippet
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("youtube update video: marshal metadata: %w", err)
	}

	parts := "status"
	if len(snippet) > 0 {
		parts = "snippet,status"
	}
	reqURL := "https://www.googleapis.com/youtube/v3/videos?part=" + parts
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("youtube update video: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &YouTubeAPIError{StatusCode: 0, Category: "network", Message: fmt.Sprintf("youtube update video: request: %v", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	rbody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return &YouTubeAPIError{StatusCode: http.StatusUnauthorized, Category: "auth", Message: "youtube update video: unauthorized (status 401)"}
	case resp.StatusCode == http.StatusForbidden:
		return &YouTubeAPIError{StatusCode: http.StatusForbidden, Category: "auth", Message: "youtube update video: forbidden (status 403)"}
	case resp.StatusCode == http.StatusNotFound:
		return &YouTubeAPIError{StatusCode: http.StatusNotFound, Category: "not_found", Message: "youtube update video: video not found (status 404)"}
	case resp.StatusCode == http.StatusTooManyRequests:
		return &YouTubeAPIError{StatusCode: http.StatusTooManyRequests, Category: "rate_limit", Message: "youtube update video: rate limited (status 429)"}
	case resp.StatusCode >= 500:
		return &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "server_error", Message: fmt.Sprintf("youtube update video: server error (status %d)", resp.StatusCode)}
	default:
		return &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "unexpected", Message: fmt.Sprintf("youtube update video: unexpected status %d: %s", resp.StatusCode, string(rbody))}
	}
}

// UpsertLocalizations sets (or replaces) a single per-language
// localization on a YouTube video via videos.update(part=localizations).
// YouTube expects one language per call (the body is shaped as
// {id, localizations: {<lang>: {title, description}}}); the
// orchestrator loops over opts.Translations calling this once per
// language AFTER the snippet+status update succeeds.
//
// Retries transient failures (3x) via doWithRetry; permanent
// errors propagate. lang is validated upstream by
// YouTubePublishOptions.Validate so this method does not re-check
// the BCP-47 shape — a malformed lang is the orchestrator's bug,
// not the API call's.
func (s *YouTubeOAuthService) UpsertLocalizations(ctx context.Context, accessToken, videoID, lang string, tr models.YouTubeTranslation) error {
	if err := ValidateYouTubeSnippet(tr.Title, tr.Description); err != nil {
		return fmt.Errorf("youtube upsert localizations %s: %w", lang, err)
	}
	payload := map[string]interface{}{
		"id": videoID,
		"localizations": map[string]interface{}{
			lang: map[string]interface{}{
				"title":       tr.Title,
				"description": tr.Description,
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("youtube upsert localizations %s: marshal payload: %w", lang, err)
	}
	reqURL := "https://www.googleapis.com/youtube/v3/videos?part=localizations"
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("youtube upsert localizations %s: create request: %w", lang, err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	return doWithRetry(ctx, 3, time.Second, func() error {
		resp, err := s.httpClient.Do(req)
		if err != nil {
			return &YouTubeAPIError{StatusCode: 0, Category: "network", Message: fmt.Sprintf("youtube upsert localizations %s: request: %v", lang, err)}
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
			_, _ = io.Copy(io.Discard, resp.Body)
			return nil
		}
		rbody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			return &YouTubeAPIError{StatusCode: http.StatusUnauthorized, Category: "auth", Message: fmt.Sprintf("youtube upsert localizations %s: unauthorized (status 401)", lang)}
		case resp.StatusCode == http.StatusForbidden:
			return &YouTubeAPIError{StatusCode: http.StatusForbidden, Category: "auth", Message: fmt.Sprintf("youtube upsert localizations %s: forbidden (status 403)", lang)}
		case resp.StatusCode == http.StatusNotFound:
			return &YouTubeAPIError{StatusCode: http.StatusNotFound, Category: "not_found", Message: fmt.Sprintf("youtube upsert localizations %s: video not found (status 404)", lang)}
		case resp.StatusCode == http.StatusTooManyRequests:
			return &YouTubeAPIError{StatusCode: http.StatusTooManyRequests, Category: "rate_limit", Message: fmt.Sprintf("youtube upsert localizations %s: rate limited (status 429)", lang)}
		case resp.StatusCode >= 500:
			return &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "server_error", Message: fmt.Sprintf("youtube upsert localizations %s: server error (status %d)", lang, resp.StatusCode)}
		default:
			return &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "unexpected", Message: fmt.Sprintf("youtube upsert localizations %s: unexpected status %d: %s", lang, resp.StatusCode, string(rbody))}
		}
	})
}

func (s *YouTubeOAuthService) UpdateVideoPrivacy(ctx context.Context, accessToken, videoID, privacyStatus string, publishAt *time.Time, title, description string) error {
	if videoID == "" {
		return fmt.Errorf("youtube update video: empty video id")
	}
	privacyStatus = strings.ToLower(strings.TrimSpace(privacyStatus))
	switch privacyStatus {
	case "public", "unlisted", "private":
		// ok
	default:
		return fmt.Errorf("youtube update video: invalid privacy status %q", privacyStatus)
	}

	if err := ValidateYouTubeSnippet(title, description); err != nil {
		return fmt.Errorf("youtube update video: %w", err)
	}

	title = strings.TrimSpace(title)
	description = strings.TrimSpace(description)

	// Blocco #1 followup — Finding #2 (videos.update rejection):
	// apply CoercePrivacyForUpdate BEFORE building the status block.
	// The future-publishAt branch forces privacyStatus="private" so
	// the YouTube v3 videos.update endpoint accepts the request
	// ("publishAt requires privacyStatus=private" is its invariant).
	// The past-or-nil branch passes each value through unchanged so
	// tests pinning the legacy "privacy=public + no publishAt" shape
	// (TestUpdateVideoPrivacy_SendsSnippetAndStatus etc.) keep
	// passing.
	privacyStatus, publishAt = CoercePrivacyForUpdate(privacyStatus, publishAt, s.now())

	status := map[string]string{
		"privacyStatus": privacyStatus,
	}
	if publishAt != nil {
		status["publishAt"] = publishAt.UTC().Format(time.RFC3339)
	}

	snippet := make(map[string]string)
	if title != "" {
		snippet["title"] = title
	}
	if description != "" {
		snippet["description"] = description
	}

	payload := map[string]interface{}{
		"id":     videoID,
		"status": status,
	}
	if len(snippet) > 0 {
		payload["snippet"] = snippet
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("youtube update video: marshal metadata: %w", err)
	}

	parts := "status"
	if len(snippet) > 0 {
		parts = "snippet,status"
	}
	reqURL := "https://www.googleapis.com/youtube/v3/videos?part=" + parts
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("youtube update video: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &YouTubeAPIError{StatusCode: 0, Category: "network", Message: fmt.Sprintf("youtube update video: request: %v", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	rbody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return &YouTubeAPIError{StatusCode: http.StatusUnauthorized, Category: "auth", Message: "youtube update video: unauthorized (status 401)"}
	case resp.StatusCode == http.StatusForbidden:
		return &YouTubeAPIError{StatusCode: http.StatusForbidden, Category: "auth", Message: "youtube update video: forbidden (status 403)"}
	case resp.StatusCode == http.StatusNotFound:
		// Blocco #1 followup — Finding #4 (Phase-1 orphan-video
		// recovery): wrap the typed sentinel via Go 1.20+ multi-%w
		// so callers can match with errors.Is(err, ErrYouTubeVideoNotFound)
		// (worker-side fallback on 404 + video_id-match) while
		// preserving the *YouTubeAPIError diagnostic shape via
		// errors.As(err, &apiErr). The wrap carries the offending
		// video_id so the orphan-recovery branch's substring
		// classifier (defense for any non-sentinel-aware code path)
		// can confirm the 404 references OUR yt_pub row's video_id
		// rather than a stale value from a previous target.
		apiErr := &YouTubeAPIError{StatusCode: http.StatusNotFound, Category: "not_found", Message: "youtube update video: video not found (status 404)"}
		return fmt.Errorf("%w: video_id=%s: %w", ErrYouTubeVideoNotFound, videoID, apiErr)
	case resp.StatusCode == http.StatusTooManyRequests:
		return &YouTubeAPIError{StatusCode: http.StatusTooManyRequests, Category: "rate_limit", Message: "youtube update video: rate limited (status 429)"}
	case resp.StatusCode >= 500:
		return &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "server_error", Message: fmt.Sprintf("youtube update video: server error (status %d)", resp.StatusCode)}
	default:
		return &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "unexpected", Message: fmt.Sprintf("youtube update video: unexpected status %d: %s", resp.StatusCode, string(rbody))}
	}
}

// ValidateYouTubeSnippet returns an error if the supplied title or
// description exceed YouTube's documented snippet limits (title 100
// characters, description 5000 characters). It counts runes, not
// bytes, and trims surrounding whitespace before measuring.
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

// YouTubeAPIError carries the HTTP status code and a machine-readable
// category for a YouTube Data API failure. It is returned by low-level
// YouTube service methods so callers can decide whether the error is
// transient and worth retrying.
