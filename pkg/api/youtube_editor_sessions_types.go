package api

import (
	"errors"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// Sentinel errors for CreateEditorSession. The HTTP handler maps them
// to status codes via errors.Is below; the reconciler worker reads
// them for retry vs skip decisions.
var (
	ErrEditorSessionWorkspaceNotFound     = errors.New("workspace not found")
	ErrEditorSessionAccountNotFound       = errors.New("youtube account not found")
	ErrEditorSessionChannelUnlinked       = errors.New("account not linked to workspace")
	ErrEditorSessionNoValidToken          = errors.New("no valid token found for this account")
	ErrEditorSessionVideoWrongChannel     = errors.New("video does not belong to selected channel")
	ErrEditorSessionVideoNotReady         = errors.New("video is not ready for thumbnail editing")
	ErrEditorSessionVideoAlreadyPub       = errors.New("video is already public; thumbnail editing allowed only for private or unlisted videos")
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
//
// UserID attributes the provider project mapping (the editor project
// bridge, Action 6 "Modifica" flow) to the authenticated operator that
// initiated the request. The user-facing handler sets it from the JWT;
// background callers (processing reconciler, thumbnail batches, the
// Velox service-to-service handoff) leave it 0 and the helper skips
// bridge creation — the bridge is then minted lazily on the first
// operator open of that session (the REUSE path is idempotent).
type CreateEditorSessionInput struct {
	WorkspaceID        int64
	PlatformAccountID  int64
	YouTubeVideoID     string
	SourceThumbnailURL string
	UserID             int64
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
//
// Besides the session identity, it carries the authoritative video
// projection fetched from YouTube during creation (videos.list) — the
// initial document InstaEditor needs: video id, title, description,
// thumbnail URL (the editing canvas), category id, privacy status and
// the source marker. The editor session contract deliberately carries
// these server-derived fields instead of trusting client-supplied
// values: title/description/thumbnail/category/privacy come from the
// channel's own videos.list response.
type createYouTubeEditorSessionResponse struct {
	SessionID      string `json:"session_id"`
	VeloxProjectID string `json:"velox_project_id"`
	EditorURL      string `json:"editor_url"`
	// Video projection handed to InstaEditor as its initial document.
	YouTubeVideoID string `json:"youtube_video_id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	ThumbnailURL   string `json:"thumbnail_url,omitempty"`
	CategoryID     string `json:"category_id,omitempty"`
	PrivacyStatus  string `json:"privacy_status"`
	Source         string `json:"source"` // always "youtube"
}

// createYouTubeVideoSessionProjection maps the authoritative videos.list
// projection (fetched by CreateEditorSession during validation) onto the
// session response so InstaEditor receives its initial document without
// any client-supplied values. The video is never nil on the success path
// (the helper validated it), but the mapping stays defensive.
func createYouTubeVideoSessionProjection(video *models.YouTubeVideoDetails) createYouTubeEditorSessionResponse {
	resp := createYouTubeEditorSessionResponse{Source: "youtube"}
	if video == nil {
		return resp
	}
	resp.YouTubeVideoID = video.ID
	resp.Title = video.Title
	resp.Description = video.Description
	resp.ThumbnailURL = video.ThumbnailURL
	resp.CategoryID = video.CategoryID
	resp.PrivacyStatus = video.Privacy
	return resp
}
