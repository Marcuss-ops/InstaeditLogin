package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// CreateThumbnailSessionInput is the canonical input for the
// thumbnail-session auto-provisioner. Used by the internal
// POST /internal/v1/thumbnail-sessions handler that Velox calls
// after PRIVATE_UPLOADED (state machine §10 in
// docs/velox-instaedit-contract.md).
//
// Distinct from CreateEditorSessionInput (in youtube_editor_sessions.go)
// because the auto-provisioner:
//   - SKIPS the YouTube videos.list round-trip (Velox has already
//     uploaded the video as private; we trust the caller);
//   - accepts a video_title hint that we stamp onto DraftTitle so
//     the InstaEditor SPA pre-fills the title input;
//   - accepts a final_privacy hint that we stamp onto DesiredPrivacy
//     (default: "public");
//   - uses a ytedit_<uuid> id format (manually-created sessions use
//     bare uuid.NewString()).
type CreateThumbnailSessionInput struct {
	WorkspaceID       int64
	PlatformAccountID int64
	YouTubeVideoID    string
	VideoTitle        string
	FinalPrivacy      string
}

// createThumbnailSessionRequest is the body accepted by
// POST /internal/v1/thumbnail-sessions.
type createThumbnailSessionRequest struct {
	WorkspaceID       int64  `json:"workspace_id"`
	PlatformAccountID int64  `json:"platform_account_id"`
	YouTubeVideoID    string `json:"youtube_video_id"`
	VideoTitle        string `json:"video_title,omitempty"`
	VideoStatus       string `json:"video_status,omitempty"`
	FinalPrivacy      string `json:"final_privacy,omitempty"`
	DeliveryID        string `json:"delivery_id,omitempty"`
}

// createThumbnailSessionResponse is the body returned on success.
//
// EditorSessionID has the ytedit_<uuid> format (auto-provisioned
// sessions only — the manual POST /api/v1/youtube/editor-sessions
// endpoint still returns bare uuid format).
//
// ThumbnailStatus reflects the initial state (pending) so the
// InstaEditor SPA can immediately distinguish "thumbnail needs to be
// applied" from "thumbnail already applied" on its first GET.
type createThumbnailSessionResponse struct {
	EditorSessionID   string `json:"editor_session_id"`
	YouTubeVideoID    string `json:"youtube_video_id"`
	VeloxProjectID    string `json:"velox_project_id"`
	WorkspaceID       int64  `json:"workspace_id"`
	PlatformAccountID int64  `json:"platform_account_id"`
	Status            string `json:"status"`
	ThumbnailStatus   string `json:"thumbnail_status"`
	FinalPrivacy      string `json:"final_privacy"`
	EditorURL         string `json:"editor_url,omitempty"`
	Duplicate         bool   `json:"duplicate,omitempty"`
}

// CreateThumbnailSessionForDelivery is the canonical helper called
// by the POST /internal/v1/thumbnail-sessions handler. It runs the
// SAME 4-step pipeline that the manual POST goes through EXCEPT for
// the YouTube videos.list validation (skipped: Velox has just
// uploaded the video as private and we trust the caller's
// youtube_video_id).
//
// Steps:
//  1. Validate workspace + platform_account consistency (the
//     account must be platform=YouTube).
//  2. Mint ytedit_<uuid> for the session id + ve_<uuid> for the
//     velox project id (these are HINTS — if a row already exists
//     for the (workspace, account, video) triple, FindOrCreateEditableSession
//     returns the existing row with its existing ids untouched).
//  3. Call FindOrCreateEditableSession to find or create the row.
//  4. On a fresh INSERT, stamp DraftTitle (from VideoTitle) +
//     DesiredPrivacy (from FinalPrivacy) so the InstaEditor SPA
//     sees the operator-supplied title + final-privacy hint on its
//     first read.
//
// On replay (same triple routed twice), step 3's FindOrCreateEditableSession
// SELECT fast-path returns the existing row; the helper echoes it
// back with Duplicate=true so the handler can return 200 instead
// of 201.
//
// The helper performs ZERO YouTube API calls (vs Router.CreateEditorSession
// which does one videos.list round-trip). The Velox caller has
// already uploaded the video; a re-validation would burn quota + add
// latency for zero information gain.
func (m *VeloxModule) CreateThumbnailSessionForDelivery(ctx context.Context, in CreateThumbnailSessionInput) (*models.YouTubeVideoEdit, bool, error) {
	if in.WorkspaceID <= 0 {
		return nil, false, ErrEditorSessionWorkspaceNotFound
	}
	if in.PlatformAccountID <= 0 {
		return nil, false, ErrEditorSessionAccountNotFound
	}
	if in.YouTubeVideoID == "" {
		return nil, false, errors.New("youtube_video_id is required")
	}
	if m.deps.WorkspaceStore == nil {
		return nil, false, ErrEditorSessionWorkspaceNotFound
	}
	workspace, err := m.deps.WorkspaceStore.FindByID(in.WorkspaceID)
	if err != nil || workspace == nil {
		return nil, false, ErrEditorSessionWorkspaceNotFound
	}
	if m.deps.UserStore == nil {
		return nil, false, ErrEditorSessionAccountNotFound
	}
	account, err := m.deps.UserStore.FindPlatformAccountByID(in.PlatformAccountID)
	if err != nil || account == nil || account.Platform != models.PlatformYouTube {
		return nil, false, ErrEditorSessionAccountNotFound
	}
	// Defense-in-depth: validate the (workspace, account) binding
	// exists, matching the manual-creator Router.CreateEditorSession
	// invariant. The publish pipeline would BLOCK_TARGET the row later
	// if the binding is missing — catching it here turns a late
	// failure into an early 4xx so Velox doesn't carry a session id
	// it'll never be able to publish.
	//
	// Two failure modes both surface ErrEditorSessionChannelUnlinked:
	//   - channel == nil: binding absent (the common case for a
	//     fresh Velox handoff where the operator never wired the
	//     channel into the workspace).
	//   - err != nil: real DB / store error.
	// Both map to HTTP 404 via the handler's error mapping — same
	// as the manual creator's behavior.
	channel, err := m.deps.WorkspaceStore.FindChannel(ctx, in.WorkspaceID, in.PlatformAccountID)
	if err != nil || channel == nil {
		return nil, false, ErrEditorSessionChannelUnlinked
	}
	if m.deps.YouTubeVideoEditStore == nil {
		return nil, false, ErrEditorSessionEditStoreUnconfigured
	}

	// Step 2 — mint the hints.
	sessionIDHint := "ytedit_" + uuid.NewString()
	projectIDHint := "ve_" + uuid.NewString()
	persisted, err := m.deps.YouTubeVideoEditStore.FindOrCreateEditableSession(
		ctx,
		in.WorkspaceID,
		in.PlatformAccountID,
		in.YouTubeVideoID,
		sessionIDHint,
		projectIDHint,
	)
	if err != nil {
		return nil, false, fmt.Errorf("find or create thumbnail session: %w", err)
	}

	// Detect a fresh INSERT vs an existing-row hit. A fresh INSERT
	// uses the ytedit_<uuid> hint we minted; an existing-row hit
	// keeps its original id (could be a manually-created bare uuid
	// OR a previous ytedit_<uuid> from a prior replay).
	//
	// MIXED-FORMAT REPLAY: when the existing row was created via
	// POST /api/v1/youtube/editor-sessions (manual creator, bare
	// uuid format), the response echoes the bare-uuid id with
	// duplicate=true. Spec §7's example uses ytedit_<uuid>; the
	// Thumbnail Maker SPA keys UI off the id but does NOT regex-
	// match the prefix, so the format inconsistency is observable
	// but not breaking. Documented here + pinned by the
	// TestHandleCreateThumbnailSession_ReplayMixedFormat test.
	duplicate := persisted.ID != sessionIDHint

	// Step 4 — on a fresh INSERT, stamp DraftTitle + DesiredPrivacy
	// from the operator hints. On the REPLAY path, leave the
	// row's existing values untouched (the first call's hints win).
	if !duplicate {
		title := strings.TrimSpace(in.VideoTitle)
		// Truncate to YouTube's 100-char title limit so the
		// InstaEditor SPA doesn't crash on a 5000-char title
		// hint during the next save-draft cycle.
		const maxTitle = 100
		if len(title) > maxTitle {
			title = title[:maxTitle]
		}
		if title != "" {
			persisted.DraftTitle = &title
		}
		privacy := strings.ToLower(strings.TrimSpace(in.FinalPrivacy))
		switch privacy {
		case "public", "unlisted", "private":
			persisted.DesiredPrivacy = privacy
		default:
			persisted.DesiredPrivacy = "public"
		}
		persisted.UpdatedAt = time.Now().UTC()
		if updateErr := m.deps.YouTubeVideoEditStore.Update(ctx, persisted); updateErr != nil {
			return nil, false, fmt.Errorf("stamp thumbnail session hints: %w", updateErr)
		}
	}

	return persisted, duplicate, nil
}

// handleCreateThumbnailSession is the HTTP entry point of
// POST /internal/v1/thumbnail-sessions. Mounted on VeloxModule
// behind internalVeloxAuthMiddleware; the caller is Velox
// (service-to-service, not a logged-in user).
//
// Wire shape:
//   - Body: createThumbnailSessionRequest JSON.
//   - Headers: Authorization: Bearer <VELOX_API_TOKEN> (enforced
//     by the middleware). Idempotency-Key is OPTIONAL but
//     RECOMMENDED for replay safety — the underlying
//     FindOrCreateEditableSession is already idempotent on the
//     (workspace, account, video) triple, so the Idempotency-Key
//     is defence-in-depth + log correlation only.
//   - Response: 201 Created on fresh INSERT (Duplicate=false),
//     200 OK on REPLAY (Duplicate=true). Both paths return the
//     same body shape.
//
// Error mapping:
//   - 400: missing fields, malformed JSON, invalid final_privacy.
//   - 401/403/503: emitted by internalVeloxAuthMiddleware.
//   - 404: workspace or platform_account not found.
//   - 500: real *sql.DB error from the repository layer.
func (m *VeloxModule) handleCreateThumbnailSession(w http.ResponseWriter, req *http.Request) {
	if m.deps.YouTubeVideoEditStore == nil {
		writeError(w, http.StatusServiceUnavailable, "youtube video edit store not configured")
		return
	}
	var payload createThumbnailSessionRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if payload.WorkspaceID <= 0 {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	if payload.PlatformAccountID <= 0 {
		writeError(w, http.StatusBadRequest, "platform_account_id is required")
		return
	}
	if payload.YouTubeVideoID == "" {
		writeError(w, http.StatusBadRequest, "youtube_video_id is required")
		return
	}
	if payload.VideoStatus != "" &&
		!strings.EqualFold(payload.VideoStatus, "private") &&
		!strings.EqualFold(payload.VideoStatus, "public") &&
		!strings.EqualFold(payload.VideoStatus, "unlisted") {
		writeError(w, http.StatusBadRequest, "video_status must be private|public|unlisted when provided")
		return
	}
	if payload.FinalPrivacy != "" {
		lower := strings.ToLower(strings.TrimSpace(payload.FinalPrivacy))
		if lower != "public" && lower != "unlisted" && lower != "private" {
			writeError(w, http.StatusBadRequest, "final_privacy must be public|unlisted|private")
			return
		}
		payload.FinalPrivacy = lower
	}

	edit, duplicate, err := m.CreateThumbnailSessionForDelivery(req.Context(), CreateThumbnailSessionInput{
		WorkspaceID:       payload.WorkspaceID,
		PlatformAccountID: payload.PlatformAccountID,
		YouTubeVideoID:    payload.YouTubeVideoID,
		VideoTitle:        payload.VideoTitle,
		FinalPrivacy:      payload.FinalPrivacy,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrEditorSessionWorkspaceNotFound):
			writeError(w, http.StatusNotFound, "workspace not found")
			return
		case errors.Is(err, ErrEditorSessionAccountNotFound):
			writeError(w, http.StatusNotFound, "youtube account not found")
			return
		case errors.Is(err, ErrEditorSessionChannelUnlinked):
			// Defense-in-depth: the (workspace, account) binding is
			// missing OR the binding lookup errored. Both surface as
			// the canonical "channel not bound" 404, matching the
			// manual-creator (Router.CreateEditorSession) error
			// contract so the two paths return identical wire shapes.
			writeError(w, http.StatusNotFound, "account not linked to workspace")
			return
		case errors.Is(err, ErrEditorSessionEditStoreUnconfigured):
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		default:
			slog.Error("velox create thumbnail session: helper failed",
				"workspace_id", payload.WorkspaceID,
				"platform_account_id", payload.PlatformAccountID,
				"youtube_video_id", payload.YouTubeVideoID,
				"delivery_id", payload.DeliveryID,
				"err", err)
			writeError(w, http.StatusInternalServerError, "create thumbnail session failed: "+err.Error())
			return
		}
	}

	status := http.StatusCreated
	if duplicate {
		status = http.StatusOK
	}
	// X-Editor-Session-Format discriminator: lets the Thumbnail Maker
	// SPA branch on the format without regex-parsing the id. Values:
	//   - "ytedit" → auto-provisioned (this handler)
	//   - "uuid"   → manually-created (echoed back on mixed-format
	//                replay, see MIXED-FORMAT REPLAY in
	//                CreateThumbnailSessionForDelivery docstring)
	w.Header().Set("X-Editor-Session-Format", editorSessionFormat(edit.ID))
	writeJSON(w, status, createThumbnailSessionResponse{
		EditorSessionID:   edit.ID,
		YouTubeVideoID:    edit.YouTubeVideoID,
		VeloxProjectID:    edit.VeloxProjectID,
		WorkspaceID:       edit.WorkspaceID,
		PlatformAccountID: edit.PlatformAccountID,
		Status:            edit.Status,
		ThumbnailStatus:   "pending",
		FinalPrivacy:      edit.DesiredPrivacy,
		EditorURL:         m.editorURLForVeloxProject(edit.VeloxProjectID),
		Duplicate:         duplicate,
	})
}

// editorURLForVeloxProject is the VeloxModule-local helper that
// constructs the canonical editor URL from a velox_project_id.
// Mirrors Router.editorURLForProject (in youtube_editor_sessions.go)
// but lives on the VeloxModule so the internal handler doesn't
// need a *Router receiver for a single helper call.
//
// The VeloxModuleDeps struct carries an optional EditorBaseURL
// (production wiring passes cfg.EditorBaseURL). When empty we
// fall back to the canonical production hostname
// "https://editor.instaedit.org" — same default as
// Router.editorURLForProject so the two paths return URLs of the
// same shape.
func (m *VeloxModule) editorURLForVeloxProject(projectID string) string {
	base := m.deps.EditorBaseURL
	if base == "" {
		base = "https://editor.instaedit.org"
	}
	return strings.TrimRight(base, "/") + "/editor/" + projectID
}

// editorSessionFormat discriminates between auto-provisioned
// (ytedit_<uuid>) and manually-created (bare uuid) editor
// session ids. The Thumbnail Maker SPA uses the
// X-Editor-Session-Format response header (set by
// handleCreateThumbnailSession) to branch its UI without
// regex-parsing the id.
//
// Centralised here (not in the handler body) so future
// refactors that add a third id format (e.g. ve_<uuid> for
// future InstaEditor sessions) only touch one function.
func editorSessionFormat(id string) string {
	if strings.HasPrefix(id, "ytedit_") {
		return "ytedit"
	}
	return "uuid"
}
