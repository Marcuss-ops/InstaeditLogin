package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// handleDriveBatchImport implements POST /api/v1/media/import/drive/folder.
// See DriveBatchImportRequest for the body shape. The response is 202
// Accepted with a DriveBatchImportResponse describing every queued job.
//
// Idempotency (LEVEL 1, migration 021 + side table 039):
//   - Reads body bytes once and computes SHA-256 hash.
//   - Validates schema.
//   - Verifies workspace ownership (BEFORE the cache lookup so an
//     attacker cannot forge another tenant's workspace_id in body to
//     "steal" their cached batch by collision on (workspace, key)).
//     This matches the order in handleCreatePost / handleDriveImport.
//   - Looks up cache keyed on (ws.ID, idemKey, hash, resource_type=
//     "drive_batch"). On hit+match → byte-identical 202 replay. On
//     hit+mismatch → 409 idempotency_key_conflict. On miss → run handler.
//   - On full success (ScheduledCount > 0), writes BOTH parent
//     idempotency_records row (resource_id = first job id, response_status
//     = 202) and idempotency_batch_replays side row (response_payload =
//     JSON bytes that the handler wrote to the wire) so a future retry
//     with the same key + same hash replays byte-for-byte.
//
// Idempotency ONLY caches successful batches (ScheduleCount > 0).
// Edge-case responses (empty folder, missing API key, upstream 502)
// skip the cache write — those return non-202 statuses or non-N>0
// payloads, and re-trying them after the underlying problem is fixed
// SHOULD re-run the handler to get a fresh response. Caching them
// would lock the operator out of re-running after config fixups.
func (r *Router) handleDriveBatchImport(w http.ResponseWriter, req *http.Request) {
	if r.uploadJobStore == nil {
		writeError(w, http.StatusNotImplemented, "upload jobs not configured on this server")
		return
	}
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspace not configured on this server")
		return
	}

	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}

	// Read body bytes once + hash before json.Decode. Rewinds req.Body
	// so any downstream json.NewDecoder sees the same payload.
	bodyBytes, bodyErr := idempotencyReadBody(req)
	if bodyErr != nil {
		writeError(w, http.StatusBadRequest, "request body unreadable: "+bodyErr.Error())
		return
	}
	hash := idempotencyHash(bodyBytes)

	var body DriveBatchImportRequest
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(body.FolderID) == "" {
		writeError(w, http.StatusUnprocessableEntity, "folder_id is required")
		return
	}
	if body.WorkspaceID == 0 {
		writeError(w, http.StatusUnprocessableEntity, "workspace_id is required")
		return
	}
	if body.FacebookAccountID == 0 {
		writeError(w, http.StatusUnprocessableEntity, "facebook_account_id is required")
		return
	}
	// P0 hardening refactor: the public_drive download path was
	// removed from the Drive service. Every batch import must
	// flow through an authenticated Drive account's OAuth grant,
	// so drive_account_id is now required (was previously optional
	// for the legacy public-folder path).
	if body.DriveAccountID == 0 {
		writeError(w, http.StatusUnprocessableEntity,
			"drive_account_id is required (the public_drive download path was removed in the Drive pipeline hardening refactor)")
		return
	}

	// Default jitter bounds: 3h-4.5h (matches the user-facing spec).
	if body.MinJitterSeconds == 0 {
		body.MinJitterSeconds = 3 * 60 * 60
	}
	if body.MaxJitterSeconds == 0 {
		body.MaxJitterSeconds = int(4.5 * 60 * 60)
	}
	if body.MinJitterSeconds < 60 {
		writeError(w, http.StatusUnprocessableEntity, "min_jitter_seconds must be >= 60 (1 minute)")
		return
	}
	if body.MaxJitterSeconds < body.MinJitterSeconds {
		writeError(w, http.StatusUnprocessableEntity, "max_jitter_seconds must be >= min_jitter_seconds")
		return
	}

	// Workspace ownership check BEFORE the idempotency cache lookup.
	// Without this gate, an attacker could send Idempotency-Key=X with
	// body.WorkspaceID=Y (some other tenant's id) and "steal" that
	// workspace's cached entries. The (workspace_id, idempotency_key)
	// UNIQUE on idempotency_records would still return their entry on
	// hit, leaking their data. Verifying ws.OwnerID == userID first
	// closes that vector.
	ws, err := r.workspaceStore.FindByID(body.WorkspaceID)
	if err != nil {
		code, msg := mapWorkspaceError(err)
		writeError(w, code, "workspace lookup: "+msg)
		return
	}
	if ws == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if ws.OwnerID != userID {
		writeError(w, http.StatusForbidden, "workspace not owned by this user")
		return
	}

	// Cache lookup: returns one of {Continue, Replay, Conflict}. The
	// resource_type discriminator is "drive_batch" so a future replay
	// will dispatch into the drive_batch branch of
	// replayIdempotentResource, which reads the side row from
	// idempotency_batch_replays and writes the cached bytes verbatim.
	idemKey := strings.TrimSpace(req.Header.Get("Idempotency-Key"))
	idemOutcome, idemRec, idemErr := idempotencyLookup(r, ws.ID, idemKey, hash, idempotencyResourceTypeDriveBatch)
	if idemErr != nil {
		if strings.Contains(idemErr.Error(), "exceeds") {
			// Idempotency-Key exceeds 255 chars — client-side
			// contract violation (Stripe-mandated limit).
			writeError(w, http.StatusBadRequest, idemErr.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "idempotency lookup: "+idemErr.Error())
		return
	}
	switch idemOutcome {
	case idempotencyConflict:
		writeError(w, http.StatusConflict, "idempotency_key_conflict")
		return
	case idempotencyReplay:
		if replayErr := replayIdempotentResource(r, w, idemRec, idemRec.ResponseStatus); replayErr != nil {
			writeError(w, http.StatusInternalServerError, "idempotency replay: "+replayErr.Error())
		}
		return
	case idempotencyContinue:
		// Fall through to the rest of the handler.
	}

	// Facebook target ownership.
	fbAccount, err := r.userRepo.FindPlatformAccountByID(body.FacebookAccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find facebook account: "+err.Error())
		return
	}
	if fbAccount == nil || fbAccount.UserID != userID || fbAccount.Platform != models.PlatformFacebook {
		writeError(w, http.StatusNotFound, "facebook page account not found")
		return
	}

	// Resolve the folder lister from capRouter. The Google Drive provider
	// is registered when GOOGLE_DRIVE_CLIENT_ID is set OR when the
	// registry decides it should be present — practically it's always
	// there because OAuth linking was added previously.
	lister, ok := r.capabilities.Get("google-drive")
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "google-drive provider not configured")
		return
	}
	folderLister, ok := lister.(services.DriveFolderLister)
	if !ok {
		// Generic message — no info leak about whether it's "old build"
		// vs "config gap"; both 503s and the operator can grep logs.
		writeError(w, http.StatusServiceUnavailable, "drive folder listing not available on this server")
		return
	}

	// Resolve listing mode: authenticated (via user's Drive grant) or
	// public (needs GOOGLE_DRIVE_API_KEY on the drive service). The public
	// path's API-key gap is surfaced later via the typed sentinel by the
	// service (ErrDriveListRequiresAPIKey) — no extra state needed here.
	var listingAccessToken string
	var needsDriveAccount bool
	if body.DriveAccountID > 0 {
		driveAccount, err := r.userRepo.FindPlatformAccountByID(body.DriveAccountID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "find drive account: "+err.Error())
			return
		}
		if driveAccount == nil || driveAccount.UserID != userID || driveAccount.Platform != "google-drive" {
			writeError(w, http.StatusNotFound, "google drive account not found")
			return
		}
		if r.vault == nil {
			writeError(w, http.StatusNotImplemented, "credential vault not configured")
			return
		}
		driveProvider, ok := lister.(services.DriveImporter)
		if !ok {
			writeError(w, http.StatusServiceUnavailable, "google-drive provider does not implement drive import")
			return
		}
		accessToken, err := driveAccessToken(req.Context(), r.vault, driveProvider, driveAccount.ID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "failed to refresh google drive token: "+err.Error())
			return
		}
		listingAccessToken = accessToken
	} else {
		needsDriveAccount = true
	}

	// Task 6/10 — Shared Drive auto-resolve. Resolve the folder's
	// driveId for ListFolder's driveID parameter so Shared Drive
	// folders get `corpora=drive&driveId=…` while My Drive folders
	// stay on the default corpus. Best-effort by design: a failure
	// here (network, 404, parse, type-assertion miss) just logs a
	// warn-level remediation hint and falls back to "" (= pre-T6/10
	// behaviour, full back-compat). The folder's driveId is stable
	// for the lifetime of a folder, so this resolution is once per
	// import — NOT per page — to halve Drive API quota usage.
	inspector, canInspect := lister.(services.DriveFolderInspector)
	resolvedDriveID, resolveErr := services.ResolveFolderDriveID(req.Context(), inspector, body.FolderID, listingAccessToken)
	if resolveErr != nil {
		slog.Warn("drive batch import: folder metadata fetch failed; falling back to My Drive corpus",
			"folder_id", body.FolderID,
			"user_id", userID,
			"inspector_available", canInspect,
			"error", resolveErr,
		)
		resolvedDriveID = ""
	}

	// List folder contents — page_token (when present) makes Drive
	// continue from the previous page instead of returning page 1.
	// resolvedDriveID is "" for My Drive folders (no driveId scoping)
	// and a Shared Drive's id for Shared Drive folders (corpora=drive).
	files, nextPageToken, err := folderLister.ListFolder(req.Context(), body.FolderID, resolvedDriveID, listingAccessToken, body.PageToken)
	if err != nil {
		// Typed sentinel: missing API key on the server is a deploy
		// configuration gap (operator-fixable), NOT a transient
		// upstream failure. We return HTTP 200 with structured flags
		// so the SPA can render a clear CTA (configure API key or
		// link a Drive account) instead of treating it as a fatal
		// error. Networking / upstream Drive errors still 502.
		// Generic message in the body — upstream error details stay in
		// server logs (don't echo Drive's raw error to the client).
		if errors.Is(err, services.ErrDriveListRequiresAPIKey) {
			writeJSON(w, http.StatusOK, DriveBatchImportResponse{
				FolderID:               body.FolderID,
				ScheduledCount:         0,
				Entries:                []DriveBatchImportItem{},
				NeedsDriveAccount:      needsDriveAccount,
				NeedsGoogleDriveAPIKey: true,
				Note:                   "Server is missing GOOGLE_DRIVE_API_KEY (or link a Google Drive account for authenticated listing). Either set GOOGLE_DRIVE_API_KEY in the server env, OR pass drive_account_id in this request body to use your linked Drive account.",
			})
			return
		}
		slog.Warn("drive batch import: upstream folder list failed", "folder_id", body.FolderID, "error", err)
		writeError(w, http.StatusBadGateway, "drive folder list failed (see server logs for details)")
		return
	}

	if len(files) == 0 {
		// Empty or non-existent folder. 200 OK so the SPA renders a
		// productive "no videos found" message instead of an error.
		writeJSON(w, http.StatusOK, DriveBatchImportResponse{
			FolderID:          body.FolderID,
			ScheduledCount:    0,
			Entries:           []DriveBatchImportItem{},
			Note:              "no videos found in the folder (or folder is empty / has zero video files)",
			NeedsDriveAccount: needsDriveAccount,
		})
		return
	}

	// Build the staggered schedule. Index 0 of THIS PAGE publishes at
	// `cursor` (the previous response's last_scheduled_at, when supplied
	// for a pagination call, otherwise NOW). For i>0 within this page
	// each job is `previous + rand(min,max)` — across-page continuity is
	// what cursor_scheduled_at preserves.
	now := time.Now()
	cursor := now
	var cursorClampedToNow bool
	if body.CursorScheduledAt != nil {
		// Only honour the cursor for forward-looking schedules; if the
		// user (or a previous buggy operator script) sends a cursor in the
		// past, we'd start publishing backdated posts and they'd fire
		// immediately. Clamp to max(now, cursor) AND surface the clamp
		// in the response so the caller can self-correct.
		if body.CursorScheduledAt.After(now.Add(-1 * time.Minute)) {
			cursor = *body.CursorScheduledAt
		} else {
			cursor = now
			cursorClampedToNow = true
			slog.Warn("drive batch import: cursor_scheduled_at was too far in the past, clamped to NOW",
				"user_id", userID,
				"folder_id", body.FolderID,
				"workspace_id", body.WorkspaceID,
				"supplied_cursor", body.CursorScheduledAt.Format(time.RFC3339),
				"now", now.Format(time.RFC3339),
			)
		}
	}
	entries := make([]DriveBatchImportItem, 0, len(files))
	for idx, f := range files {
		scheduledAt := cursor
		if idx > 0 {
			gap, gapErr := randomDurationInRange(body.MinJitterSeconds, body.MaxJitterSeconds)
			if gapErr != nil {
				writeError(w, http.StatusInternalServerError, "jitter rand failed: "+gapErr.Error())
				return
			}
			scheduledAt = cursor.Add(gap)
		}
		if scheduledAt.Sub(now) > time.Duration(driveBatchJitterMaxSeconds)*time.Second {
			scheduledAt = now.Add(time.Duration(driveBatchJitterMaxSeconds) * time.Second)
		}

		title := body.Title
		if title == "" {
			title = f.Name
		}
		caption := body.CaptionPrefix
		if caption == "" {
			caption = f.Name
		} else {
			caption = caption + " — " + f.Name
		}

		job := &models.UploadJob{
			UserID:         userID,
			WorkspaceID:    body.WorkspaceID,
			SourceType:     models.UploadJobSourceAuthenticatedDrive,
			DriveAccountID: &body.DriveAccountID,
			SourceID:       f.ID,
			// FolderID is the new migration-038 column. Wiring it here so the
			// dashboard status endpoint can GROUP BY folder and report counts
			// without scanning the entire upload_jobs table on every poll.
			FolderID:  &body.FolderID, // pointer so SQL NULL when empty
			Title:     title,
			Caption:   caption,
			Targets:   []int64{body.FacebookAccountID},
			Status:    models.UploadJobStatusPending,
			PublishAt: &scheduledAt,
		}
		if err := r.uploadJobStore.Create(job); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("create upload job for %s: %v", f.Name, err))
			return
		}

		entries = append(entries, DriveBatchImportItem{
			Index:         idx,
			DriveFileID:   f.ID,
			Name:          f.Name,
			MimeType:      f.MimeType,
			JobID:         job.ID,
			PublishAt:     scheduledAt,
			RelativeHours: scheduledAt.Sub(now).Hours(),
		})
		cursor = scheduledAt
	}

	resp := DriveBatchImportResponse{
		FolderID:            body.FolderID,
		ScheduledCount:      len(entries),
		TotalRuntimeSeconds: int(cursor.Sub(now).Seconds()),
		FirstPublishAt:      entries[0].PublishAt,
		LastScheduledAt:     entries[len(entries)-1].PublishAt,
		NextPageToken:       nextPageToken,
		Entries:             entries,
		NeedsDriveAccount:   needsDriveAccount,
		CursorClampedToNow:  cursorClampedToNow,
	}
	if nextPageToken != "" {
		resp.Note = "folder contains more videos than fit on one page. To continue: re-call this endpoint with `page_token` = next_page_token AND `cursor_scheduled_at` = last_scheduled_at (in RFC3339). The cursor is what keeps the random 3-4.5h gap continuous across pages \u2014 sending cursor_scheduled_at empty collapses the gap at the page boundary. Stop re-calling when next_page_token comes back empty."
	}

	slog.Info("drive batch import queued",
		"user_id", userID,
		"folder_id", body.FolderID,
		"workspace_id", body.WorkspaceID,
		"facebook_account_id", body.FacebookAccountID,
		"video_count", len(entries),
		"first_publish_at", resp.FirstPublishAt,
		"last_scheduled_at", resp.LastScheduledAt,
	)

	// Marshal the response once so the SAME bytes are both written
	// to the wire (the SPA receives them) and cached for replay
	// (insertBatchIdempotentRecord stores them verbatim in
	// idempotency_batch_replays.response_payload). Marshal-once is
	// stricter than doubling work, and it guarantees a future replay
	// returns byte-identical JSON even if writeJSON's internals or
	// json.Marshal's field-ordering rules ever change.
	respBytes, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		// Should never happen — DriveBatchImportResponse has only
		// stdlib-compatible field types — but degrade to the
		// existing writeJSON path and log loudly so an operator
		// can investigate without losing the response.
		slog.Warn("drive batch import: response marshal failed; falling back to writeJSON",
			"folder_id", body.FolderID,
			"error", marshalErr)
		writeJSON(w, http.StatusAccepted, resp)
	} else {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write(respBytes)
	}

	// Idempotency-Key post-handler write (LEVEL 1, migrations 021 +
	// 039). Best-effort: insertBatchIdempotentRecord logs warnings
	// on side-row write failure and never propagates (the original
	// batch is already persisted in upload_jobs). We deliberately
	// cache ONLY successful batches (ScheduledCount > 0) — empty
	// folders / missing-API-key guidance / upstream 502 responses
	// skip caching so a retry after the underlying issue is fixed
	// can re-run the handler to get a fresh response. resource_id
	// on the parent row is the first scheduled job's id (always
	// > 0 once any job was created), satisfying the existing
	// NOT NULL + > 0 validator on idempotency_records.resource_id.
	if idemKey != "" && resp.ScheduledCount > 0 && len(entries) > 0 && respBytes != nil {
		insertBatchIdempotentRecord(
			r,
			ws.ID,
			idemKey,
			entries[0].JobID,
			hash,
			http.StatusAccepted,
			respBytes,
		)
	}
}
