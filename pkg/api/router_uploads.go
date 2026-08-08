package api

import (
	"context"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"time"
)

type StorageProvider interface {
	Provider() string
	SignUpload(ctx context.Context, userID int64, key, contentType string, sizeBytes int64, ttl time.Duration) (*services.UploadGrant, error)
	// VerifyUpload (Taglio 3.2) HEADs the object and returns
	// server-reported content-type + size for /complete verification.
	VerifyUpload(ctx context.Context, key string) (contentType string, sizeBytes int64, err error)
	// AssetURL (Taglio 3.2) returns the trusted internal URL for an
	// uploaded asset. The publish flow goes through this — the
	// platform API never sees a user-controlled URL.
	AssetURL(key string) string
	// GetObject (Taglio 4.4) returns a presigned GET URL the server
	// can use to download the asset bytes for internal processing.
	GetObject(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// UploadJobStore is the persistence contract for the background
// upload_jobs queue. The API layer both creates new jobs (batches)
// AND reads aggregates for the dashboard status endpoint. The
// worker claims and updates the underlying rows; the API layer does
// NOT touch status transitions from the request path.
type UploadJobStore interface {
	Create(job *models.UploadJob) error
	// ListByUser returns upload_jobs scoped to the caller (userID)
	// with optional filters (account_id / status / from-to). Backs
	// the dashboard "Programmati" view (per-account calendar) and
	// any future "pending uploads" widget. nil filter fields are
	// no-ops; the SQL is one statement with NULL-or-equal predicates
	// so the planner keeps a single plan across all combinations.
	ListByUser(userID int64, filter repository.UploadJobListFilter) ([]models.UploadJob, error)
	// PendingCountsByAccount returns one aggregate row per target
	// account the user has pending uploads on (count + earliest
	// scheduled_at). Single GROUP BY query, no row cap — exact
	// counts even when the user has 10k scheduled rows. Handler
	// maps to GET /api/v1/uploads/counts for the dashboard widget.
	PendingCountsByAccount(userID int64) ([]repository.UploadJobPendingCount, error)
	// PendingDistinctCount returns the user's total number of pending
	// upload_jobs (distinct rows, not per-target expansions). The
	// dashboard's "Pending uploads" stat reads from this — using
	// SUM(PendingCountsByAccount.count) over-counts one upload that
	// targets multiple accounts.
	PendingDistinctCount(userID int64) (int64, error)
	// Reschedule atomically updates scheduled_at for a pending
	// upload_job. Returns the updated row on success; typed
	// repository.ErrUploadJobNotFound when the id is unknown OR
	// the job has already moved past `pending` (worker claimed
	// / completed / failed). handler maps to HTTP 404.
	Reschedule(jobID, userID int64, newScheduledAt time.Time) (models.UploadJob, error)
	// Cancel atomically deletes a pending upload_job. Same state-
	// machine + authz contract as Reschedule; returns
	// repository.ErrUploadJobNotFound on missing / non-pending rows.
	Cancel(jobID, userID int64) error
	// AggregateByFolder returns the per-status counts + min/max
	// scheduled_at scoped to (folder_id, user_id). Used by
	// GET /api/v1/media/import/drive/batch/status for the
	// dashboard. Returns a zero-value BatchStatusSummary (not an
	// error) when no rows match — the handler turns that into a
	// 200 + note rather than 404.
	AggregateByFolder(folderID string, userID int64) (models.BatchStatusSummary, error)
}

// YouTubeVideoEditStore is the persistence contract for thumbnail
// editor sessions. Defined inline in pkg/api so tests can supply a
// fake; production wiring passes *repository.YouTubeVideoEditRepository.
type YouTubeVideoEditStore interface {
	Create(ctx context.Context, edit *models.YouTubeVideoEdit) error
	FindByID(ctx context.Context, id string) (*models.YouTubeVideoEdit, error)
	FindByVeloxProjectID(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error)
	// FindDraftByVeloxProjectID returns the current draft_* persistence
	// columns (P2 auto-save) for the given velox project id, or
	// (nil, nil) when no row matches. Used by the draft PUT handler
	// to MERGE partial updates — a field absent from the request body
	// keeps its current draft value instead of being overwritten (the
	// InstaEditor rename-pill sync sends {"title": ...} only).
	FindDraftByVeloxProjectID(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error)
	Update(ctx context.Context, edit *models.YouTubeVideoEdit) error
	// MarkPublishing (Blocco #5 P0 #2) atomically transitions the row to
	// status='publishing' WITH desired_privacy + publish_at stamped in the
	// same statement. CAS predicate (extended form): status IN
	// ('editing','failed') OR (status='publishing' AND updated_at <
	// NOW() - make_interval(secs => inFlightTimeout)). The strict
	// (inFlightTimeout <= 0) branch runs the same SQL minus the
	// orphan-recovery branch (E1 — Go-level guard). The handler maps
	// (nil, repository.ErrYouTubeVideoEditNotFound) to HTTP 409
	// (CAS-loss). Mirrors repository.YouTubeVideoEditRepository.MarkPublishing.
	MarkPublishing(ctx context.Context, id string, desiredPrivacy string, publishAt *time.Time, inFlightTimeout time.Duration) (*models.YouTubeVideoEdit, error)
	// AttachThumbnail (Blocco #5 P0 #4) atomically links a verified
	// media asset (thumbnail) to an editor session. Single UPDATE
	// statement with CAS predicate `status IN ('editing','failed')` so
	// concurrent publish requests cannot race the link (a session in
	// 'publishing' or 'published' state will not match — handler maps
	// 0-rows to 409). Mirrors
	// repository.YouTubeVideoEditRepository.AttachThumbnail.
	AttachThumbnail(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error)
	// ListByWorkspace feeds the dashboard "code da modificare" widget.
	// Workspace-scoped + optional AccountID/Statuses filters + bounded
	// LIMIT. See repository.YouTubeEditorSessionListFilter for the full
	// semantics; the handler validates ?workspace_id and parses
	// ?account_id / ?status / ?limit, defaulting the status set to
	// YouTubeVideoEditNonTerminalStatuses when no ?status= is supplied.
	ListByWorkspace(ctx context.Context, filter repository.YouTubeEditorSessionListFilter) ([]*models.YouTubeVideoEdit, error)
	// ListByWorkspaceAccountIDs (P0 group videos endpoint) feeds the
	// GET /api/v1/groups/{group_id}/youtube/videos join: one SQL
	// query returns every editor session in the workspace whose
	// platform_account_id is in the supplied slice. The handler
	// caller (pkg/api/youtube_group_videos.go) joins the result onto
	// YouTube's fresh per-channel listing by (account_id, video_id)
	// tuple. See repository.YouTubeVideoEditRepository.ListByWorkspaceAccountIDs
	// for the SQL contract + index hint.
	ListByWorkspaceAccountIDs(ctx context.Context, workspaceID int64, accountIDs []int64) ([]*models.YouTubeVideoEdit, error)
	// ListCoversByGroupAccounts (covers hub) returns every cover
	// project linked to the supplied group accounts — thumbnail
	// projects joined through velox_project_bridges to their
	// youtube_video_edits session, ordered by project update time
	// DESC. Backs GET /api/v1/groups/{group_id}/covers so the SPA
	// renders the per-group covers grid (current + archived history)
	// in one SQL round-trip. See
	// repository.YouTubeVideoEditRepository.ListCoversByGroupAccounts.
	ListCoversByGroupAccounts(ctx context.Context, workspaceID int64, accountIDs []int64) ([]*models.GroupCover, error)
	// FindOrCreateEditableSession (P0#3 click-idempotency) returns the
	// open (non-terminal) editor session for the given (workspace,
	// account, video) triple, or inserts a fresh one.
	FindOrCreateEditableSession(ctx context.Context, workspaceID int64, platformAccountID int64, youtubeVideoID string, sessionIDHint string, projectIDHint string) (*models.YouTubeVideoEdit, error)
	// SaveDraft (P2 — InstaEditor auto-save) atomically writes the
	// operator's mid-edit form values to youtube_video_edits.draft_*
	// AND stamps dirty_flag=false AND draft_updated_at=NOW().
	SaveDraft(ctx context.Context, id string, title string, description string, tags []string, defaultLanguage string, defaultAudioLanguage string, translations map[string]models.YouTubeTranslation, desiredPrivacy string, publishAt *time.Time, draftUpdatedAt time.Time) error
	// MarkPublishedWithActualPrivacy (P0#7) atomically transitions
	// status='publishing' → 'published' AND stamps actual_privacy +
	// youtube_sync_status.
	MarkPublishedWithActualPrivacy(ctx context.Context, id string, actualPrivacy string, syncStatus string) (*models.YouTubeVideoEdit, error)
}

// YouTubeThumbnailBatchStore persists the durable batch envelope and its
// per-video progress. The API keeps this contract local so handler tests can
// use an in-memory fake while production wires the SQL repository.
type YouTubeThumbnailBatchStore interface {
	Create(ctx context.Context, batch *models.YouTubeThumbnailBatch, items []models.YouTubeThumbnailBatchItem) error
	FindByID(ctx context.Context, id string) (*models.YouTubeThumbnailBatch, error)
	FindByKey(ctx context.Context, workspaceID int64, key string) (*models.YouTubeThumbnailBatch, error)
	ListItems(ctx context.Context, batchID string) ([]models.YouTubeThumbnailBatchItem, error)
	ClaimBatch(ctx context.Context, batchID string, staleBefore time.Time) (bool, error)
	ClaimItem(ctx context.Context, itemID int64, staleBefore time.Time) (bool, error)
	UpdateItem(ctx context.Context, item *models.YouTubeThumbnailBatchItem) error
	Recompute(ctx context.Context, batchID string) (*models.YouTubeThumbnailBatch, error)
}
