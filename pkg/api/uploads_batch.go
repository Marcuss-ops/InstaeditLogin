// Package api — server-side batch folder import.
//
// POST /api/v1/uploads/batch/by-folder reuses the listing + scheduling
// pipeline of /api/v1/media/import/drive/folder but auto-pages through
// Drive's next_page_token transparently server-side. One HTTP
// round-trip per folder regardless of size (up to driveBatchMaxPages
// = 50 pages × 200 videos/page = 10,000 entries).
//
// Why a separate file instead of refactoring handleDriveBatchImport:
// handleDriveBatchImport's tests pin very specific HTTP statuses (202 /
// 200 / 422 / 502 / 404) and JSON shape (cursor_clamped_to_now as an
// omitempty field, next_page_token always emitted, etc.). Refactoring
// the existing handler to share a runDriveBatchPage helper risks
// regressing those tests on subtle marshalling / ordering details.
// This file duplicates the ~80 listing+scheduling lines so the existing
// handler stays 100% untouched. The duplication is bounded to one
// function (foldersByFolderRunPage below) and the two paths will
// diverge naturally as new endpoint-specific features land.
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

// driveBatchMaxPages caps how many Drive pages the by-folder endpoint
// will auto-process in a single call. 50 pages × 200 videos per page
// = 10,000 videos, the asymptote where the cumulative schedule
// (driveBatchJitterMaxSeconds = 7 days) runs out of room. Folders
// with >10,000 videos should be split into multiple manual calls
// using /api/v1/media/import/drive/folder.
//
// This cap is for the new SPA-facing endpoint only; the CLI loopper
// (cmd/batch-import-drive-folder) has no hard cap because its
// SIGINT/SIGTERM abort path means an operator can resume by hand.
const driveBatchMaxPages = 50

// UploadsBatchByFolderRequest is the body for
// POST /api/v1/uploads/batch/by-folder.
//
// Strict subset of DriveBatchImportRequest — page_token and
// cursor_scheduled_at are deliberately absent because the handler
// advances the cursor across pages itself. Sending them would be a
// no-op (we ignore the keys when present) so we don't have to 422.
type UploadsBatchByFolderRequest struct {
	FolderID          string `json:"folder_id"`
	DriveAccountID    int64  `json:"drive_account_id"`
	WorkspaceID       int64  `json:"workspace_id"`
	FacebookAccountID int64  `json:"facebook_account_id"`
	Title             string `json:"title"`
	CaptionPrefix     string `json:"caption_prefix"`
	MinJitterSeconds  int    `json:"min_jitter_seconds"`
	MaxJitterSeconds  int    `json:"max_jitter_seconds"`
}

// UploadsBatchByFolderResponse is the flat response that merges every
// page's entries into one document.
//
// PartialFailure signals that the by-folder auto-pagination aborted
// mid-folder (page N of M returned an upstream 5xx after pages 1..N-1
// succeeded). The response still emits every job that WAS
// successfully queued + the page token Drive returned on the failing
// page + the last_scheduled_at, so the operator can resume
// manually via /api/v1/media/import/drive/folder with the supplied
// `failed_at_page_token` + `cursor_scheduled_at`. We do NOT cache
// partial responses — a retry should re-run from page 1 so the
// cache-vs-truth stays clean.
//
// Note is always set when ScheduledCount==0 (folder had no videos
// OR exhausted pages without any successful entries) so the SPA
// can render "nothing scheduled" without inferring from empty
// string fields.
type UploadsBatchByFolderResponse struct {
	FolderID               string                 `json:"folder_id"`
	ScheduledCount         int                    `json:"scheduled_count"`
	PageCount              int                    `json:"page_count"`
	TotalRuntimeSeconds    int                    `json:"total_runtime_estimate_seconds"`
	FirstPublishAt         time.Time              `json:"first_publish_at"`
	LastScheduledAt        time.Time              `json:"last_scheduled_at"`
	Entries                []DriveBatchImportItem `json:"entries"`
	NeedsGoogleDriveAPIKey bool                   `json:"needs_google_drive_api_key,omitempty"`
	NeedsDriveAccount      bool                   `json:"needs_drive_account,omitempty"`
	PartialFailure         bool                   `json:"partial_failure,omitempty"`
	FailedAtPageToken      string                 `json:"failed_at_page_token,omitempty"`
	FailedAtPage           int                    `json:"failed_at_page,omitempty"`
	CursorClampedToNow     bool                   `json:"cursor_clamped_to_now,omitempty"`
	Note                   string                 `json:"note,omitempty"`
}

// handleUploadsBatchByFolder implements POST /api/v1/uploads/batch/by-folder.
// Auto-paginates the single-page handleDriveBatchImport equivalent
// server-side. See UploadsBatchByFolderRequest / Response for the
// contract. The diff from handleDriveBatchImport:
//
//  1. body omits page_token + cursor_scheduled_at;
//  2. response flattens every page into one entries[] (and adds
//     page_count + partial_failure + failed_at_page_token);
//  3. on upstream failure mid-pagination, returns 200 + partial_failure=true
//     with everything queued so far + the Drive page_token that
//     failed (operator can resume manually);
//  4. caps at driveBatchMaxPages; above cap → 413;
//  5. caches the FULL response on success (ScheduleCount>0, no
//     partial) via insertBatchIdempotentRecord so retry replay
//     returns the complete cross-page body byte-for-byte; partial
//     failures + zero-entry responses are deliberately skipped from
//     the cache so retry re-runs.
//
// Authz + idempotency mirror handleDriveBatchImport step-by-step
// (workspace ownership check BEFORE idempotency cache lookup, same
// Keys/MaxLen contract via idempotencyKeyMaxLen) so a wrong tenant
// cannot "steal" another's cached batch by collision on
// (workspace_id, key).
func (r *Router) handleUploadsBatchByFolder(w http.ResponseWriter, req *http.Request) {
	if r.uploadJobStore == nil || r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "upload jobs not configured on this server")
		return
	}

	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}

	// Read + hash the body once. The hash spans every field the
	// caller can vary (folder_id, account ids, jitter, title,
	// caption). page_token/cursor are NOT in this body, so the hash
	// spans the operator-relevant fields (matches the existing
	// endpoint's contract — accidental double-paste of
	// page_token should produce the SAME batch).
	bodyBytes, bodyErr := idempotencyReadBody(req)
	if bodyErr != nil {
		writeError(w, http.StatusBadRequest, "request body unreadable: "+bodyErr.Error())
		return
	}
	hash := idempotencyHash(bodyBytes)

	var body UploadsBatchByFolderRequest
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

	// Default jitter: 3h-4.5h (matches the user-facing spec + the
	// single-page endpoint). Anything tighter than 60s collapses
	// anti-pattern-detection so the floor is enforced.
	minJitter := body.MinJitterSeconds
	if minJitter == 0 {
		minJitter = 3 * 60 * 60
	}
	maxJitter := body.MaxJitterSeconds
	if maxJitter == 0 {
		maxJitter = int(4.5 * 60 * 60)
	}
	if minJitter < 60 {
		writeError(w, http.StatusUnprocessableEntity, "min_jitter_seconds must be >= 60 (1 minute)")
		return
	}
	if maxJitter < minJitter {
		writeError(w, http.StatusUnprocessableEntity, "max_jitter_seconds must be >= min_jitter_seconds")
		return
	}

	// Workspace ownership gate — MUST run before idempotency cache
	// lookup (same order as handleDriveBatchImport) so an attacker
	// can't forge another tenant's workspace_id in body to "steal"
	// their cached batch via (workspace_id, key) collision.
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

	// Idempotency-Key: lookup before any side-effects (Drive listing
	// or job creation). On hit+match the cached bytes are returned
	// verbatim; on hit+mismatch we 409; on miss we run.
	idemKey := strings.TrimSpace(req.Header.Get("Idempotency-Key"))
	idemOutcome, idemRec, idemErr := idempotencyLookup(r, ws.ID, idemKey, hash, idempotencyResourceTypeDriveBatch)
	if idemErr != nil {
		if strings.Contains(idemErr.Error(), "exceeds") {
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
		// fall through to the loop below
	}

	// Facebook target ownership (the upload_jobs.targets[] entry).
	fbAccount, err := r.userRepo.FindPlatformAccountByID(body.FacebookAccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find facebook account: "+err.Error())
		return
	}
	if fbAccount == nil || fbAccount.UserID != userID || fbAccount.Platform != models.PlatformFacebook {
		writeError(w, http.StatusNotFound, "facebook page account not found")
		return
	}

	// Resolve Drive listing token: either via the user's linked
	// Drive OAuth grant (body.DriveAccountID>0) or via the server
	// GOOGLE_DRIVE_API_KEY (only valid for public folders; the
	// service surfaces ErrDriveListRequiresAPIKey if it's missing).
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
		lister, _ := r.capabilities.Get("google-drive")
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

	// Resolve folder lister once (capRouter keyed on "google-drive").
	lister, ok := r.capabilities.Get("google-drive")
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "google-drive provider not configured")
		return
	}
	folderLister, ok := lister.(services.DriveFolderLister)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "drive folder listing not available on this server")
		return
	}

	// Multi-page loop. The cursor advances monotonically across
	// pages via the LAST entry's scheduled_at on every iteration so
	// the cumulative stagger is uninterrupted (matching what
	// cmd/batch-import-drive-folder does after each page).
	startedAt := time.Now()
	cursor := startedAt
	var allEntries []DriveBatchImportItem
	firstPublish := time.Time{}
	pageNum := 0
	pageToken := ""
	partialFailure := false
	failedAtPageToken := ""
	failedAtPage := 0

	for {
		pageNum++
		if pageNum > driveBatchMaxPages {
			// We've already queued upload_jobs for the previous
			// pages — they STAY queued (no rollback). The 413
			// response surfaces the cap so the SPA can split the
			// import into smaller chunks (split folder, or break
			// the source folder into N folders of ≤10k each).
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("folder has more than driveBatchMaxPages=%d pages; split the import into smaller chunks or use the CLI", driveBatchMaxPages))
			return
		}
		files, nextPageToken, err := folderLister.ListFolder(req.Context(), body.FolderID, listingAccessToken, pageToken)
		if err != nil {
			// Config gap (server-side GOOGLE_DRIVE_API_KEY missing
			// AND no per-user Drive grant provided) is detected on
			// the FIRST page only. The typed sentinel
			// ErrDriveListRequiresAPIKey surfaces a clean CTA,
			// not a fatal 5xx — same pattern as handleDriveBatchImport.
			if errors.Is(err, services.ErrDriveListRequiresAPIKey) && pageNum == 1 {
				writeJSON(w, http.StatusOK, UploadsBatchByFolderResponse{
					FolderID:               body.FolderID,
					ScheduledCount:         0,
					PageCount:              1,
					Entries:                []DriveBatchImportItem{},
					NeedsDriveAccount:      needsDriveAccount,
					NeedsGoogleDriveAPIKey: true,
					Note:                   "Server is missing GOOGLE_DRIVE_API_KEY (or link a Google Drive account for authenticated listing). Either set GOOGLE_DRIVE_API_KEY in the server env, OR pass drive_account_id in this request body to use your linked Drive account.",
				})
				return
			}
			// Generic upstream 5xx (the folder lister service
			// returns the wrapped error after its own retries): if
			// we already queued entries, surface partial state so
			// the operator can resume; if this was page 1, full
			// 502 + log the error.
			slog.Warn("uploads batch by-folder: upstream page failed",
				"page_num", pageNum,
				"page_token", pageToken,
				"folder_id", body.FolderID,
				"user_id", userID,
				"error", err)
			if len(allEntries) > 0 {
				partialFailure = true
				failedAtPage = pageNum
				failedAtPageToken = pageToken
				break
			}
			writeError(w, http.StatusBadGateway, "drive folder list failed (see server logs for details)")
			return
		}

		if len(files) == 0 {
			// Empty page: this means either the folder has no
			// videos at all (page 1) — surface 200 with note —
			// OR we hit a phantom empty page mid-pagination (rare;
			// Drive would normally return next_page_token + N>0).
			// Mid-pagination empty is treated as end-of-folder.
			if pageNum == 1 {
				writeJSON(w, http.StatusOK, UploadsBatchByFolderResponse{
					FolderID:        body.FolderID,
					ScheduledCount:  0,
					PageCount:       1,
					Entries:         []DriveBatchImportItem{},
					Note:            "no videos found in the folder",
					NeedsDriveAccount: needsDriveAccount,
				})
				return
			}
			break
		}

		// Schedule this page's files. Index offset across the WHOLE
		// folder so the SPA can identify "this is the 47th video
		// overall" not just "the 27th on page 3".
		var pageEntries []DriveBatchImportItem
		for idx, f := range files {
			scheduledAt := cursor
			if idx > 0 {
				gap, gapErr := randomDurationInRange(minJitter, maxJitter)
				if gapErr != nil {
					writeError(w, http.StatusInternalServerError, "jitter rand failed: "+gapErr.Error())
					return
				}
				scheduledAt = cursor.Add(gap)
			}
			// Cap forward-looking schedule at driveBatchJitterMaxSeconds
			// (7 days). Anything beyond would silently collapse a
			// long batch — clamp + keep going.
			if scheduledAt.Sub(startedAt) > time.Duration(driveBatchJitterMaxSeconds)*time.Second {
				scheduledAt = startedAt.Add(time.Duration(driveBatchJitterMaxSeconds) * time.Second)
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
				UserID:      userID,
				WorkspaceID: body.WorkspaceID,
				SourceType:  models.UploadJobSourcePublicDrive,
				SourceID:    f.ID,
				FolderID:    &body.FolderID,
				Title:       title,
				Caption:     caption,
				Targets:     []int64{body.FacebookAccountID},
				Status:      models.UploadJobStatusPending,
				ScheduledAt: &scheduledAt,
			}
			if err := r.uploadJobStore.Create(job); err != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("create upload job for %s: %v", f.Name, err))
				return
			}

			pageEntries = append(pageEntries, DriveBatchImportItem{
				Index:         len(allEntries) + idx,
				DriveFileID:   f.ID,
				Name:          f.Name,
				MimeType:      f.MimeType,
				JobID:         job.ID,
				ScheduledAt:   scheduledAt,
				RelativeHours: scheduledAt.Sub(startedAt).Hours(),
			})
			cursor = scheduledAt
		}
		if firstPublish.IsZero() && len(pageEntries) > 0 {
			firstPublish = pageEntries[0].ScheduledAt
		}
		allEntries = append(allEntries, pageEntries...)

		if nextPageToken == "" {
			break
		}
		pageToken = nextPageToken
	}

	// Build the flat response.
	resp := UploadsBatchByFolderResponse{
		FolderID:        body.FolderID,
		ScheduledCount:  len(allEntries),
		PageCount:       pageNum,
		Entries:         allEntries,
		NeedsDriveAccount: needsDriveAccount,
		PartialFailure:  partialFailure,
		FailedAtPageToken: failedAtPageToken,
		FailedAtPage:    failedAtPage,
	}
	if len(allEntries) > 0 {
		resp.FirstPublishAt = firstPublish
		resp.LastScheduledAt = allEntries[len(allEntries)-1].ScheduledAt
		resp.TotalRuntimeSeconds = int(allEntries[len(allEntries)-1].ScheduledAt.Sub(startedAt).Seconds())
	} else {
		resp.Note = "no videos found in the folder"
	}
	if partialFailure {
		resp.Note = fmt.Sprintf(
			"partial failure on page %d: %d jobs were queued before the upstream error. To resume, re-call POST /api/v1/media/import/drive/folder with page_token=%q and cursor_scheduled_at=%q.",
			failedAtPage, len(allEntries), failedAtPageToken,
			resp.LastScheduledAt.UTC().Format(time.RFC3339),
		)
	}

	slog.Info("uploads batch by-folder queued",
		"user_id", userID,
		"folder_id", body.FolderID,
		"workspace_id", body.WorkspaceID,
		"page_count", pageNum,
		"video_count", resp.ScheduledCount,
		"partial_failure", partialFailure,
		"first_publish_at", resp.FirstPublishAt,
		"last_scheduled_at", resp.LastScheduledAt,
	)

	// Marshal once so the SAME bytes are both written to the wire
	// (SPA receives them) and cached for replay (insertBatchIdempotentRecord
	// stores them verbatim in idempotency_batch_replays.response_payload).
	// Identical pattern to handleDriveBatchImport's marshal-once.
	respBytes, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		slog.Warn("uploads batch by-folder: response marshal failed; falling back to writeJSON",
			"folder_id", body.FolderID,
			"error", marshalErr)
		writeJSON(w, http.StatusAccepted, resp)
	} else {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write(respBytes)
	}

	// Cache ONLY on full success. Partial failures are NOT cached
	// — the partial response is incomplete (page 3 of 5 was lost)
	// and a retry should re-run from page 1 to converge on truth.
	// Zero-entry responses are also not cached (matches the
	// existing endpoint's "only successful batches" policy).
	if !partialFailure && resp.ScheduledCount > 0 && len(allEntries) > 0 && respBytes != nil {
		insertBatchIdempotentRecord(
			r, ws.ID, idemKey, allEntries[0].JobID, hash, http.StatusAccepted, respBytes,
		)
	}
}
