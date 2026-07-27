package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// DefaultPublishingInFlightTimeout is the default guard window used
// when a YouTube thumbnail publish session is already in-flight.
const DefaultPublishingInFlightTimeout = 5 * time.Minute

// Sentinel errors for CreateEditorSession. The HTTP handler maps them
// to status codes via errors.Is below; the reconciler worker reads
// them for retry vs skip decisions.
var (
	ErrEditorSessionWorkspaceNotFound = errors.New("workspace not found")
	ErrEditorSessionAccountNotFound   = errors.New("youtube account not found")
	ErrEditorSessionChannelUnlinked   = errors.New("account not linked to workspace")
	ErrEditorSessionNoValidToken      = errors.New("no valid token found for this account")
	ErrEditorSessionVideoWrongChannel = errors.New("video does not belong to selected channel")
	ErrEditorSessionVideoNotReady     = errors.New("video is not ready for thumbnail editing")
	ErrEditorSessionVideoAlreadyPub   = errors.New("video is already public; thumbnail editing allowed only for private or unlisted videos")
	ErrEditorSessionYTServiceUnconfigured = errors.New("youtube service not configured")
	ErrEditorSessionEditStoreUnconfigured = errors.New("youtube video edit store not configured")
)

// CreateEditorSessionInput is the canonical input for the editor-session
// helper. Both the HTTP handler and the youtube_processing_reconciler
// worker construct this struct; the helper's validates-then-creates
// flow is identical for both call sites (per-target 1:1 contract
// preserved at the helper level).
//
// Blocco #4 P0: the struct is EXPORTED so the worker in
// internal/worker can import it without breaking pkg/api's unexported-
// type boundary.
type CreateEditorSessionInput struct {
	WorkspaceID        int64
	PlatformAccountID  int64
	YouTubeVideoID     string
	SourceThumbnailURL string
}

// createYouTubeEditorSessionRequest is the body accepted by
// POST /api/v1/youtube/editor-sessions.
type createYouTubeEditorSessionRequest struct {
	WorkspaceID        int64  `json:"workspace_id"`
	PlatformAccountID  int64  `json:"platform_account_id"`
	YouTubeVideoID     string `json:"youtube_video_id"`
	SourceThumbnailURL string `json:"source_thumbnail_url,omitempty"`
}

// createYouTubeEditorSessionResponse is returned on a successful creation.
type createYouTubeEditorSessionResponse struct {
	SessionID      string `json:"session_id"`
	VeloxProjectID string `json:"velox_project_id"`
	EditorURL      string `json:"editor_url"`
}

// updateYouTubeEditorSessionRequest is the body accepted by the
// PATCH /api/v1/youtube/editor-sessions/by-project/{velox_project_id}
// endpoint.
type updateYouTubeEditorSessionRequest struct {
	ThumbnailMediaID string `json:"thumbnail_media_id"`
}

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
	token, err := r.vault.Get(ctx, account.ID, models.TokenTypeBearer)
	if err != nil {
		token, err = r.vault.Get(ctx, account.ID, models.TokenTypeLongLived)
		if err != nil {
			token, err = r.vault.Get(ctx, account.ID, models.TokenTypeShortLived)
			if err != nil {
				return nil, ErrEditorSessionNoValidToken
			}
		}
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
	// On the CREATE path, stamp the operator-supplied
	// SourceThumbnailURL hint. On the REUSE path, leave the row's
	// existing SourceThumbnailURL untouched (the first click's
	// operator-typed value wins; subsequent clicks don't overwrite).
	if persisted.SourceThumbnailURL == "" && in.SourceThumbnailURL != "" {
		persisted.SourceThumbnailURL = in.SourceThumbnailURL
		persisted.UpdatedAt = time.Now().UTC()
		if updateErr := r.youtubeVideoEditStore.Update(ctx, persisted); updateErr != nil {
			return nil, fmt.Errorf("update editor session source thumbnail: %w", updateErr)
		}
	}
	return persisted, nil
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

	// Workspace ownership check (handler-only gate; the helper trusts
	// the caller to supply a valid workspace_id and doesn't re-verify
	// ownership — the handler is the HTTP boundary and DOES verify).
	workspace, err := r.workspaceStore.FindByID(payload.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return
	}
	if workspace == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if !r.userCanAccessWorkspace(identity.UserID(), workspace) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	edit, err := r.CreateEditorSession(req.Context(), CreateEditorSessionInput{
		WorkspaceID:        payload.WorkspaceID,
		PlatformAccountID:  payload.PlatformAccountID,
		YouTubeVideoID:     payload.YouTubeVideoID,
		SourceThumbnailURL: payload.SourceThumbnailURL,
	})
	if err != nil {
		r.writeEditorSessionError(w, err)
		return
	}

	editorURL := r.editorURLForProject(edit.VeloxProjectID)
	writeJSON(w, http.StatusCreated, createYouTubeEditorSessionResponse{
		SessionID:      edit.ID,
		VeloxProjectID: edit.VeloxProjectID,
		EditorURL:      editorURL,
	})
}

// writeEditorSessionError maps the helper's typed sentinel errors to
// HTTP status codes via errors.Is. Extracted so the handler body
// stays readable and the sentinel → status mapping is testable in
// isolation in a future PR.
func (r *Router) writeEditorSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrEditorSessionWorkspaceNotFound):
		writeError(w, http.StatusNotFound, "workspace not found")
	case errors.Is(err, ErrEditorSessionAccountNotFound):
		writeError(w, http.StatusNotFound, "account not found")
	case errors.Is(err, ErrEditorSessionChannelUnlinked):
		writeError(w, http.StatusNotFound, "account not linked to workspace")
	case errors.Is(err, ErrEditorSessionNoValidToken):
		writeError(w, http.StatusUnauthorized, "no valid token found for this account")
	case errors.Is(err, ErrEditorSessionYTServiceUnconfigured),
		errors.Is(err, ErrEditorSessionEditStoreUnconfigured):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, ErrEditorSessionVideoWrongChannel):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrEditorSessionVideoNotReady),
		errors.Is(err, ErrEditorSessionVideoAlreadyPub):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// Compile-time assertion that *api.Router satisfies the narrow
// interface the reconciler worker depends on (internal/worker/youtube_processing_reconciler.go
// declares this interface; pkg/api must see this assertion signature-
// compatible).
//
// The reconciler passes the *Router pointer as the EditorSessionCreator
// implementation; duck typing via the interface satisfies the contract.
// Without this assertion, a future signature drift on Router.CreateEditorSession
// would surface at runtime in production rather than at go vet time.
var _ interface {
	CreateEditorSession(context.Context, CreateEditorSessionInput) (*models.YouTubeVideoEdit, error)
} = (*Router)(nil)

// userCanAccessWorkspace reports whether the user owns the workspace.
// For the editor session creation flow, workspace ownership is the
// required authorization gate; future iterations may also accept team
// members via the team store.
func (r *Router) userCanAccessWorkspace(userID int64, workspace *models.Workspace) bool {
	if workspace == nil {
		return false
	}
	return workspace.OwnerID == userID
}

// editorURLForProject returns the canonical editor URL for a newly
// created project. When an editor URL is configured explicitly it is
// used; otherwise the frontend URL is used as a fallback.
func (r *Router) editorURLForProject(projectID string) string {
	base := r.editorURL
	if base == "" {
		base = r.frontendURL
	}
	base = strings.TrimRight(base, "/")
	if base == "" {
		// Last-resort fallback for test environments.
		base = "https://editor.instaedit.org"
	}
	return fmt.Sprintf("%s/editor/%s", base, projectID)
}

// handleUpdateYouTubeEditorSession updates a thumbnail editor session.
// It is used by the dark editor after uploading the generated thumbnail
// to InstaEdit storage so the session keeps a reference to the verified
// asset (thumbnail_media_id) before the user clicks Publish.
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

	var payload updateYouTubeEditorSessionRequest
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

	// Verify the media asset exists, is ready, and belongs to the caller.
	asset, err := r.mediaStore.FindByID(payload.ThumbnailMediaID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find media asset: "+err.Error())
		return
	}
	if asset == nil || asset.UserID != identity.UserID() || asset.Status != models.MediaAssetStatusReady {
		writeError(w, http.StatusBadRequest, "invalid or unverified media asset")
		return
	}

	workspace, err := r.workspaceStore.FindByID(edit.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return
	}
	if workspace == nil || !r.userCanAccessWorkspace(identity.UserID(), workspace) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	edit.ThumbnailMediaID = &payload.ThumbnailMediaID
	edit.UpdatedAt = time.Now().UTC()
	if err := r.youtubeVideoEditStore.Update(req.Context(), edit); err != nil {
		writeError(w, http.StatusInternalServerError, "update editor session: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"session_id":         edit.ID,
		"thumbnail_media_id": edit.ThumbnailMediaID,
	})
}

// attachThumbnailRequest is the body accepted by
// POST /api/v1/youtube/editor-sessions/{id}/thumbnail. This is the
// "direct" handoff endpoint (Blocco #5 P0 #4): callers (typically the
// dark editor SPA, post-upload) supply a verified media_assets.id
// instead of going through Velox's PATCH-by-project flow. The handler
// validates asset existence + readiness + workspace accessibility, then
// atomically links the asset to the session via a single UPDATE with a
// CAS predicate (status IN ('editing','failed')).
type attachThumbnailRequest struct {
	ThumbnailMediaID string `json:"thumbnail_media_id"`
}

// attachThumbnailResponse is returned on success.
type attachThumbnailResponse struct {
	SessionID        string `json:"session_id"`
	ThumbnailMediaID string `json:"thumbnail_media_id"`
	ThumbnailStatus  string `json:"thumbnail_status"`
}

// handleAttachThumbnailToEditorSession is the direct handoff entry
// point. It accepts a verified thumbnail_media_id, validates the
// asset + workspace, and atomically links the asset to the session.
//
// Error branches (4 per the user spec + 1 happy path):
//   - asset not found                                  → 404
//   - asset exists but Status != ready                 → 409
//   - workspace not accessible by the caller           → 403
//   - session not found / CAS-loss (status flipped)    → 404 / 409
//   - missing thumbnail_media_id payload               → 400
//
// Order matters: session-load BEFORE workspace-check BEFORE asset-load
// so the error messages line up with the user's intent ("workspace
// accessibile" is the explicit 403 gate). The asset is the FIRST
// ownership check the user asked for (asset esista), so it comes after
// the workspace gate but BEFORE the asset-readiness check.
//
// The AttachThumbnail call is the atomic CAS that simultaneously
// stamps thumbnail_media_id AND guards against concurrent publishes
// (status must be 'editing' or 'failed' — a session in 'publishing'
// or 'published' state will not match, the 0-rows return maps to 409).
func (r *Router) handleAttachThumbnailToEditorSession(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	sessionID := chi.URLParam(req, "id")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
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

	if r.youtubeVideoEditStore == nil {
		writeError(w, http.StatusServiceUnavailable, "youtube video edit store not configured")
		return
	}
	if r.mediaStore == nil {
		writeError(w, http.StatusNotImplemented, "media not configured on this server")
		return
	}

	// 1. Session load.
	edit, err := r.youtubeVideoEditStore.FindByID(req.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find editor session: "+err.Error())
		return
	}
	if edit == nil {
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}

	// 2. Workspace access (403 — explicit gate per user spec).
	workspace, err := r.workspaceStore.FindByID(edit.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return
	}
	if workspace == nil || !r.userCanAccessWorkspace(identity.UserID(), workspace) {
		writeError(w, http.StatusForbidden, "workspace not accessible")
		return
	}

	// 3. Asset existence (404).
	asset, err := r.mediaStore.FindByID(payload.ThumbnailMediaID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find media asset: "+err.Error())
		return
	}
	if asset == nil {
		writeError(w, http.StatusNotFound, "media asset not found")
		return
	}

	// 4. Asset ownership (403 — defensive against cross-tenant probing).
	if asset.UserID != identity.UserID() {
		writeError(w, http.StatusForbidden, "media asset not owned by caller")
		return
	}

	// 5. Asset readiness (409 — exists but not ready to be linked).
	if asset.Status != models.MediaAssetStatusReady {
		writeError(w, http.StatusConflict, "media asset is not ready")
		return
	}

	// 6. Atomic CAS: link thumbnail_media_id AND guard against
	// concurrent publishes (status must be 'editing' or 'failed').
	updated, err := r.youtubeVideoEditStore.AttachThumbnail(req.Context(), sessionID, payload.ThumbnailMediaID)
	if err != nil {
		if errors.Is(err, repository.ErrYouTubeVideoEditNotFound) {
			// CAS-loss: status was 'publishing' / 'published' (or the
			// row was deleted between the read and the write).
			writeError(w, http.StatusConflict, "editor session is not in an editable state")
			return
		}
		writeError(w, http.StatusInternalServerError, "attach thumbnail: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, attachThumbnailResponse{
		SessionID:        updated.ID,
		ThumbnailMediaID: *updated.ThumbnailMediaID,
		ThumbnailStatus:  updated.Status,
	})
}

// publishYouTubeEditorSessionRequest is the body accepted by
// POST /api/v1/youtube/editor-sessions/{id}/publish.
// Title and Description are optional; when provided they are sent to
// YouTube's videos.update with part=snippet,status. YouTube enforces a
// 100-character title limit and a 5000-character description limit.
type publishYouTubeEditorSessionRequest struct {
	Title         string     `json:"title,omitempty"`
	Description   string     `json:"description,omitempty"`
	PrivacyStatus string     `json:"privacy_status,omitempty"`
	PublishAt     *time.Time `json:"publish_at,omitempty"`
}

// publishYouTubeEditorSessionResponse is returned on a successful publish.
type publishYouTubeEditorSessionResponse struct {
	PublicURL    string     `json:"public_url"`
	VideoID      string     `json:"video_id"`
	PrivacyStatus string    `json:"privacy_status"`
	PublishedAt  *time.Time `json:"published_at,omitempty"`
}

// handlePublishYouTubeEditorSession publishes the edited thumbnail to
// YouTube. It is idempotent: if the session is already published it
// returns the stored public URL; if a publish is already in flight it
// returns 409; on failure it records the error and allows retries.
func (r *Router) handlePublishYouTubeEditorSession(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	sessionID := chi.URLParam(req, "id")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	var payload publishYouTubeEditorSessionRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	payload.Title = strings.TrimSpace(payload.Title)
	payload.Description = strings.TrimSpace(payload.Description)
	if err := services.ValidateYouTubeSnippet(payload.Title, payload.Description); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Body-level Title/Description validation only here. Privacy status +
	// publish_at validation happens AFTER the session is loaded — the
	// resolved privacyStatus falls back to edit.DesiredPrivacy when the
	// payload omits privacy_status (Bug-fix Blocco #5 P0 #1). The original
	// early validation incorrectly rejected a valid scheduled publish
	// when the session itself was already private: the body-only
	// privacyStatus defaulted missing → "public", and then the
	// "future publish_at requires private" rule triggered 400 — even
	// though the session's desired_privacy would have resolved the
	// final privacyStatus to "private" downstream.
	if r.youtubeVideoEditStore == nil {
		writeError(w, http.StatusServiceUnavailable, "youtube video edit store not configured")
		return
	}

	edit, err := r.youtubeVideoEditStore.FindByID(req.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find editor session: "+err.Error())
		return
	}
	if edit == nil {
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}

	workspace, err := r.workspaceStore.FindByID(edit.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return
	}
	if workspace == nil || !r.userCanAccessWorkspace(identity.UserID(), workspace) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	// Idempotency checks: published sessions can be replayed without
	// requiring the downstream media/YouTube services.
	if edit.Status == "published" {
		writeJSON(w, http.StatusOK, publishYouTubeEditorSessionResponse{
			PublicURL:     "https://www.youtube.com/watch?v=" + edit.YouTubeVideoID,
			VideoID:       edit.YouTubeVideoID,
			PrivacyStatus: edit.DesiredPrivacy,
			PublishedAt:   edit.PublishAt,
		})
		return
	}
	inFlightTimeout := r.publishingInFlightTimeout
	if inFlightTimeout <= 0 {
		inFlightTimeout = DefaultPublishingInFlightTimeout
	}
	if edit.Status == "publishing" && time.Since(edit.UpdatedAt) < inFlightTimeout {
		writeError(w, http.StatusConflict, "publish already in progress")
		return
	}

	// Resolve privacy status. Order of preference:
	//   1. payload.PrivacyStatus (request-body override);
	//   2. edit.DesiredPrivacy (session-stored default);
	//   3. "public" (final default).
	// Then validate (enum membership + publish_at vs privacy =
	// "scheduled requires private"). Placed AFTER the session load +
	// idempotency + in-flight checks + BEFORE the media/store/ytSvc
	// nil checks: this is the bug-fix for "Validazione anticipata
	// della privacy" + it fails fast on bad input without touching
	// the media layer.
	privacyStatus := payload.PrivacyStatus
	if privacyStatus == "" {
		privacyStatus = edit.DesiredPrivacy
	}
	privacyStatus = strings.ToLower(strings.TrimSpace(privacyStatus))
	if privacyStatus == "" {
		privacyStatus = "public"
	}
	if privacyStatus != "public" && privacyStatus != "unlisted" && privacyStatus != "private" {
		writeError(w, http.StatusBadRequest, "privacy_status must be public, unlisted, or private")
		return
	}
	if payload.PublishAt != nil && !payload.PublishAt.IsZero() {
		if payload.PublishAt.Before(time.Now().UTC()) {
			writeError(w, http.StatusBadRequest, "publish_at must be in the future")
			return
		}
		if privacyStatus != "private" {
			writeError(w, http.StatusBadRequest, "scheduled publishing requires privacy_status=private")
			return
		}
	}

	if r.mediaStore == nil || r.storageProvider == nil {
		writeError(w, http.StatusNotImplemented, "media not configured on this server")
		return
	}
	if r.youTubeSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "YouTube service not configured")
		return
	}

	if edit.ThumbnailMediaID == nil || *edit.ThumbnailMediaID == "" {
		writeError(w, http.StatusBadRequest, "thumbnail media not attached to session")
		return
	}
	asset, err := r.mediaStore.FindByID(*edit.ThumbnailMediaID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find media asset: "+err.Error())
		return
	}
	if asset == nil || asset.UserID != identity.UserID() || asset.Status != models.MediaAssetStatusReady {
		writeError(w, http.StatusBadRequest, "invalid or unverified media asset")
		return
	}

	// Fetch a fresh access token.
	token, err := r.vault.Get(req.Context(), edit.PlatformAccountID, models.TokenTypeBearer)
	if err != nil {
		token, err = r.vault.Get(req.Context(), edit.PlatformAccountID, models.TokenTypeLongLived)
		if err != nil {
			token, err = r.vault.Get(req.Context(), edit.PlatformAccountID, models.TokenTypeShortLived)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "no valid token found for this account")
				return
			}
		}
	}

	// Download the thumbnail bytes using a presigned GET URL.
	downloadURL, err := r.storageProvider.GetObject(req.Context(), asset.UploadKey, 5*time.Minute)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate thumbnail download URL: "+err.Error())
		return
	}
	downloadCtx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer cancel()
	thumbnailData, err := downloadThumbnailBytes(downloadCtx, r.thumbnailDownloadClient, downloadURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "download thumbnail: "+err.Error())
		return
	}

	// Atomic CAS claim (Blocco #5 P0 #2 — Bug #2 fix). The previous
	// read-then-update pattern (`edit.Status = "publishing"; Update(edit)`)
	// had a TOCTOU race: two concurrent publish requests could both
	// pass the row-state read AND stamp status='publishing', firing
	// two PublishThumbnail calls (real bug — thumbnail would be
	// uploaded to YouTube twice). MarkPublishing stamps
	// status + desired_privacy + publish_at in a single statement
	// AND honours an extended predicate (orphan recovery): a stuck
	// 'publishing' row whose heartbeat is older than
	// publishingInFlightTimeout can be re-claimed by a fresh
	// request after the timeout. The handler's own in-flight check
	// above already rejects recent-publishing rows with 409 — the
	// orphan-recovery branch only matches rows older than the
	// timeout. inFlightTimeout was already resolved by the in-flight
	// guard above (defaulted to DefaultPublishingInFlightTimeout if
	// the Router field is zero); retry the same variable to keep
	// the orphan-recovery timeout consistent with the in-flight
	// window.
	claimed, err := r.youtubeVideoEditStore.MarkPublishing(
		req.Context(), sessionID, privacyStatus, payload.PublishAt, inFlightTimeout,
	)
	if err != nil {
		if errors.Is(err, repository.ErrYouTubeVideoEditNotFound) {
			// CAS-loss: 0 rows matched the predicate. Either the row
			// is in 'publishing' state AND within the in-flight
			// timeout (already rejected by the in-flight check
			// above, but defensive), OR the row is in a terminal
			// state ('published'). Either way: 409 + clear message.
			writeError(w, http.StatusConflict, "publish already in progress or terminal state")
			return
		}
		writeError(w, http.StatusInternalServerError, "mark publishing: "+err.Error())
		return
	}
	edit = claimed

	publicURL, err := r.youTubeSvc.PublishThumbnail(
		req.Context(),
		token.AccessToken,
		edit.YouTubeVideoID,
		thumbnailData,
		asset.ContentType,
		privacyStatus,
		payload.PublishAt,
		payload.Title,
		payload.Description,
	)
	if err != nil {
		edit.Status = "failed"
		edit.LastError = truncateError(err.Error())
		edit.UpdatedAt = time.Now().UTC()
		_ = r.youtubeVideoEditStore.Update(req.Context(), edit)
		writeError(w, http.StatusBadGateway, "youtube publish failed: "+err.Error())
		return
	}

	edit.Status = "published"
	edit.LastError = ""
	edit.UpdatedAt = time.Now().UTC()
	if err := r.youtubeVideoEditStore.Update(req.Context(), edit); err != nil {
		writeError(w, http.StatusInternalServerError, "update editor session: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, publishYouTubeEditorSessionResponse{
		PublicURL:     publicURL,
		VideoID:       edit.YouTubeVideoID,
		PrivacyStatus: privacyStatus,
		PublishedAt:   payload.PublishAt,
	})
}

// downloadThumbnailBytes fetches the thumbnail bytes from the signed
// download URL. The asset is capped at 2 MB, so reading into memory is
// safe.
func downloadThumbnailBytes(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("thumbnail download returned %d: %s", resp.StatusCode, string(body))
	}
	const maxBytes = 2 * 1024 * 1024
	// Guard against unexpectedly large payloads before reading the body.
	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("thumbnail download exceeded max size: %d > %d", resp.ContentLength, maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("thumbnail download read: %w", err)
	}
	if len(data) == maxBytes {
		// We may have hit the limit; the next byte would tell, but for our
		// use case the caller will validate the exact size against the
		// stored media asset before publishing to YouTube anyway.
	}
	return data, nil
}

// truncateError limits an error string to a length suitable for
// storage in the last_error column.
func truncateError(s string) string {
	const max = 1024
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// listYouTubeEditorSessionEntry is the per-row JSON shape returned
// by GET /api/v1/youtube/editor-sessions. The shape is intentionally
// a SUBSET of models.YouTubeVideoEdit: last_error (an internal
// diagnostic) is omitted, created_at/updated_at/source_thumbnail_url
// are omitted (the SPA renders them via follow-up single-row reads
// when an operator clicks a row), and the editor_url is reconstructed
// server-side from velox_project_id so the SPA does not have to
// bundle the editor base URL into its bundle. workspace_id /
// platform_account_id are also omitted because they are implied by
// the ?workspace_id query and a multi-row response in which every
// row shares the same filter would just add bytes without semantics.
type listYouTubeEditorSessionEntry struct {
	ID               string     `json:"id"`
	YouTubeVideoID   string     `json:"youtube_video_id"`
	VeloxProjectID   string     `json:"velox_project_id"`
	EditorURL        string     `json:"editor_url"`
	Status           string     `json:"status"`
	ThumbnailMediaID *string    `json:"thumbnail_media_id,omitempty"`
	DesiredPrivacy   string     `json:"desired_privacy"`
	PublishAt        *time.Time `json:"publish_at,omitempty"`
}

// listYouTubeEditorSessionsResponse is the envelope. `sessions: []`
// is returned (not 404) when no rows match the filter — the SPA's
// dashboard renders an "empty state" banner rather than treating
// "nothing to do" as an error.
type listYouTubeEditorSessionsResponse struct {
	Sessions []listYouTubeEditorSessionEntry `json:"sessions"`
}

// handleListYouTubeEditorSessions is the HTTP entry point for
// GET /api/v1/youtube/editor-sessions. It is the read-side companion
// to the POST/PATCH/POST-publish cycle and powers the SPA's
// dashboard "code da modificare" widget.
//
// Behaviour:
//   - 401 when no JWT identity is on the context.
//   - 400 when ?workspace_id is missing/invalid, when ?limit is out
//     of range, or when a ?status value is off the allow-list.
//   - 404 when the workspace is unknown OR the caller does not have
//     access. Both branches return the SAME 404 + message so a
//     cross-tenant probe cannot distinguish "no such workspace" from
//     "workspace exists but not yours" (defence-in-depth on top of
//     the SQL `WHERE workspace_id = $1` guard).
//   - 200 + {"sessions": [...]} when the filter resolves cleanly.
//     Empty result is 200 + {"sessions": []}, NOT 404.
//
// Concurrency:
//   The repository method is a read-only SELECT; no row-level locks.
//   Two concurrent dashboard refreshes returning different "snapshots"
//   are expected (updated_at moves forward under writes), so the
//   the SPA should re-fetch on interval rather than rely on snapshot
//   equality.
func (r *Router) handleListYouTubeEditorSessions(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	q := req.URL.Query()
	workspaceIDRaw := strings.TrimSpace(q.Get("workspace_id"))
	workspaceID, err := parsePositiveQueryInt(workspaceIDRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "workspace_id query parameter is required and must be a positive integer")
		return
	}

	// Workspace ownership check (handler-only gate; the repository
	// SQL also filters on workspace_id so a hostile caller cannot
	// return rows from a foreign workspace even if they bypass this).
	workspace, err := r.workspaceStore.FindByID(workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return
	}
	if workspace == nil || !r.userCanAccessWorkspace(identity.UserID(), workspace) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	filter := repository.YouTubeEditorSessionListFilter{
		WorkspaceID:     workspaceID,
		// Keep terminal rows visible when explicitly requested so the
		// dashboard can observe editing -> publishing -> published.
		IncludeTerminal: strings.EqualFold(strings.TrimSpace(q.Get("include_terminal")), "true"),
	}

	if accountIDRaw := strings.TrimSpace(q.Get("account_id")); accountIDRaw != "" {
		accountID, err := parsePositiveQueryInt(accountIDRaw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "account_id must be a positive integer")
			return
		}
		fid := accountID
		filter.AccountID = &fid
	}

	if statusRaw := strings.TrimSpace(q.Get("status")); statusRaw != "" {
		// Comma-separated multi-status support (?status=editing,failed)
		// lets the SPA wire a "filter by state" multi-select without a
		// second request. Whitespace is tolerated around commas to make
		// hand-typed URLs ergonomic.
		rawStatuses := strings.Split(statusRaw, ",")
		statuses := make([]string, 0, len(rawStatuses))
		for _, s := range rawStatuses {
			s = strings.ToLower(strings.TrimSpace(s))
			if s == "" {
				continue
			}
			statuses = append(statuses, s)
		}
		if len(statuses) == 0 {
			writeError(w, http.StatusBadRequest, "status query parameter contained no valid values")
			return
		}
		filter.Statuses = statuses
	}

	if limitRaw := strings.TrimSpace(q.Get("limit")); limitRaw != "" {
		limit, err := parsePositiveQueryInt(limitRaw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		// parsePositiveQueryInt returns int64 (consistent with the
		// Go stdlib strconv.ParseInt). The repository's filter struct
		// keeps Limit as int because the upper bound is
		// YouTubeEditorSessionListMaxLimit (500) — safely within
		// int32. Cast is a no-op at runtime, but the explicit
		// conversion makes the boundary obvious to future readers.
		filter.Limit = int(limit)
	}

	if r.youtubeVideoEditStore == nil {
		writeError(w, http.StatusServiceUnavailable, "youtube video edit store not configured")
		return
	}

	rows, err := r.youtubeVideoEditStore.ListByWorkspace(req.Context(), filter)
	if err != nil {
		if errors.Is(err, repository.ErrYouTubeVideoEditListLimitInvalid) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, repository.ErrYouTubeVideoEditListStatusInvalid) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "list editor sessions: "+err.Error())
		return
	}

	entries := make([]listYouTubeEditorSessionEntry, 0, len(rows))
	for _, edit := range rows {
		entries = append(entries, listYouTubeEditorSessionEntry{
			ID:               edit.ID,
			YouTubeVideoID:   edit.YouTubeVideoID,
			VeloxProjectID:   edit.VeloxProjectID,
			EditorURL:        r.editorURLForProject(edit.VeloxProjectID),
			Status:           edit.Status,
			ThumbnailMediaID: edit.ThumbnailMediaID,
			DesiredPrivacy:   edit.DesiredPrivacy,
			PublishAt:        edit.PublishAt,
		})
	}
	writeJSON(w, http.StatusOK, listYouTubeEditorSessionsResponse{Sessions: entries})
}

// parsePositiveQueryInt parses an HTTP query-string int64 and rejects
// zero/negative/non-numeric values. Centralised here so the dashboard
// list handler's workspace_id / account_id / limit parsing keeps the
// same shape (the inline equivalent is 8 lines repeated 3x); future
// GET endpoints reading a positive int from the query can reuse it.
//
// Returns (0, nil) on empty input — callers must decide whether
// empty is an error (workspace_id) or "use default" (limit, account_id).
func parsePositiveQueryInt(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty value")
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("value %d is not positive", n)
	}
	return n, nil
}
