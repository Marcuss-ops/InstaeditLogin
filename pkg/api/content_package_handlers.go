package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

type contentPackageCreateRequest struct {
	WorkspaceID       int64                       `json:"workspace_id"`
	SourceType        string                      `json:"source_type"`
	DriveAccountID    *int64                      `json:"drive_account_id,omitempty"`
	DriveFileID       string                      `json:"drive_file_id"`
	SourceFilename    string                      `json:"source_filename"`
	SourceFingerprint string                      `json:"source_fingerprint"`
	VeloxProjectID    *string                     `json:"velox_project_id,omitempty"`
	SourceLanguage    string                      `json:"source_language"`
	Title             string                      `json:"title"`
	Description       string                      `json:"description"`
	Tags              json.RawMessage             `json:"tags"`
	Targets           []contentPackageTargetInput `json:"targets"`
}

type contentPackageTargetInput struct {
	PlatformAccountID int64   `json:"platform_account_id"`
	Language          string  `json:"language"`
	PrivacyStatus     string  `json:"privacy_status"`
	PlaylistID        *string `json:"playlist_id,omitempty"`
	Enabled           *bool   `json:"enabled,omitempty"`
}

type contentPackagePatchRequest struct {
	ExpectedPackageVersion int64   `json:"expected_package_version"`
	SourceFilename         *string `json:"source_filename,omitempty"`
	SourceFingerprint      *string `json:"source_fingerprint,omitempty"`
	SourceLanguage         *string `json:"source_language,omitempty"`
	CurrentCoverMediaID    *string `json:"current_cover_media_id,omitempty"`
	State                  *string `json:"state,omitempty"`
}

type contentMetadataRequest struct {
	ExpectedPackageVersion int64           `json:"expected_package_version"`
	SourceLanguage         string          `json:"source_language"`
	Title                  string          `json:"title"`
	Description            string          `json:"description"`
	Tags                   json.RawMessage `json:"tags"`
}

type contentTranslationRequest struct {
	ExpectedPackageVersion int64                     `json:"expected_package_version"`
	Generate               bool                      `json:"generate,omitempty"`
	Entries                []contentTranslationInput `json:"entries"`
}

type contentTranslationInput struct {
	Language    string          `json:"language"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Tags        json.RawMessage `json:"tags"`
}

type contentScheduleRequest struct {
	ExpectedPackageVersion int64  `json:"expected_package_version"`
	ScheduledAt            string `json:"scheduled_at"`
	Timezone               string `json:"timezone"`
}

type contentPackageResponse struct {
	Package      *models.ContentPackage                    `json:"package"`
	Targets      []*models.ContentPackageTarget            `json:"targets"`
	Metadata     *models.ContentMetadataRevision           `json:"metadata,omitempty"`
	Schedule     *models.ContentSchedule                   `json:"schedule,omitempty"`
	Publications []*models.ContentPackagePublicationStatus `json:"publications,omitempty"`
}

type contentPreviewTarget struct {
	PlatformAccountID int64                         `json:"platform_account_id"`
	ChannelName       string                        `json:"channel_name,omitempty"`
	Language          string                        `json:"language"`
	Title             string                        `json:"title"`
	Description       string                        `json:"description"`
	Tags              json.RawMessage               `json:"tags"`
	ThumbnailMediaID  *string                       `json:"thumbnail_media_id,omitempty"`
	PrivacyStatus     string                        `json:"privacy_status"`
	ScheduledAt       *time.Time                    `json:"scheduled_at,omitempty"`
	Ready             bool                          `json:"ready"`
	Blockers          []services.PublicationBlocker `json:"blockers"`
	Warnings          []string                      `json:"warnings,omitempty"`
}

type contentPreviewResponse struct {
	PackageID      int64                         `json:"package_id"`
	PackageVersion int64                         `json:"package_version"`
	Ready          bool                          `json:"ready"`
	Blockers       []services.PublicationBlocker `json:"blockers"`
	Targets        []contentPreviewTarget        `json:"targets"`
	Schedule       *models.ContentSchedule       `json:"schedule,omitempty"`
}

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
	pkg := &models.ContentPackage{WorkspaceID: body.WorkspaceID, CreatedBy: identity.UserID(), SourceType: body.SourceType, DriveAccountID: body.DriveAccountID, DriveFileID: strings.TrimSpace(body.DriveFileID), SourceFilename: body.SourceFilename, SourceFingerprint: body.SourceFingerprint, VeloxProjectID: body.VeloxProjectID, SourceLanguage: body.SourceLanguage}
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
		targets = append(targets, &models.ContentPackageTarget{PlatformAccountID: input.PlatformAccountID, Language: input.Language, PrivacyStatus: privacy, PlaylistID: input.PlaylistID, Enabled: enabled})
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
	if body.State != nil {
		pkg.State = models.ContentPackageState(*body.State)
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

func (r *Router) handleContentMetadata(w http.ResponseWriter, req *http.Request) {
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
	var body contentMetadataRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.ExpectedPackageVersion <= 0 {
		writeError(w, http.StatusBadRequest, "expected_package_version is required")
		return
	}
	if body.SourceLanguage == "" {
		body.SourceLanguage = pkg.SourceLanguage
	}
	if body.Tags == nil {
		body.Tags = json.RawMessage("[]")
	}
	if err := models.CheckBCP47Like("source_language", body.SourceLanguage); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	revision := &models.ContentMetadataRevision{ContentPackageID: id, SourceLanguage: body.SourceLanguage, Title: body.Title, Description: body.Description, Tags: body.Tags, CreatedBy: identity.UserID()}
	if err := r.contentPackageStore.CreateMetadataRevision(req.Context(), revision, body.ExpectedPackageVersion); err != nil {
		if errors.Is(err, repository.ErrContentPackageVersionConflict) {
			writeError(w, http.StatusConflict, "PACKAGE_CHANGED")
			return
		}
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	pkg.Version = body.ExpectedPackageVersion + 1
	pkg.SourceLanguage = body.SourceLanguage
	pkg.CurrentMetadataRevisionID = &revision.ID
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

func (r *Router) handleContentTranslations(w http.ResponseWriter, req *http.Request) {
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
	var body contentTranslationRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.ExpectedPackageVersion != pkg.Version {
		writeError(w, http.StatusConflict, "PACKAGE_CHANGED")
		return
	}
	metadata, err := r.contentPackageStore.FindCurrentMetadata(req.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if body.Generate {
		if r.nvidiaMetadataSvc == nil || !r.nvidiaMetadataSvc.Configured() {
			writeError(w, http.StatusServiceUnavailable, "NVIDIA AI translation is not configured")
			return
		}
		targets, err := r.contentPackageStore.ListTargets(req.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list package targets: "+err.Error())
			return
		}
		languages := make([]string, 0, len(targets))
		seen := make(map[string]bool)
		for _, target := range targets {
			if target == nil || !target.Enabled || strings.EqualFold(target.Language, metadata.SourceLanguage) || seen[target.Language] {
				continue
			}
			if err := models.CheckBCP47Like("target.language", target.Language); err != nil {
				writeError(w, http.StatusUnprocessableEntity, err.Error())
				return
			}
			seen[target.Language] = true
			languages = append(languages, target.Language)
		}
		if len(languages) == 0 {
			writeError(w, http.StatusUnprocessableEntity, "no translated target languages are configured")
			return
		}
		prompt := fmt.Sprintf("Source language: %s\nTitle: %s\nDescription: %s\nGenerate only title and description translations for these exact BCP-47 languages: %s. Return JSON with a translations object.", metadata.SourceLanguage, metadata.Title, metadata.Description, strings.Join(languages, ", "))
		generated, err := r.nvidiaMetadataSvc.Generate(req.Context(), prompt)
		if err != nil || generated == nil {
			if err == nil {
				err = errors.New("empty response")
			}
			writeError(w, http.StatusBadGateway, "NVIDIA translation failed: "+err.Error())
			return
		}
		entries := make([]*models.TranslationEntry, 0, len(languages))
		for _, language := range languages {
			translation, ok := generated.Translations[language]
			if !ok || strings.TrimSpace(translation.Title) == "" {
				writeError(w, http.StatusUnprocessableEntity, "NVIDIA returned an incomplete translation bundle for "+language)
				return
			}
			entries = append(entries, &models.TranslationEntry{Language: language, Title: translation.Title, Description: translation.Description, Tags: json.RawMessage("[]"), Origin: "nvidia"})
		}
		bundle := &models.TranslationBundle{ContentPackageID: id, SourceMetadataRevisionID: metadata.ID, Provider: "nvidia", Status: "completed", RequestedLanguages: languages}
		if err := r.contentPackageStore.CreateTranslationBundle(req.Context(), bundle, entries); err != nil {
			writeError(w, http.StatusInternalServerError, "save translation bundle: "+err.Error())
			return
		}
		_ = r.contentPackageStore.AppendPublicationEvent(req.Context(), &models.PublicationEvent{ContentPackageID: id, Stage: "translation", EventType: "TRANSLATION_COMPLETED"})
		writeJSON(w, http.StatusCreated, map[string]any{"bundle": bundle, "entries": entries})
		return
	}
	currentVersion := body.ExpectedPackageVersion
	for _, input := range body.Entries {
		if err := models.CheckBCP47Like("translation.language", input.Language); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if input.Tags == nil {
			input.Tags = json.RawMessage("[]")
		}
		entry := &models.TranslationEntry{Language: input.Language, Title: input.Title, Description: input.Description, Tags: input.Tags, Origin: "manual"}
		if err := r.contentPackageStore.UpsertManualTranslation(req.Context(), id, metadata.ID, currentVersion, entry); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		currentVersion++
	}
	_ = identity
	pkg.Version = currentVersion
	writeJSON(w, http.StatusOK, map[string]any{"package": pkg})
}

func (r *Router) handleContentPreview(w http.ResponseWriter, req *http.Request) {
	if r.contentPackageStore == nil || r.publicationResolver == nil {
		writeError(w, http.StatusServiceUnavailable, "content package resolver is not configured")
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
	targets, err := r.publicationResolver.ResolveAll(req.Context(), workspaceID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "resolve preview: "+err.Error())
		return
	}
	schedule, _ := r.contentPackageStore.FindSchedule(req.Context(), id)
	response := contentPreviewResponse{PackageID: pkg.ID, PackageVersion: pkg.Version, Schedule: schedule, Ready: true, Blockers: []services.PublicationBlocker{}, Targets: make([]contentPreviewTarget, 0, len(targets))}
	for _, resolved := range targets {
		name := ""
		if r.userRepo != nil {
			if account, accountErr := r.userRepo.FindPlatformAccountByID(resolved.Target.PlatformAccountID); accountErr == nil && account != nil {
				name = account.Username
			}
		}
		item := contentPreviewTarget{PlatformAccountID: resolved.Target.PlatformAccountID, ChannelName: name, Language: resolved.Target.Language, Title: resolved.Title, Description: resolved.Description, Tags: resolved.Tags, ThumbnailMediaID: resolved.ThumbnailMediaID, PrivacyStatus: resolved.PrivacyStatus, Ready: resolved.Ready(), Blockers: resolved.Blockers, Warnings: resolved.Warnings}
		if schedule != nil {
			item.ScheduledAt = &schedule.ScheduledAt
		}
		response.Targets = append(response.Targets, item)
		if !resolved.Ready() {
			response.Ready = false
			response.Blockers = append(response.Blockers, resolved.Blockers...)
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (r *Router) handleScheduleContentPackage(w http.ResponseWriter, req *http.Request) {
	if r.contentPackageStore == nil || r.publicationResolver == nil {
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
		writeError(w, http.StatusInternalServerError, "find content package: "+err.Error())
		return
	}
	var body contentScheduleRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.ExpectedPackageVersion <= 0 {
		writeError(w, http.StatusBadRequest, "expected_package_version is required")
		return
	}
	scheduledAt, err := time.Parse(time.RFC3339, body.ScheduledAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "scheduled_at must be RFC3339")
		return
	}
	if scheduledAt.Before(time.Now().Add(5 * time.Second)) {
		writeError(w, http.StatusBadRequest, "scheduled_at must be in the future")
		return
	}
	if body.Timezone == "" {
		body.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(body.Timezone); err != nil {
		writeError(w, http.StatusBadRequest, "timezone is invalid")
		return
	}
	if scheduledAt.After(time.Now().Add(time.Duration(r.publishHorizonDays()) * 24 * time.Hour)) {
		writeError(w, http.StatusUnprocessableEntity, "scheduled_at exceeds publish horizon")
		return
	}
	preview, err := r.publicationResolver.ResolveAll(req.Context(), workspaceID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(preview) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "content_not_ready", "blockers": []services.PublicationBlocker{{Code: "targets_missing", Message: "at least one enabled target is required"}}})
		return
	}
	for _, item := range preview {
		if !item.Ready() {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "content_not_ready", "blockers": item.Blockers})
			return
		}
	}
	schedule := &models.ContentSchedule{ContentPackageID: id, ScheduledAt: scheduledAt.UTC(), PrepareAt: r.prepareAtForPublish(scheduledAt.UTC()), Timezone: body.Timezone}
	if err := r.contentPackageStore.UpsertSchedule(req.Context(), schedule, body.ExpectedPackageVersion); err != nil {
		if errors.Is(err, repository.ErrContentPackageVersionConflict) {
			writeError(w, http.StatusConflict, "PACKAGE_CHANGED")
			return
		}
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	pkg.Version = body.ExpectedPackageVersion + 1
	pkg.State = models.ContentPackageStateScheduled
	_ = r.contentPackageStore.AppendPublicationEvent(req.Context(), &models.PublicationEvent{ContentPackageID: id, EventType: "SCHEDULE_CREATED", Stage: "schedule"})
	r.writeContentPackageResponse(w, req, http.StatusCreated, pkg)
}

func (r *Router) handleCancelContentPackage(w http.ResponseWriter, req *http.Request) {
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
		writeError(w, http.StatusInternalServerError, "find content package: "+err.Error())
		return
	}
	schedule, err := r.contentPackageStore.FindSchedule(req.Context(), id)
	if err != nil || schedule == nil {
		writeError(w, http.StatusNotFound, "content schedule not found")
		return
	}
	var body struct {
		ExpectedPackageVersion int64 `json:"expected_package_version"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.ExpectedPackageVersion <= 0 {
		writeError(w, http.StatusBadRequest, "expected_package_version is required")
		return
	}
	if err := r.contentPackageStore.CancelSchedule(req.Context(), id, body.ExpectedPackageVersion); err != nil {
		if errors.Is(err, repository.ErrContentPackageVersionConflict) {
			writeError(w, http.StatusConflict, "PACKAGE_CHANGED")
			return
		}
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	schedule.Status = "cancelled"
	pkg.State = models.ContentPackageStateDraft
	pkg.Version = body.ExpectedPackageVersion + 1
	writeJSON(w, http.StatusOK, map[string]any{"package": pkg, "schedule": schedule})
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
