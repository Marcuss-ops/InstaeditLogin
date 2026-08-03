package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// ---------------------------------------------------------------------------
// Validation helpers
// ---------------------------------------------------------------------------

func normalizeLivestreamTitle(s string) (string, error) {
	title := strings.TrimSpace(s)
	if title == "" {
		return "", errors.New("title is required")
	}
	if utf8.RuneCountInString(title) > models.LivestreamTitleMaxRunes {
		return "", fmt.Errorf("title must be at most %d characters", models.LivestreamTitleMaxRunes)
	}
	return title, nil
}

func normalizeLivestreamDescription(s string) (string, error) {
	if utf8.RuneCountInString(s) > models.LivestreamDescriptionMaxRunes {
		return "", fmt.Errorf("description must be at most %d characters", models.LivestreamDescriptionMaxRunes)
	}
	return s, nil
}

func validateLivestreamPrivacy(s string) (string, error) {
	switch strings.TrimSpace(s) {
	case models.LivestreamPrivacyPrivate, models.LivestreamPrivacyUnlisted, models.LivestreamPrivacyPublic:
		return strings.TrimSpace(s), nil
	default:
		return "", fmt.Errorf("privacy_status must be one of private, unlisted, public")
	}
}

func validateLivestreamPlaybackMode(s string) (string, error) {
	switch strings.TrimSpace(s) {
	case models.LivestreamPlaybackLoopContinuous, models.LivestreamPlaybackPlayOnce:
		return strings.TrimSpace(s), nil
	default:
		return "", fmt.Errorf("playback_mode must be one of loop_continuous, play_once")
	}
}

func validateLivestreamScheduleType(s string) (string, error) {
	switch strings.TrimSpace(s) {
	case models.LivestreamScheduleManual, models.LivestreamScheduleNow,
		models.LivestreamScheduleScheduled, models.LivestreamScheduleRecurring:
		return strings.TrimSpace(s), nil
	default:
		return "", fmt.Errorf("schedule_type must be one of manual, now, scheduled, recurring")
	}
}

func validateLivestreamResolution(s string) (string, error) {
	switch strings.TrimSpace(s) {
	case "":
		return models.LivestreamResolution1080p, nil
	case models.LivestreamResolution720p, models.LivestreamResolution1080p:
		return strings.TrimSpace(s), nil
	default:
		return "", fmt.Errorf("resolution must be one of 720p30, 1080p30")
	}
}

// validateLivestreamFrameRate accepts 0 (→ default 30) or exactly 30.
func validateLivestreamFrameRate(n int) (int, error) {
	if n == 0 {
		return models.LivestreamFrameRate, nil
	}
	if n != models.LivestreamFrameRate {
		return 0, fmt.Errorf("frame_rate must be %d", models.LivestreamFrameRate)
	}
	return n, nil
}

func parseOptionalRFC3339(s *string) (*time.Time, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(*s))
	if err != nil {
		return nil, fmt.Errorf("scheduled_start_at must be an RFC3339 timestamp: %w", err)
	}
	return &t, nil
}

// validateLivestreamCategory accepts an empty value (no category) or
// a known YouTube numeric category id (e.g. "24" = Entertainment).
// YouTube remains the authority at broadcast creation time; this is a
// local sanity gate so the wizard's select never round-trips a bogus id.
func validateLivestreamCategory(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if !models.ValidLivestreamCategory(s) {
		return "", fmt.Errorf("category must be a known YouTube category id (or empty)")
	}
	return s, nil
}

// validateLivestreamLanguage accepts an empty value or an ISO 639-1 /
// BCP-47 language code (light sanity gate; YouTube validates fully).
func validateLivestreamLanguage(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if utf8.RuneCountInString(s) > models.LivestreamLanguageMaxRunes {
		return "", fmt.Errorf("language must be at most %d characters", models.LivestreamLanguageMaxRunes)
	}
	if err := models.CheckBCP47Like("language", s); err != nil {
		return "", err
	}
	return s, nil
}

// validateLivestreamLatency accepts an empty value (→ default normal)
// or one of normal / low / ultraLow.
func validateLivestreamLatency(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return models.LivestreamLatencyNormal, nil
	}
	if !models.ValidLivestreamLatency(s) {
		return "", fmt.Errorf("latency_preference must be one of normal, low, ultraLow")
	}
	return s, nil
}

// normalizeLivestreamThumbnail trims and bounds the optional cover
// asset id. An empty/absent value yields nil (no cover). The
// authoritative existence / readiness / ownership check happens at
// prepare time in the worker; here we only run a length sanity gate.
// TODO(livestream-worker): validate the referenced media_assets row
// (exists, status=ready, owned by the workspace user) like
// resolveMediaAssets does, before mapping it onto the broadcast.
func normalizeLivestreamThumbnail(s *string) (*string, error) {
	if s == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*s)
	if trimmed == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(trimmed) > 64 {
		return nil, errors.New("thumbnail_media_id is too long")
	}
	return &trimmed, nil
}

// livestreamLiveScopes are the OAuth scopes that unlock the YouTube
// Live Streaming API. New grants always include youtube.force-ssl;
// grants issued before the scope cleanup may only carry youtube. Both
// the full Google URLs and the bare scope names are accepted.
var livestreamLiveScopes = []string{
	"https://www.googleapis.com/auth/youtube",
	"https://www.googleapis.com/auth/youtube.force-ssl",
	"youtube",
	"youtube.force-ssl",
}

// livestreamHasLiveScope reports whether the grant token carries a
// YouTube live scope. A nil token is never live-enabled.
func livestreamHasLiveScope(token *models.OAuthToken) bool {
	if token == nil {
		return false
	}
	for _, granted := range token.Scopes {
		s := strings.TrimSpace(granted)
		for _, wanted := range livestreamLiveScopes {
			if s == wanted {
				return true
			}
		}
	}
	return false
}

// livestreamTokenForAccount returns the account's bearer grant token,
// falling back to the legacy token types. Errors signal a missing or
// expired credential (an active account whose access token expired
// needs a reconnect).
func (r *Router) livestreamTokenForAccount(ctx context.Context, accountID int64) (*models.OAuthToken, error) {
	if r.vault == nil {
		return nil, errors.New("vault not configured")
	}
	for _, tokenType := range []string{models.TokenTypeBearer, models.TokenTypeLongLived, models.TokenTypeShortLived} {
		token, err := r.vault.Get(ctx, accountID, tokenType)
		if err == nil && token != nil {
			return token, nil
		}
	}
	return nil, errors.New("no valid token found for this account")
}

// ---------------------------------------------------------------------------
// GET /api/v1/livestreams/channels — creation-wizard preflight
// ---------------------------------------------------------------------------

// handleListLivestreamChannels returns the workspace's YouTube channels
// with the preflight data the creation wizard needs: OAuth grant state,
// live scope presence, last validation and how many live streams are
// currently running on each channel. Channels without the live scope
// are still listed (the UI explains why they are blocked); the create
// endpoint enforces the same scope guard server-side.
func (r *Router) handleListLivestreamChannels(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	if r.livestreamStore == nil || r.workspaceStore == nil || r.userRepo == nil || r.vault == nil {
		writeError(w, http.StatusServiceUnavailable, "livestream store not configured")
		return
	}
	workspaceID, err := strconv.ParseInt(req.URL.Query().Get("workspace_id"), 10, 64)
	if err != nil || workspaceID <= 0 {
		writeError(w, http.StatusBadRequest, "workspace_id query parameter is required")
		return
	}
	if !r.workspaceOwnedBy(identity, workspaceID) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	// One vault.Get per channel (a decrypt each) is acceptable for the
	// bounded workspace channel set; revisit only if channel counts
	// grow into the hundreds.
	channels, err := r.workspaceStore.ListChannels(req.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list workspace channels: "+err.Error())
		return
	}

	// Active-live counts per account from the same rows the sidebar
	// badge consumes (actual_state == "live").
	liveCounts := map[int64]int{}
	if streams, listErr := r.livestreamStore.ListByWorkspace(req.Context(), workspaceID); listErr == nil {
		for i := range streams {
			if streams[i].ActualState == models.LivestreamStateLive {
				liveCounts[streams[i].PlatformAccountID]++
			}
		}
	}

	resp := listLivestreamChannelsResponse{Channels: make([]livestreamChannelResponse, 0, len(channels))}
	for _, ch := range channels {
		account, accountErr := r.userRepo.FindPlatformAccountByID(ch.PlatformAccountID)
		if accountErr != nil || account == nil || account.Platform != models.PlatformYouTube {
			continue
		}
		state, _ := classifyAccountStatus(account.Status)
		item := livestreamChannelResponse{
			PlatformAccountID: account.ID,
			Username:          account.Username,
			PlatformUserID:    account.PlatformUserID,
			AccountState:      state,
			LastVerifiedAt:    account.LastValidatedAt,
			ActiveLives:       liveCounts[account.ID],
		}
		if token, tokenErr := r.livestreamTokenForAccount(req.Context(), account.ID); tokenErr == nil {
			item.OAuthReady = true
			item.LiveEnabled = livestreamHasLiveScope(token)
		}
		resp.Channels = append(resp.Channels, item)
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// GET /api/v1/livestreams
// ---------------------------------------------------------------------------

func (r *Router) handleListLivestreams(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	if r.livestreamStore == nil {
		writeError(w, http.StatusServiceUnavailable, "livestream store not configured")
		return
	}
	workspaceID, err := strconv.ParseInt(req.URL.Query().Get("workspace_id"), 10, 64)
	if err != nil || workspaceID <= 0 {
		writeError(w, http.StatusBadRequest, "workspace_id query parameter is required")
		return
	}
	if !r.workspaceOwnedBy(identity, workspaceID) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	items, err := r.livestreamStore.ListByWorkspace(req.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list livestreams: "+err.Error())
		return
	}
	resp := listLivestreamsResponse{Items: make([]livestreamResponse, 0, len(items))}
	names := r.livestreamChannelNames(req.Context(), items)
	for i := range items {
		row := toLivestreamResponse(&items[i])
		row.ChannelName = names[row.PlatformAccountID]
		resp.Items = append(resp.Items, row)
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// POST /api/v1/livestreams
// ---------------------------------------------------------------------------

func (r *Router) handleCreateLivestream(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	if r.livestreamStore == nil {
		writeError(w, http.StatusServiceUnavailable, "livestream store not configured")
		return
	}

	var payload createLivestreamRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 2<<20))
	if err := decoder.Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid livestream payload")
		return
	}
	if payload.WorkspaceID <= 0 || payload.PlatformAccountID <= 0 {
		writeError(w, http.StatusBadRequest, "workspace_id and platform_account_id are required")
		return
	}
	if !r.workspaceOwnedBy(identity, payload.WorkspaceID) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if r.userRepo == nil || r.workspaceStore == nil {
		writeError(w, http.StatusServiceUnavailable, "account or workspace store not configured")
		return
	}

	account, err := r.livestreamChannel(req, payload.WorkspaceID, payload.PlatformAccountID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if account.Status != models.AccountStatusActive {
		writeError(w, http.StatusBadRequest, "the channel is not active; reconnect it before creating a live")
		return
	}
	// Live-scope guard: a grant without a YouTube live scope cannot
	// create broadcasts. Missing/expired grants are surfaced as a
	// reconnect requirement (defence in depth on top of the status
	// check above — an active row can still carry an expired access
	// token or a grant stripped of the live scope).
	if r.vault != nil {
		token, tokenErr := r.livestreamTokenForAccount(req.Context(), account.ID)
		if tokenErr != nil {
			writeError(w, http.StatusBadRequest, "the channel OAuth grant is unavailable; reconnect the channel before creating a live")
			return
		}
		if !livestreamHasLiveScope(token) {
			writeError(w, http.StatusBadRequest, "the channel grant does not include YouTube live streaming; reconnect the channel with live permissions")
			return
		}
	}

	title, err := normalizeLivestreamTitle(payload.Title)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	description, err := normalizeLivestreamDescription(payload.Description)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	privacy, err := validateLivestreamPrivacy(payload.PrivacyStatus)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	playback, err := validateLivestreamPlaybackMode(payload.PlaybackMode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	schedule, err := validateLivestreamScheduleType(payload.ScheduleType)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resolution, err := validateLivestreamResolution(payload.Resolution)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	frameRate, err := validateLivestreamFrameRate(payload.FrameRate)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	scheduledAt, err := parseOptionalRFC3339(payload.ScheduledStartAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if schedule == models.LivestreamScheduleScheduled && scheduledAt == nil {
		writeError(w, http.StatusBadRequest, "scheduled_start_at is required when schedule_type is scheduled")
		return
	}
	autoRestart := true
	if payload.AutoRestart != nil {
		autoRestart = *payload.AutoRestart
	}
	category, err := validateLivestreamCategory(payload.Category)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	language, err := validateLivestreamLanguage(payload.Language)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	latency, err := validateLivestreamLatency(payload.LatencyPreference)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	thumbnail, err := normalizeLivestreamThumbnail(payload.ThumbnailMediaID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	madeForKids := false
	if payload.MadeForKids != nil {
		madeForKids = *payload.MadeForKids
	}
	dvrEnabled := false
	if payload.DVREnabled != nil {
		dvrEnabled = *payload.DVREnabled
	}
	autoStart := false
	if payload.AutoStart != nil {
		autoStart = *payload.AutoStart
	}
	autoStop := false
	if payload.AutoStop != nil {
		autoStop = *payload.AutoStop
	}

	now := time.Now().UTC()
	ls := &models.Livestream{
		ID:                "ls_" + uuid.NewString(),
		WorkspaceID:       payload.WorkspaceID,
		PlatformAccountID: payload.PlatformAccountID,
		CreatedBy:         identity.UserID(),
		Title:             title,
		Description:       description,
		PrivacyStatus:     privacy,
		PlaybackMode:      playback,
		ScheduleType:      schedule,
		ScheduledStartAt:  scheduledAt,
		DesiredState:      models.LivestreamStateDraft,
		ActualState:       models.LivestreamStateDraft,
		Resolution:        resolution,
		FrameRate:         frameRate,
		AutoRestart:       autoRestart,
		Category:          category,
		MadeForKids:       madeForKids,
		Language:          language,
		ThumbnailMediaID:  thumbnail,
		DVREnabled:        dvrEnabled,
		AutoStart:         autoStart,
		AutoStop:          autoStop,
		LatencyPreference: latency,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := r.livestreamStore.Create(req.Context(), ls); err != nil {
		writeError(w, http.StatusInternalServerError, "create livestream: "+err.Error())
		return
	}
	resp := toLivestreamResponse(ls)
	resp.ChannelName = account.Username
	writeJSON(w, http.StatusCreated, resp)
}

// ---------------------------------------------------------------------------
// GET /api/v1/livestreams/{id}
// ---------------------------------------------------------------------------

func (r *Router) handleGetLivestream(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	if r.livestreamStore == nil {
		writeError(w, http.StatusServiceUnavailable, "livestream store not configured")
		return
	}
	ls, ok := r.loadLivestreamForIdentity(w, req, identity)
	if !ok {
		return
	}
	row := toLivestreamResponse(ls)
	row.ChannelName = r.livestreamChannelNames(req.Context(), []models.Livestream{*ls})[ls.PlatformAccountID]
	writeJSON(w, http.StatusOK, row)
}

// ---------------------------------------------------------------------------
// PATCH /api/v1/livestreams/{id}
// ---------------------------------------------------------------------------

func (r *Router) handlePatchLivestream(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	if r.livestreamStore == nil {
		writeError(w, http.StatusServiceUnavailable, "livestream store not configured")
		return
	}
	ls, ok := r.loadLivestreamForIdentity(w, req, identity)
	if !ok {
		return
	}

	var payload patchLivestreamRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 2<<20))
	if err := decoder.Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid livestream payload")
		return
	}
	if payload.DesiredState != nil || payload.ActualState != nil {
		writeError(w, http.StatusBadRequest, "desired_state and actual_state are managed by the livestream worker")
		return
	}

	if payload.Title != nil {
		title, err := normalizeLivestreamTitle(*payload.Title)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		ls.Title = title
	}
	if payload.Description != nil {
		description, err := normalizeLivestreamDescription(*payload.Description)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		ls.Description = description
	}
	if payload.PrivacyStatus != nil {
		privacy, err := validateLivestreamPrivacy(*payload.PrivacyStatus)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		ls.PrivacyStatus = privacy
	}
	if payload.PlaybackMode != nil {
		playback, err := validateLivestreamPlaybackMode(*payload.PlaybackMode)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		ls.PlaybackMode = playback
	}
	if payload.ScheduleType != nil {
		schedule, err := validateLivestreamScheduleType(*payload.ScheduleType)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		ls.ScheduleType = schedule
	}
	if payload.ScheduledStartAt != nil {
		scheduledAt, err := parseOptionalRFC3339(payload.ScheduledStartAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		ls.ScheduledStartAt = scheduledAt
	}
	if payload.Resolution != nil {
		resolution, err := validateLivestreamResolution(*payload.Resolution)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		ls.Resolution = resolution
	}
	if payload.FrameRate != nil {
		frameRate, err := validateLivestreamFrameRate(*payload.FrameRate)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		ls.FrameRate = frameRate
	}
	if payload.AutoRestart != nil {
		ls.AutoRestart = *payload.AutoRestart
	}
	if payload.Category != nil {
		category, err := validateLivestreamCategory(*payload.Category)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		ls.Category = category
	}
	if payload.MadeForKids != nil {
		ls.MadeForKids = *payload.MadeForKids
	}
	if payload.Language != nil {
		language, err := validateLivestreamLanguage(*payload.Language)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		ls.Language = language
	}
	if payload.ThumbnailMediaID != nil {
		thumbnail, err := normalizeLivestreamThumbnail(payload.ThumbnailMediaID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		ls.ThumbnailMediaID = thumbnail
	}
	if payload.DVREnabled != nil {
		ls.DVREnabled = *payload.DVREnabled
	}
	if payload.AutoStart != nil {
		ls.AutoStart = *payload.AutoStart
	}
	if payload.AutoStop != nil {
		ls.AutoStop = *payload.AutoStop
	}
	if payload.LatencyPreference != nil {
		latency, err := validateLivestreamLatency(*payload.LatencyPreference)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		ls.LatencyPreference = latency
	}
	if ls.ScheduleType == models.LivestreamScheduleScheduled && ls.ScheduledStartAt == nil {
		writeError(w, http.StatusBadRequest, "scheduled_start_at is required when schedule_type is scheduled")
		return
	}

	if err := r.livestreamStore.Update(req.Context(), ls); err != nil {
		writeError(w, http.StatusInternalServerError, "update livestream: "+err.Error())
		return
	}
	updated, err := r.livestreamStore.FindByID(req.Context(), ls.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reload livestream: "+err.Error())
		return
	}
	if updated == nil {
		writeError(w, http.StatusNotFound, "livestream not found")
		return
	}
	row := toLivestreamResponse(updated)
	row.ChannelName = r.livestreamChannelNames(req.Context(), []models.Livestream{*updated})[updated.PlatformAccountID]
	writeJSON(w, http.StatusOK, row)
}

// ---------------------------------------------------------------------------
// DELETE /api/v1/livestreams/{id}
// ---------------------------------------------------------------------------

func (r *Router) handleDeleteLivestream(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	if r.livestreamStore == nil {
		writeError(w, http.StatusServiceUnavailable, "livestream store not configured")
		return
	}
	ls, ok := r.loadLivestreamForIdentity(w, req, identity)
	if !ok {
		return
	}
	if err := r.livestreamStore.Delete(req.Context(), ls.ID); err != nil {
		// TOCTOU guard: the row may have been deleted between the
		// ownership load and this DELETE.
		if errors.Is(err, repository.ErrLivestreamNotFound) {
			writeError(w, http.StatusNotFound, "livestream not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "delete livestream: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// workspaceOwnedBy reports whether the caller owns the workspace. A
// missing workspace or a non-owner caller both yield 404 semantics to
// avoid leaking workspace existence.
func (r *Router) workspaceOwnedBy(identity auth.Identity, workspaceID int64) bool {
	if identity == nil || workspaceID <= 0 || r.workspaceStore == nil {
		return false
	}
	workspace, err := r.workspaceStore.FindByID(workspaceID)
	if err != nil || workspace == nil {
		return false
	}
	return r.userCanAccessWorkspace(identity.UserID(), workspace)
}

// livestreamChannel validates the (workspace, account) tuple for a new
// live: the account must exist, be a YouTube channel and be linked to
// the workspace via the workspace_channels join. Returns the account
// or a 404-shaped error message.
// livestreamChannel validates the (workspace, account) tuple for a new
// live: the account must exist, be a YouTube channel and be linked to
// the workspace via the workspace_channels join. Returns the account
// or a 404-shaped error message. The caller guards the store
// nil-cases (503) before invoking this helper.
func (r *Router) livestreamChannel(req *http.Request, workspaceID, accountID int64) (*models.PlatformAccount, error) {
	account, err := r.userRepo.FindPlatformAccountByID(accountID)
	if err != nil || account == nil || account.Platform != models.PlatformYouTube {
		return nil, errors.New("youtube account not found")
	}
	channel, err := r.workspaceStore.FindChannel(req.Context(), workspaceID, accountID)
	if err != nil || channel == nil {
		return nil, errors.New("account not linked to workspace")
	}
	return account, nil
}

// livestreamChannelNames resolves the display name of the YouTube
// channel backing each livestream row. Unknown accounts (or a missing
// userRepo, e.g. a partially wired test router) yield an empty name;
// the frontend falls back to "Canale #id".
func (r *Router) livestreamChannelNames(ctx context.Context, items []models.Livestream) map[int64]string {
	names := make(map[int64]string)
	if r.userRepo == nil {
		return names
	}
	for i := range items {
		id := items[i].PlatformAccountID
		if _, seen := names[id]; seen {
			continue
		}
		account, err := r.userRepo.FindPlatformAccountByID(id)
		if err == nil && account != nil {
			names[id] = account.Username
		}
	}
	return names
}

// loadLivestreamForIdentity loads the {id} URL param row and verifies
// the caller owns its workspace. On failure it writes the response and
// returns ok=false.
func (r *Router) loadLivestreamForIdentity(w http.ResponseWriter, req *http.Request, identity auth.Identity) (*models.Livestream, bool) {
	id := strings.TrimSpace(chi.URLParam(req, "id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "livestream id is required")
		return nil, false
	}
	ls, err := r.livestreamStore.FindByID(req.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find livestream: "+err.Error())
		return nil, false
	}
	if ls == nil || !r.workspaceOwnedBy(identity, ls.WorkspaceID) {
		writeError(w, http.StatusNotFound, "livestream not found")
		return nil, false
	}
	return ls, true
}
