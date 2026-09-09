package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func (r *Router) contentPackageIdentity(w http.ResponseWriter, req *http.Request, write bool) (auth.Identity, int64, bool) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return nil, 0, false
	}
	workspaceID := identity.WorkspaceID()
	if raw := strings.TrimSpace(req.URL.Query().Get("workspace_id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "workspace_id must be positive")
			return nil, 0, false
		}
		workspaceID = parsed
	}
	if workspaceID <= 0 || r.workspaceStore == nil {
		writeError(w, http.StatusServiceUnavailable, "workspace store is not configured")
		return nil, 0, false
	}
	workspace, err := r.workspaceStore.FindByID(workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return nil, 0, false
	}
	if workspace == nil || !workspaceRoleAllowed(identity.UserID(), workspace, r.teamStore, func() string {
		if write {
			return workspaceRoleEditor
		}
		return workspaceRoleViewer
	}()) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return nil, 0, false
	}
	return identity, workspaceID, true
}

func contentPackageID(w http.ResponseWriter, req *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(req, "id")), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "content package id must be positive")
		return 0, false
	}
	return id, true
}

// platformAccountBatchFetcher is the narrow optional capability used by
// fan-out loops: when the wired UserStore implements it (the production
// *repository.UserRepository does), the content-package preview resolves
// channel names in ONE query instead of one FindPlatformAccountByID per
// target. Test fakes implementing only the single fetch fall back to the
// per-target path.
type platformAccountBatchFetcher interface {
	FindPlatformAccountsByIDs(ctx context.Context, ids []int64) (map[int64]*models.PlatformAccount, error)
}

// resolveChannelNamesByIDs maps platform_account_id → username for the given
// distinct account ids. Batch path when the store supports it, per-target
// fallback otherwise; lookup failures degrade to an empty channel name (the
// preview is a read projection — a missing username must not 500 the whole
// response).
func (r *Router) resolveChannelNamesByIDs(req *http.Request, accountIDs []int64) map[int64]string {
	names := make(map[int64]string, len(accountIDs))
	if r.userRepo == nil || len(accountIDs) == 0 {
		return names
	}
	if batch, ok := r.userRepo.(platformAccountBatchFetcher); ok {
		byID, err := batch.FindPlatformAccountsByIDs(req.Context(), accountIDs)
		if err != nil {
			r.logger.Warn("content package: batch resolve channel names failed; previewing with empty names",
				"target_count", len(accountIDs), "error", err)
			return names
		}
		for id, account := range byID {
			if account != nil {
				names[id] = account.Username
			}
		}
		return names
	}
	for _, id := range accountIDs {
		if account, err := r.userRepo.FindPlatformAccountByID(id); err == nil && account != nil {
			names[id] = account.Username
		}
	}
	return names
}

func (r *Router) handleCreateContentPackage(w http.ResponseWriter, req *http.Request) {
	if r.contentPackageStore == nil {
		writeError(w, http.StatusServiceUnavailable, "content package store is not configured")
		return
	}
	identity, workspaceID, ok := r.contentPackageIdentity(w, req, true)
	if !ok {
		return
	}
	var body contentPackageCreateRequest
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
	if strings.TrimSpace(body.DriveFileID) == "" {
		writeError(w, http.StatusUnprocessableEntity, "drive_file_id is required")
		return
	}
	if strings.TrimSpace(body.SourceLanguage) == "" {
		body.SourceLanguage = "it"
	}
	if err := models.CheckBCP47Like("source_language", body.SourceLanguage); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	// Boundary validation (P0 contract fix): the DB CHECK only enforces
	// btrim(source_type) <> '' (migration 120), so the accepted value
	// space must be pinned here. Content packages are sourced from
	// Drive ingest — repo.CreatePackage defaults empty to
	// "google_drive" — so exactly one value is valid today.
	if body.SourceType == "" {
		body.SourceType = "google_drive"
	}
	if body.SourceType != "google_drive" {
		writeError(w, http.StatusUnprocessableEntity, "source_type must be google_drive")
		return
	}
	if body.Tags == nil {
		body.Tags = json.RawMessage("[]")
	}
	var targets []*models.ContentPackageTarget
	if len(body.Targets) > 0 {
		var valid bool
		targets, valid = r.validateContentPackageTargets(w, req, workspaceID, body.Targets)
		if !valid {
			return
		}
	}
	pkg := &models.ContentPackage{WorkspaceID: body.WorkspaceID, CreatedBy: identity.UserID(), SourceType: body.SourceType, DriveAccountID: body.DriveAccountID, DriveFileID: strings.TrimSpace(body.DriveFileID), SourceFilename: body.SourceFilename, SourceFingerprint: body.SourceFingerprint, VeloxProjectID: body.VeloxProjectID, SourceLanguage: body.SourceLanguage, CurrentCoverMediaID: body.CurrentCoverMediaID, CurrentCoverTemplateVersionID: body.CurrentCoverTemplateVersionID}
	revision := &models.ContentMetadataRevision{SourceLanguage: body.SourceLanguage, Title: body.Title, Description: body.Description, Tags: body.Tags, CreatedBy: identity.UserID()}
	if err := r.contentPackageStore.CreatePackage(req.Context(), pkg, revision); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "create content package: "+err.Error())
		return
	}
	if len(targets) > 0 {
		for _, target := range targets {
			target.ContentPackageID = pkg.ID
		}
		if _, err := r.contentPackageStore.ReplaceTargets(req.Context(), pkg.ID, pkg.Version, targets); err != nil {
			writeError(w, http.StatusConflict, "set content package targets: "+err.Error())
			return
		}
		pkg.Version++
	}
	r.writeContentPackageResponse(w, req, http.StatusCreated, pkg)
}

func (r *Router) validateContentPackageTargets(w http.ResponseWriter, req *http.Request, workspaceID int64, inputs []contentPackageTargetInput) ([]*models.ContentPackageTarget, bool) {
	identity := auth.IdentityFromContext(req.Context())
	targets := make([]*models.ContentPackageTarget, 0, len(inputs))
	seen := make(map[int64]bool)
	for _, input := range inputs {
		if input.PlatformAccountID <= 0 || seen[input.PlatformAccountID] {
			writeError(w, http.StatusUnprocessableEntity, "targets must contain unique positive platform_account_id values")
			return nil, false
		}
		seen[input.PlatformAccountID] = true
		if err := models.CheckBCP47Like("target.language", input.Language); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return nil, false
		}
		account, err := r.userRepo.FindPlatformAccountByID(input.PlatformAccountID)
		if err != nil || account == nil || account.UserID != identity.UserID() {
			writeError(w, http.StatusNotFound, "target account not found")
			return nil, false
		}
		if r.workspaceStore != nil {
			channel, channelErr := r.workspaceStore.FindChannel(req.Context(), workspaceID, input.PlatformAccountID)
			if channelErr != nil || channel == nil {
				writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("target account %d is not linked to the workspace", input.PlatformAccountID))
				return nil, false
			}
		}
		enabled := true
		if input.Enabled != nil {
			enabled = *input.Enabled
		}
		privacy := input.PrivacyStatus
		if privacy == "" {
			privacy = "private"
		}
		targets = append(targets, &models.ContentPackageTarget{PlatformAccountID: input.PlatformAccountID, Language: input.Language, PrivacyStatus: privacy, PlaylistID: input.PlaylistID, CoverMediaID: input.CoverMediaID, CoverTemplateVersionID: input.CoverTemplateVersionID, Enabled: enabled})
	}
	return targets, true
}

func (r *Router) handleGetContentPackage(w http.ResponseWriter, req *http.Request) {
	if r.contentPackageStore == nil {
		writeError(w, http.StatusServiceUnavailable, "content package store is not configured")
		return
	}
	_, workspaceID, ok := r.contentPackageIdentity(w, req, false)
	if !ok {
		return
	}
	id, ok := contentPackageID(w, req)
	if !ok {
		return
	}
	pkg, err := r.contentPackageStore.FindPackage(req.Context(), workspaceID, id)
	if errors.Is(err, repository.ErrContentPackageNotFound) || pkg == nil {
		writeError(w, http.StatusNotFound, "content package not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find content package: "+err.Error())
		return
	}
	r.writeContentPackageResponse(w, req, http.StatusOK, pkg)
}

func (r *Router) handlePatchContentPackage(w http.ResponseWriter, req *http.Request) {
	if r.contentPackageStore == nil {
		writeError(w, http.StatusServiceUnavailable, "content package store is not configured")
		return
	}
	_, workspaceID, ok := r.contentPackageIdentity(w, req, true)
	if !ok {
		return
	}
	id, ok := contentPackageID(w, req)
	if !ok {
		return
	}
	pkg, err := r.contentPackageStore.FindPackage(req.Context(), workspaceID, id)
	if errors.Is(err, repository.ErrContentPackageNotFound) || pkg == nil {
		writeError(w, http.StatusNotFound, "content package not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var body contentPackagePatchRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.ExpectedPackageVersion <= 0 {
		writeError(w, http.StatusBadRequest, "expected_package_version is required")
		return
	}
	if body.SourceFilename != nil {
		pkg.SourceFilename = *body.SourceFilename
	}
	if body.SourceFingerprint != nil {
		pkg.SourceFingerprint = *body.SourceFingerprint
	}
	if body.SourceLanguage != nil {
		pkg.SourceLanguage = *body.SourceLanguage
	}
	if body.CurrentCoverMediaID != nil {
		pkg.CurrentCoverMediaID = body.CurrentCoverMediaID
	}
	if body.CurrentCoverTemplateVersionID != nil {
		pkg.CurrentCoverTemplateVersionID = body.CurrentCoverTemplateVersionID
	}
	if body.State != nil {
		state := models.ContentPackageState(*body.State)
		// Boundary validation (P0 contract fix): the DB only constrains
		// content_packages.state to NOT NULL, so an unvalidated cast here
		// would persist a state the publication state machine
		// (content_package_publication.go) never recognises. models
		// .ContentPackageState.IsValid is the single authority.
		if !state.IsValid() {
			writeError(w, http.StatusUnprocessableEntity, "state is invalid")
			return
		}
		pkg.State = state
	}
	if err := r.contentPackageStore.UpdatePackage(req.Context(), pkg, body.ExpectedPackageVersion); err != nil {
		if errors.Is(err, repository.ErrContentPackageVersionConflict) {
			writeError(w, http.StatusConflict, "PACKAGE_CHANGED")
			return
		}
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	r.writeContentPackageResponse(w, req, http.StatusOK, pkg)
}

func (r *Router) handleContentTargets(w http.ResponseWriter, req *http.Request) {
	if r.contentPackageStore == nil {
		writeError(w, http.StatusServiceUnavailable, "content package store is not configured")
		return
	}
	identity, workspaceID, ok := r.contentPackageIdentity(w, req, true)
	if !ok {
		return
	}
	id, ok := contentPackageID(w, req)
	if !ok {
		return
	}
	pkg, err := r.contentPackageStore.FindPackage(req.Context(), workspaceID, id)
	if errors.Is(err, repository.ErrContentPackageNotFound) || pkg == nil {
		writeError(w, http.StatusNotFound, "content package not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find content package: "+err.Error())
		return
	}
	var body struct {
		ExpectedPackageVersion int64                       `json:"expected_package_version"`
		Targets                []contentPackageTargetInput `json:"targets"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.ExpectedPackageVersion <= 0 {
		writeError(w, http.StatusBadRequest, "expected_package_version is required")
		return
	}
	_ = identity
	targets, valid := r.validateContentPackageTargets(w, req, workspaceID, body.Targets)
	if !valid {
		return
	}
	result, err := r.contentPackageStore.ReplaceTargets(req.Context(), id, body.ExpectedPackageVersion, targets)
	if err != nil {
		if errors.Is(err, repository.ErrContentPackageVersionConflict) {
			writeError(w, http.StatusConflict, "PACKAGE_CHANGED")
			return
		}
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	pkg.Version = body.ExpectedPackageVersion + 1
	writeJSON(w, http.StatusOK, map[string]any{"package": pkg, "targets": result})
}

func (r *Router) handleContentActivity(w http.ResponseWriter, req *http.Request) {
	if r.contentPackageStore == nil {
		writeError(w, http.StatusServiceUnavailable, "content package store is not configured")
		return
	}
	_, workspaceID, ok := r.contentPackageIdentity(w, req, false)
	if !ok {
		return
	}
	id, ok := contentPackageID(w, req)
	if !ok {
		return
	}
	pkg, err := r.contentPackageStore.FindPackage(req.Context(), workspaceID, id)
	if errors.Is(err, repository.ErrContentPackageNotFound) || pkg == nil {
		writeError(w, http.StatusNotFound, "content package not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find content package: "+err.Error())
		return
	}
	events, err := r.contentPackageStore.ListPublicationEvents(req.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"package_id": pkg.ID, "events": events})
}

func (r *Router) writeContentPackageResponse(w http.ResponseWriter, req *http.Request, status int, pkg *models.ContentPackage) {
	targets, err := r.contentPackageStore.ListTargets(req.Context(), pkg.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list content package targets: "+err.Error())
		return
	}
	metadata, err := r.contentPackageStore.FindCurrentMetadata(req.Context(), pkg.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find content package metadata: "+err.Error())
		return
	}
	schedule, err := r.contentPackageStore.FindSchedule(req.Context(), pkg.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find content package schedule: "+err.Error())
		return
	}
	publications, err := r.contentPackageStore.ListPublicationStatuses(req.Context(), pkg.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list content package publication statuses: "+err.Error())
		return
	}
	writeJSON(w, status, contentPackageResponse{Package: pkg, Targets: targets, Metadata: metadata, Schedule: schedule, Publications: publications})
}
