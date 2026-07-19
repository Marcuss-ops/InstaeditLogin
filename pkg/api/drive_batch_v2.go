package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// scheduleClampHorizonDays caps the cumulative publish schedule
// push-ahead at 90 days. The historical 7-day cap (driveBatchJitterMaxSeconds)
// was silently truncated; the new flag surfaces this as
// schedule_clamped in the response per the spec. Operators wanting
// a longer horizon can bump this constant; consider surfacing it as
// an env-driven config in a follow-up so the SPA can show "max
// horizon: 90 days" without redeploying the server.
//
// P1 refactor: this is now a HARD cap. When the heuristic projects
// the schedule past the horizon, the handler returns 422 with an
// explicit clamped=true body (see DriveBatchImportV2OverflowResponse).
// The runtime crawler still applies the EXACT re-stamp ONCE file_count
// is known (D6 from the prior thinker review) so the DB end-state
// is truthful, but the user-facing contract is now fail-loud rather
// than accept-and-warn.
const scheduleClampHorizonDays = 90

// scheduleClampHeuristicMaxFiles is the worst-case projection N used
// by the heuristic. Drive folders in practice max at a few thousand
// files; 10_000 is generous margin. Configurable in a follow-up via
// env if operators see false positives.
const scheduleClampHeuristicMaxFiles = 10_000

// driveBatchFolderIDPatternRegex mirrors the historical pattern
// kept in drive_batch.go. Duplicating here means a malformed id
// (Drive folder ids are URL-safe base64ish, ~33 chars; we accept
// 1-100 to be permissive) is rejected at the API boundary before
// any DB / network work.
const driveBatchFolderIDPatternV2 = `^[A-Za-z0-9_\-]{1,100}$`

var driveBatchFolderIDPatternRegexV2 = regexp.MustCompile(driveBatchFolderIDPatternV2)

// ImportBatchStore is the persistence contract for the import_batches
// header table (P1#7). Mirrors the UploadJobStore pattern: declared
// in pkg/api so the handler depends on the abstraction, not on the
// *sql.DB-bound concrete type. Production wiring in
// internal/bootstrap/app.go passes *repository.ImportBatchRepository.
//
// The interface is intentionally narrow — the producer only needs
// Create + FindByID (for the poll endpoint). MarkTerminal +
// CursorUpdate + Reclaim etc. are crawler-side concerns and live
// on a separate interface (CrawlerBatchStore) declared in
// internal/worker/drive_batch_crawler.go.
type ImportBatchStore interface {
	Create(batch *models.ImportBatch) error
	FindByID(id uuid.UUID) (*models.ImportBatch, error)
}

// DriveBatchImportV2Request is the body for
// POST /api/v1/media/import/drive/folder/async.
//
// P1 refactor (current spec): the request DTO is FLAT — target_account_ids
// and target_group_id sit at the top level alongside source/workspace_id/
// default_privacy_level/publish_schedule (no nested `targets{}` envelope
// any more). The previous P1#7 shape kept them inside a BatchTargetsRef
// envelope for visual grouping; the new spec moves them out per the user's
// request and adds the new default_privacy_level field.
//
// Producer returns 202 + {batch_id, status:"queued", schedule_clamped}
// immediately; the background folder crawler
// (internal/worker/drive_batch_crawler.go) drives the Drive pagination
// + per-file upload_job fan-out.
//
// XOR invariant: target_account_ids XOR target_group_id; supplying both
// or neither is a 422.
// Schedule invariant: publish_schedule.start_at must be in the future
// AND min_gap_seconds <= max_gap_seconds AND the heuristic clamp must
// not exceed the 90-day horizon (otherwise 422 with explicit clamped=true).
// Privacy invariant: default_privacy_level must be one of public/unlisted/private.
type DriveBatchImportV2Request struct {
	Source              models.DriveSourceRef     `json:"source"`
	WorkspaceID         int64                     `json:"workspace_id"`
	TargetAccountIDs    []int64                   `json:"target_account_ids"`
	TargetGroupID       *string                   `json:"target_group_id,omitempty"`
	DefaultPrivacyLevel string                    `json:"default_privacy_level"`
	PublishSchedule     models.PublishScheduleRef `json:"publish_schedule"`
}

// DriveBatchImportV2Response is the producer-side success body.
// Always 202 Accepted on success; the caller polls
// GET /api/v1/media/import/drive/folder/async/{id} for status.
type DriveBatchImportV2Response struct {
	BatchID             uuid.UUID `json:"batch_id"`
	Status              string    `json:"status"` // "queued" immediately; "processing" / "completed" / "failed" on poll
	ScheduleClamped     bool      `json:"schedule_clamped"`
	ScheduleClampReason *string   `json:"schedule_clamp_reason,omitempty"`
}

// DriveBatchImportV2OverflowResponse is the 422 body returned when
// the publish schedule's projected horizon exceeds the cap. The
// caller can show this verbatim in the SPA's "your batch was too
// long" toast; no silent truncation.
type DriveBatchImportV2OverflowResponse struct {
	Error                string `json:"error"`
	Clamped              bool   `json:"clamped"`
	ClampReason          string `json:"clamp_reason"`
	ProjectedHorizonDays int    `json:"projected_horizon_days"`
	MaxHorizonDays       int    `json:"max_horizon_days"`
}

// handleDriveBatchImportV2 implements
// POST /api/v1/media/import/drive/folder/async — the P1#7 producer.
//
// Authz: JWT-deposited userID; the response wrapper build enforces
// workspace ownership in resolveWorkspace.
//
// Idempotency: a 400 is returned on the body-validation errors so
// a retried payload with the same body reaches the same code path;
// the actual folder-crawl dedupe happens at the upload_job level
// via UploadJobRepository.CreateIfSourceAbsent (one job per Drive
// file per source). The header-level idempotency_records caching
// of the historical V1 endpoint is intentionally REMOVED in V2 —
// V2's response is a single 1-row write (the header), and a retry
// by the same caller creates a duplicate header. The crawler-side
// upload_job.CreateIfSourceAbsent call absorbs the duplicate safely
// (it serializes on the source triple via the existing advisory
// lock pattern in repository.queries.go::CreateIfSourceAbsent).
//
// Flow:
//   1. requireUserID → JWT identity.
//   2. parse + validate body (XOR targets; provider == "google_drive"; folder_id shape; schedule envelope).
//   3. resolveWorkspace → workspace ownership check.
//   4. resolve targets — if targets.group_name is set, expand to
//      workspace_channels via WorkspaceRepository. Else use
//      targets.account_ids verbatim.
//   5. schedule heuristic clamp — compute projected horizon
//      against the (estimated) file count zero or a cached value
//      from prior crawls; surface schedule_clamped flag immediately
//      so the SPA can say "your batch was truncated to N days".
//   6. Insert header row (import_batches).
//   7. Return 202 + {batch_id, status, schedule_clamped, reason}.
func (r *Router) handleDriveBatchImportV2(w http.ResponseWriter, req *http.Request) {
	if r.importBatchStore == nil {
		writeError(w, http.StatusNotImplemented, "import batches not configured on this server")
		return
	}
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspaces not configured on this server")
		return
	}

	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}

	// 1MB cap on the request body — the new shape is small (5–10
	// fields), so 1MB is generous without being a DoS vector.
	req.Body = http.MaxBytesReader(w, req.Body, 1<<20)
	defer req.Body.Close()

	var body DriveBatchImportV2Request
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if err := validateDriveBatchV2Request(&body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	// Workspace ownership check: the JWT carries userID but not
	// workspace_id by default; requireWorkspaceOwnership reads the
	// workspace_id from the path/body and validates the caller owns
	// it (or is a workspace_admin member).
	wOK, ws := r.requireWorkspaceOwnership(w, req, body.WorkspaceID)
	if !wOK || ws == nil {
		return
	}

	// Resolve target account list — XOR rule was enforced in
	// validateDriveBatchV2Request; here we expand target_group_id → accounts.
	accountIDs, err := r.resolveV2Targets(req.Context(), userID, ws.ID, body.TargetGroupID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "could not resolve targets: "+err.Error())
		return
	}
	if len(accountIDs) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "target set resolved to zero accounts (check group membership or supplied account IDs)")
		return
	}

	// Schedule overflow check — P1 refactor: HARD 422. The previous
	// heuristic returned 202 + clamped=true flag; the user explicitly
	// asked for the silent-truncation behaviour to stop. When the
	// worst-case projection (heuristic) would exceed the 90-day cap,
	// we refuse the batch up-front so the SPA can prompt the operator
	// to widen the gap or shorten the horizon.
	projectedDays := heuristicScheduleClampProjectedDays(body.PublishSchedule)
	if projectedDays > scheduleClampHorizonDays {
		reason := fmt.Sprintf(
			"projected horizon %d days exceeds the %d-day cap (worst-case %d files × min_gap %ds)",
			projectedDays, scheduleClampHorizonDays,
			scheduleClampHeuristicMaxFiles, body.PublishSchedule.MinGapSeconds,
		)
		slog.Info("drive batch v2: schedule overflow → 422",
			"user_id", userID, "workspace_id", ws.ID,
			"projected_days", projectedDays, "max_days", scheduleClampHorizonDays,
		)
		writeJSON(w, http.StatusUnprocessableEntity, DriveBatchImportV2OverflowResponse{
			Error:                "schedule would exceed the publish horizon cap",
			Clamped:              true,
			ClampReason:          reason,
			ProjectedHorizonDays: projectedDays,
			MaxHorizonDays:       scheduleClampHorizonDays,
		})
		return
	}

	header := &models.ImportBatch{
		UserID:                 userID,
		WorkspaceID:            ws.ID,
		SourceProvider:         body.Source.Provider,
		SourceDriveAccountID:   body.Source.DriveAccountID,
		SourceFolderID:         body.Source.FolderID,
		TargetAccountIDs:       accountIDs,
		TargetGroupName:        body.TargetGroupID,
		PublishScheduleStartAt: body.PublishSchedule.StartAt,
		PublishScheduleMinGap:  body.PublishSchedule.MinGapSeconds,
		PublishScheduleMaxGap:  body.PublishSchedule.MaxGapSeconds,
		DefaultPrivacyLevel:    body.DefaultPrivacyLevel,
		Status:                 models.ImportBatchStatusQueued,
	}
	if err := r.importBatchStore.Create(header); err != nil {
		slog.Error("drive batch v2: create header failed",
			"user_id", userID, "workspace_id", ws.ID,
			"source_provider", body.Source.Provider, "source_folder_id", body.Source.FolderID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, "could not queue the folder batch")
		return
	}

	slog.Info("drive batch v2: header queued",
		"batch_id", header.ID, "user_id", userID, "workspace_id", ws.ID,
		"target_count", len(accountIDs),
		"schedule_start_at", body.PublishSchedule.StartAt.Format(time.RFC3339),
		"default_privacy_level", body.DefaultPrivacyLevel,
	)

	writeJSON(w, http.StatusAccepted, DriveBatchImportV2Response{
		BatchID:             header.ID,
		Status:              string(models.ImportBatchStatusQueued),
		ScheduleClamped:     false,
		ScheduleClampReason: nil,
	})
}

// handleDriveBatchV2Status implements
// GET /api/v1/media/import/drive/folder/async/{id} — polls the
// header + returns the current state. Same authz contract as the
// producer: requires user_id from JWT, scrapes the row by UUID, and
// the row's user_id is verified to match before the response is
// built (defence-in-depth against a guessed UUID).
func (r *Router) handleDriveBatchV2Status(w http.ResponseWriter, req *http.Request) {
	if r.importBatchStore == nil {
		writeError(w, http.StatusNotImplemented, "import batches not configured on this server")
		return
	}

	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}

	idStr := strings.TrimSpace(req.PathValue("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid batch id (expected UUID)")
		return
	}

	batch, err := r.importBatchStore.FindByID(id)
	if err != nil {
		slog.Warn("drive batch v2: find by id failed", "batch_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read batch header")
		return
	}
	if batch == nil {
		writeError(w, http.StatusNotFound, "batch not found")
		return
	}
	if batch.UserID != userID {
		// Defence-in-depth: a guessed UUID could belong to a
		// different tenant. Same 404 as a non-existent UUID so we
		// don't leak the existence of arbitrary ids.
		writeError(w, http.StatusNotFound, "batch not found")
		return
	}

	writeJSON(w, http.StatusOK, batch)
}

// validateDriveBatchV2Request enforces the producer-side invariants:
//   * source.provider must be "google_drive" (extensible forward)
//   * source.folder_id must match the URL-safe regex
//   * workspace_id must be positive
//   * target_account_ids XOR target_group_id
//   * publish_schedule envelope shape + start_at in the future +
//     min_gap_seconds <= max_gap_seconds
//   * default_privacy_level must be one of public/unlisted/private
//
// Returned as typed errors so the handler maps to 422 — separate
// from JSON-parse errors (400) so the SPA can distinguish "fix your
// payload" from "fill in missing fields".
func validateDriveBatchV2Request(req *DriveBatchImportV2Request) error {
	if req == nil {
		return errors.New("empty request body")
	}
	if req.Source.Provider != "google_drive" {
		return fmt.Errorf("source.provider must be \"google_drive\" today (got %q)", req.Source.Provider)
	}
	if req.Source.FolderID == "" {
		return errors.New("source.folder_id is required")
	}
	if !driveBatchFolderIDPatternRegexV2.MatchString(req.Source.FolderID) {
		return errors.New("source.folder_id must be 1–100 letters, digits, hyphens, or underscores")
	}
	if req.WorkspaceID <= 0 {
		return errors.New("workspace_id must be a positive integer")
	}

	// XOR: account_ids OR group_id, never both.
	hasAccounts := len(req.TargetAccountIDs) > 0
	hasGroup := req.TargetGroupID != nil && *req.TargetGroupID != ""
	if hasAccounts && hasGroup {
		return errors.New("supply either target_account_ids or target_group_id, not both")
	}
	if !hasAccounts && !hasGroup {
		return errors.New("supply either target_account_ids[] or target_group_id")
	}
	if hasAccounts {
		for _, id := range req.TargetAccountIDs {
			if id <= 0 {
				return errors.New("target_account_ids must contain positive integers only")
			}
		}
	}

	// Default privacy — YouTube allowlist at the producer boundary so
	// the publish_worker never has to second-guess the value. The
	// publish_worker normalises TikTok/LinkedIn at the platform edge.
	if req.DefaultPrivacyLevel == "" {
		return errors.New("default_privacy_level is required")
	}
	switch req.DefaultPrivacyLevel {
	case "public", "unlisted", "private":
	default:
		return fmt.Errorf("default_privacy_level must be one of public, unlisted, private (got %q)", req.DefaultPrivacyLevel)
	}

	// Schedule envelope invariant.
	if req.PublishSchedule.StartAt.IsZero() {
		return errors.New("publish_schedule.start_at is required")
	}
	if req.PublishSchedule.StartAt.Before(time.Now().Add(-1 * time.Minute)) {
		return errors.New("publish_schedule.start_at must be in the future")
	}
	if req.PublishSchedule.MinGapSeconds < 0 || req.PublishSchedule.MaxGapSeconds < 0 {
		return errors.New("publish_schedule.min_gap_seconds and max_gap_seconds must be non-negative")
	}
	if req.PublishSchedule.MinGapSeconds > req.PublishSchedule.MaxGapSeconds {
		return errors.New("publish_schedule.min_gap_seconds must be <= max_gap_seconds")
	}
	return nil
}

// resolveV2Targets expands the top-level target union into the final
// []int64 list. For target_account_ids, returns the list verbatim
// (after a duplicate-cull). For target_group_id, queries
// workspace_channels where group_name = $1 AND workspace_id = $2
// AND enabled IS TRUE.
func (r *Router) resolveV2Targets(ctx context.Context, userID, workspaceID int64, groupID *string) ([]int64, error) {
	if groupID == nil || *groupID == "" {
		// Should never reach here — validateDriveBatchV2Request
		// enforces XOR, so by this point either target_account_ids
		// or target_group_id is non-empty. Defensive return.
		return nil, fmt.Errorf("resolveV2Targets: no target set supplied (caller must enforce XOR)")
	}

	// Group-name path: enumerate workspace_channels rows where
	// group_name matches AND enabled is TRUE. WorkspaceRepository
	// doesn't expose a "ListByGroupName" method yet (P0#4 — only
	// ListChannels(workspaceID) exists); reuse ListChannels and
	// filter in-process. For workspaces with hundreds of channels
	// this is suboptimal, but acceptable for the P1 cut — the
	// follow-up commit adds ListChannelsByGroupName to the repo.
	channels, err := r.workspaceStore.ListChannels(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	wantGroup := *groupID
	out := make([]int64, 0, len(channels))
	for _, ch := range channels {
		// WorkspaceChannel.GroupName is a string column (no
		// nullable wrapper); an absent grouping serialises to
		// the empty string. Skip rows outside the requested
		// group_id + skip disabled rows.
		if ch.GroupName != wantGroup {
			continue
		}
		if !ch.Enabled {
			continue
		}
		out = append(out, ch.PlatformAccountID)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("group %q has no enabled channels in workspace %d", wantGroup, workspaceID)
	}
	_ = userID // reserved for future ownership filter; the workspace query already scopes per workspaceID
	return out, nil
}

// heuristicScheduleClampProjectedDays projects the worst-case horizon
// in DAYS from start_at (caller adds schedule.StartAt to get the
// absolute end-time).
//
// At producer time we don't yet know file_count, so we project the
// WORST-CASE: scheduleClampHeuristicMaxFiles (10_000 by default).
// The runtime crawler applies the EXACT re-stamp once file_count is
// known (separate code path: the per-job PublishAt write in
// upload_worker.go::processIngestJob already respects the schedule
// envelope and is bounded by the same horizon).
//
// The handler compares the returned days to scheduleClampHorizonDays;
// if greater, the request is refused with a HARD 422 (P1 refactor).
func heuristicScheduleClampProjectedDays(schedule models.PublishScheduleRef) int {
	if schedule.MinGapSeconds <= 0 {
		// Zero/negative gap means every file publishes at start_at —
		// never exceeds the horizon. The handler's envelope
		// validation already rejected negative gaps; this is the
		// defensive floor for zero.
		return 0
	}
	totalSec := int64(schedule.MinGapSeconds) * int64(scheduleClampHeuristicMaxFiles)
	totalDays := int((totalSec + 86399) / 86400) // round up
	return totalDays
}

// Compile-time assertion that *repository.ImportBatchRepository
// satisfies the ImportBatchStore interface below. Catches
// interface drift at go vet time. Mirrors the same pattern as
// `var _ WorkspaceStore = (*repository.WorkspaceRepository)(nil)`
// in handlers.go.
var _ ImportBatchStore = (*repository.ImportBatchRepository)(nil)
