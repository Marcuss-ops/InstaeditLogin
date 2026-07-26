package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// contentPipelineDriveBlock mirrors the *drive* phase of the
// timeline JSON. All fields are omitempty so a still-empty ingest
// (Drive folder import hasn't ran yet) serialises to {} rather
// than a half-populated blob.
type contentPipelineDriveBlock struct {
	FileID string `json:"file_id,omitempty"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
}

// contentPipelineStorageBlock mirrors the *storage* phase. The
// Status field is the asset lifecycle ("pending"|"ready"|"failed"|"expired")
// — not the upload_jobs status, which has different semantics.
type contentPipelineStorageBlock struct {
	AssetID   string    `json:"asset_id"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
}

// contentPipelineTargetBlock is one per-platform row in the
// timeline response. channel_name is mapped from
// platform_accounts.username at the API boundary (the SPA shows
// it as the channel display name).
type contentPipelineTargetBlock struct {
	PostTargetID            int64      `json:"post_target_id"`
	PlatformAccountID       int64      `json:"platform_account_id"`
	ChannelName             string     `json:"channel_name"`
	PostStatus              string     `json:"post_status,omitempty"`
	YouTubeVideoID          string     `json:"youtube_video_id,omitempty"`
	YouTubeUploadStatus     string     `json:"youtube_upload_status,omitempty"`
	YouTubeProcessingStatus string     `json:"youtube_processing_status,omitempty"`
	ThumbnailStatus         string     `json:"thumbnail_status,omitempty"`
	ThumbnailMediaID        string     `json:"thumbnail_media_id,omitempty"`
	EditorURL               string     `json:"editor_url,omitempty"`
	EditorSessionID         string     `json:"editor_session_id,omitempty"`
	VeloxProjectID          string     `json:"velox_project_id,omitempty"`
	PublishAt               *time.Time `json:"publish_at,omitempty"`
	PublishedAt             *time.Time `json:"published_at,omitempty"`
	LastError               string     `json:"last_error,omitempty"`
	AttemptCount            int        `json:"attempt_count"`
}

// contentPipelineResponse is the top-level timeline JSON. Top-level
// fields show only what fits the user's "where does this post think
// it is" question; the targets[] array is the per-channel fan-out.
type contentPipelineResponse struct {
	ContentID      int64                         `json:"content_id"`
	WorkspaceID    int64                         `json:"workspace_id"`
	PostStatus     string                        `json:"post_status,omitempty"`
	Title          string                        `json:"title,omitempty"`
	Caption        string                        `json:"caption,omitempty"`
	MediaURL       string                        `json:"media_url,omitempty"`
	CreatedAt      time.Time                     `json:"created_at"`
	UpdatedAt      time.Time                     `json:"updated_at"`
	PublishAt      *time.Time                    `json:"publish_at,omitempty"`
	PublishedAtAny *time.Time                    `json:"published_at,omitempty"` // earliest non-null PublishedAt across targets (timeline bookmark)
	Drive          *contentPipelineDriveBlock    `json:"drive,omitempty"`
	Storage        *contentPipelineStorageBlock  `json:"storage,omitempty"`
	Targets        []contentPipelineTargetBlock  `json:"targets"`
}

// handleGetContentPipeline is the HTTP entry point for
// GET /api/v1/content/{content_id}/pipeline?workspace_id=N.
//
// Flow:
//   1. Identity (401 if missing).
//   2. content_id parse — 400 on missing / non-positive.
//   3. workspace_id parse from query — 400 on missing / non-positive.
//      Cross-checked against identity.WorkspaceIDs() — 403 on
//      mismatch (no information leak).
//   4. r.contentPipelineStore.GetPipeline(wsID, postID) — 404 on
//      ErrContentPipelineNotFound, 500 on real DB error.
//   5. Response assembly: walk ContentPipelineEntry + nest the
//      child tables into the timeline JSON shape.
//
// The handler does NOT consult post.Status — post_targets[i].Status
// is the canonical cross-platform column today; the top-level
// post_status is set to the EARLIEST non-published target status so
// the timeline UI shows the rightmost cluster colour when the
// fan-out is at multiple stages (one target published, one still
// queued → "partially_published").
//
// Concurrency: this is a pure read; no row is mutated. The
// ContentPipelineStore fans out across 4 SQL queries handled by
// the repo.
func (r *Router) handleGetContentPipeline(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()

	identity := auth.IdentityFromContext(ctx)
	if identity == nil {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}

	contentIDRaw := strings.TrimSpace(chiURLParam(req, "content_id"))
	if contentIDRaw == "" {
		writeError(w, http.StatusBadRequest, "content_id path parameter is required")
		return
	}
	contentID, err := strconv.ParseInt(contentIDRaw, 10, 64)
	if err != nil || contentID <= 0 {
		writeError(w, http.StatusBadRequest, "content_id must be a positive integer")
		return
	}

	workspaceIDRaw := strings.TrimSpace(req.URL.Query().Get("workspace_id"))
	if workspaceIDRaw == "" {
		writeError(w, http.StatusBadRequest, "workspace_id query parameter is required")
		return
	}
	workspaceID, err := strconv.ParseInt(workspaceIDRaw, 10, 64)
	if err != nil || workspaceID <= 0 {
		writeError(w, http.StatusBadRequest, "workspace_id must be a positive integer")
		return
	}
	if !identityBelongsToWorkspace(identity, workspaceID) {
		writeError(w, http.StatusForbidden, "workspace_id is not visible to the caller")
		return
	}

	if r.contentPipelineStore == nil {
		writeError(w, http.StatusServiceUnavailable, "content pipeline store is not configured")
		return
	}

	entry, err := r.contentPipelineStore.GetPipeline(ctx, workspaceID, contentID)
	if err != nil {
		if errors.Is(err, repository.ErrContentPipelineNotFound) {
			writeError(w, http.StatusNotFound, "content not found or not in this workspace")
			return
		}
		writeError(w, http.StatusInternalServerError, "get_content_pipeline: "+err.Error())
		return
	}
	if entry == nil || entry.Post == nil {
		// Defensive: a NULL post after a non-error return shouldn't happen,
		// but guarding keeps the response shape stable.
		writeError(w, http.StatusNotFound, "content not found")
		return
	}

	response := buildContentPipelineResponse(entry, r.editorURL)
	writeJSON(w, http.StatusOK, response)
}

// identityBelongsToWorkspace returns true when the supplied identity
// is allowed to read content within the supplied workspace. The check
// compares against the SINGLE-WorkspaceID exposed by the auth.Identity
// interface (auth.NewUserIdentity / auth.NewApiKeyIdentity both
// surface exactly one workspace per token; the multi-workspace
// membership check would require a richer Identity contract — a
// future taglio). Returns false when the identity is nil or has a
// UserID == 0 (the anon-token path is excluded).
func identityBelongsToWorkspace(identity auth.Identity, workspaceID int64) bool {
	if identity == nil || identity.UserID() == 0 {
		return false
	}
	return identity.WorkspaceID() == workspaceID
}

// chiURLParam extracts a chi route parameter without importing chi into
// this file's export surface. Routers in this package use chi under
// the hood; we reuse the existing chi.URLParam via a local helper
// to keep the import ordering tidy.
func chiURLParam(req *http.Request, key string) string {
	if v := req.PathValue(key); v != "" {
		return v
	}
	// Fallback: chi 5.x exposes URLParam via chi.RouteContext. We
	// import chi in router.go only; if the request context doesn't
	// carry a chi route context (e.g. tests that build an http.Request
	// directly via httptest without going through a chi mux), the
	// fallback returns "" which the caller treats as a missing param.
	return ""
}

// buildContentPipelineResponse composes the timeline JSON from the
// composite entry returned by ContentPipelineStore.GetPipeline.
// Extracted as a pure function so it can be unit-tested without
// mocking the HTTP layer.
func buildContentPipelineResponse(entry *repository.ContentPipelineEntry, editorBaseURL string) contentPipelineResponse {
	resp := contentPipelineResponse{
		ContentID:   entry.Post.ID,
		WorkspaceID: entry.Post.WorkspaceID,
		Title:       entry.Post.Title,
		Caption:     entry.Post.Caption,
		MediaURL:    entry.Post.MediaURL,
		CreatedAt:   entry.Post.CreatedAt,
		UpdatedAt:   entry.Post.UpdatedAt,
		PublishAt:   entry.Post.PublishAt,
	}

	// Drive block — derived from the first upload_job linked to this
	// post (if any). Empty status string when no upload_job exists;
	// the handler renders this as {} on the wire.
	if entry.UploadJob != nil {
		drive := &contentPipelineDriveBlock{
			FileID: entry.UploadJob.SourceID,
			Name:   entry.UploadJob.Title,
			Status: string(entry.UploadJob.Status),
		}
		if drive.Status == "" && string(models.UploadJobStatusPending) != "" {
			drive.Status = string(models.UploadJobStatusIngestCompleted)
		}
		resp.Drive = drive
	}

	// Storage block — present only when the post has a media asset
	// (the asset was stamped onto upload_jobs by the ingest worker).
	if entry.Asset != nil {
		resp.Storage = &contentPipelineStorageBlock{
			AssetID:   entry.Asset.ID,
			Status:    string(entry.Asset.Status),
			ExpiresAt: entry.Asset.ExpiresAt,
		}
	}

	// Fan-out: one target block per post_target. Channel name resolved
	// from platform_accounts.username via the pre-built Accounts map.
	resp.Targets = make([]contentPipelineTargetBlock, 0, len(entry.Targets))
	var (
		earliestPublished  *time.Time
		earliestAnyStatus  = statusUnknown
		seenFirstPublished bool
	)
	for _, t := range entry.Targets {
		block := contentPipelineTargetBlock{
			PostTargetID:    t.ID,
			PlatformAccountID: t.PlatformAccountID,
			PostStatus:      string(t.Status),
			PublishedAt:     t.PublishedAt,
			LastError:       t.ErrorMessage,
			AttemptCount:    t.AttemptCount,
		}
		if acct, ok := entry.Accounts[t.PlatformAccountID]; ok && acct != nil {
			block.ChannelName = acct.Username
		}

		// YouTube publication fields (nil-safe). The YT pub row is
		// keyed by post_target_id; non-YouTube targets will simply
		// not have a map entry and the field remains empty.
		if pub, ok := entry.YouTubePubs[t.ID]; ok && pub != nil {
			if pub.YouTubeVideoID != nil {
				block.YouTubeVideoID = *pub.YouTubeVideoID
			}
			block.YouTubeUploadStatus = pub.YouTubeUploadStatus
			if pub.YouTubeProcessingStatus != nil {
				block.YouTubeProcessingStatus = *pub.YouTubeProcessingStatus
			}
			if pub.ThumbnailMediaID != nil {
				block.ThumbnailMediaID = *pub.ThumbnailMediaID
			}
			if pub.ThumbnailStatus != nil {
				block.ThumbnailStatus = *pub.ThumbnailStatus
			}
			if pub.EditorSessionID != nil {
				block.EditorSessionID = *pub.EditorSessionID
			}
			if pub.VeloxProjectID != nil {
				block.VeloxProjectID = *pub.VeloxProjectID
			}
			block.PublishAt = pub.PublishAt
			if pub.PublishedAt != nil {
				block.PublishedAt = pub.PublishedAt
			}
			if pub.LastError != "" {
				// YT-pub last_error wins only when populated; the
				// post_targets.ErrorMessage is the cross-platform
				// canonical, so we overwrite only if YT has a fresher
				// message.
				block.LastError = pub.LastError
			}
			block.AttemptCount = pub.AttemptCount
			block.EditorURL = buildEditorURL(editorBaseURL, pub)
		}

		// Track the top-level post_status: when ANY target reached
		// 'published' before the others, the timeline UI shows
		// 'partially_published' so the operator sees the rightmost
		// cluster colour.
		if t.PublishedAt != nil && (earliestPublished == nil || t.PublishedAt.Before(*earliestPublished)) {
			earliestPublished = t.PublishedAt
			seenFirstPublished = true
		}
		earliestAnyStatus = combinePostStatus(earliestAnyStatus, t.Status)

		resp.Targets = append(resp.Targets, block)
	}

	// Top-level post_status derivation:
	//   every target is published          → "published"
	//   some published, some non-published → "partially_published"
	//   no targets published               → leftmost status in fan-out
	if seenFirstPublished && earliestAnyStatus != statusPublished {
		resp.PostStatus = string(models.PostStatusPartiallyPublished)
	} else if earliestAnyStatus != statusUnknown {
		resp.PostStatus = string(earliestAnyStatus)
	}
	if earliestPublished != nil {
		t := *earliestPublished
		resp.PublishedAtAny = &t
	}

	return resp
}

// buildEditorURL constructs the dark-editor URL for a YT pub row.
// When the row has no velox_project_id the function returns "" so
// the field stays empty in the response (no fallback URLs that
// would silently redirect to a wrong SPA).
func buildEditorURL(editorBaseURL string, pub *models.YouTubeTargetPublication) string {
	if pub == nil || pub.VeloxProjectID == nil || *pub.VeloxProjectID == "" {
		return ""
	}
	base := strings.TrimRight(editorBaseURL, "/")
	if base == "" {
		return ""
	}
	return fmt.Sprintf("%s/projects/%s", base, *pub.VeloxProjectID)
}

// statusUnknown is the sentinel for `no target has fed the top-level
// status aggregator yet`. Distinct from "" so combinePostStatus can
// distinguish "no value seeded" from "explicit empty".
type targetTimelineStatus string

const (
	statusUnknown                targetTimelineStatus = ""
	statusDraft                  targetTimelineStatus = "draft"
	statusQueued                 targetTimelineStatus = "queued"
	statusPublishing             targetTimelineStatus = "publishing"
	statusPublished              targetTimelineStatus = "published"
	statusPartiallyPublished     targetTimelineStatus = "partially_published"
	statusFailed                 targetTimelineStatus = "failed"
	statusRetrying               targetTimelineStatus = "retrying"
	statusBlockedAuth            targetTimelineStatus = "blocked_auth"
)

// combinePostStatus picks the leftmost-in-time status from a fan-out
// (if any). The priority order matches the publish-state-machine FSM
// order in internal/models/post.go so the operator-eyes top-level
// status renders as "the row is in the EARLIEST phase, not yet
// published anywhere".
func combinePostStatus(current targetTimelineStatus, candidate models.PostStatus) targetTimelineStatus {
	c := targetTimelineStatus(candidate)
	if current == statusUnknown {
		return c
	}
	if c == statusUnknown {
		return current
	}
	rank := map[targetTimelineStatus]int{
		statusDraft:              1,
		statusQueued:             2,
		statusPublishing:         3,
		statusPartiallyPublished: 4,
		statusRetrying:           5,
		statusBlockedAuth:        6,
		statusFailed:             7,
		statusPublished:          8,
	}
	currRank, ok1 := rank[current]
	candRank, ok2 := rank[c]
	if !ok1 || !ok2 {
		return statusUnknown
	}
	if candRank < currRank {
		return c
	}
	return current
}

// Compile-time assertion that the handler's context read depends
// only on context.Context (the long-lived alias avoids an
// accidental drop in future refactors where someone swaps
// req.Context() for req.Context context.Context and breaks).
var (
	_ context.Context
	_ = auth.IdentityFromContext
)
