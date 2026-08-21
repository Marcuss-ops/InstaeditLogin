package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// youTubeEditorSessionDraftRequest is the body accepted by
// PUT /api/v1/youtube/editor-sessions/by-project/{velox_project_id}/draft.
//
// The contract is intentionally permissive (P2 architecture verdict:
// "relax strict validations for drafts"). A draft by definition can be
// incomplete: the operator may have typed a title but not set a default
// language yet, or be testing the boundary limits of a description.
// Bounds-validation lives at the publish boundary (where
// YouTubePublishOptions.Validate() runs). This handler:
//
//   - accepts any non-malformed JSON body in the shape below;
//   - persists the values verbatim into the draft_* columns;
//   - returns 200 + the echoed draft + draft_updated_at so the SPA can
//     render "Bozza salvata hh:mm" without re-fetching the full DTO.
//
// All fields are optional. An empty payload produces an empty draft
// (operator intentionally clearing a previously-saved draft).
type youTubeEditorSessionDraftRequest struct {
	Title                string                               `json:"title"`
	Description          string                               `json:"description"`
	Tags                 []string                             `json:"tags"`
	DefaultLanguage      string                               `json:"default_language"`
	DefaultAudioLanguage string                               `json:"default_audio_language"`
	Translations         map[string]models.YouTubeTranslation `json:"translations"`
	DesiredPrivacy       string                               `json:"desired_privacy"`
	PublishAt            *time.Time                           `json:"publish_at,omitempty"`
}

// youTubeEditorSessionDraftResponse is returned on a 200. It echoes
// back the same shape + draft_updated_at so the SPA's "Bozza salvata hh:mm"
// indicator can render without a follow-up GET round-trip:
//
//   - draft_updated_at: the timestamp the server stamped, server-authoritative
//     (in case the operator's local clock is off — the server is the truth).
//   - the rest of the fields are the just-persisted values (verbatim from
//     the request, after server-side trim/normalize). The SPA does NOT
//     render the response values into the form (the form already shows
//     those values); the response is informational so tests can assert
//     "what the server saw == what the SPA sent".
type youTubeEditorSessionDraftResponse struct {
	VeloxProjectID            string                               `json:"velox_project_id"`
	DraftTitle                string                               `json:"draft_title"`
	DraftDescription          string                               `json:"draft_description"`
	DraftTags                 []string                             `json:"draft_tags"`
	DraftDefaultLanguage      string                               `json:"draft_default_language"`
	DraftDefaultAudioLanguage string                               `json:"draft_default_audio_language"`
	DraftTranslations         map[string]models.YouTubeTranslation `json:"draft_translations"`
	DraftDesiredPrivacy       string                               `json:"draft_desired_privacy"`
	DraftPublishAt            *time.Time                           `json:"draft_publish_at,omitempty"`
	DraftUpdatedAt            time.Time                            `json:"draft_updated_at"`
}

// handleSaveEditorSessionDraftByProject is the HTTP entry point for
// PUT /api/v1/youtube/editor-sessions/by-project/{velox_project_id}/draft.
//
// Behaviour:
//
//   - 401 when no JWT identity is on the context.
//   - 400 when {velox_project_id} is empty OR JSON is malformed.
//   - 404 when the session is unknown OR the caller does not have
//     access to its workspace. Both branches return the SAME 404 +
//     message so a cross-tenant probe cannot distinguish "no such
//     session" from "session exists but not yours" (defence-in-depth
//     on top of the SQL `WHERE velox_project_id = $1` guard).
//   - 503 when the youtube video edit store is not configured.
//   - 409 when the session is in 'publishing' or 'published' state —
//     the CAS predicate on SaveDraft rejected the row. The SPA treats
//     this as "publish already ran or is running" (no retry from this
//     dialog).
//   - 200 + the draft echo (with draft_updated_at) on success.
//
// Why CAS instead of an unconditional UPDATE:
//
//	The publish orchestrator owns the row during the 'publishing'
//	window. A plain UPDATE here would let an operator's typo overwrite
//	the privacy/title the orchestrator just pushed to YouTube — a
//	subtle data-loss bug. CAS on status IN ('editing', 'failed') keeps
//	the operator's draft writes out of the publish critical section
//	entirely. The handler maps 0-row CAS-match to 409 so the SPA can
//	surface "publish already in progress" instead of silently dropping
//	the operator's keystroke.
//
// Why relaxed validation here, strict at /publish:
//
//	YouTubePublishOptions.Validate() runs at the publish boundary (NOT
//	here). The draft endpoint accepts incomplete payloads because the
//	operator is mid-typing; e.g. a temporarily over-length title while
//	the user is shortening it should NOT bounce a 400 mid-keystroke.
//	Strict validation at /publish preserves the YouTube-published
//	bounds without making the auto-save indicator annoying to use.
//
// Idempotency:
//
//	A duplicate PUT (same draft content + same second) is idempotent.
//	The handler does NOT increment draft_updated_at per read-modify
//	cycle, it always sets it to NOW() so the indicator advances. The
//	dirty_flag column flips true on the form-change side and false on
//	the PUT 200 side (mirrors the dashboard "unsaved changes" pill).
func (r *Router) handleSaveEditorSessionDraftByProject(w http.ResponseWriter, req *http.Request) {
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

	if r.youtubeVideoEditStore == nil {
		writeError(w, http.StatusServiceUnavailable, "youtube video edit store not configured")
		return
	}

	// Pre-flight ownership check (handler-only gate; the repository's
	// SaveDraft CAS predicate guards against status='publishing'/'published').
	edit, err := r.youtubeVideoEditStore.FindByVeloxProjectID(req.Context(), veloxProjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find editor session: "+err.Error())
		return
	}
	if edit == nil {
		// Standalone thumbnail projects use the same editor surface but do
		// not have a youtube_video_edits row. Their opaque Velox id is
		// resolved through the persisted bridge and title/description are
		// saved on the thumbnail project itself.
		if r.saveStandaloneEditorDraft(w, req, identity.UserID(), veloxProjectID) {
			return
		}
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}
	workspace, err := r.workspaceStore.FindByID(edit.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return
	}
	if workspace == nil || !r.userCanEditWorkspace(identity.UserID(), workspace) {
		// Same 404-as-foreign pattern as the other endpoints: the caller
		// cannot tell "not found" from "not yours".
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}

	// Read the body once into bytes so we can both decode the typed
	// payload and detect which keys the caller actually supplied
	// (partial-update semantics below). Empty body is a valid no-op.
	//
	// 5 MB ceiling on a draft payload is plenty for the max-100-title
	// + max-5000-description + translations combination; protects
	// against hostile-large bodies.
	var bodyBytes []byte
	if req.Body != nil {
		var readErr error
		bodyBytes, readErr = io.ReadAll(io.LimitReader(req.Body, 5<<20))
		if readErr != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+readErr.Error())
			return
		}
	}

	// Decode the draft payload. Empty body is a valid no-op (per
	// code-reviewer verdict, an empty body must NOT 400).
	var payload youTubeEditorSessionDraftRequest
	if len(bodyBytes) > 0 {
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}

	// Key-presence map: a field is written ONLY when its key is present
	// in the JSON body — `publish_at: null` is present (explicit clear),
	// an omitted publish_at is not (keep the current value).
	present := map[string]bool{}
	if len(bodyBytes) > 0 {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(bodyBytes, &raw); err == nil {
			for key := range raw {
				present[key] = true
			}
		}
	}

	// MERGE: partial-update semantics — absent fields keep the
	// session's current draft value. This lets the InstaEditor sync
	// just the renamed project title (PUT {"title": "..."}) without
	// wiping the operator's description/tags/privacy. Every existing
	// caller (SPA quick-create, SPA metadata save, editor form
	// auto-save) sends the complete shape, so their behaviour is
	// unchanged; an empty body `{}` is now a no-op instead of a full
	// draft clear.
	current, err := r.youtubeVideoEditStore.FindDraftByVeloxProjectID(req.Context(), veloxProjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find editor session draft: "+err.Error())
		return
	}
	if current == nil {
		current = &models.YouTubeVideoEdit{}
	}

	title := payload.Title
	if !present["title"] {
		title = draftStringValue(current.DraftTitle)
	}
	description := payload.Description
	if !present["description"] {
		description = draftStringValue(current.DraftDescription)
	}
	tags := payload.Tags
	if !present["tags"] {
		tags = current.DraftTags
	}
	defaultLanguage := payload.DefaultLanguage
	if !present["default_language"] {
		defaultLanguage = draftStringValue(current.DraftDefaultLanguage)
	}
	defaultAudioLanguage := payload.DefaultAudioLanguage
	if !present["default_audio_language"] {
		defaultAudioLanguage = draftStringValue(current.DraftDefaultAudioLanguage)
	}
	translations := payload.Translations
	if !present["translations"] {
		translations = current.DraftTranslations
	}
	desiredPrivacy := payload.DesiredPrivacy
	if !present["desired_privacy"] {
		desiredPrivacy = draftStringValue(current.DraftDesiredPrivacy)
	}
	publishAt := payload.PublishAt
	if !present["publish_at"] {
		// Merge from the draft's OWN scheduling column
		// (draft_publish_at). The session row's publish_at is the
		// publish-click stamp — using it here would wipe a scheduled
		// draft time on a rename-only sync.
		publishAt = current.DraftPublishAt
	}

	// Server-side trim/normalize for display values. We do NOT enforce
	// length bounds here — the publish endpoint does that.
	title = strings.TrimSpace(title)
	description = strings.TrimSpace(description)
	defaultLanguage = strings.TrimSpace(defaultLanguage)
	defaultAudioLanguage = strings.TrimSpace(defaultAudioLanguage)
	desiredPrivacy = strings.ToLower(strings.TrimSpace(desiredPrivacy))

	// Persist via the store's SaveDraft CAS. Returns 0-rows mapped to
	// 409 by the repository contract (handler maps via errors.Is).
	draftUpdatedAt := time.Now().UTC()
	if saveErr := r.youtubeVideoEditStore.SaveDraft(
		req.Context(),
		edit.ID,
		title,
		description,
		tags,
		defaultLanguage,
		defaultAudioLanguage,
		translations,
		desiredPrivacy,
		publishAt,
		draftUpdatedAt,
	); saveErr != nil {
		if errors.Is(saveErr, repository.ErrYouTubeVideoEditNotFound) {
			// CAS-loss: the row is in 'publishing' or 'published' state.
			// The SPA surfaces this as "publish already in progress or
			// complete — your draft was NOT saved (the publish owns the
			// row right now)". No retry from this dialog.
			writeError(w, http.StatusConflict, "publish already in progress or terminal")
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save draft: %v", saveErr))
		return
	}

	// Echo the draft back so the SPA indicator can render without a
	// follow-up GET. We do NOT project into the full YouTubeVideoEdit
	// DTO — the response is a narrow draft-shaped echo so the SPA
	// independent logic stays simple.
	tagsCopy := tags
	if tagsCopy == nil {
		tagsCopy = []string{}
	}
	translationsCopy := translations
	if translationsCopy == nil {
		translationsCopy = map[string]models.YouTubeTranslation{}
	}
	writeJSON(w, http.StatusOK, youTubeEditorSessionDraftResponse{
		VeloxProjectID:            edit.VeloxProjectID,
		DraftTitle:                title,
		DraftDescription:          description,
		DraftTags:                 tagsCopy,
		DraftDefaultLanguage:      defaultLanguage,
		DraftDefaultAudioLanguage: defaultAudioLanguage,
		DraftTranslations:         translationsCopy,
		DraftDesiredPrivacy:       desiredPrivacy,
		DraftPublishAt:            publishAt,
		DraftUpdatedAt:            draftUpdatedAt,
	})
}

// saveStandaloneEditorDraft handles the metadata autosave for a standalone
// thumbnail project bridged into the editor. It returns true when the request
// was resolved as a standalone project (including errors), and false when no
// bridge exists so the caller can preserve the normal 404 behaviour.
func (r *Router) saveStandaloneEditorDraft(w http.ResponseWriter, req *http.Request, userID int64, veloxProjectID string) bool {
	if r.thumbnailProjectStore == nil {
		return false
	}
	bridgeStore, ok := r.thumbnailProjectStore.(thumbnailExternalProjectBridgeStore)
	if !ok {
		return false
	}
	bridge, err := bridgeStore.FindVeloxProjectBridgeByExternalProjectID(req.Context(), auth.IdentityFromContext(req.Context()).WorkspaceID(), veloxProjectID)
	if err != nil || bridge == nil {
		return false
	}
	project, err := r.thumbnailProjectStore.FindByID(req.Context(), bridge.WorkspaceID, bridge.ProjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find thumbnail project: "+err.Error())
		return true
	}
	workspace, workspaceErr := r.workspaceStore.FindByID(bridge.WorkspaceID)
	if workspaceErr != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+workspaceErr.Error())
		return true
	}
	if project == nil || project.WorkspaceID != bridge.WorkspaceID || workspace == nil || !r.userCanEditWorkspace(userID, workspace) {
		writeError(w, http.StatusNotFound, "editor session not found")
		return true
	}

	var payload youTubeEditorSessionDraftRequest
	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, err = io.ReadAll(io.LimitReader(req.Body, 5<<20))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return true
		}
	}
	if len(bodyBytes) > 0 {
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return true
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(bodyBytes, &raw); err == nil {
			if _, present := raw["title"]; present {
				project.Name = strings.TrimSpace(payload.Title)
			}
			if _, present := raw["description"]; present {
				project.Description = strings.TrimSpace(payload.Description)
			}
		}
	}

	draftUpdatedAt := time.Now().UTC()
	if err := r.thumbnailProjectStore.UpdateCAS(req.Context(), project, project.Version); err != nil {
		if errors.Is(err, repository.ErrThumbnailProjectNotFound) {
			writeError(w, http.StatusConflict, "thumbnail project changed; reload and retry")
		} else {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("save thumbnail draft: %v", err))
		}
		return true
	}
	writeJSON(w, http.StatusOK, youTubeEditorSessionDraftResponse{
		VeloxProjectID:    veloxProjectID,
		DraftTitle:        project.Name,
		DraftDescription:  project.Description,
		DraftTags:         []string{},
		DraftTranslations: map[string]models.YouTubeTranslation{},
		DraftUpdatedAt:    draftUpdatedAt,
	})
	return true
}

// draftStringValue dereferences an optional draft column value,
// collapsing SQL NULL ("no draft yet") to the empty string for the
// merge fallback. An absent field falls back to the current stored
// value; a present field is written verbatim, so an explicit empty
// string still means "operator cleared this field".
func draftStringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
