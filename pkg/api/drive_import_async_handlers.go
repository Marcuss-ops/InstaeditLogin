package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// DriveImportAsyncRequest is the body for POST /api/v1/media/import/drive/async.
// It queues a background job to download a video from Google Drive and publish
// it to the selected platform accounts. Jobs survive server restarts.
type DriveImportAsyncRequest struct {
	// SourceType is "public_drive" or "authenticated_drive".
	SourceType string `json:"source_type"`
	// SourceID is the Google Drive file id (the part after /d/ in the share URL).
	SourceID string `json:"source_id"`
	// DriveAccountID is required when SourceType is "authenticated_drive".
	DriveAccountID int64 `json:"drive_account_id"`
	// WorkspaceID is the workspace that will own the post.
	WorkspaceID int64 `json:"workspace_id"`
	// Title of the post.
	Title string `json:"title"`
	// Caption is the post body text.
	Caption string `json:"caption"`
	// Targets are the platform account IDs where the clip should be published.
	Targets []int64 `json:"targets"`
}

// DriveImportAsyncResponse returns the queued upload job id.
type DriveImportAsyncResponse struct {
	JobID int64 `json:"job_id"`
}

// handleDriveImportAsync queues a background upload job for a Google Drive video.
// POST /api/v1/media/import/drive/async
func (r *Router) handleDriveImportAsync(w http.ResponseWriter, req *http.Request) {
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

	var body DriveImportAsyncRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// P0 hardening refactor: the public_drive source_type was
	// REMOVED from the Drive pipeline alongside the
	// `drive.google.com/uc` + HTML-scraping fallback. Only the
	// authenticated_drive path remains — every drive import must
	// go through a connected Drive account's OAuth grant.
	if body.SourceType != string(models.UploadJobSourceAuthenticatedDrive) {
		writeError(w, http.StatusUnprocessableEntity,
			"source_type must be \"authenticated_drive\" (the public_drive download path was removed in the Drive pipeline hardening refactor)")
		return
	}
	if strings.TrimSpace(body.SourceID) == "" {
		writeError(w, http.StatusUnprocessableEntity, "source_id is required")
		return
	}
	if body.WorkspaceID == 0 {
		writeError(w, http.StatusUnprocessableEntity, "workspace_id is required")
		return
	}
	if len(body.Targets) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "at least one target is required")
		return
	}

	// Workspace ownership check.
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

	// Verify every publish target belongs to the user.
	accounts, err := r.userRepo.ListPlatformAccountsByUser(userID, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list user accounts: "+err.Error())
		return
	}
	owned := make(map[int64]bool, len(accounts))
	for _, a := range accounts {
		owned[a.ID] = true
	}
	for i, accountID := range body.Targets {
		if !owned[accountID] {
			writeError(w, http.StatusForbidden,
				fmt.Sprintf("targets[%d] (platform_account_id=%d) does not belong to this user", i, accountID))
			return
		}
	}

	// For authenticated drive, verify the Drive account belongs to the user.
	var driveAccountID *int64
	if body.SourceType == string(models.UploadJobSourceAuthenticatedDrive) {
		if body.DriveAccountID == 0 {
			writeError(w, http.StatusUnprocessableEntity, "drive_account_id is required for authenticated_drive")
			return
		}
		driveAccount, err := r.userRepo.FindPlatformAccountByID(body.DriveAccountID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to find drive account: "+err.Error())
			return
		}
		if driveAccount == nil || driveAccount.UserID != userID || driveAccount.Platform != "google-drive" {
			writeError(w, http.StatusNotFound, "google drive account not found")
			return
		}
		driveAccountID = &body.DriveAccountID
	}

	job := &models.UploadJob{
		UserID:         userID,
		WorkspaceID:    body.WorkspaceID,
		SourceType:     models.UploadJobSource(body.SourceType),
		SourceID:       body.SourceID,
		DriveAccountID: driveAccountID,
		Title:          body.Title,
		Caption:        body.Caption,
		Targets:        body.Targets,
		Status:         models.UploadJobStatusPending,
	}
	if err := r.uploadJobStore.Create(job); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create upload job: "+err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, DriveImportAsyncResponse{JobID: job.ID})
}
