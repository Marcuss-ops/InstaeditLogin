// Package-level note: the YouTube editor-session handlers are split per
// concern (split-by-concern, 2026-08):
//
//	youtube_editor_sessions.go       — session CRUD: CreateEditorSession
//	                                   helper + handleCreate + handleUpdate
//	youtube_editor_sessions_types.go — DTO types (CreateEditorSessionInput,
//	                                   request/response) + sentinel errors
//	youtube_editor_sessions_helpers.go — writeEditorSessionError +
//	                                   compile-time assertion +
//	                                   userCanAccessWorkspace + editorURLForProject
//	youtube_editor_sessions_list.go  — handleList… (session list)
//	youtube_editor_sessions_thumbnail.go — errAttach* sentinels +
//	                                   attachThumbnailToSession +
//	                                   writeAttachThumbnailError +
//	                                   handleAttachThumbnailToEditorSession
//	youtube_editor_sessions_inflight.go — DefaultPublishingInFlightTimeout
//	youtube_editor_sessions_publish.go / _by_project*.go — publish core
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// CreateEditorSession is the central helper for the per-target YouTube
// thumbnail editor session creation. Both the HTTP handler (POST
// /api/v1/youtube/editor-sessions) and the
// youtube_processing_reconciler worker call this helper so the 1-per-
// target contract lives in one place.
//
// Click-idempotency (P0#3): the helper enforces the *FindOrCreate*
// semantics — same (workspace, account, video) tuple routed here
// twice returns the SAME row, with the SAME (id, velox_project_id).
// The partial UNIQUE INDEX `uniq_youtube_video_edits_open_session`
// (migration 071) + the SELECT-then-INSERT race-safe sequence in
// YouTubeVideoEditRepository.FindOrCreateEditableSession guarantee
// that two concurrent goroutines (e.g. a slow operator + the
// processing reconciler re-claiming the same video) converge on a
// single row, never two. Once a session lands in 'published' state
// the partial UNIQUE filter no longer matches it, so the next click
// mints a fresh row (the operator can re-edit a previously-published
// video in a new session).
//
// The helper enforces the same invariants in BOTH call sites:
//   - workspace exists;
//   - platform account is platform=YouTube;
//   - (workspace, account) channel linkage exists;
//   - a valid token is in the vault;
//   - YouTube API confirms the video is on the channel AND
//     processingStatus='processed' AND privacy != 'public'.
//   - The fresh (session_id, velox_project_id) hints are USED only
//     when no open session exists for the triple; on the REUSE path
//     the hints are discarded and the existing row wins.
//
// Returns the persisted YouTubeVideoEdit row + nil error on success
// (whether the row was newly created or already existed).
// A typed sentinel error (above) is returned on each failure mode so
// the HTTP handler can map to 4xx via errors.Is.
func (r *Router) CreateEditorSession(ctx context.Context, in CreateEditorSessionInput) (*models.YouTubeVideoEdit, error) {
	if in.WorkspaceID <= 0 {
		return nil, ErrEditorSessionWorkspaceNotFound
	}
	if in.PlatformAccountID <= 0 {
		return nil, ErrEditorSessionAccountNotFound
	}
	if in.YouTubeVideoID == "" {
		return nil, fmt.Errorf("youtube_video_id is required")
	}
	if r.workspaceStore == nil {
		return nil, ErrEditorSessionWorkspaceNotFound
	}
	workspace, err := r.workspaceStore.FindByID(in.WorkspaceID)
	if err != nil || workspace == nil {
		return nil, ErrEditorSessionWorkspaceNotFound
	}
	// Authenticated creation is intentionally owner-only. Background
	// workers leave UserID at zero and are allowed to validate/create the
	// technical session; HTTP callers must not bypass this gate by invoking
	// the shared helper through a future route.
	if in.UserID > 0 && !r.userOwnsWorkspace(in.UserID, workspace) {
		return nil, ErrEditorSessionWorkspaceNotFound
	}
	if r.userRepo == nil {
		return nil, ErrEditorSessionAccountNotFound
	}
	account, err := r.userRepo.FindPlatformAccountByID(in.PlatformAccountID)
	if err != nil || account == nil || account.Platform != models.PlatformYouTube {
		return nil, ErrEditorSessionAccountNotFound
	}
	if r.workspaceStore == nil {
		return nil, ErrEditorSessionChannelUnlinked
	}
	channel, err := r.workspaceStore.FindChannel(ctx, in.WorkspaceID, in.PlatformAccountID)
	if err != nil || channel == nil {
		return nil, ErrEditorSessionChannelUnlinked
	}
	if r.vault == nil {
		return nil, ErrEditorSessionNoValidToken
	}
	// Renew first (P0): CreateEditorSession is the FIRST step of the
	// thumbnail-batch chain and makes a remote GetYouTubeVideo call, so
	// an expired access token must be refreshed automatically from the
	// stored grant. Legacy rows are eligible only when the canonical
	// modern grant is explicitly missing; hard OAuth failures are not
	// masked by stale-token fallback. youTubeSvc is still optional at
	// this point, so the refresher is only used when the service is wired.
	var token *models.OAuthToken
	if r.youTubeSvc != nil {
		token, err = r.vault.Renew(ctx, account.ID, models.TokenTypeBearer, r.youTubeSvc.RefreshOAuthToken)
	}
	if token == nil && err == nil {
		err = errors.New("vault returned an empty token")
	}
	if errors.Is(err, credentials.ErrModernGrantMissing) {
		token, err = r.vault.Get(ctx, account.ID, models.TokenTypeLongLived)
		if errors.Is(err, credentials.ErrModernGrantMissing) {
			token, err = r.vault.Get(ctx, account.ID, models.TokenTypeShortLived)
		}
	}
	if err != nil {
		return nil, ErrEditorSessionNoValidToken
	}
	if r.youTubeSvc == nil {
		return nil, ErrEditorSessionYTServiceUnconfigured
	}
	video, err := r.youTubeSvc.GetYouTubeVideo(ctx, token.AccessToken, in.YouTubeVideoID)
	if err != nil {
		return nil, fmt.Errorf("youtube video: %w", err)
	}
	if video.ChannelID != account.PlatformUserID {
		return nil, ErrEditorSessionVideoWrongChannel
	}
	if video.UploadStatus != "processed" {
		return nil, ErrEditorSessionVideoNotReady
	}
	if video.Privacy == "public" {
		return nil, ErrEditorSessionVideoAlreadyPub
	}
	if r.youtubeVideoEditStore == nil {
		return nil, ErrEditorSessionEditStoreUnconfigured
	}
	// Pre-generate UUID hints for the (rare) INSERT path. The FindOrCreate
	// repository method mints fresh UUIDs internally if these hints are
	// empty; supplying them explicitly keeps the response shape
	// stable for the CREATE path and lets a debugger trace the
	// session_id back to the originating request log entry.
	sessionID := uuid.NewString()
	projectID := "ve_" + uuid.NewString()
	persisted, err := r.youtubeVideoEditStore.FindOrCreateEditableSession(
		ctx,
		in.WorkspaceID,
		in.PlatformAccountID,
		in.YouTubeVideoID,
		sessionID,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("find or create editor session: %w", err)
	}
	// Action 6 "Modifica" flow: the session row carries the opaque
	// velox_project_id (created or reused by FindOrCreateEditableSession
	// above), but the provider project itself must exist and be mapped
	// through the EditorService boundary. ensureEditorProjectBridge is
	// create-or-reuse: it either finds the durable bridge for
	// (workspace, session) or asks the provider adapter to create the
	// project and persists the mapping. Background callers (no
	// authenticated user) skip it; the bridge is minted lazily on the
	// first operator open.
	if err := r.ensureEditorProjectBridge(ctx, in, persisted); err != nil {
		// The session row is durable and may have been newly inserted
		// before the provider bridge failed. Mark it failed so the
		// operator cannot observe an apparently editable orphan; the
		// next Modifica attempt reuses this row and retries the same
		// idempotent bridge resolution.
		persisted.Status = "failed"
		persisted.LastError = err.Error()
		if compensateErr := r.youtubeVideoEditStore.Update(ctx, persisted); compensateErr != nil {
			return nil, fmt.Errorf("%w (failed to mark editor session failed: %v)", err, compensateErr)
		}
		return nil, err
	}
	// YouTube's videos.list response is authoritative for the source
	// thumbnail. This matters for an existing session: older rows can
	// contain a stale/broken URL, and the InstaEditor will otherwise
	// keep rendering its grey placeholder forever. Fall back to the
	// browser hint only when YouTube did not return a thumbnail.
	//
	// Do not overwrite an existing value with a less authoritative empty
	// response or with a second browser hint. A non-empty YouTube URL,
	// however, is deliberately allowed to repair an old row.
	videoThumbnailURL := safeEditorAssetURL(video.ThumbnailURL)
	persistedRawThumbnailURL := strings.TrimSpace(persisted.SourceThumbnailURL)
	sourceThumbnailURL := videoThumbnailURL
	if sourceThumbnailURL == "" && persistedRawThumbnailURL == "" {
		sourceThumbnailURL = safeEditorAssetURL(in.SourceThumbnailURL)
	}
	persistedThumbnailURL := safeEditorAssetURL(persistedRawThumbnailURL)
	if sourceThumbnailURL == "" {
		sourceThumbnailURL = persistedThumbnailURL
	}
	if persistedRawThumbnailURL != persistedThumbnailURL ||
		(videoThumbnailURL != "" && persistedThumbnailURL != videoThumbnailURL) ||
		(persistedRawThumbnailURL == "" && sourceThumbnailURL != persistedThumbnailURL) {
		persisted.SourceThumbnailURL = sourceThumbnailURL
		persisted.UpdatedAt = time.Now().UTC()
		if updateErr := r.youtubeVideoEditStore.Update(ctx, persisted); updateErr != nil {
			return nil, fmt.Errorf("repair editor session source thumbnail: %w", updateErr)
		}
	}
	return persisted, nil
}

// ensureEditorProjectBridge resolves the provider project mapping for a
// persisted editor session (Action 6 "Modifica" flow). It is strictly
// create-or-reuse through the EditorService boundary:
//
//   - a bridge already exists for (workspace, session) → the service
//     returns it unchanged (idempotent REUSE, same velox_project_id);
//   - no bridge yet → the service asks the provider adapter to create
//     the project (validating the session's opaque handle) and persists
//     the mapping (CREATE).
//
// The adapter performs no remote call when the external project id is
// already a valid opaque handle: the Velox control plane lazily
// materializes the project on its first document write. The bridge row
// therefore records the mapping before the launcher is returned.
//
// Background callers (processing reconciler, thumbnail batches, the
// Velox service-to-service handoff) pass UserID=0 and are skipped here:
// the mapping is minted lazily on the first operator open, which runs
// the same idempotent path.
func (r *Router) ensureEditorProjectBridge(ctx context.Context, in CreateEditorSessionInput, edit *models.YouTubeVideoEdit) error {
	if edit == nil || r.editorService == nil || in.UserID <= 0 {
		return nil
	}
	created, err := r.editorService.CreateProject(ctx, services.CreateEditorProjectRequest{
		UserID:               in.UserID,
		WorkspaceID:          in.WorkspaceID,
		ApplicationProjectID: edit.ID,
		ExternalProjectID:    edit.VeloxProjectID,
	})
	if err != nil {
		return fmt.Errorf("ensure editor project bridge: %w", err)
	}
	// Defence-in-depth: the service may only hand back the very external
	// project the session already owns. A different handle would mean a
	// foreign bridge was adopted — never redirect the operator there.
	if created == nil || created.ExternalProjectID != edit.VeloxProjectID {
		return fmt.Errorf("%w: resolved project does not match the session handle", services.ErrEditorProjectInvalid)
	}
	return nil
}

// handleCreateYouTubeEditorSession is the HTTP entry point of POST
// /api/v1/youtube/editor-sessions. After Blocco #4 P0 refactor it is a
// thin wrapper that does:
//   - JWT identity extraction;
//   - JSON payload decoding;
//   - workspace ownership check (handler-only auth gate);
//   - delegate to CreateEditorSession (the shared helper);
//   - HTTP response shaping (session_id, velox_project_id, editor_url).
//
// The per-target 1-per-channel contract lives in the helper, NOT in
// this handler — every call generates fresh UUIDs.
func (r *Router) handleCreateYouTubeEditorSession(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	var payload createYouTubeEditorSessionRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if payload.WorkspaceID <= 0 || payload.PlatformAccountID <= 0 || payload.YouTubeVideoID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id, platform_account_id, youtube_video_id are required")
		return
	}

	// Workspace ownership check is repeated at the shared helper boundary;
	// keeping it here fails closed before any editor/session side effect.
	workspace, err := r.workspaceStore.FindByID(payload.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return
	}
	if workspace == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if !r.userOwnsWorkspace(identity.UserID(), workspace) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	// Fail before any repository or provider mutation. A missing
	// INSTAEDITOR_URL is an operator configuration error, not a reason
	// to create an orphan editor session that cannot be opened. The
	// gate sits AFTER the ownership check so a foreign-tenant probe
	// cannot distinguish editor configuration state (404-as-foreign
	// must win over 503), matching the by-id/by-project GET handlers.
	if strings.TrimSpace(r.editorURL) == "" {
		writeError(w, http.StatusServiceUnavailable, "Editor unavailable / misconfigured")
		return
	}

	edit, err := r.CreateEditorSession(req.Context(), CreateEditorSessionInput{
		WorkspaceID:        payload.WorkspaceID,
		PlatformAccountID:  payload.PlatformAccountID,
		YouTubeVideoID:     payload.YouTubeVideoID,
		SourceThumbnailURL: payload.SourceThumbnailURL,
		UserID:             identity.UserID(),
	})
	if err != nil {
		r.writeEditorSessionError(w, err)
		return
	}

	// The gate above guarantees a non-empty editorURL, so the launcher
	// URL is always available here (the session handle is never empty).
	editorURL := r.editorURLForProject(edit.VeloxProjectID)
	writeJSON(w, http.StatusCreated, createYouTubeEditorSessionResponse{
		SessionID:      edit.ID,
		VeloxProjectID: edit.VeloxProjectID,
		EditorURL:      editorURL,
	})
}

// handleUpdateYouTubeEditorSession updates a thumbnail editor session.
// It is used by InstaEditor after uploading the generated thumbnail
// to InstaEdit storage so the session keeps a reference to the verified
// asset (thumbnail_media_id) before the user clicks Publish.
//
// The core logic is delegated to the shared attachThumbnailToSession
// resolver, so it behaves identically to the direct
// POST /api/v1/youtube/editor-sessions/{id}/thumbnail endpoint.
func (r *Router) handleUpdateYouTubeEditorSession(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	veloxProjectID := chi.URLParam(req, "velox_project_id")
	if veloxProjectID == "" {
		writeError(w, http.StatusBadRequest, "velox_project_id is required")
		return
	}

	var payload attachThumbnailRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if payload.ThumbnailMediaID == "" {
		writeError(w, http.StatusBadRequest, "thumbnail_media_id is required")
		return
	}

	if r.mediaStore == nil {
		writeError(w, http.StatusNotImplemented, "media not configured on this server")
		return
	}

	if r.youtubeVideoEditStore == nil {
		writeError(w, http.StatusServiceUnavailable, "youtube video edit store not configured")
		return
	}

	edit, err := r.youtubeVideoEditStore.FindByVeloxProjectID(req.Context(), veloxProjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find editor session: "+err.Error())
		return
	}
	if edit == nil {
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}

	updated, err := r.attachThumbnailToSession(req.Context(), identity, edit, payload.ThumbnailMediaID)
	if err != nil {
		r.writeAttachThumbnailError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"session_id":         updated.ID,
		"thumbnail_media_id": updated.ThumbnailMediaID,
	})
}
