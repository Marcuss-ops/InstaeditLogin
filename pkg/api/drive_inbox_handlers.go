package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

type driveInboxCreateRequest struct {
	WorkspaceID    int64  `json:"workspace_id"`
	DriveAccountID int64  `json:"drive_account_id"`
	FolderID       string `json:"folder_id"`
}

type driveInboxClaimRequest struct {
	SourceLanguage string `json:"source_language"`
	Title          string `json:"title"`
	Description    string `json:"description"`
}

func driveInboxID(w http.ResponseWriter, req *http.Request, key string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(req, key)), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, key+" must be positive")
		return 0, false
	}
	return id, true
}

func (r *Router) handleCreateDriveInbox(w http.ResponseWriter, req *http.Request) {
	if r.driveInboxStore == nil {
		writeError(w, http.StatusServiceUnavailable, "drive inbox store is not configured")
		return
	}
	identity, workspaceID, ok := r.contentPackageIdentity(w, req, true)
	if !ok {
		return
	}
	var body driveInboxCreateRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.WorkspaceID <= 0 {
		body.WorkspaceID = workspaceID
	}
	if body.WorkspaceID != workspaceID {
		writeError(w, http.StatusForbidden, "workspace is not visible to the caller")
		return
	}
	if body.DriveAccountID <= 0 || strings.TrimSpace(body.FolderID) == "" {
		writeError(w, http.StatusUnprocessableEntity, "drive_account_id and folder_id are required")
		return
	}
	account, err := r.userRepo.FindPlatformAccountByID(body.DriveAccountID)
	if err != nil || account == nil || account.UserID != identity.UserID() || models.NormalizePlatformIdentifier(account.Platform) != models.PlatformGoogleDrive {
		writeError(w, http.StatusNotFound, "Drive account not found")
		return
	}
	inbox := &models.DriveInbox{WorkspaceID: body.WorkspaceID, DriveAccountID: body.DriveAccountID, FolderID: strings.TrimSpace(body.FolderID), Enabled: true}
	if err := r.driveInboxStore.CreateInbox(req.Context(), inbox); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "create Drive inbox: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, inbox)
}

func (r *Router) handleListDriveInboxes(w http.ResponseWriter, req *http.Request) {
	if r.driveInboxStore == nil {
		writeError(w, http.StatusServiceUnavailable, "drive inbox store is not configured")
		return
	}
	_, workspaceID, ok := r.contentPackageIdentity(w, req, false)
	if !ok {
		return
	}
	inboxes, err := r.driveInboxStore.ListInboxes(req.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": inboxes})
}

func (r *Router) handleListDriveInboxItems(w http.ResponseWriter, req *http.Request) {
	if r.driveInboxStore == nil {
		writeError(w, http.StatusServiceUnavailable, "drive inbox store is not configured")
		return
	}
	_, workspaceID, ok := r.contentPackageIdentity(w, req, false)
	if !ok {
		return
	}
	inboxID, ok := driveInboxID(w, req, "inbox_id")
	if !ok {
		return
	}
	inbox, err := r.driveInboxStore.FindInbox(req.Context(), workspaceID, inboxID)
	if errors.Is(err, repository.ErrDriveInboxItemNotFound) || inbox == nil {
		writeError(w, http.StatusNotFound, "Drive inbox not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items, err := r.driveInboxStore.ListInboxItems(req.Context(), inbox.ID, req.URL.Query().Get("status"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"inbox": inbox, "items": items})
}

func (r *Router) handleClaimDriveInboxItem(w http.ResponseWriter, req *http.Request) {
	if r.driveInboxStore == nil {
		writeError(w, http.StatusServiceUnavailable, "drive inbox store is not configured")
		return
	}
	identity, workspaceID, ok := r.contentPackageIdentity(w, req, true)
	if !ok {
		return
	}
	inboxID, ok := driveInboxID(w, req, "inbox_id")
	if !ok {
		return
	}
	itemID, ok := driveInboxID(w, req, "item_id")
	if !ok {
		return
	}
	inbox, err := r.driveInboxStore.FindInbox(req.Context(), workspaceID, inboxID)
	if err != nil || inbox == nil {
		writeError(w, http.StatusNotFound, "Drive inbox not found")
		return
	}
	var body driveInboxClaimRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.SourceLanguage == "" {
		body.SourceLanguage = "it"
	}
	if err := models.CheckBCP47Like("source_language", body.SourceLanguage); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	pkg := &models.ContentPackage{WorkspaceID: inbox.WorkspaceID, CreatedBy: identity.UserID(), SourceType: "google_drive", DriveAccountID: &inbox.DriveAccountID, SourceLanguage: body.SourceLanguage}
	revision := &models.ContentMetadataRevision{SourceLanguage: body.SourceLanguage, Title: body.Title, Description: body.Description, Tags: []byte("[]"), CreatedBy: identity.UserID()}
	claimed, err := r.driveInboxStore.ClaimInboxItem(req.Context(), inbox.ID, itemID, identity.UserID(), pkg, revision)
	if errors.Is(err, repository.ErrDriveInboxItemNotFound) {
		writeError(w, http.StatusConflict, "inbox item is already claimed or unavailable")
		return
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "claim inbox item: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"package": claimed, "metadata": revision})
}
