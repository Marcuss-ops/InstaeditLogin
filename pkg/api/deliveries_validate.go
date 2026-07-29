// Package api — validation rules for the POST /internal/v1/deliveries
// contract path (spec §7.1).
//
// Part of the split of deliveries_create.go (audit T3: versioned contract).
package api

import (
	"fmt"
	"strings"
)

// validateContractRequest enforces the contract's hard validation
// rules. Returns nil if valid; the err message is the human-readable
// 422 detail (the standard `writeError` envelope). Strict: every
// rule is enforced, no soft warnings, no fallback.
//
// The Idempotency-Key format check uses the canonical regex and
// reconciles each segment against the corresponding body field so
// a mismatch between header and source/destination is surfaced at
// the validation step rather than later in the pipeline.
func validateContractRequest(r *VeloxDeliverContractRequest, headerIdempotencyKey string) error {
	if r.Source.System != "velox" {
		return fmt.Errorf("validation: source.system must be 'velox', got %q", r.Source.System)
	}
	if headerIdempotencyKey == "" {
		return fmt.Errorf("validation: Idempotency-Key header is required for contract requests")
	}
	if !VeloxContractIdempotencyKeyRegex.MatchString(headerIdempotencyKey) {
		return fmt.Errorf("validation: Idempotency-Key header must match format velox-<job>-<artifact>-<platform>-<account|group>, got %q", headerIdempotencyKey)
	}
	parsedKey, ok := ParseVeloxContractIdempotencyKey(headerIdempotencyKey)
	if !ok {
		return fmt.Errorf("validation: Idempotency-Key regex match succeeded but segment split failed")
	}
	if parsedKey.JobID != r.Source.JobID {
		return fmt.Errorf("validation: Idempotency-Key job_id (%q) must match source.job_id (%q)", parsedKey.JobID, r.Source.JobID)
	}
	if parsedKey.ArtifactID != r.Source.ArtifactID {
		return fmt.Errorf("validation: Idempotency-Key artifact_id (%q) must match source.artifact_id (%q)", parsedKey.ArtifactID, r.Source.ArtifactID)
	}
	if parsedKey.Platform != r.Destination.Platform {
		return fmt.Errorf("validation: Idempotency-Key platform (%q) must match destination.platform (%q)", parsedKey.Platform, r.Destination.Platform)
	}
	switch r.Destination.TargetType {
	case "channel":
		if r.Destination.PlatformAccountID <= 0 {
			return fmt.Errorf("validation: destination.platform_account_id required when target_type=channel")
		}
		if !accountOrGroupMatches(parsedKey.AccountOrGroup, "channel", r.Destination.PlatformAccountID) {
			return fmt.Errorf("validation: Idempotency-Key account component (%q) must match destination.platform_account_id (%d) in some form (e.g. \"account_381\" or \"381\")", parsedKey.AccountOrGroup, r.Destination.PlatformAccountID)
		}
	case "group":
		if r.Destination.GroupID <= 0 {
			return fmt.Errorf("validation: destination.group_id required when target_type=group")
		}
		if !accountOrGroupMatches(parsedKey.AccountOrGroup, "group", r.Destination.GroupID) {
			return fmt.Errorf("validation: Idempotency-Key group component (%q) must match destination.group_id (%d) in some form (e.g. \"group_27\" or \"27\")", parsedKey.AccountOrGroup, r.Destination.GroupID)
		}
	default:
		return fmt.Errorf("validation: destination.target_type must be 'channel' or 'group', got %q", r.Destination.TargetType)
	}
	if r.Destination.Platform != "youtube" {
		return fmt.Errorf("validation: destination.platform must be 'youtube' (currently the only supported platform), got %q", r.Destination.Platform)
	}
	if !sha256HexRegex.MatchString(r.Media.SHA256) {
		return fmt.Errorf("validation: media.sha256 must be 64 lowercase hex characters")
	}
	if r.Media.SizeBytes <= 0 {
		return fmt.Errorf("validation: media.size_bytes must be > 0")
	}
	if r.Media.DurationSeconds <= 0 {
		return fmt.Errorf("validation: media.duration_seconds must be > 0")
	}
	if !mimeAllowlist[r.Media.MimeType] {
		return fmt.Errorf("validation: media.mime_type %q not supported (allowed: video/mp4, video/quicktime, video/webm, video/x-matroska)", r.Media.MimeType)
	}
	if !strings.HasPrefix(r.Media.DownloadURL, "https://") {
		return fmt.Errorf("validation: media.download_url must be HTTPS, got %q", r.Media.DownloadURL)
	}
	if r.Publication.InitialPrivacy != "private" {
		return fmt.Errorf("validation: publication.initial_privacy must always be 'private', got %q", r.Publication.InitialPrivacy)
	}
	switch r.Publication.FinalPrivacy {
	case "public", "unlisted", "private":
	default:
		return fmt.Errorf("validation: publication.final_privacy must be public|unlisted|private, got %q", r.Publication.FinalPrivacy)
	}
	if strings.TrimSpace(r.Publication.Title) == "" && strings.TrimSpace(r.Publication.Description) == "" {
		return fmt.Errorf("validation: publication must have a non-empty title or description")
	}
	return nil
}
