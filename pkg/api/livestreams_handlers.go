package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

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
	limit, rawCursor, err := parseListPage(req.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cursorContext := listCursorFilterContext(req.URL.Query(), "workspace_id")
	cursorTime, cursorID, cursorNull, err := decodeListCursorDetails(rawCursor, "livestreams", cursorContext)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if cursorNull {
		writeError(w, http.StatusBadRequest, "invalid list cursor: livestream cursor timestamp is required")
		return
	}
	var items []models.Livestream
	hasMore := false
	if paged, ok := r.livestreamStore.(interface {
		ListByWorkspacePage(context.Context, int64, *time.Time, string, int) ([]models.Livestream, bool, error)
	}); ok {
		var afterTime *time.Time
		if rawCursor != "" {
			afterTime = &cursorTime
		}
		items, hasMore, err = paged.ListByWorkspacePage(req.Context(), workspaceID, afterTime, cursorID, limit)
	} else {
		if rawCursor != "" {
			writeError(w, http.StatusNotImplemented, "cursor pagination is not supported by this livestream store")
			return
		}
		items, err = r.livestreamStore.ListByWorkspace(req.Context(), workspaceID)
		if len(items) > limit {
			hasMore = true
			items = items[:limit]
		}
	}
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
	resp.HasMore = hasMore
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		resp.NextCursor = encodeListCursorForContext("livestreams", cursorContext, last.UpdatedAt, last.ID)
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

	now := time.Now().UTC()
	ls := &models.Livestream{
		ID:                "ls_" + uuid.NewString(),
		WorkspaceID:       payload.WorkspaceID,
		PlatformAccountID: payload.PlatformAccountID,
		CreatedBy:         identity.UserID(),
		DesiredState:      models.LivestreamStateDraft,
		ActualState:       models.LivestreamStateDraft,
		Resolution:        models.LivestreamResolution1080p,
		FrameRate:         models.LivestreamFrameRate,
		AutoRestart:       true,
		LatencyPreference: models.LivestreamLatencyNormal,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := applyLivestreamFields(ls, livestreamCreateFields(payload)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
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

	if err := applyLivestreamFields(ls, livestreamPatchFields(payload)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
