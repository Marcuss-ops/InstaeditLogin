package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

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

func (r *Router) handleContentTranslations(w http.ResponseWriter, req *http.Request) {
	if r.contentPackageStore == nil {
		writeError(w, http.StatusServiceUnavailable, "content package store is not configured")
		return
	}
	// Identity is validated (and write-role enforced) by the helper; the
	// value itself is not needed by this handler.
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
		if err := r.contentPackageStore.AppendPublicationEvent(req.Context(), &models.PublicationEvent{ContentPackageID: id, Stage: "translation", EventType: "TRANSLATION_COMPLETED"}); err != nil {
			// The bundle is already committed; the audit event is
			// best-effort. Never fail the request for it, but never drop
			// it silently either.
			r.logger.Warn("content package: append publication event failed",
				"content_package_id", id, "stage", "translation", "event_type", "TRANSLATION_COMPLETED", "error", err)
		}
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
	// DB errors must never collapse into "no schedule": a transient
	// failure here would make the preview answer 200 with schedule
	// fields silently missing. (nil, nil) stays the legitimate
	// "not scheduled yet" case.
	schedule, err := r.contentPackageStore.FindSchedule(req.Context(), id)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "find schedule: "+err.Error())
		return
	}
	response := contentPreviewResponse{PackageID: pkg.ID, PackageVersion: pkg.Version, Schedule: schedule, Ready: true, Blockers: []services.PublicationBlocker{}, Targets: make([]contentPreviewTarget, 0, len(targets))}
	// Collect the distinct target account ids up front so channel names
	// resolve in ONE batched query (or the per-target fallback for stores
	// without the batch capability) instead of one FindPlatformAccountByID
	// per target inside the loop below (N+1 → batch).
	accountIDs := make([]int64, 0, len(targets))
	seenAccounts := make(map[int64]bool, len(targets))
	for _, resolved := range targets {
		if resolved.Target != nil && resolved.Target.PlatformAccountID > 0 && !seenAccounts[resolved.Target.PlatformAccountID] {
			seenAccounts[resolved.Target.PlatformAccountID] = true
			accountIDs = append(accountIDs, resolved.Target.PlatformAccountID)
		}
	}
	names := r.resolveChannelNamesByIDs(req, accountIDs)
	for _, resolved := range targets {
		name := names[resolved.Target.PlatformAccountID]
		item := contentPreviewTarget{PlatformAccountID: resolved.Target.PlatformAccountID, ChannelName: name, Language: resolved.Target.Language, Title: resolved.Title, Description: resolved.Description, Tags: resolved.Tags, ThumbnailMediaID: resolved.ThumbnailMediaID, CoverTemplateVersionID: resolved.CoverTemplateVersionID, PrivacyStatus: resolved.PrivacyStatus, Ready: resolved.Ready(), Blockers: resolved.Blockers, Warnings: resolved.Warnings}
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
	if err := r.contentPackageStore.AppendPublicationEvent(req.Context(), &models.PublicationEvent{ContentPackageID: id, EventType: "SCHEDULE_CREATED", Stage: "schedule"}); err != nil {
		// The schedule row is already committed; the audit event is
		// best-effort. Never fail the request for it, but never drop it
		// silently either — the operator timeline must not silently
		// diverge from reality.
		r.logger.Warn("content package: append publication event failed",
			"content_package_id", id, "stage", "schedule", "event_type", "SCHEDULE_CREATED", "error", err)
	}
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
