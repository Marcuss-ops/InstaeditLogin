package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// publishYouTubeEditorSessionRequest is the body accepted by
// POST /api/v1/youtube/editor-sessions/{id}/publish.
//
// Title and Description are optional; when provided they are sent to
// YouTube's videos.update with part=snippet,status. YouTube enforces a
// 100-character title limit and a 5000-character description limit.
//
// Tags + DefaultLanguage + DefaultAudioLanguage + Translations are
// the P1#1 "metadati completi" extensions: when provided, the
// orchestrator updates the YouTube snippet (single combined call
// alongside title/description/privacy) and then iterates over
// Translations to upsert each per-language localization.
//
//   - Tags: max 30 items, total character count (incl. commas) must
//     not exceed 500. Enforced by YouTubePublishOptions.Validate().
//   - DefaultLanguage / DefaultAudioLanguage: BCP-47 codes (e.g. "en",
//     "it", "pt-BR"); a light sanity check is run before the API call
//     to fail fast on typos.
//   - Translations: map[lang]YouTubeTranslation; when non-empty,
//     DefaultLanguage MUST be set (YouTube rejects otherwise and
//     burns a 1600-quota call).
//
// All fields are omitempty so existing callers that only send
// {title, description, privacy_status, publish_at} keep working
// unchanged — the orchestrator path is identical for both shapes.
type publishYouTubeEditorSessionRequest struct {
	Title                string                               `json:"title,omitempty"`
	Description          string                               `json:"description,omitempty"`
	PrivacyStatus        string                               `json:"privacy_status,omitempty"`
	PublishAt            *time.Time                           `json:"publish_at,omitempty"`
	Tags                 []string                             `json:"tags,omitempty"`
	DefaultLanguage      string                               `json:"default_language,omitempty"`
	DefaultAudioLanguage string                               `json:"default_audio_language,omitempty"`
	Translations         map[string]models.YouTubeTranslation `json:"translations,omitempty"`
}

// publishYouTubeEditorSessionResponse is returned on a successful publish.
type publishYouTubeEditorSessionResponse struct {
	Status            string     `json:"status"`
	PublicURL         string     `json:"public_url"`
	VideoID           string     `json:"video_id"`
	PrivacyStatus     string     `json:"privacy_status"`
	ActualPrivacy     string     `json:"actual_privacy,omitempty"`
	YouTubeSyncStatus string     `json:"youtube_sync_status,omitempty"`
	PublishedAt       *time.Time `json:"published_at,omitempty"`
}

// youTubePublishOptionsForRequest folds a publishYouTubeEditorSessionRequest
// into a models.YouTubePublishOptions struct for the service layer.
// The orchestrator passes Title + Description into the same single
// videos.update(part=snippet,status) call that carries the new tags +
// languages, so the request DTO and the service struct must keep the
// same shape (per the thinker's quota-conservation recommendation:
// a single combined call instead of two).
func youTubePublishOptionsForRequest(payload publishYouTubeEditorSessionRequest) models.YouTubePublishOptions {
	return models.YouTubePublishOptions{
		Title:                payload.Title,
		Description:          payload.Description,
		Tags:                 payload.Tags,
		DefaultLanguage:      payload.DefaultLanguage,
		DefaultAudioLanguage: payload.DefaultAudioLanguage,
		Translations:         payload.Translations,
	}
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

	// Delegate to the shared publish core. The session-id-keyed
	// (this handler) and velox-project-id-keyed
	// (handlePublishYouTubeEditorSessionByProject) endpoints both
	// resolve + authorise the session in their own wrapper and hand
	// off the resolved edit + payload to the helper. The helper
	// owns every step from idempotency / in-flight through the
	// YouTube API call + response write so the two paths cannot
	// drift. (Bug-fix Blocco #5 P0 #2 — MarkPublishing CAS
	// orphans-recovery stays inside the helper.)
	r.executePublishYouTubeEditorSession(req.Context(), w, identity, edit, payload)
}

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
	// Read one byte beyond the limit so a streaming response without a
	// Content-Length header cannot be silently truncated to exactly 2 MiB
	// and then forwarded as if it were a complete thumbnail.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("thumbnail download read: %w", err)
	}
	if len(data) > maxBytes {
		return nil, fmt.Errorf("thumbnail download exceeded max size: %d > %d", len(data), maxBytes)
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
