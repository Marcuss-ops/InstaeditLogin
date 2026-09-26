package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// AgentVideoPublisher turns a successful remote render into an InstaEdit
// Media Library asset and a scheduled post. The post is the calendar event;
// the existing publish worker owns the actual platform upload and statuses.
type AgentVideoPublisher interface {
	Authorize(context.Context, auth.Identity, int64) error
	Validate(context.Context, auth.Identity, int64, json.RawMessage) error
	Reserve(context.Context, auth.Identity, int64, string, repository.AgentRunStep) (json.RawMessage, error)
	Publish(context.Context, auth.Identity, int64, string, repository.AgentRunStep, json.RawMessage) (json.RawMessage, error)
}

type agentVideoCalendarProjection interface {
	UpdateAgentVideoProgress(context.Context, int64, int64, string, string, string, *int, []byte) error
}

type agentVideoCalendarFinalizer interface {
	FinalizeAgentVideoEvent(*models.Post, []*models.PostTarget) error
}

func workerCalendarEventStoreFrom(posts PostStore) WorkerCalendarEventStore {
	store, _ := posts.(WorkerCalendarEventStore)
	return store
}

type agentVideoAssetStore interface {
	MediaStore
	FindByUploadKey(context.Context, int64, string) (*models.MediaAsset, error)
}

type artifactDownloader interface {
	DownloadArtifact(context.Context, string) (*http.Response, error)
}

type generatedVideoStorage interface {
	StorageProvider
	Upload(context.Context, io.Reader, string, string, int64) (int64, error)
}

type agentVideoPublisher struct {
	assets        agentVideoAssetStore
	storage       generatedVideoStorage
	posts         PostStore
	workspaces    WorkspaceStore
	teams         TeamStore
	idempotency   IdempotencyStore
	maxBytes      int64
	retentionDays int
	horizonDays   int
}

func newAgentVideoPublisher(assets MediaStore, storage StorageProvider, posts PostStore, workspaces WorkspaceStore, teams TeamStore, idempotency IdempotencyStore, maxBytes int64, horizonDays int) AgentVideoPublisher {
	assetStore, ok := assets.(agentVideoAssetStore)
	videoStorage, storageOK := storage.(generatedVideoStorage)
	if !ok || !storageOK || posts == nil || workspaces == nil || idempotency == nil {
		return nil
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxUploadBytes
	}
	if horizonDays <= 0 {
		horizonDays = 30
	}
	return &agentVideoPublisher{assets: assetStore, storage: videoStorage, posts: posts, workspaces: workspaces, teams: teams, idempotency: idempotency, maxBytes: maxBytes, retentionDays: 7, horizonDays: horizonDays}
}

func (p *agentVideoPublisher) UpdateProgress(ctx context.Context, workspaceID int64, runID string, step repository.AgentRunStep, status string, progress *int, snapshot []byte) error {
	if p.idempotency == nil {
		return errors.New("calendar event idempotency store is unavailable")
	}
	rec, err := p.idempotency.FindActiveByKey(workspaceID, "agent-video-event-"+step.ID, time.Now())
	if err != nil {
		return fmt.Errorf("find calendar event progress target: %w", err)
	}
	if rec == nil || rec.ResourceType != "post" {
		return errors.New("calendar event for video generation is missing")
	}
	store, ok := p.posts.(agentVideoCalendarProjection)
	if !ok {
		return errors.New("post store does not support generated video progress")
	}
	return store.UpdateAgentVideoProgress(ctx, workspaceID, rec.ResourceID, runID, step.ID, status, progress, snapshot)
}

type videoPublicationRequest struct {
	CalendarPostID int64                `json:"calendar_post_id,omitempty"`
	Title          string               `json:"title"`
	Caption        string               `json:"caption"`
	Language       string               `json:"language"`
	ScheduledAt    string               `json:"scheduled_at"`
	Privacy        string               `json:"privacy"`
	Targets        []videoPublishTarget `json:"targets"`
}

func validateVideoGeneration(raw json.RawMessage) error {
	var request struct {
		Topic           string   `json:"topic"`
		Language        string   `json:"language"`
		DurationSeconds int      `json:"duration_seconds"`
		MediaSources    []string `json:"media_sources"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &request) != nil || strings.TrimSpace(request.Topic) == "" || strings.TrimSpace(request.Language) == "" || request.DurationSeconds <= 0 || len(request.MediaSources) == 0 {
		return errors.New("generation requires topic, language, positive duration_seconds, and media_sources")
	}
	allowed := map[string]bool{"youtube": true, "stock": true, "artlist": true}
	seen := make(map[string]bool, len(request.MediaSources))
	for _, source := range request.MediaSources {
		source = strings.TrimSpace(source)
		if !allowed[source] || seen[source] {
			return fmt.Errorf("generation media_sources contains unsupported or duplicate source %q", source)
		}
		seen[source] = true
	}
	return nil
}

func (p *agentVideoPublisher) Publish(ctx context.Context, identity auth.Identity, workspaceID int64, runID string, step repository.AgentRunStep, remote json.RawMessage) (json.RawMessage, error) {
	if identity == nil || identity.UserID() <= 0 || identity.WorkspaceID() != workspaceID {
		return nil, errors.New("agent video identity does not own the run workspace")
	}
	var saved struct {
		IdempotencyKey string          `json:"idempotency_key"`
		Payload        json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(step.InputJSON, &saved); err != nil {
		return nil, fmt.Errorf("decode persisted video request: %w", err)
	}
	var plan createVideoPayload
	if err := json.Unmarshal(saved.Payload, &plan); err != nil {
		return nil, fmt.Errorf("decode persisted video plan: %w", err)
	}
	var publish videoPublicationRequest
	if err := json.Unmarshal(plan.Publish, &publish); err != nil {
		return nil, fmt.Errorf("decode publish plan: %w", err)
	}
	scheduledAt, err := time.Parse(time.RFC3339, publish.ScheduledAt)
	if err != nil {
		return nil, errors.New("publish.scheduled_at must be RFC3339")
	}
	workspace, err := p.workspaces.FindByID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("find publish workspace: %w", err)
	}
	if !workspaceRoleAllowed(identity.UserID(), workspace, p.teams, workspaceRoleEditor) {
		return nil, errors.New("agent identity is not permitted to publish in this workspace")
	}
	channels, err := p.workspaces.ListChannels(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list workspace channels: %w", err)
	}
	allowed := make(map[int64]bool, len(channels))
	for _, channel := range channels {
		allowed[channel.PlatformAccountID] = channel.Enabled
	}
	seen := map[int64]bool{}
	for _, target := range publish.Targets {
		if !allowed[target.PlatformAccountID] || seen[target.PlatformAccountID] {
			return nil, fmt.Errorf("publish target %d is missing, disabled, or duplicated in the workspace", target.PlatformAccountID)
		}
		seen[target.PlatformAccountID] = true
	}
	privacy := strings.TrimSpace(publish.Privacy)
	if privacy == "" {
		privacy = "unlisted"
	}
	if privacy != "public" && privacy != "unlisted" && privacy != "private" {
		return nil, errors.New("publish.privacy must be public, unlisted, or private")
	}
	postKey := "agent-video-" + step.ID
	requestHash := sha256.Sum256(step.InputJSON)
	if p.idempotency != nil {
		rec, lookupErr := p.idempotency.FindActiveByKey(workspaceID, postKey, time.Now())
		if lookupErr != nil {
			return nil, fmt.Errorf("find generated post replay: %w", lookupErr)
		}
		if rec != nil {
			if rec.ResourceType != "post" || !equalBytes(rec.RequestHash, requestHash[:]) {
				return nil, repository.ErrIdempotencyConflict
			}
			post, findErr := p.posts.FindByID(rec.ResourceID)
			if findErr != nil {
				return nil, fmt.Errorf("read generated post replay: %w", findErr)
			}
			if post == nil || post.WorkspaceID != workspaceID {
				return nil, errors.New("generated post replay is missing or belongs to another workspace")
			}
			return json.Marshal(map[string]any{"media_asset_id": post.MediaAssetID, "post": post, "post_id": post.ID, "scheduled_at": post.PublishAt})
		}
	}
	artifactURL, expectedSize, err := artifactReference(remote)
	if err != nil {
		return nil, err
	}
	return p.importAndSchedule(ctx, identity, workspaceID, runID, step, saved.IdempotencyKey, requestHash[:], scheduledAt, privacy, publish, artifactURL, expectedSize)
}

func (p *agentVideoPublisher) Validate(ctx context.Context, identity auth.Identity, workspaceID int64, payload json.RawMessage) error {
	if identity == nil || identity.UserID() <= 0 || identity.WorkspaceID() != workspaceID {
		return errors.New("agent video identity does not own the run workspace")
	}
	var plan createVideoPayload
	if err := json.Unmarshal(payload, &plan); err != nil || len(plan.Publish) == 0 {
		return errors.New("video payload must include generation and publish details")
	}
	if err := validateVideoGeneration(plan.Generation); err != nil {
		return err
	}
	var publish videoPublicationRequest
	if err := json.Unmarshal(plan.Publish, &publish); err != nil {
		return fmt.Errorf("decode publish plan: %w", err)
	}
	if strings.TrimSpace(publish.Title) == "" || len(publish.Targets) == 0 {
		return errors.New("publish requires title and at least one target")
	}
	scheduledAt, err := time.Parse(time.RFC3339, publish.ScheduledAt)
	if err != nil || !scheduledAt.After(time.Now().Add(5*time.Second)) {
		return errors.New("publish.scheduled_at must be a future RFC3339 timestamp")
	}
	if scheduledAt.After(time.Now().Add(time.Duration(p.horizonDays) * 24 * time.Hour)) {
		return fmt.Errorf("publish.scheduled_at exceeds the %d day scheduling horizon", p.horizonDays)
	}
	privacy := strings.TrimSpace(publish.Privacy)
	if privacy != "" && privacy != "public" && privacy != "unlisted" && privacy != "private" {
		return errors.New("publish.privacy must be public, unlisted, or private")
	}
	if err := p.Authorize(ctx, identity, workspaceID); err != nil {
		return err
	}
	channels, err := p.workspaces.ListChannels(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("list workspace channels: %w", err)
	}
	allowed := make(map[int64]bool, len(channels))
	for _, channel := range channels {
		allowed[channel.PlatformAccountID] = channel.Enabled
	}
	seen := map[int64]bool{}
	for _, target := range publish.Targets {
		if target.PlatformAccountID <= 0 || !allowed[target.PlatformAccountID] || seen[target.PlatformAccountID] {
			return fmt.Errorf("publish target %d is missing, disabled, or duplicated in the workspace", target.PlatformAccountID)
		}
		seen[target.PlatformAccountID] = true
	}
	return nil
}

func (p *agentVideoPublisher) Authorize(_ context.Context, identity auth.Identity, workspaceID int64) error {
	if identity == nil || identity.UserID() <= 0 || identity.WorkspaceID() != workspaceID {
		return errors.New("agent video identity does not own the run workspace")
	}
	workspace, err := p.workspaces.FindByID(workspaceID)
	if err != nil {
		return fmt.Errorf("find publish workspace: %w", err)
	}
	if !workspaceRoleAllowed(identity.UserID(), workspace, p.teams, workspaceRoleEditor) {
		return errors.New("agent identity is not permitted to publish in this workspace")
	}
	return nil
}

// Reserve creates the scheduled calendar event before remote work starts.
// It intentionally has no targets, so the publication outbox cannot dispatch
// until the final artifact has been imported and attached.
func (p *agentVideoPublisher) Reserve(ctx context.Context, identity auth.Identity, workspaceID int64, runID string, step repository.AgentRunStep) (json.RawMessage, error) {
	if identity == nil || identity.UserID() <= 0 || identity.WorkspaceID() != workspaceID {
		return nil, errors.New("agent video identity does not own the run workspace")
	}
	var saved struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(step.InputJSON, &saved); err != nil {
		return nil, fmt.Errorf("decode persisted video request: %w", err)
	}
	var plan createVideoPayload
	if err := json.Unmarshal(saved.Payload, &plan); err != nil {
		return nil, fmt.Errorf("decode persisted video plan: %w", err)
	}
	var publish videoPublicationRequest
	if err := json.Unmarshal(plan.Publish, &publish); err != nil {
		return nil, fmt.Errorf("decode publish plan: %w", err)
	}
	scheduledAt, err := time.Parse(time.RFC3339, publish.ScheduledAt)
	if err != nil {
		return nil, errors.New("publish.scheduled_at must be RFC3339")
	}
	workspace, err := p.workspaces.FindByID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("find calendar workspace: %w", err)
	}
	if !workspaceRoleAllowed(identity.UserID(), workspace, p.teams, workspaceRoleEditor) {
		return nil, errors.New("agent identity is not permitted to schedule in this workspace")
	}
	requestHash := sha256.Sum256(step.InputJSON)
	eventKey := "agent-video-event-" + step.ID
	if p.idempotency != nil {
		rec, findErr := p.idempotency.FindActiveByKey(workspaceID, eventKey, time.Now())
		if findErr != nil {
			return nil, fmt.Errorf("find calendar event replay: %w", findErr)
		}
		if rec != nil {
			if rec.ResourceType != "post" || !equalBytes(rec.RequestHash, requestHash[:]) {
				return nil, repository.ErrIdempotencyConflict
			}
			post, postErr := p.posts.FindByID(rec.ResourceID)
			if postErr != nil {
				return nil, fmt.Errorf("read calendar event replay: %w", postErr)
			}
			if post == nil || post.WorkspaceID != workspaceID {
				return nil, errors.New("calendar event replay is missing or belongs to another workspace")
			}
			return json.Marshal(map[string]any{"post_id": post.ID, "post": post})
		}
	}
	if publish.CalendarPostID > 0 {
		intentStore := agentVideoIntentStoreFrom(p.posts)
		if intentStore == nil {
			return nil, errors.New("post store does not support scheduled video intents")
		}
		if err := intentStore.LinkAgentVideoIntent(ctx, workspaceID, publish.CalendarPostID, runID, step.ID); err != nil {
			return nil, fmt.Errorf("link scheduled calendar event: %w", err)
		}
		post, err := p.posts.FindByID(publish.CalendarPostID)
		if err != nil || post == nil || post.WorkspaceID != workspaceID {
			return nil, errors.New("scheduled calendar event was not found in workspace")
		}
		if err := p.idempotency.Insert(&models.IdempotencyRecord{WorkspaceID: workspaceID, IdempotencyKey: eventKey, ResourceType: "post", ResourceID: post.ID, RequestHash: requestHash[:], ResponseStatus: http.StatusOK, ExpiresAt: time.Now().Add(365 * 24 * time.Hour)}); err != nil {
			return nil, fmt.Errorf("persist scheduled event reference: %w", err)
		}
		return json.Marshal(map[string]any{"post_id": post.ID, "post": post})
	}
	privacy := strings.TrimSpace(publish.Privacy)
	if privacy == "" {
		privacy = "unlisted"
	}
	metadata, _ := json.Marshal(map[string]any{
		"agent_run_id": runID, "agent_step_id": step.ID,
		"generation_status": "QUEUED", "generation_progress": 0,
		"generation_phase": "QUEUED", "generation_snapshot": map[string]any{"phase": "QUEUED"},
	})
	event := &models.Post{
		WorkspaceID: workspaceID, Title: strings.TrimSpace(publish.Title), Caption: publish.Caption,
		PrivacyLevel: privacy, DefaultPrivacyLevel: privacy, PublishAt: &scheduledAt,
		Status: models.PostStatusDraft, Metadata: metadata,
	}
	if err := p.posts.Create(event, nil); err != nil {
		return nil, fmt.Errorf("create video calendar event: %w", err)
	}
	if p.idempotency != nil {
		if err := p.idempotency.Insert(&models.IdempotencyRecord{WorkspaceID: workspaceID, IdempotencyKey: eventKey, ResourceType: "post", ResourceID: event.ID, RequestHash: requestHash[:], ResponseStatus: http.StatusCreated, ExpiresAt: time.Now().Add(365 * 24 * time.Hour)}); err != nil {
			return nil, fmt.Errorf("persist calendar event idempotency: %w", err)
		}
	}
	return json.Marshal(map[string]any{"post_id": event.ID, "post": event})
}

// The remote downloader is passed in request context by AgentRunsModule so
// the publisher remains independent of the Job Master implementation.
type artifactDownloaderContextKey struct{}

func withArtifactDownloader(ctx context.Context, downloader artifactDownloader) context.Context {
	return context.WithValue(ctx, artifactDownloaderContextKey{}, downloader)
}

func remoteDownloaderFromContext(ctx context.Context) (artifactDownloader, bool) {
	d, ok := ctx.Value(artifactDownloaderContextKey{}).(artifactDownloader)
	return d, ok
}

func (p *agentVideoPublisher) importAndSchedule(ctx context.Context, identity auth.Identity, workspaceID int64, runID string, step repository.AgentRunStep, workflowKey string, requestHash []byte, scheduledAt time.Time, privacy string, publish videoPublicationRequest, artifactURL string, expectedSize int64) (json.RawMessage, error) {
	downloader, ok := remoteDownloaderFromContext(ctx)
	if !ok {
		return nil, errors.New("job master artifact download is unavailable")
	}
	filename := "generated-" + step.ID + ".mp4"
	key := fmt.Sprintf("uploads/%d/agent-generated/%s/%s.mp4", identity.UserID(), runID, step.ID)
	asset, err := p.assets.FindByUploadKey(ctx, workspaceID, key)
	if err != nil {
		return nil, fmt.Errorf("find imported video asset: %w", err)
	}
	if asset == nil || asset.Status != models.MediaAssetStatusReady {
		resp, err := downloader.DownloadArtifact(ctx, artifactURL)
		if err != nil {
			return nil, fmt.Errorf("download generated video artifact: %w", err)
		}
		defer resp.Body.Close()
		if expectedSize > 0 && resp.ContentLength >= 0 && resp.ContentLength != expectedSize {
			return nil, fmt.Errorf("artifact size mismatch: master=%d response=%d", expectedSize, resp.ContentLength)
		}
		tmp, err := os.CreateTemp("", "instaedit-agent-video-*.mp4")
		if err != nil {
			return nil, fmt.Errorf("create artifact staging file: %w", err)
		}
		tmpName := tmp.Name()
		defer os.Remove(tmpName)
		hasher := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(resp.Body, p.maxBytes+1))
		if copyErr == nil && written > p.maxBytes {
			copyErr = fmt.Errorf("generated video exceeds %d byte limit", p.maxBytes)
		}
		if copyErr == nil && expectedSize > 0 && written != expectedSize {
			copyErr = fmt.Errorf("artifact size mismatch: master=%d downloaded=%d", expectedSize, written)
		}
		if copyErr == nil {
			var header [12]byte
			if _, seekErr := tmp.Seek(0, io.SeekStart); seekErr != nil {
				copyErr = seekErr
			} else if _, readErr := io.ReadFull(tmp, header[:]); readErr != nil || string(header[4:8]) != "ftyp" {
				copyErr = errors.New("generated artifact is not an MP4 video")
			}
		}
		if copyErr != nil {
			_ = tmp.Close()
			return nil, fmt.Errorf("verify generated video artifact: %w", copyErr)
		}
		bucket := storageBucket(p.storage)
		asset = &models.MediaAsset{UserID: identity.UserID(), UploadKey: key, Bucket: bucket, ContentType: "video/mp4", SizeBytes: written, Status: models.MediaAssetStatusPending, ExpiresAt: scheduledAt.Add(time.Duration(p.retentionDays) * 24 * time.Hour)}
		if existing, findErr := p.assets.FindByUploadKey(ctx, workspaceID, key); findErr != nil {
			_ = tmp.Close()
			return nil, findErr
		} else if existing != nil {
			asset = existing
		} else if err := p.assets.Create(asset); err != nil {
			_ = tmp.Close()
			return nil, fmt.Errorf("create generated media asset: %w", err)
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			_ = tmp.Close()
			return nil, err
		}
		uploadCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		_, uploadErr := p.storage.Upload(uploadCtx, tmp, key, "video/mp4", written)
		cancel()
		_ = tmp.Close()
		if uploadErr != nil {
			_ = p.assets.MarkFailedWithReason(asset.ID, "generated video storage upload failed", uploadErr)
			return nil, fmt.Errorf("store generated video: %w", uploadErr)
		}
		sha := hex.EncodeToString(hasher.Sum(nil))
		if err := p.assets.MarkReady(asset.ID, sha, written, "video/mp4"); err != nil {
			return nil, fmt.Errorf("mark generated video ready: %w", err)
		}
		asset.SHA256, asset.SizeBytes, asset.Status = sha, written, models.MediaAssetStatusReady
	}

	mediaID, objectKey, bucket := asset.ID, asset.UploadKey, asset.Bucket
	eventRec, err := p.idempotency.FindActiveByKey(workspaceID, "agent-video-event-"+step.ID, time.Now())
	if err != nil {
		return nil, fmt.Errorf("find reserved calendar event: %w", err)
	}
	if eventRec == nil || eventRec.ResourceType != "post" {
		return nil, errors.New("reserved calendar event is missing")
	}
	post, err := p.posts.FindByID(eventRec.ResourceID)
	if err != nil {
		return nil, fmt.Errorf("load reserved calendar event: %w", err)
	}
	if post == nil || post.WorkspaceID != workspaceID {
		return nil, errors.New("reserved calendar event is missing or outside workspace")
	}
	post.Title, post.Caption, post.MediaURL = strings.TrimSpace(publish.Title), publish.Caption, p.storage.AssetURL(asset.UploadKey)
	post.MediaAssetID, post.StorageObjectKey, post.Bucket = &mediaID, &objectKey, &bucket
	post.PrivacyLevel, post.DefaultPrivacyLevel, post.PublishAt = privacy, privacy, &scheduledAt
	post.Status, post.IdempotencyKey = models.PostStatusQueued, &workflowKey
	post.Metadata, _ = json.Marshal(map[string]any{"source_language": publish.Language, "agent_run_id": runID, "agent_step_id": step.ID, "generation_status": "CONTENT_READY", "generation_progress": 100, "generation_phase": "CONTENT_READY", "generation_snapshot": map[string]any{"phase": "CONTENT_READY", "progress": 100}})
	targets := make([]*models.PostTarget, 0, len(publish.Targets))
	for _, target := range publish.Targets {
		targets = append(targets, &models.PostTarget{PlatformAccountID: target.PlatformAccountID, Status: models.PostStatusQueued})
	}
	finalizer, ok := p.posts.(agentVideoCalendarFinalizer)
	if !ok {
		return nil, errors.New("post store does not support atomic calendar event finalization")
	}
	if err := finalizer.FinalizeAgentVideoEvent(post, targets); err != nil {
		return nil, fmt.Errorf("finalize scheduled generated video event: %w", err)
	}
	if p.idempotency != nil {
		p.idempotency.Insert(&models.IdempotencyRecord{WorkspaceID: workspaceID, IdempotencyKey: "agent-video-" + step.ID, ResourceType: "post", ResourceID: post.ID, RequestHash: requestHash, ResponseStatus: http.StatusCreated, ExpiresAt: time.Now().Add(24 * time.Hour)})
	}
	return json.Marshal(map[string]any{"filename": filename, "media_asset_id": asset.ID, "post": post, "post_id": post.ID, "scheduled_at": scheduledAt})
}

func artifactReference(raw json.RawMessage) (string, int64, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", 0, fmt.Errorf("decode remote job result: %w", err)
	}
	var urlValue string
	var size int64
	for _, key := range []string{"artifact_url", "download_url", "media_url"} {
		if json.Unmarshal(value[key], &urlValue) == nil && strings.TrimSpace(urlValue) != "" {
			break
		}
	}
	for _, key := range []string{"artifact_size_bytes", "size_bytes"} {
		if json.Unmarshal(value[key], &size) == nil && size > 0 {
			break
		}
	}
	if nested, ok := value["job"]; ok {
		return artifactReference(nested)
	}
	if nested, ok := value["result"]; ok {
		nestedURL, nestedSize, err := artifactReference(nested)
		if err != nil {
			return "", 0, err
		}
		if urlValue == "" {
			urlValue = nestedURL
		}
		if size == 0 {
			size = nestedSize
		}
	}
	// video.create returns a structured result. Its final video is the
	// publication artifact; keep URL origin enforcement in DownloadArtifact.
	if nested, ok := value["final_video"]; ok {
		nestedURL, nestedSize, err := artifactReference(nested)
		if err != nil {
			return "", 0, err
		}
		if urlValue == "" {
			urlValue = nestedURL
		}
		if size == 0 {
			size = nestedSize
		}
	}
	if urlValue == "" {
		return "", 0, errors.New("completed render has no artifact/download/media URL")
	}
	return urlValue, size, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
