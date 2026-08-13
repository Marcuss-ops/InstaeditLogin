package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/sampler"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

const defaultUploadPrepareLeadTime = 15 * time.Minute

// prepareAtForPublish keeps Drive downloads out of the scheduling request's
// critical path while ensuring the asset is staged before its public cursor.
func prepareAtForPublish(publishAt time.Time) time.Time {
	return prepareAtForPublishWithLead(publishAt, defaultUploadPrepareLeadTime)
}

func prepareAtForPublishWithLead(publishAt time.Time, lead time.Duration) time.Time {
	prepareAt := publishAt.Add(-lead)
	if prepareAt.Before(time.Now()) {
		return time.Now()
	}
	return prepareAt
}

// driveAccessToken fetches a fresh access token for a Drive account
// via the central credential vault (uses the platform's refresh flow
// when the stored token is expired).
func driveAccessToken(ctx context.Context, vault credentials.VaultAPI, importer services.DriveImporter, accountID int64) (string, error) {
	oauth, err := vault.Renew(ctx, accountID, models.TokenTypeBearer,
		func(c context.Context, refresh string) (*models.TokenData, error) {
			return importer.RefreshOAuthToken(c, refresh)
		})
	if err != nil {
		return "", err
	}
	return oauth.AccessToken, nil
}

// driveBatchCommonParams is the field set shared by the two batch
// folder-import bodies (DriveBatchImportRequest and
// UploadsBatchByFolderRequest). The single-page endpoint's extra
// fields (page_token, cursor_scheduled_at) stay on its own struct.
type driveBatchCommonParams struct {
	FolderID          string
	DriveAccountID    int64
	WorkspaceID       int64
	FacebookAccountID int64
	Title             string
	CaptionPrefix     string
	MinJitterSeconds  int
	MaxJitterSeconds  int
}

// validateDriveBatchCommon runs the shared field validation + jitter
// defaulting for the two batch folder-import endpoints. It mutates the
// jitter fields in place (defaults 3h-4.5h) and writes the 422 error
// response on failure. Returns false when a response was written.
//
// Error messages and check ORDER are pinned by the endpoint tests —
// do not reorder.
func validateDriveBatchCommon(w http.ResponseWriter, p *driveBatchCommonParams) bool {
	if strings.TrimSpace(p.FolderID) == "" {
		writeError(w, http.StatusUnprocessableEntity, "folder_id is required")
		return false
	}
	if p.WorkspaceID == 0 {
		writeError(w, http.StatusUnprocessableEntity, "workspace_id is required")
		return false
	}
	if p.FacebookAccountID == 0 {
		writeError(w, http.StatusUnprocessableEntity, "facebook_account_id is required")
		return false
	}
	// P0 hardening refactor: the public_drive download path was
	// removed from the Drive service. Every batch import must
	// flow through an authenticated Drive account's OAuth grant,
	// so drive_account_id is now required (was previously optional
	// for the legacy public-folder path).
	if p.DriveAccountID == 0 {
		writeError(w, http.StatusUnprocessableEntity,
			"drive_account_id is required (the public_drive download path was removed in the Drive pipeline hardening refactor)")
		return false
	}

	// Default jitter bounds: 3h-4.5h (matches the user-facing spec).
	if p.MinJitterSeconds == 0 {
		p.MinJitterSeconds = 3 * 60 * 60
	}
	if p.MaxJitterSeconds == 0 {
		p.MaxJitterSeconds = int(4.5 * 60 * 60)
	}
	if p.MinJitterSeconds < 60 {
		writeError(w, http.StatusUnprocessableEntity, "min_jitter_seconds must be >= 60 (1 minute)")
		return false
	}
	if p.MaxJitterSeconds < p.MinJitterSeconds {
		writeError(w, http.StatusUnprocessableEntity, "max_jitter_seconds must be >= min_jitter_seconds")
		return false
	}
	return true
}

// requireOwnedWorkspaceByID looks up the workspace and verifies the
// caller owns it, writing the error response on failure. Shared by the
// Drive import family of handlers. The ownership gate MUST run before
// any idempotency cache lookup (see the callers' comments) so an
// attacker cannot forge another tenant's workspace_id to "steal"
// cached entries by collision on (workspace_id, key).
func (r *Router) requireOwnedWorkspaceByID(w http.ResponseWriter, workspaceID, userID int64) (*models.Workspace, bool) {
	ws, err := r.workspaceStore.FindByID(workspaceID)
	if err != nil {
		code, msg := mapWorkspaceError(err)
		writeError(w, code, "workspace lookup: "+msg)
		return nil, false
	}
	if ws == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return nil, false
	}
	if ws.OwnerID != userID {
		writeError(w, http.StatusForbidden, "workspace not owned by this user")
		return nil, false
	}
	return ws, true
}

// runIdempotencyGate performs the shared Idempotency-Key
// lookup/replay/conflict dance. Returns true when the caller should
// continue with the handler body; false when a response (replay,
// conflict, or error) was already written.
func (r *Router) runIdempotencyGate(w http.ResponseWriter, wsID int64, idemKey string, hash []byte, resourceType string) bool {
	idemOutcome, idemRec, idemErr := idempotencyLookup(r, wsID, idemKey, hash, resourceType)
	if idemErr != nil {
		if strings.Contains(idemErr.Error(), "exceeds") {
			// Idempotency-Key exceeds 255 chars — client-side
			// contract violation (Stripe-mandated limit).
			writeError(w, http.StatusBadRequest, idemErr.Error())
			return false
		}
		writeError(w, http.StatusInternalServerError, "idempotency lookup: "+idemErr.Error())
		return false
	}
	switch idemOutcome {
	case idempotencyConflict:
		writeError(w, http.StatusConflict, "idempotency_key_conflict")
		return false
	case idempotencyReplay:
		if replayErr := replayIdempotentResource(r, w, idemRec, idemRec.ResponseStatus); replayErr != nil {
			writeError(w, http.StatusInternalServerError, "idempotency replay: "+replayErr.Error())
		}
		return false
	}
	// idempotencyContinue — fall through to the handler body.
	return true
}

// requireOwnedFacebookPage verifies the Facebook Page target belongs
// to the caller. Shared by the batch folder-import endpoints.
func (r *Router) requireOwnedFacebookPage(w http.ResponseWriter, accountID, userID int64) bool {
	fbAccount, err := r.userRepo.FindPlatformAccountByID(accountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find facebook account: "+err.Error())
		return false
	}
	if fbAccount == nil || fbAccount.UserID != userID || fbAccount.Platform != models.PlatformFacebook {
		writeError(w, http.StatusNotFound, "facebook page account not found")
		return false
	}
	return true
}

// resolveDriveListingToken verifies the caller's linked google-drive
// account and mints a fresh listing access token via the vault. lister
// is the raw capability value from r.capabilities.Get("google-drive")
// (the caller decides when/whether to nil-check it first — the two
// batch endpoints pin different check orders). Returns ("", false)
// after writing the error response.
func (r *Router) resolveDriveListingToken(w http.ResponseWriter, req *http.Request, userID, driveAccountID int64, lister any) (string, bool) {
	driveAccount, err := r.userRepo.FindPlatformAccountByID(driveAccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find drive account: "+err.Error())
		return "", false
	}
	if driveAccount == nil || driveAccount.UserID != userID || driveAccount.Platform != "google-drive" {
		writeError(w, http.StatusNotFound, "google drive account not found")
		return "", false
	}
	if r.vault == nil {
		writeError(w, http.StatusNotImplemented, "credential vault not configured")
		return "", false
	}
	driveProvider, ok := lister.(services.DriveImporter)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "google-drive provider does not implement drive import")
		return "", false
	}
	accessToken, err := driveAccessToken(req.Context(), r.vault, driveProvider, driveAccount.ID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "failed to refresh google drive token: "+err.Error())
		return "", false
	}
	return accessToken, true
}

// resolveSharedDriveID performs the Task 6/10 Shared Drive
// auto-resolve: fetches the folder's driveId once per import so Shared
// Drive folders get `corpora=drive&driveId=…` while My Drive folders
// stay on the default corpus. Best-effort by design: a failure just
// logs a warn-level remediation hint (prefixed with logPrefix to keep
// the per-endpoint log identity) and falls back to "" (full
// back-compat with the pre-T6/10 behaviour).
func resolveSharedDriveID(ctx context.Context, lister any, folderID, listingAccessToken string, userID int64, logPrefix string) string {
	inspector, canInspect := lister.(services.DriveFolderInspector)
	resolvedDriveID, resolveErr := services.ResolveFolderDriveID(ctx, inspector, folderID, listingAccessToken)
	if resolveErr != nil {
		slog.Warn(logPrefix+": folder metadata fetch failed; falling back to My Drive corpus",
			"folder_id", folderID,
			"user_id", userID,
			"inspector_available", canInspect,
			"error", resolveErr,
		)
		return ""
	}
	return resolvedDriveID
}

// scheduleDriveBatchFiles creates one upload_jobs row per Drive file
// with the cumulative random stagger. Index 0 of the page publishes at
// `cursor`; for i>0 each job is `previous + rand(min,max)`. The
// forward-looking schedule is capped at driveBatchJitterMaxSeconds
// past `base` (the request start time). indexOffset shifts the
// entries' Index field for multi-page callers ("47th video overall",
// not "27th on page 3").
//
// On failure the error response is written and ok=false is returned;
// jobs created before the failure stay queued (no rollback — matches
// the historical behaviour of both callers).
func (r *Router) scheduleDriveBatchFiles(
	w http.ResponseWriter,
	userID int64,
	p driveBatchCommonParams,
	files []services.GoogleDriveFile,
	cursor time.Time,
	base time.Time,
	indexOffset int,
) (entries []DriveBatchImportItem, newCursor time.Time, ok bool) {
	entries = make([]DriveBatchImportItem, 0, len(files))
	for idx, f := range files {
		scheduledAt := cursor
		if idx > 0 {
			gap, gapErr := sampler.RandomDurationInRange(p.MinJitterSeconds, p.MaxJitterSeconds)
			if gapErr != nil {
				writeError(w, http.StatusInternalServerError, "jitter rand failed: "+gapErr.Error())
				return nil, cursor, false
			}
			scheduledAt = cursor.Add(gap)
		}
		if scheduledAt.Sub(base) > time.Duration(driveBatchJitterMaxSeconds)*time.Second {
			scheduledAt = base.Add(time.Duration(driveBatchJitterMaxSeconds) * time.Second)
		}

		title := p.Title
		if title == "" {
			title = f.Name
		}
		caption := p.CaptionPrefix
		if caption == "" {
			caption = f.Name
		} else {
			caption = caption + " — " + f.Name
		}

		job := &models.UploadJob{
			UserID:         userID,
			WorkspaceID:    p.WorkspaceID,
			SourceType:     models.UploadJobSourceAuthenticatedDrive,
			DriveAccountID: &p.DriveAccountID,
			SourceID:       f.ID,
			// FolderID is the migration-038 column: the dashboard
			// status endpoint GROUPs BY folder without scanning the
			// whole upload_jobs table on every poll.
			FolderID:    &p.FolderID, // pointer so SQL NULL when empty
			Title:       title,
			Caption:     caption,
			Targets:     []int64{p.FacebookAccountID},
			Status:      models.UploadJobStatusPending,
			IngestAfter: r.prepareAtForPublish(scheduledAt),
			PublishAt:   &scheduledAt,
		}
		if err := r.uploadJobStore.Create(job); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("create upload job for %s: %v", f.Name, err))
			return nil, cursor, false
		}

		entries = append(entries, DriveBatchImportItem{
			Index:         indexOffset + idx,
			DriveFileID:   f.ID,
			Name:          f.Name,
			MimeType:      f.MimeType,
			JobID:         job.ID,
			PublishAt:     scheduledAt,
			RelativeHours: scheduledAt.Sub(base).Hours(),
		})
		cursor = scheduledAt
	}
	return entries, cursor, true
}

// resolveBatchScheduleCursor resolves the schedule start cursor for a
// single-page batch import. Only forward-looking cursors are honoured;
// a cursor in the past (previous buggy operator script, stale value)
// would start publishing backdated posts that fire immediately, so it
// is clamped to NOW with clamped=true surfaced in the response so the
// caller can self-correct.
func resolveBatchScheduleCursor(supplied *time.Time, now time.Time, userID int64, p driveBatchCommonParams) (cursor time.Time, clamped bool) {
	cursor = now
	if supplied == nil {
		return cursor, false
	}
	if supplied.After(now.Add(-1 * time.Minute)) {
		return *supplied, false
	}
	slog.Warn("drive batch import: cursor_scheduled_at was too far in the past, clamped to NOW",
		"user_id", userID,
		"folder_id", p.FolderID,
		"workspace_id", p.WorkspaceID,
		"supplied_cursor", supplied.Format(time.RFC3339),
		"now", now.Format(time.RFC3339),
	)
	return now, true
}

// writeBatchResponseMarshalOnce marshals the batch response ONCE so
// the SAME bytes are both written to the wire (the SPA receives them)
// and returned for the idempotency replay cache
// (insertBatchIdempotentRecord stores them verbatim). Marshal-once
// guarantees a future replay returns byte-identical JSON even if
// writeJSON's internals or json.Marshal's field-ordering rules ever
// change. On a (should-never-happen) marshal failure it degrades to
// writeJSON and returns nil so the caller skips the cache write.
func writeBatchResponseMarshalOnce(w http.ResponseWriter, resp any, logPrefix, folderID string) []byte {
	respBytes, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		slog.Warn(logPrefix+": response marshal failed; falling back to writeJSON",
			"folder_id", folderID,
			"error", marshalErr)
		writeJSON(w, http.StatusAccepted, resp)
		return nil
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write(respBytes)
	return respBytes
}
