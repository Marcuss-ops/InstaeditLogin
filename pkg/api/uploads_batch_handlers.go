// Package api — server-side batch folder import.
//
// POST /api/v1/uploads/batch/by-folder reuses the listing + scheduling
// pipeline of /api/v1/media/import/drive/folder but auto-pages through
// Drive's next_page_token transparently server-side. One HTTP
// round-trip per folder regardless of size (up to driveBatchMaxPages
// = 50 pages × 200 videos/page = 10,000 entries).
//
// The historically duplicated ~80 listing+scheduling lines now live in
// drive_batch_helpers.go and are shared with handleDriveBatchImport
// (validateDriveBatchCommon, requireOwnedWorkspaceByID,
// runIdempotencyGate, requireOwnedFacebookPage,
// resolveDriveListingToken, resolveSharedDriveID,
// scheduleDriveBatchFiles, writeBatchResponseMarshalOnce). This file
// keeps only the by-folder specifics: the auto-pagination loop, the
// page cap, and the partial-failure surface.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

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

// uploadsBatchPaginationResult carries the outcome of the multi-page
// scheduling loop back to the handler for response assembly.
type uploadsBatchPaginationResult struct {
	entries           []DriveBatchImportItem
	firstPublish      time.Time
	pageCount         int
	partialFailure    bool
	failedAtPageToken string
	failedAtPage      int
}

// runUploadsBatchPagination drives the by-folder auto-pagination:
// per-page ListFolder + scheduleDriveBatchFiles, advancing the cursor
// monotonically across pages. Returns ok=false when a terminal
// response was already written (413 page cap, first-page config-gap
// 200, first-page empty 200, first-page 502, or a scheduling error).
// A mid-pagination upstream failure is NOT terminal: it surfaces as
// partialFailure=true with everything queued so far so the operator
// can resume manually.
func (r *Router) runUploadsBatchPagination(
	w http.ResponseWriter,
	req *http.Request,
	userID int64,
	params driveBatchCommonParams,
	folderLister services.DriveFolderLister,
	resolvedDriveID, listingAccessToken string,
	needsDriveAccount bool,
	startedAt time.Time,
) (uploadsBatchPaginationResult, bool) {
	var out uploadsBatchPaginationResult
	cursor := startedAt
	pageToken := ""

	for {
		out.pageCount++
		if out.pageCount > driveBatchMaxPages {
			// We've already queued upload_jobs for the previous
			// pages — they STAY queued (no rollback). The 413
			// response surfaces the cap so the SPA can split the
			// import into smaller chunks (split folder, or break
			// the source folder into N folders of ≤10k each).
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("folder has more than driveBatchMaxPages=%d pages; split the import into smaller chunks or use the CLI", driveBatchMaxPages))
			return out, false
		}
		files, nextPageToken, err := folderLister.ListFolder(req.Context(), params.FolderID, resolvedDriveID, listingAccessToken, pageToken)
		if err != nil {
			// Config gap (server-side GOOGLE_DRIVE_API_KEY missing
			// AND no per-user Drive grant provided) is detected on
			// the FIRST page only. The typed sentinel
			// ErrDriveListRequiresAPIKey surfaces a clean CTA,
			// not a fatal 5xx — same pattern as handleDriveBatchImport.
			if errors.Is(err, services.ErrDriveListRequiresAPIKey) && out.pageCount == 1 {
				writeJSON(w, http.StatusOK, UploadsBatchByFolderResponse{
					FolderID:               params.FolderID,
					ScheduledCount:         0,
					PageCount:              1,
					Entries:                []DriveBatchImportItem{},
					NeedsDriveAccount:      needsDriveAccount,
					NeedsGoogleDriveAPIKey: true,
					Note:                   "Server is missing GOOGLE_DRIVE_API_KEY (or link a Google Drive account for authenticated listing). Either set GOOGLE_DRIVE_API_KEY in the server env, OR pass drive_account_id in this request body to use your linked Drive account.",
				})
				return out, false
			}
			// Generic upstream 5xx (the folder lister service
			// returns the wrapped error after its own retries): if
			// we already queued entries, surface partial state so
			// the operator can resume; if this was page 1, full
			// 502 + log the error.
			slog.Warn("uploads batch by-folder: upstream page failed",
				"page_num", out.pageCount,
				"page_token", pageToken,
				"folder_id", params.FolderID,
				"user_id", userID,
				"error", err)
			if len(out.entries) > 0 {
				out.partialFailure = true
				out.failedAtPage = out.pageCount
				out.failedAtPageToken = pageToken
				return out, true
			}
			writeError(w, http.StatusBadGateway, "drive folder list failed (see server logs for details)")
			return out, false
		}

		if len(files) == 0 {
			// Empty page: this means either the folder has no
			// videos at all (page 1) — surface 200 with note —
			// OR we hit a phantom empty page mid-pagination (rare;
			// Drive would normally return next_page_token + N>0).
			// Mid-pagination empty is treated as end-of-folder.
			if out.pageCount == 1 {
				writeJSON(w, http.StatusOK, UploadsBatchByFolderResponse{
					FolderID:          params.FolderID,
					ScheduledCount:    0,
					PageCount:         1,
					Entries:           []DriveBatchImportItem{},
					Note:              "no videos found in the folder",
					NeedsDriveAccount: needsDriveAccount,
				})
				return out, false
			}
			return out, true
		}

		// Schedule this page's files. Index offset across the WHOLE
		// folder so the SPA can identify "this is the 47th video
		// overall" not just "the 27th on page 3".
		pageEntries, newCursor, ok := r.scheduleDriveBatchFiles(w, userID, params, files, cursor, startedAt, len(out.entries))
		if !ok {
			return out, false
		}
		cursor = newCursor
		if out.firstPublish.IsZero() && len(pageEntries) > 0 {
			out.firstPublish = pageEntries[0].PublishAt
		}
		out.entries = append(out.entries, pageEntries...)

		if nextPageToken == "" {
			return out, true
		}
		pageToken = nextPageToken
	}
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
	bodyBytes, bodyErr := idempotencyReadBody(w, req)
	if bodyErr != nil {
		writeRequestBodyError(w, bodyErr)
		return
	}
	hash := idempotencyHash(bodyBytes)

	var body UploadsBatchByFolderRequest
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	params := driveBatchCommonParams{
		FolderID:          body.FolderID,
		DriveAccountID:    body.DriveAccountID,
		WorkspaceID:       body.WorkspaceID,
		FacebookAccountID: body.FacebookAccountID,
		Title:             body.Title,
		CaptionPrefix:     body.CaptionPrefix,
		MinJitterSeconds:  body.MinJitterSeconds,
		MaxJitterSeconds:  body.MaxJitterSeconds,
	}
	if !validateDriveBatchCommon(w, &params) {
		return
	}

	// Workspace ownership gate — MUST run before idempotency cache
	// lookup (same order as handleDriveBatchImport) so an attacker
	// can't forge another tenant's workspace_id in body to "steal"
	// their cached batch via (workspace_id, key) collision.
	ws, ok := r.requireOwnedWorkspaceByID(w, params.WorkspaceID, userID)
	if !ok {
		return
	}

	// Idempotency-Key: lookup before any side-effects (Drive listing
	// or job creation). On hit+match the cached bytes are returned
	// verbatim; on hit+mismatch we 409; on miss we run.
	idemKey := strings.TrimSpace(req.Header.Get("Idempotency-Key"))
	if !r.runIdempotencyGate(w, ws.ID, idemKey, hash, idempotencyResourceTypeDriveBatch) {
		return
	}

	// Facebook target ownership (the upload_jobs.targets[] entry).
	if !r.requireOwnedFacebookPage(w, params.FacebookAccountID, userID) {
		return
	}

	// Resolve Drive listing token: either via the user's linked
	// Drive OAuth grant (body.DriveAccountID>0) or via the server
	// GOOGLE_DRIVE_API_KEY (only valid for public folders; the
	// service surfaces ErrDriveListRequiresAPIKey if it's missing).
	//
	// NOTE: the token resolution historically ran BEFORE the
	// provider nil-check on this endpoint (r.capabilities.Get is
	// called inside resolveDriveListingToken's type assertion with
	// the raw value fetched below) — the order is preserved.
	var listingAccessToken string
	var needsDriveAccount bool
	if params.DriveAccountID > 0 {
		rawLister, _ := r.capabilities.Get("google-drive")
		token, ok := r.resolveDriveListingToken(w, req, userID, params.DriveAccountID, rawLister)
		if !ok {
			return
		}
		listingAccessToken = token
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

	// Task 6/10 — Shared Drive auto-resolve ONCE before the
	// pagination loop (a folder's driveId is stable for its
	// lifetime, so per-page resolution would double the Drive API
	// quota for nothing).
	resolvedDriveID := resolveSharedDriveID(req.Context(), lister, params.FolderID, listingAccessToken, userID, "uploads batch by-folder")

	// Multi-page loop — extracted to runUploadsBatchPagination. The
	// cursor advances monotonically across pages via the LAST entry's
	// scheduled_at so the cumulative stagger is uninterrupted
	// (matching what cmd/batch-import-drive-folder does per page).
	startedAt := time.Now()
	pg, ok := r.runUploadsBatchPagination(w, req, userID, params, folderLister, resolvedDriveID, listingAccessToken, needsDriveAccount, startedAt)
	if !ok {
		return
	}
	allEntries := pg.entries
	partialFailure := pg.partialFailure

	// Build the flat response.
	resp := UploadsBatchByFolderResponse{
		FolderID:          params.FolderID,
		ScheduledCount:    len(allEntries),
		PageCount:         pg.pageCount,
		Entries:           allEntries,
		NeedsDriveAccount: needsDriveAccount,
		PartialFailure:    partialFailure,
		FailedAtPageToken: pg.failedAtPageToken,
		FailedAtPage:      pg.failedAtPage,
	}
	if len(allEntries) > 0 {
		resp.FirstPublishAt = pg.firstPublish
		resp.LastScheduledAt = allEntries[len(allEntries)-1].PublishAt
		resp.TotalRuntimeSeconds = int(allEntries[len(allEntries)-1].PublishAt.Sub(startedAt).Seconds())
	} else {
		resp.Note = "no videos found in the folder"
	}
	if partialFailure {
		resp.Note = fmt.Sprintf(
			"partial failure on page %d: %d jobs were queued before the upstream error. To resume, re-call POST /api/v1/media/import/drive/folder with page_token=%q and cursor_scheduled_at=%q.",
			pg.failedAtPage, len(allEntries), pg.failedAtPageToken,
			resp.LastScheduledAt.UTC().Format(time.RFC3339),
		)
	}

	slog.Info("uploads batch by-folder queued",
		"user_id", userID,
		"folder_id", params.FolderID,
		"workspace_id", params.WorkspaceID,
		"page_count", pg.pageCount,
		"video_count", resp.ScheduledCount,
		"partial_failure", partialFailure,
		"first_publish_at", resp.FirstPublishAt,
		"last_scheduled_at", resp.LastScheduledAt,
	)

	respBytes := writeBatchResponseMarshalOnce(w, resp, "uploads batch by-folder", params.FolderID)

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
