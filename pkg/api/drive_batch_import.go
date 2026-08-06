package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// listDriveBatchFolderPage lists one Drive folder page for the
// single-page endpoint and handles its three non-continue outcomes:
//
//   - ErrDriveListRequiresAPIKey → HTTP 200 with structured CTA flags
//     (missing API key is a deploy configuration gap, operator-fixable,
//     NOT a transient upstream failure — the SPA renders "configure
//     API key or link a Drive account" instead of a fatal error).
//   - upstream error → 502 with a generic body (Drive's raw error
//     stays in server logs — no echo to the client).
//   - empty/non-existent folder → 200 with a productive "no videos
//     found" note.
//
// resolvedDriveID is "" for My Drive folders (no driveId scoping) and
// a Shared Drive's id for Shared Drive folders (corpora=drive).
func (r *Router) listDriveBatchFolderPage(
	w http.ResponseWriter,
	req *http.Request,
	folderLister services.DriveFolderLister,
	folderID, resolvedDriveID, listingAccessToken, pageToken string,
	needsDriveAccount bool,
) ([]services.GoogleDriveFile, string, bool) {
	files, nextPageToken, err := folderLister.ListFolder(req.Context(), folderID, resolvedDriveID, listingAccessToken, pageToken)
	if err != nil {
		if errors.Is(err, services.ErrDriveListRequiresAPIKey) {
			writeJSON(w, http.StatusOK, DriveBatchImportResponse{
				FolderID:               folderID,
				ScheduledCount:         0,
				Entries:                []DriveBatchImportItem{},
				NeedsDriveAccount:      needsDriveAccount,
				NeedsGoogleDriveAPIKey: true,
				Note:                   "Server is missing GOOGLE_DRIVE_API_KEY (or link a Google Drive account for authenticated listing). Either set GOOGLE_DRIVE_API_KEY in the server env, OR pass drive_account_id in this request body to use your linked Drive account.",
			})
			return nil, "", false
		}
		slog.Warn("drive batch import: upstream folder list failed", "folder_id", folderID, "error", err)
		writeError(w, http.StatusBadGateway, "drive folder list failed (see server logs for details)")
		return nil, "", false
	}
	if len(files) == 0 {
		writeJSON(w, http.StatusOK, DriveBatchImportResponse{
			FolderID:          folderID,
			ScheduledCount:    0,
			Entries:           []DriveBatchImportItem{},
			Note:              "no videos found in the folder (or folder is empty / has zero video files)",
			NeedsDriveAccount: needsDriveAccount,
		})
		return nil, "", false
	}
	return files, nextPageToken, true
}

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
//
// The shared validation / authz / jitter / idempotency / scheduling
// steps live in drive_batch_helpers.go — this handler keeps only the
// single-page specifics (page_token + cursor_scheduled_at handling).
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
	bodyBytes, bodyErr := idempotencyReadBody(w, req)
	if bodyErr != nil {
		writeRequestBodyError(w, bodyErr)
		return
	}
	hash := idempotencyHash(bodyBytes)

	var body DriveBatchImportRequest
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

	// Workspace ownership check BEFORE the idempotency cache lookup.
	// Without this gate, an attacker could send Idempotency-Key=X with
	// body.WorkspaceID=Y (some other tenant's id) and "steal" that
	// workspace's cached entries. See requireOwnedWorkspaceByID.
	ws, ok := r.requireOwnedWorkspaceByID(w, params.WorkspaceID, userID)
	if !ok {
		return
	}

	// Cache lookup: replay/conflict/continue. The resource_type
	// discriminator is "drive_batch" so a future replay dispatches
	// into the drive_batch branch of replayIdempotentResource.
	idemKey := strings.TrimSpace(req.Header.Get("Idempotency-Key"))
	if !r.runIdempotencyGate(w, ws.ID, idemKey, hash, idempotencyResourceTypeDriveBatch) {
		return
	}

	// Facebook target ownership.
	if !r.requireOwnedFacebookPage(w, params.FacebookAccountID, userID) {
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
	if params.DriveAccountID > 0 {
		token, ok := r.resolveDriveListingToken(w, req, userID, params.DriveAccountID, lister)
		if !ok {
			return
		}
		listingAccessToken = token
	} else {
		needsDriveAccount = true
	}

	// Task 6/10 — Shared Drive auto-resolve (once per import, not per
	// page — the folder's driveId is stable for the folder's lifetime).
	resolvedDriveID := resolveSharedDriveID(req.Context(), lister, params.FolderID, listingAccessToken, userID, "drive batch import")

	// List folder contents — page_token (when present) makes Drive
	// continue from the previous page instead of returning page 1.
	// The config-gap 200 (missing API key CTA), empty-folder 200 and
	// upstream 502 branches live in listDriveBatchFolderPage.
	files, nextPageToken, ok := r.listDriveBatchFolderPage(w, req, folderLister, params.FolderID, resolvedDriveID, listingAccessToken, body.PageToken, needsDriveAccount)
	if !ok {
		return
	}

	// Build the staggered schedule. Index 0 of THIS PAGE publishes at
	// `cursor` (the previous response's last_scheduled_at, when supplied
	// for a pagination call, otherwise NOW). For i>0 within this page
	// each job is `previous + rand(min,max)` — across-page continuity is
	// what cursor_scheduled_at preserves.
	now := time.Now()
	cursor, cursorClampedToNow := resolveBatchScheduleCursor(body.CursorScheduledAt, now, userID, params)
	entries, cursor, ok := r.scheduleDriveBatchFiles(w, userID, params, files, cursor, now, 0)
	if !ok {
		return
	}

	resp := DriveBatchImportResponse{
		FolderID:            params.FolderID,
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
		"folder_id", params.FolderID,
		"workspace_id", params.WorkspaceID,
		"facebook_account_id", params.FacebookAccountID,
		"video_count", len(entries),
		"first_publish_at", resp.FirstPublishAt,
		"last_scheduled_at", resp.LastScheduledAt,
	)

	respBytes := writeBatchResponseMarshalOnce(w, resp, "drive batch import", params.FolderID)

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
