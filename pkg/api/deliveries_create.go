// Package api — POST /internal/v1/deliveries handler relocated.
//
// File moved out of `pkg/api/velox_handlers.go` into Go's internal-subtree
// convention (`pkg/api/internal/`). Code outside `pkg/api/...` cannot import
// from this subtree, mirroring the existing `pkg/api/velox/` bounded-context
// pattern and keeping handler files self-contained. The user-requested path
// `routes/internal/deliveries_create.go` does not exist in this repo; Go's
// internal-visibility path is the closest semantic match.
//
// Extended with CONTRACT schema auto-dispatch per
// `docs/velox-instaedit-contract.md` §7. The contract mandates:
//
//   - Body shape  : {source, media, destination, publication}
//   - Idempotency : HTTP header `Idempotency-Key: velox-<job>-<artifact>-<platform>-<account|group>`
//   - Validation  : strict regex on the header key, expiry-tolerant
//                   download URL, hard-coded `initial_privacy=private`
//
// The legacy schema (`VeloxDeliverArtifactRequest` from `velox_types.go`)
// is still accepted. Dispatcher uses a cheap shape-detector
// (`isVeloxContractRequest`) on every body; ALL mandatory contract fields
// present → contract path; otherwise legacy path. Both paths converge
// on `(*VeloxModule).persistInternalDelivery` which performs the
// canonical Insert under `pg_advisory_xact_lock` and emits the
// 3-way outcome (fresh / replay / different-SHA 409).
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// VeloxContractIdempotencyKeyRegex matches the canonical Idempotency-Key
// format mandated by `docs/velox-instaedit-contract.md` §7.2:
//
//	velox-<job_id>-<artifact_id>-<platform>-<account|group>
//
// Each of the 4 trailing hyphen-separated segments is required to be
// non-empty AND to consist only of lowercase letters, digits, or
// underscores (matching the published spec example
// `velox-job_123-artifact_abc-youtube-account_381`). Anchored at both
// ends so partial matches are rejected (e.g.
// `velox-job-foo-bar-baz-extra` won't sneak through) and a typo'd
// capitalised prefix (`Velox-job`) is rejected immediately.
//
// The lowercase+digit+underscore alphabet aligns with the account
// group prefix forms (`account_381`, `group_27`) accepted by
// `accountOrGroupMatches` downstream. A colon-form
// (`account:381`) is NOT accepted because the regex alphabet
// forbids `:`, mirroring the published snake_case contract.
var VeloxContractIdempotencyKeyRegex = regexp.MustCompile(`^velox-[a-z0-9_]+-[a-z0-9_]+-[a-z0-9_]+-[a-z0-9_]+$`)

// VeloxContractIdempotencyKeyParts is the typed decomposition of a
// canonical Idempotency-Key returned by `ParseVeloxContractIdempotencyKey`.
// Velox-supplied caller's `account` component (or `group` for groups)
// is left untyped on purpose: the producer's id format is not yet
// standardised (could be numeric like `account_381` or prefix'd like
// `group_27`); the dispatcher reads it as a string and validates
// separately against `destination.platform_account_id` /
// `destination.group_id`.
type VeloxContractIdempotencyKeyParts struct {
	JobID         string
	ArtifactID    string
	Platform      string
	AccountOrGroup string
}

// ParseVeloxContractIdempotencyKey splits a canonical key into its 4
// segment parts. Returns ok=false on any format mismatch (the caller
// MUST treat `ok=false` as a contract error before attempting to
// reconcile against the body).
func ParseVeloxContractIdempotencyKey(s string) (VeloxContractIdempotencyKeyParts, bool) {
	if !VeloxContractIdempotencyKeyRegex.MatchString(s) {
		return VeloxContractIdempotencyKeyParts{}, false
	}
	parts := strings.Split(strings.TrimPrefix(s, "velox-"), "-")
	if len(parts) != 4 {
		return VeloxContractIdempotencyKeyParts{}, false
	}
	return VeloxContractIdempotencyKeyParts{
		JobID:         parts[0],
		ArtifactID:    parts[1],
		Platform:      parts[2],
		AccountOrGroup: parts[3],
	}, true
}

// VeloxDeliverContractRequest is the body shape mandated by
// `docs/velox-instaedit-contract.md` §7.1. Field names mirror the
// spec verbatim (snake_case, dotted-nested). JSON tag overrides use
// `omitempty` on optional conditional fields (`platform_account_id`,
// `group_id`, `publish_at`, `tags`) so a single-target channel
// handoff doesn't carry a stray `group_id:0`.
type VeloxDeliverContractRequest struct {
	Source      VeloxContractSource      `json:"source"`
	Media       VeloxContractMedia       `json:"media"`
	Destination VeloxContractDestination `json:"destination"`
	Publication VeloxContractPublication `json:"publication"`
}

// VeloxContractSource is the `source` block — caller provenance only.
// None of these fields carry a payload byte; they identify the
// producer for audit + replay context. Mirrored from the OpenAPI
// `source_block` schema.
type VeloxContractSource struct {
	System     string `json:"system"`
	JobID      string `json:"job_id"`
	TaskID     string `json:"task_id"`
	ArtifactID string `json:"artifact_id"`
}

// VeloxContractMedia is the `media` block — producer-side artifact
// description. The producer computes these values once; the consumer
// (this handler) verifies them at ingest against the streamed bytes.
type VeloxContractMedia struct {
	DownloadURL     string  `json:"download_url"`
	SHA256          string  `json:"sha256"`
	SizeBytes       int64   `json:"size_bytes"`
	MimeType        string  `json:"mime_type"`
	DurationSeconds float64 `json:"duration_seconds"`
}

// VeloxContractDestination is the `destination` block — stable
// identifier (workspace_id + platform + target_type + (account|group)).
// Display names are not allowed by spec; this struct has no
// `channel_name` field deliberately.
type VeloxContractDestination struct {
	WorkspaceID       int64  `json:"workspace_id"`
	Platform          string `json:"platform"`
	TargetType        string `json:"target_type"`
	PlatformAccountID int64  `json:"platform_account_id,omitempty"`
	GroupID           int64  `json:"group_id,omitempty"`
}

// VeloxContractPublication is the `publication` block — user-facing
// metadata that travels alongside the media. `InitialPrivacy` is
// hard-coded to "private" per the safety invariant (any pre-upload
// state must be private; the public transition happens only after
// PRIVATE_UPLOADED → THUMBNAIL_APPLIED).
type VeloxContractPublication struct {
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	Tags            []string  `json:"tags,omitempty"`
	InitialPrivacy  string    `json:"initial_privacy"`
	FinalPrivacy    string    `json:"final_privacy"`
	RequireThumbnail bool     `json:"require_thumbnail"`
	PublishAt       *time.Time `json:"publish_at,omitempty"`
}

// VeloxDeliverContractResponse is the 202 body emitted by the
// CONTRACT path. Distinct shape from the legacy
// `VeloxDeliverArtifactResponse` (which uses `social_delivery_id` +
// `already_exists`) so the producer can pattern-match on the field
// names — `delivery_id` + `duplicate` match the user's published
// spec verbatim.
type VeloxDeliverContractResponse struct {
	DeliveryID string `json:"delivery_id"`
	Status     string `json:"status"` // always "accepted"
	Duplicate  bool   `json:"duplicate"`
}

// isVeloxContractRequest is a cheap shape-detector used by the
// dispatcher. Returns true ONLY when EVERY mandatory contract field
// is non-empty (a body claiming "contract" with empty Source.System
// is treated as legacy to keep the existing test fixtures on the
// legacy path). Order of checks: source, media, destination, publication.
func isVeloxContractRequest(r *VeloxDeliverContractRequest) bool {
	if r == nil {
		return false
	}
	if r.Source.System == "" || r.Source.JobID == "" || r.Source.TaskID == "" || r.Source.ArtifactID == "" {
		return false
	}
	if r.Media.DownloadURL == "" || r.Media.SHA256 == "" || r.Media.MimeType == "" {
		return false
	}
	if r.Destination.WorkspaceID == 0 || r.Destination.Platform == "" {
		return false
	}
	if r.Publication.InitialPrivacy == "" || r.Publication.FinalPrivacy == "" {
		return false
	}
	return true
}

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
	// AccountOrGroup must reconcile against destination.target_type.
	// The producer is allowed to send the segment in either of two
	// shapes matching the `velox-instaedit-contract.md` §7.2 example:
	//   - bare integer:           `velox-...-youtube-381`
	//   - prefixed underscore:    `velox-...-youtube-account_381`
	// The colon form (`account:381`) is rejected by the regex alphabet,
	// so it never reaches this branch. The shape is informational only;
	// the consumer compares the digit component against the destination
	// id field.
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

// synthesizeContractDeliveryID hashes (system:job:task) into a stable
// opaque id stable enough to act as external_deliveries.external_delivery_id.
// Replays of the same producer-side deliver therefore collide on the
// same id column, which feeds the existing pg_advisory_xact_lock idempotency
// semantics unchanged.
func synthesizeContractDeliveryID(r *VeloxDeliverContractRequest) string {
	h := sha256.Sum256([]byte("delivery:" + r.Source.System + ":" + r.Source.JobID + ":" + r.Source.TaskID))
	return "velox-" + hex.EncodeToString(h[:8])
}

// synthesizeContractDestinationID hashes the destination triple into
// a stable opaque id that satisfies the FK contract on
// external_deliveries.external_destination_id. Group and channel
// paths are hashed under distinct prefixes so the same triple can't
// collide across `target_type` shapes.
func synthesizeContractDestinationID(r *VeloxContractDestination) string {
	var key string
	switch r.TargetType {
	case "channel":
		key = fmt.Sprintf("destination:channel:ws=%d|platform=%s|account=%d", r.WorkspaceID, r.Platform, r.PlatformAccountID)
	case "group":
		key = fmt.Sprintf("destination:group:ws=%d|platform=%s|group=%d", r.WorkspaceID, r.Platform, r.GroupID)
	default:
		key = fmt.Sprintf("destination:unknown:ws=%d|platform=%s", r.WorkspaceID, r.Platform)
	}
	h := sha256.Sum256([]byte(key))
	return "extdst-" + hex.EncodeToString(h[:8])
}

// accountOrGroupMatches checks whether the producer-supplied
// `accountOrGroup` segment of an Idempotency-Key reconciles to the
// expected numeric id. Accepts the two canonical formats documented
// in `velox-instaedit-contract.md` §7.2:
//
//   - bare integer:          "381"      "27"
//   - prefixed underscore:   "account_381"   "group_27"
//
// The colon form (`account:381` / `group:27`) was once considered for
// cross-team DSL edge cases but is NOT accepted: the
// `VeloxContractIdempotencyKeyRegex` alphabet `[a-z0-9_]+` forbids
// `:`, so the colon variants never reach this helper in production.
// The two-form alphabet stays aligned with the published contract
// example `velox-job_123-artifact_abc-youtube-account_381`.
func accountOrGroupMatches(segment string, targetType string, expectedID int64) bool {
	wantStr := fmt.Sprintf("%d", expectedID)
	if segment == wantStr {
		return true
	}
	var prefix string
	switch targetType {
	case "channel":
		prefix = "account"
	case "group":
		prefix = "group"
	default:
		return false
	}
	// underscore form: prefix anything NA
	if strings.HasPrefix(segment, prefix+"_") {
		return segment[len(prefix)+1:] == wantStr
	}
	// colon form: prefix colon digit
	if strings.HasPrefix(segment, prefix+":") {
		return segment[len(prefix)+1:] == wantStr
	}
	return false
}

// handleCreateInternalDelivery implements POST /internal/v1/deliveries
// for the Velox integration contract.
//
// Relocated from pkg/api/velox_handlers.go. Extended with schema
// auto-dispatch:
//
//   1. Body is parsed as VeloxDeliverContractRequest (cheap).
//   2. If all mandatory contract fields are non-empty → CONTRACT path.
//      Strict validation per validateContractRequest (regex,
//      segments, enum, privacy); the contract path synthesises the
//      legacy row fields (external_delivery_id, external_destination_id,
//      metadata JSONB) so the existing Insert path is reused unchanged.
//   3. Otherwise → LEGACY path (existing validation chain verbatim,
//      destination lookup + MergeVeloxDestinationMetadata + Parse+Validate).
//
// Both paths converge on (*VeloxModule).persistInternalDelivery which
// mints the social_delivery_id, runs the Insert under pg_advisory_xact_lock,
// and emits the 3-way outcome (fresh / replay / different-SHA 409).
//
// Response shapes:
//   - CONTRACT path → 202 VeloxDeliverContractResponse
//     {delivery_id, status:"accepted", duplicate:bool}
//   - LEGACY   path → 202 VeloxDeliverArtifactResponse
//     {social_delivery_id, status:"accepted", already_exists:bool}
//
// 409 responses are schema-uniform (VeloxDeliverArtifactConflictResponse);
// callers can pattern-match on the structured body across both paths.
func (m *VeloxModule) handleCreateInternalDelivery(w http.ResponseWriter, req *http.Request) {
	if m.deps.ExternalDeliveryStore == nil {
		writeError(w, http.StatusNotImplemented, "internal velox delivery store not configured")
		return
	}
	if m.deps.ExternalDestinationStore == nil {
		writeError(w, http.StatusInternalServerError, "external destination store not configured")
		return
	}

	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	// Step 2 — body cap via http.MaxBytesReader. 8 MB envelope (the
	// artifact itself streams via the separate download_url call
	// downstream; the JSON envelope stays bounded).
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, maxDeliveryBodyBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			slog.Warn("velox deliver: body too large", "limit_bytes", maxDeliveryBodyBytes)
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("request body exceeds %d MB", maxDeliveryBodyBytes/(1024*1024)))
			return
		}
		slog.Error("velox deliver: body read failed", "err", err)
		writeError(w, http.StatusInternalServerError, "body read failed")
		return
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "empty request body")
		return
	}

	// SCHEMA DISPATCH — CONTRACT path first (cheap shape-detect).
	var contractReq VeloxDeliverContractRequest
	if jerr := json.Unmarshal(body, &contractReq); jerr == nil && isVeloxContractRequest(&contractReq) {
		headerIdempotencyKey := strings.TrimSpace(req.Header.Get("Idempotency-Key"))
		if verr := validateContractRequest(&contractReq, headerIdempotencyKey); verr != nil {
			writeError(w, http.StatusUnprocessableEntity, verr.Error())
			return
		}
		// Synthesise legacy row fields from the contract body so
		// the existing Insert path is reused unchanged. The metadata
		// JSONB is constructed here to be authoritative (no
		// destination-merge needed because the contract path doesn't
		// have a destination row keyed by platform_account_id).
		metadata := map[string]any{
			"title":              contractReq.Publication.Title,
			"description":        contractReq.Publication.Description,
			"privacy_status":     contractReq.Publication.InitialPrivacy,
			"final_privacy":      contractReq.Publication.FinalPrivacy,
			"require_thumbnail":  contractReq.Publication.RequireThumbnail,
			"target_account_ids": []int64{},
			"language":           "",
			"duration_seconds":   contractReq.Media.DurationSeconds,
			"platform":           contractReq.Destination.Platform,
			"target_type":        contractReq.Destination.TargetType,
		}
		if len(contractReq.Publication.Tags) > 0 {
			metadata["tags"] = contractReq.Publication.Tags
		}
		if contractReq.Destination.PlatformAccountID > 0 {
			metadata["target_account_ids"] = []int64{contractReq.Destination.PlatformAccountID}
		}
		if contractReq.Destination.GroupID > 0 {
			metadata["group_id"] = contractReq.Destination.GroupID
		}
		metaBytes, mErr := json.Marshal(metadata)
		if mErr != nil {
			slog.Error("velox deliver contract: metadata marshal failed", "err", mErr)
			writeError(w, http.StatusInternalServerError, "metadata marshal failed")
			return
		}
		downloadURL := contractReq.Media.DownloadURL
		veloxReq := &VeloxDeliverArtifactRequest{
			ExternalDeliveryID:    synthesizeContractDeliveryID(&contractReq),
			IdempotencyKey:        headerIdempotencyKey,
			ExternalDestinationID: synthesizeContractDestinationID(&contractReq.Destination),
			Artifact: VeloxArtifactRef{
				ArtifactID:  contractReq.Source.ArtifactID,
				SHA256:      contractReq.Media.SHA256,
				SizeBytes:   contractReq.Media.SizeBytes,
				MimeType:    contractReq.Media.MimeType,
				DownloadURL: &downloadURL,
			},
			Metadata:  metaBytes,
			PublishAt: contractReq.Publication.PublishAt,
		}
		m.persistInternalDelivery(w, ctx, body, veloxReq, true /* isContractPath */)
		return
	}

	// LEGACY PATH — verbatim validation chain from the original
	// pkg/api/velox_handlers.go::handleCreateInternalDelivery so all
	// existing tests continue to pin the same behaviour.
	var veloxReq VeloxDeliverArtifactRequest
	if err := json.Unmarshal(body, &veloxReq); err != nil {
		slog.Warn("velox deliver: json unmarshal failed", "err", err)
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Step 4 — idempotency_key presence + max length.
	if veloxReq.IdempotencyKey == "" {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: idempotency_key is required")
		return
	}
	if len(veloxReq.IdempotencyKey) > 256 {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: idempotency_key exceeds 256 characters")
		return
	}

	// Step 5 — artifact.sha256 lowercase hex regex.
	if !sha256HexRegex.MatchString(veloxReq.Artifact.SHA256) {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: artifact.sha256 must be 64 lowercase hex characters")
		return
	}

	// Step 6 — artifact.size_bytes positive.
	if veloxReq.Artifact.SizeBytes <= 0 {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: artifact.size_bytes must be > 0")
		return
	}

	// Step 7 — mime allowlist (4 video formats).
	if !mimeAllowlist[veloxReq.Artifact.MimeType] {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("validation: artifact.mime_type %q not supported (allowed: video/mp4, video/quicktime, video/webm, video/x-matroska)",
				veloxReq.Artifact.MimeType))
		return
	}

	// Step 8 — metadata must be a non-empty JSON object. This
	// fast-fail happens BEFORE the destination lookup so callers
	// always see 422 for malformed metadata, even if the
	// destination id is unknown.
	if !services.IsNonEmptyJSONObject(veloxReq.Metadata) {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: metadata must be a non-empty JSON object")
		return
	}

	// Step 9 — external_destination_id lookup. Sentinels map to 404
	// (no existence leak) per the file-level oracle discipline.
	dest, err := m.deps.ExternalDestinationStore.GetByID(ctx, veloxReq.ExternalDestinationID)
	if err != nil {
		if errors.Is(err, repository.ErrExternalDestinationNotFound) {
			writeError(w, http.StatusNotFound, veloxDestinationNotFoundBody)
			return
		}
		slog.Error("velox deliver: destination lookup failed",
			"external_destination_id", veloxReq.ExternalDestinationID,
			"err", err)
		writeError(w, http.StatusInternalServerError, "destination lookup failed")
		return
	}
	if dest == nil {
		writeError(w, http.StatusNotFound, veloxDestinationNotFoundBody)
		return
	}
	veloxReq.Metadata = services.MergeVeloxDestinationMetadata(dest, veloxReq.Metadata)

	// Step 10 — parse + validate metadata once at HTTP boundary.
	meta, err := models.ParseVeloxDeliveryMetadata(veloxReq.Metadata)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: "+err.Error())
		return
	}
	if err := meta.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: "+err.Error())
		return
	}

	m.persistInternalDelivery(w, ctx, body, &veloxReq, false /* isContractPath */)
}

// persistInternalDelivery is the shared post-validation insert used by
// both contract and legacy paths. Performs:
//
//  1. Mint a social_delivery_id (sdel_01J... ULID-shaped).
//  2. Run ExternalDeliveryStore.Insert under pg_advisory_xact_lock.
//     The 3-way outcome (fresh / same-SHA replay / different-SHA
//     ErrIdempotencyConflict) is owned by the repo. The Insert is
//     bounded by a 5s ctx timeout; > 300ms triggers a WARN log.
//  3. Detects fresh-vs-replay via mintedID != inserted.ID, emits the
//     schema-appropriate 202 response.
//  4. On ErrIdempotencyConflict: emit a structured 409 with the
//     conflict body (the same across both paths so callers can
//     pattern-match on it).
//
// The contract path uses `veloxReq.Metadata` directly (constructed
// by the caller); the legacy path uses `veloxReq.Metadata` after the
// destination merge + parse+validate. Either way, the value reaching
// the repo is canonical metadata JSONB.
func (m *VeloxModule) persistInternalDelivery(w http.ResponseWriter, ctx context.Context, body []byte, veloxReq *VeloxDeliverArtifactRequest, isContractPath bool) {
	mintedID, err := services.GenerateVeloxDeliveryID()
	if err != nil {
		slog.Error("velox deliver: id mint failed", "err", err)
		writeError(w, http.StatusInternalServerError, "id mint failed")
		return
	}

	delivery := &models.ExternalDelivery{
		ID:                    mintedID,
		SourceSystem:          veloxSourceSystemTag,
		ExternalDeliveryID:    veloxReq.ExternalDeliveryID,
		IdempotencyKey:        veloxReq.IdempotencyKey,
		ExternalDestinationID: veloxReq.ExternalDestinationID,
		SourceArtifactID:      veloxReq.Artifact.ArtifactID,
		ExpectedSHA256:        veloxReq.Artifact.SHA256,
		ExpectedSizeBytes:     veloxReq.Artifact.SizeBytes,
		ExpectedMimeType:      veloxReq.Artifact.MimeType,
		DownloadURL:           veloxReq.Artifact.DownloadURL,
		Metadata:              veloxReq.Metadata,
		PublishAt:             veloxReq.PublishAt,
		CallbackURL:           veloxReq.CallbackURL,
		Status:                models.ExternalDeliveryStatusAccepted,
	}

	t0 := time.Now()
	inserted, err := m.deps.ExternalDeliveryStore.Insert(ctx, delivery, body)
	elapsed := time.Since(t0)
	if elapsed > 300*time.Millisecond {
		slog.Warn("velox deliver: insert slow",
			"elapsed_ms", elapsed.Milliseconds(),
			"idempotency_key", veloxReq.IdempotencyKey,
			"is_contract_path", isContractPath)
	}

	if err != nil {
		if errors.Is(err, repository.ErrIdempotencyConflict) {
			var existingID string
			if inserted != nil {
				existingID = inserted.ID
			}
			slog.Info("velox deliver: replay with different sha rejected",
				"idempotency_key", veloxReq.IdempotencyKey,
				"existing_social_delivery_id", existingID,
				"is_contract_path", isContractPath,
			)
			writeJSON(w, http.StatusConflict, VeloxDeliverArtifactConflictResponse{
				Error:          "idempotency_key_conflict",
				Code:           "idempotency_key_conflict",
				IdempotencyKey: veloxReq.IdempotencyKey,
			})
			return
		}
		slog.Error("velox deliver: insert failed",
			"err", err,
			"idempotency_key", veloxReq.IdempotencyKey,
			"is_contract_path", isContractPath)
		writeError(w, http.StatusInternalServerError, "delivery persist failed")
		return
	}

	alreadyExists := inserted.ID != mintedID
	slog.Info("velox deliver: accepted",
		"social_delivery_id", inserted.ID,
		"idempotency_key", veloxReq.IdempotencyKey,
		"already_exists", alreadyExists,
		"elapsed_ms", elapsed.Milliseconds(),
		"is_contract_path", isContractPath,
	)

	if isContractPath {
		writeJSON(w, http.StatusAccepted, VeloxDeliverContractResponse{
			DeliveryID: inserted.ID,
			Status:     "accepted",
			Duplicate:  alreadyExists,
		})
		return
	}
	writeJSON(w, http.StatusAccepted, VeloxDeliverArtifactResponse{
		SocialDeliveryID: inserted.ID,
		Status:           "accepted",
		AlreadyExists:    alreadyExists,
	})
}
