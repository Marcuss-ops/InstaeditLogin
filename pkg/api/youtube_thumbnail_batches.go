package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

const (
	youtubeThumbnailBatchMaxItems = 200
)

type youtubeThumbnailBatchItemRequest struct {
	PlatformAccountID int64    `json:"platform_account_id"`
	YouTubeVideoID    string   `json:"youtube_video_id"`
	VariantID         string   `json:"variant_id"`
	ThumbnailMediaID  string   `json:"thumbnail_media_id"`
	Title             string   `json:"title,omitempty"`
	Description       string   `json:"description,omitempty"`
	Tags              []string `json:"tags,omitempty"`
}

type youtubeThumbnailBatchRequest struct {
	GroupID        int64                              `json:"group_id"`
	IdempotencyKey string                             `json:"idempotency_key,omitempty"`
	Items          []youtubeThumbnailBatchItemRequest `json:"items"`
}

type youtubeThumbnailBatchResponse struct {
	BatchID   string `json:"batch_id"`
	Status    string `json:"status"`
	Total     int    `json:"total"`
	Completed int    `json:"completed,omitempty"`
	Failed    int    `json:"failed,omitempty"`
}

type youtubeThumbnailBatchStatusResponse struct {
	BatchID   string                             `json:"batch_id"`
	Status    string                             `json:"status"`
	Total     int                                `json:"total"`
	Completed int                                `json:"completed"`
	Failed    int                                `json:"failed"`
	Items     []models.YouTubeThumbnailBatchItem `json:"items"`
	LastError string                             `json:"last_error,omitempty"`
}

func (r *Router) handleCreateYouTubeThumbnailBatch(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	if r.youtubeThumbnailBatchStore == nil {
		writeError(w, http.StatusServiceUnavailable, "YouTube thumbnail batch store not configured")
		return
	}
	if r.groupStore == nil || r.workspaceStore == nil {
		writeError(w, http.StatusServiceUnavailable, "group stores not configured")
		return
	}

	var payload youtubeThumbnailBatchRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 2<<20))
	if err := decoder.Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid batch payload")
		return
	}
	if payload.GroupID <= 0 {
		writeError(w, http.StatusBadRequest, "group_id is required")
		return
	}
	if len(payload.Items) == 0 || len(payload.Items) > youtubeThumbnailBatchMaxItems {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("items must contain between 1 and %d videos", youtubeThumbnailBatchMaxItems))
		return
	}
	key := strings.TrimSpace(req.Header.Get("Idempotency-Key"))
	if key == "" {
		key = strings.TrimSpace(payload.IdempotencyKey)
	}
	if key == "" {
		writeError(w, http.StatusBadRequest, "Idempotency-Key is required")
		return
	}
	if len(key) > 255 {
		writeError(w, http.StatusBadRequest, "Idempotency-Key is too long")
		return
	}

	group, err := r.groupStore.FindByID(payload.GroupID)
	if err != nil || group == nil {
		writeError(w, http.StatusNotFound, "group not found")
		return
	}
	workspace, err := r.workspaceStore.FindByID(group.WorkspaceID)
	if err != nil || workspace == nil || !r.userCanEditWorkspace(identity.UserID(), workspace) {
		writeError(w, http.StatusNotFound, "group not found")
		return
	}
	accountIDs, err := r.groupStore.ListAccountsInGroup(group.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "resolve group accounts: "+err.Error())
		return
	}
	allowedAccounts := make(map[int64]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		allowedAccounts[accountID] = struct{}{}
	}

	items := make([]models.YouTubeThumbnailBatchItem, 0, len(payload.Items))
	seen := make(map[string]struct{}, len(payload.Items))
	for _, input := range payload.Items {
		input.YouTubeVideoID = strings.TrimSpace(input.YouTubeVideoID)
		input.VariantID = strings.TrimSpace(input.VariantID)
		input.ThumbnailMediaID = strings.TrimSpace(input.ThumbnailMediaID)
		if input.PlatformAccountID <= 0 || input.YouTubeVideoID == "" || input.VariantID == "" || input.ThumbnailMediaID == "" {
			writeError(w, http.StatusBadRequest, "each item requires platform_account_id, youtube_video_id, variant_id and thumbnail_media_id")
			return
		}
		if _, ok := allowedAccounts[input.PlatformAccountID]; !ok {
			writeError(w, http.StatusForbidden, "one or more videos target a channel outside the selected group")
			return
		}
		itemKey := fmt.Sprintf("%d:%s:%s", input.PlatformAccountID, input.YouTubeVideoID, input.VariantID)
		if _, ok := seen[itemKey]; ok {
			writeError(w, http.StatusBadRequest, "duplicate video variant in batch")
			return
		}
		seen[itemKey] = struct{}{}
		items = append(items, models.YouTubeThumbnailBatchItem{
			PlatformAccountID: input.PlatformAccountID,
			YouTubeVideoID:    input.YouTubeVideoID,
			VariantID:         input.VariantID,
			ThumbnailMediaID:  input.ThumbnailMediaID,
			Title:             strings.TrimSpace(input.Title),
			Description:       input.Description,
			Tags:              input.Tags,
		})
	}

	hashPayload := struct {
		GroupID int64                              `json:"group_id"`
		Items   []youtubeThumbnailBatchItemRequest `json:"items"`
	}{GroupID: payload.GroupID, Items: payload.Items}
	hashJSON, err := json.Marshal(hashPayload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hash batch payload: "+err.Error())
		return
	}
	hash := sha256.Sum256(hashJSON)
	existing, err := r.youtubeThumbnailBatchStore.FindByKey(req.Context(), workspace.ID, key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find existing batch: "+err.Error())
		return
	}
	if existing != nil {
		if !bytes.Equal(existing.RequestHash, hash[:]) {
			writeError(w, http.StatusConflict, "Idempotency-Key was already used with a different batch")
			return
		}
		r.startYouTubeThumbnailBatch(existing.ID, identity)
		writeJSON(w, http.StatusOK, youtubeThumbnailBatchResponse{BatchID: existing.ID, Status: existing.Status, Total: existing.Total, Completed: existing.Completed, Failed: existing.Failed})
		return
	}

	batch := &models.YouTubeThumbnailBatch{
		ID:             "batch_" + uuid.NewString(),
		WorkspaceID:    workspace.ID,
		GroupID:        group.ID,
		IdempotencyKey: key,
		RequestHash:    hash[:],
	}
	if err := r.youtubeThumbnailBatchStore.Create(req.Context(), batch, items); err != nil {
		if errors.Is(err, repository.ErrYouTubeThumbnailBatchKeyCollision) {
			winner, findErr := r.youtubeThumbnailBatchStore.FindByKey(req.Context(), workspace.ID, key)
			if findErr == nil && winner != nil && bytes.Equal(winner.RequestHash, hash[:]) {
				r.startYouTubeThumbnailBatch(winner.ID, identity)
				writeJSON(w, http.StatusOK, youtubeThumbnailBatchResponse{BatchID: winner.ID, Status: winner.Status, Total: winner.Total, Completed: winner.Completed, Failed: winner.Failed})
				return
			}
			writeError(w, http.StatusConflict, "Idempotency-Key was already used with a different batch")
			return
		}
		writeError(w, http.StatusInternalServerError, "create thumbnail batch: "+err.Error())
		return
	}
	r.startYouTubeThumbnailBatch(batch.ID, identity)
	writeJSON(w, http.StatusAccepted, youtubeThumbnailBatchResponse{BatchID: batch.ID, Status: "queued", Total: len(items)})
}

func (r *Router) handleGetYouTubeThumbnailBatch(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	if r.youtubeThumbnailBatchStore == nil {
		writeError(w, http.StatusServiceUnavailable, "YouTube thumbnail batch store not configured")
		return
	}
	id := strings.TrimSpace(chi.URLParam(req, "batch_id"))
	batch, err := r.youtubeThumbnailBatchStore.FindByID(req.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find thumbnail batch: "+err.Error())
		return
	}
	if batch == nil {
		writeError(w, http.StatusNotFound, "thumbnail batch not found")
		return
	}
	workspace, err := r.workspaceStore.FindByID(batch.WorkspaceID)
	if err != nil || workspace == nil || !r.userCanAccessWorkspace(identity.UserID(), workspace) {
		writeError(w, http.StatusNotFound, "thumbnail batch not found")
		return
	}
	if batch.Status == "queued" || batch.Status == "processing" {
		r.startYouTubeThumbnailBatch(batch.ID, identity)
	}
	items, err := r.youtubeThumbnailBatchStore.ListItems(req.Context(), batch.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list thumbnail batch items: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, youtubeThumbnailBatchStatusResponse{BatchID: batch.ID, Status: batch.Status, Total: batch.Total, Completed: batch.Completed, Failed: batch.Failed, Items: items, LastError: batch.LastError})
}

func (r *Router) startYouTubeThumbnailBatch(batchID string, identity auth.Identity) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		r.processYouTubeThumbnailBatch(ctx, batchID, identity)
	}()
}

func (r *Router) processYouTubeThumbnailBatch(ctx context.Context, batchID string, identity auth.Identity) {
	if r.youtubeThumbnailBatchStore == nil {
		return
	}
	claimed, err := r.youtubeThumbnailBatchStore.ClaimBatch(ctx, batchID, time.Now().UTC().Add(-10*time.Minute))
	if err != nil || !claimed {
		return
	}
	batch, err := r.youtubeThumbnailBatchStore.FindByID(ctx, batchID)
	if err != nil || batch == nil {
		return
	}
	items, err := r.youtubeThumbnailBatchStore.ListItems(ctx, batchID)
	if err != nil {
		return
	}
	for i := range items {
		item := &items[i]
		if item.Status == "completed" || item.Status == "failed" {
			continue
		}
		itemClaimed, claimErr := r.youtubeThumbnailBatchStore.ClaimItem(ctx, item.ID, time.Now().UTC().Add(-10*time.Minute))
		if claimErr != nil || !itemClaimed {
			continue
		}
		edit, _, processErr := r.CreateEditorSession(ctx, CreateEditorSessionInput{
			WorkspaceID:       batch.WorkspaceID,
			PlatformAccountID: item.PlatformAccountID,
			YouTubeVideoID:    item.YouTubeVideoID,
		})
		if processErr == nil {
			processErr = r.ensurePrivateYouTubeBatchVideo(ctx, edit)
		}
		if processErr == nil {
			_, processErr = r.attachThumbnailToSession(ctx, identity, edit, item.ThumbnailMediaID)
		}
		if processErr == nil {
			payload := publishYouTubeEditorSessionRequest{Title: item.Title, Description: item.Description, Tags: item.Tags, PrivacyStatus: "private"}
			// Production capture writer: background batch orchestration
			// reuses the same publish core as the HTTP endpoints but must
			// not depend on net/http/httptest (a test package) in shipped
			// code. Behavior is identical to httptest.NewRecorder for the
			// writeJSON/writeError surface this core uses.
			recorder := &batchPublishCaptureWriter{}
			r.executePublishYouTubeEditorSession(ctx, recorder, identity, edit, payload)
			if recorder.Status() < http.StatusOK || recorder.Status() >= http.StatusMultipleChoices {
				processErr = fmt.Errorf("publish returned HTTP %d: %s", recorder.Status(), batchErrorMessage(recorder.BodyBytes()))
			} else {
				var result publishYouTubeEditorSessionResponse
				if decodeErr := json.Unmarshal(recorder.BodyBytes(), &result); decodeErr != nil {
					processErr = fmt.Errorf("decode publish response: %w", decodeErr)
				} else {
					item.EditorSessionID = edit.ID
					item.PublicURL = result.PublicURL
				}
			}
		}
		if processErr != nil {
			item.Status = "failed"
			item.LastError = truncateError(processErr.Error())
		} else {
			item.Status = "completed"
			item.LastError = ""
		}
		if updateErr := r.youtubeThumbnailBatchStore.UpdateItem(ctx, item); updateErr != nil {
			// Swallowing this write silently re-published items: after
			// the claim lease expires (10 min) another cycle re-claims
			// the item and re-runs a REAL YouTube publish. The failure
			// must at minimum be observable.
			slog.Error("youtube thumbnail batch: item status writeback failed; item will be re-claimed after lease expiry and re-published",
				"batch_id", batchID, "item_id", item.ID, "item_status", item.Status, "err", updateErr)
		}
		if _, err := r.youtubeThumbnailBatchStore.Recompute(ctx, batchID); err != nil {
			slog.Warn("youtube thumbnail batch: recompute failed",
				"batch_id", batchID, "err", err)
		}
	}
	if _, err := r.youtubeThumbnailBatchStore.Recompute(ctx, batchID); err != nil {
		slog.Warn("youtube thumbnail batch: final recompute failed",
			"batch_id", batchID, "err", err)
	}
}

// batchPublishCaptureWriter is a minimal production http.ResponseWriter
// that captures the status code and body bytes written by the shared
// publish core so background batch processing can inspect the outcome
// without a real HTTP connection. It replaces the previous misuse of
// httptest.NewRecorder (a net/http/httptest test type) in production
// code; the observable behavior for writeJSON/writeError is identical.
type batchPublishCaptureWriter struct {
	header http.Header
	body   bytes.Buffer
	code   int
}

func (b *batchPublishCaptureWriter) Header() http.Header {
	if b.header == nil {
		b.header = make(http.Header)
	}
	return b.header
}

func (b *batchPublishCaptureWriter) WriteHeader(code int) {
	if b.code == 0 {
		b.code = code
	}
}

func (b *batchPublishCaptureWriter) Write(p []byte) (int, error) {
	if b.code == 0 {
		b.code = http.StatusOK
	}
	return b.body.Write(p)
}

// Status returns the captured status code, defaulting to 200 when the
// core never wrote a header — matching httptest.NewRecorder's default.
func (b *batchPublishCaptureWriter) Status() int {
	if b.code == 0 {
		return http.StatusOK
	}
	return b.code
}

func (b *batchPublishCaptureWriter) BodyBytes() []byte {
	return b.body.Bytes()
}

func (r *Router) ensurePrivateYouTubeBatchVideo(ctx context.Context, edit *models.YouTubeVideoEdit) error {
	if edit == nil || r.vault == nil || r.youTubeSvc == nil || r.userRepo == nil {
		return errors.New("YouTube validation is not configured")
	}
	// Resolve the EXPECTED channel. With shared Google grants
	// (migrations 084/085) the token alone proves nothing about WHICH
	// channel the payload targets, so the batch must verify the video
	// belongs to the exact channel selected in the payload — never
	// just to a sibling that happens to share the same grant.
	account, err := r.userRepo.FindPlatformAccountByID(edit.PlatformAccountID)
	if err != nil || account == nil || account.Platform != models.PlatformYouTube {
		return errors.New("batch channel not found")
	}
	// 1. Renew first (P0): an expired access token is refreshed
	// automatically from the stored grant. Legacy rows are eligible for
	// compatibility only when the canonical modern grant is explicitly
	// absent; hard OAuth failures must never be hidden by stale-token reads.
	token, err := r.vault.Renew(ctx, edit.PlatformAccountID, models.TokenTypeBearer, r.youTubeSvc.RefreshOAuthToken)
	if errors.Is(err, credentials.ErrModernGrantMissing) {
		token, err = r.vault.Get(ctx, edit.PlatformAccountID, models.TokenTypeLongLived)
		if errors.Is(err, credentials.ErrModernGrantMissing) {
			token, err = r.vault.Get(ctx, edit.PlatformAccountID, models.TokenTypeShortLived)
		}
	}
	if err != nil {
		return errors.New("no valid token found for this account")
	}
	// 2. The grant must be bound to the expected channel
	// (channels.list mine=true). Access to several sibling channels is
	// not enough — the operator targeted THIS channel.
	if err := r.youTubeSvc.ValidateChannelBinding(ctx, token.AccessToken, account.PlatformUserID); err != nil {
		return fmt.Errorf("validate YouTube channel binding: %w", err)
	}
	// 3. Fetch the video under the same token (also proves the grant
	// can see it at all). The error string lands on the operator
	// dashboard via truncateError, so name the actual step.
	video, err := r.youTubeSvc.GetYouTubeVideo(ctx, token.AccessToken, edit.YouTubeVideoID)
	if err != nil {
		return fmt.Errorf("fetch batch video: %w", err)
	}
	// 4. The video MUST belong to the selected channel — not merely to
	// a sibling sharing the grant. This is the P0 cross-channel guard.
	if video == nil || video.ChannelID != account.PlatformUserID {
		return errors.New("batch video does not belong to the selected channel")
	}
	// 5. Only private videos may be modified by the batch.
	if strings.ToLower(strings.TrimSpace(video.Privacy)) != "private" {
		return errors.New("batch thumbnail updates are allowed only for private videos")
	}
	return nil
}

func batchErrorMessage(body []byte) string {
	var payload map[string]any
	if json.Unmarshal(body, &payload) == nil {
		if message, ok := payload["error"].(string); ok && message != "" {
			return message
		}
		if message, ok := payload["message"].(string); ok && message != "" {
			return message
		}
	}
	return strings.TrimSpace(string(body))
}

var _ YouTubeThumbnailBatchStore = (*repository.YouTubeThumbnailBatchRepository)(nil)
