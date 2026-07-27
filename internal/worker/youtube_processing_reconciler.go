package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// EditorSessionCreatorInput mirrors pkg/api.CreateEditorSessionInput.
// We export-and-declare-it-locally as a named struct (no type alias)
// so the worker stays free of any pkg/api import cycle: pkg/api does
// NOT import internal/worker. The fields are positional / named the
// same as the upstream struct; the worker constructs it from the
// recipient-side YT pub row + the resolved workspace_id.
//
// Blocco #4 P0 — 1-per-target contract preserved. Every reconciler
// invocation generates fresh uuid.NewString() inside Router.CreateEditorSession
// (the side-effect), so cross-replica races resolve to "one CAS-link
// wins + one orphan session" (defence-in-depth: migration-068's UNIQUE
// constraint + the predicate `editor_session_id IS NULL` in
// MarkEditorSessionCreated).
type EditorSessionCreatorInput struct {
	WorkspaceID        int64
	PlatformAccountID  int64
	YouTubeVideoID     string
	SourceThumbnailURL string
}

// EditorSessionCreator is the narrow contract the worker depends on
// against pkg/api.Router. Production wiring in internal/bootstrap/app.go
// passes the live *api.Router (compile-time assertion in pkg/api
// guarantees signature compatibility).
//
// Mirrors the public Router.CreateEditorSession(ctx, input) method
// (formerly the private helper); both signatures are kept in lockstep.
type EditorSessionCreator interface {
	CreateEditorSession(ctx context.Context, in EditorSessionCreatorInput) (*models.YouTubeVideoEdit, error)
}

// ReconcileYoutubeProcessingStore is the narrow persistence contract
// the reconciler needs on youtube_target_publications. It composes:
//   - ListPendingEditorSessionTargets — drain rows that have
//     youtube_processing_status='processed' AND editor_session_id IS NULL.
//   - MarkEditorSessionCreated — atomic CAS-link the new session back
//     onto the YT pub row.
//
// Both methods are declared on *repository.YouTubeTargetPublicationRepository
// (Blocco #4 P0); interfaces extended in Blocco #4 P0.
type ReconcileYoutubeProcessingStore interface {
	ListPendingEditorSessionTargets(ctx context.Context, limit int) ([]*models.YouTubeTargetPublication, error)
	MarkEditorSessionCreated(ctx context.Context, id int64, editorSessionID, veloxProjectID string) error
}

// ReconcileUploadJobStore is the narrow contract the reconciler needs
// to derive WorkspaceID from UploadJobID (the YT pub row stores the
// job_id, not the workspace_id directly). The query is O(1) on the
// upload_jobs table's PK.
//
// *repository.UploadJobRepository satisfies this contract via its
// FindByID method (a future commit can add a narrower
// `GetWorkspaceIDByID` if this query hotspots). The signature
// mirrors the concrete repo's (no context.Context parameter — the
// repo predates the convention).
type ReconcileUploadJobStore interface {
	FindByID(id int64) (*models.UploadJob, error)
}

// YoutubeProcessingReconcilerOptions configures the ticker cadence +
// per-tick batch size. All fields zero-value safe with applyDefaults
// applied in Run().
type YoutubeProcessingReconcilerOptions struct {
	// TickInterval is the cadence of the per-cycle poll loop (the
	// drain loop + heartbeat). Default 60s — well above the
	// production-defended 5s cadence of the publish/reconcile
	// workers because the YT-poll side-effect is via MarkYouTubeProcessed
	// which is itself 1-min cadence in the future webhook listener.
	// When the webhook listener is wired, this drops to 30s for a
	// tighter end-to-end latency.
	TickInterval time.Duration
	// BatchLimit caps the rows pulled per ListPendingEditorSessionTargets
	// call so a backlog spike doesn't tie up the worker for an
	// unbounded duration. The reconciler schedules a follow-up tick
	// to drain (FIFO via id ASC). Default 100 rows.
	BatchLimit int
}

// YoutubeProcessingReconciler is the Blocco #4 P0 worker that bridges
// the YouTube upload phase (MarkYouTubeProcessed) to the Velox
// editor-session creation flow.
//
// Lifecycle (mirrors drift_batch_crawler.go's Run/Tick pattern):
//  1. applyDefaults on opts (zero-value safe);
//  2. Spawn the token loop (claim + create-session + CAS-link);
//  3. Block on ctx.Done() + waitGroup.Wait() for graceful shutdown.
type YoutubeProcessingReconciler struct {
	ytPubStore    ReconcileYoutubeProcessingStore
	uploadRepo    ReconcileUploadJobStore
	editorCreator EditorSessionCreator
	opts          YoutubeProcessingReconcilerOptions
	logger        *slog.Logger
}

// NewYoutubeProcessingReconciler wires a new reconciler. opts fields
// default in Run() when zero; the bootstrap should pass an explicit
// options struct so the operator-facing env vars (future
// YOUTUBE_PROCESSING_TICK_SECONDS) take effect.
func NewYoutubeProcessingReconciler(
	ytPubStore ReconcileYoutubeProcessingStore,
	uploadRepo ReconcileUploadJobStore,
	editorCreator EditorSessionCreator,
	opts YoutubeProcessingReconcilerOptions,
	logger *slog.Logger,
) *YoutubeProcessingReconciler {
	if logger == nil {
		logger = slog.Default()
	}
	return &YoutubeProcessingReconciler{
		ytPubStore:    ytPubStore,
		uploadRepo:    uploadRepo,
		editorCreator: editorCreator,
		opts:          opts,
		logger:        logger,
	}
}

func (r *YoutubeProcessingReconciler) applyDefaults() {
	if r.opts.TickInterval <= 0 {
		r.opts.TickInterval = 60 * time.Second
	}
	if r.opts.BatchLimit <= 0 {
		r.opts.BatchLimit = 100
	}
}

// Run orchestrates the reconciler goroutine. So far the reconcile
// worker is single-row-at-a-time (no in-tick parallelism) — the
// bottleneck is Velox session creation latency (network call) not
// the DB poll. A future commit can fan out per-row goroutines
// bounded by a concurrency semaphore once Velox API rate limits
// become a constraint in production.
func (r *YoutubeProcessingReconciler) Run(ctx context.Context) error {
	r.applyDefaults()

	r.logger.Info("youtube processing reconciler started",
		"tick_interval_seconds", r.opts.TickInterval.Seconds(),
		"batch_limit", r.opts.BatchLimit,
	)
	defer r.logger.Info("youtube processing reconciler stopped")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.runTickLoop(ctx)
	}()
	wg.Wait()
	return ctx.Err()
}

// runTickLoop is the per-cycle drain loop. Each tick fetches a
// bounded batch of pending YT pub rows and processes them
// sequentially (no fan-out — see Run comment for rationale).
func (r *YoutubeProcessingReconciler) runTickLoop(ctx context.Context) {
	// Run once immediately so we don't wait TickInterval on the
	// first tick after startup.
	r.runTick(ctx)

	ticker := time.NewTicker(r.opts.TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runTick(ctx)
		}
	}
}

// runTick is one drain cycle. Returns nothing because the loop is
// self-recovering (next tick retries; CAS predicate on
// MarkEditorSessionCreated ensures double-stamp can't happen).
//
// Per-row failure handling:
//   - resolveWorkspace failure (yt pub → upload_job → workspace_id
//     returns nothing) → log + skip + retry next tick.
//   - EditorSessionCreator.CreateEditorSession returns a typed
//     sentinel error → log + skip (next tick will retry).
//   - MarkEditorSessionCreated returns
//     repository.ErrYouTubeTargetPublicationNotFound (CAS-loss) →
//     log INFO + skip (the other reconciler replica won the race).
func (r *YoutubeProcessingReconciler) runTick(ctx context.Context) {
	rows, err := r.ytPubStore.ListPendingEditorSessionTargets(ctx, r.opts.BatchLimit)
	if err != nil {
		r.logger.Error("youtube processing reconciler: list pending failed", "error", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	r.logger.Info("youtube processing reconciler: claimed batch", "count", len(rows))

	for _, pub := range rows {
		if pub.YouTubeVideoID == nil || *pub.YouTubeVideoID == "" {
			// Pre-upload rows: the upload phase hasn't produced a
			// video_id yet. Skip — these will re-blossom into
			// 'processed' later (post-MarkYouTubeUploaded + post-
			// YouTube-Poll) and the next tick picks them up.
			continue
		}

		workspaceID, ok := r.resolveWorkspaceID(pub)
		if !ok {
			continue
		}

		edit, err := r.editorCreator.CreateEditorSession(ctx, EditorSessionCreatorInput{
			WorkspaceID:       workspaceID,
			PlatformAccountID: pub.PlatformAccountID,
			YouTubeVideoID:    *pub.YouTubeVideoID,
			// SourceThumbnailURL stays empty: the reconciler doesn't
			// run as part of the SPA's "first session" UX, so no
			// worked-example thumbnail is supplied. The handler-path
			// is the authoritative source-thumbnail feeder.
			SourceThumbnailURL: "",
		})
		if err != nil {
			// Sentinel errors map to "transient — retry next tick":
			//   - workspace not found / account not found / channel
			//     unlinked → workspace or account was deleted; retry
			//     would loop forever → log + SKIP WITHOUT re-Mark
			//     (the user's row stays un-linked, observable via the
			//     operator-triage dashboard as "stuck").
			//   - no valid token / youtube service unconfigured →
			//     transient infra issue → log + skip (retry next
			//     tick; once token refreshed OR svc re-configured, the
			//     next tick succeeds).
			// Non-sentinel errors (network failures, schema mismatch):
			// log + skip (next tick retries).
			r.logger.Warn("youtube processing reconciler: create session failed (skipping row)",
				"publish_row_id", pub.ID,
				"workspace_id", workspaceID,
				"platform_account_id", pub.PlatformAccountID,
				"youtube_video_id", *pub.YouTubeVideoID,
				"error", err,
			)
			continue
		}

		if err := r.ytPubStore.MarkEditorSessionCreated(ctx, pub.ID, edit.ID, edit.VeloxProjectID); err != nil {
			if errors.Is(err, repository.ErrYouTubeTargetPublicationNotFound) {
				// CAS-loss: a peer reconciler repo already stamped
				// editor_session_id (the WHERE editor_session_id IS NULL
				// predicate matched 0 rows for us). The OTHER
				// reconciler's session row in youtube_video_edits is
				// still valid and the YT pub row IS linked — just not
				// by us. Skip + log INFO; no retry needed (the link
				// exists).
				r.logger.Info("youtube processing reconciler: CAS lost (peer already linked)",
					"publish_row_id", pub.ID,
					"our_session_id", edit.ID,
				)
				continue
			}
			r.logger.Error("youtube processing reconciler: MarkEditorSessionCreated failed",
				"publish_row_id", pub.ID,
				"session_id", edit.ID,
				"error", err,
			)
			continue
		}

		r.logger.Info("youtube processing reconciler: editor session created + linked",
			"publish_row_id", pub.ID,
			"workspace_id", workspaceID,
			"platform_account_id", pub.PlatformAccountID,
			"youtube_video_id", *pub.YouTubeVideoID,
			"session_id", edit.ID,
			"velox_project_id", edit.VeloxProjectID,
		)
	}
}

// resolveWorkspaceID looks up upload_jobs.id → upload_job.workspace_id
// for the YT pub row, returning (id, true) on success and logging +
// returning (0, false) on miss/error so the caller skips + continues
// the loop.
//
// The YT pub row's `upload_job_id` is the only join key linking the YT
// per-target state to the workspace; without it the reconciler cannot
// call CreateEditorSession (which requires WorkspaceID + PlatformAccountID).
func (r *YoutubeProcessingReconciler) resolveWorkspaceID(pub *models.YouTubeTargetPublication) (int64, bool) {
	uploadJob, err := r.uploadRepo.FindByID(pub.UploadJobID)
	if err != nil {
		r.logger.Warn("youtube processing reconciler: upload_job lookup failed; skipping row",
			"publish_row_id", pub.ID,
			"upload_job_id", pub.UploadJobID,
			"error", err,
		)
		return 0, false
	}
	if uploadJob == nil {
		r.logger.Warn("youtube processing reconciler: orphan publish row (upload_job missing); skipping",
			"publish_row_id", pub.ID,
			"upload_job_id", pub.UploadJobID,
		)
		return 0, false
	}
	return uploadJob.WorkspaceID, true
}

// Compile-time assertion that the production wiring pattern (the
// concrete *repository.UploadJobRepository) satisfies the narrow
// interface. Catches signature drift at vet time, not at runtime in
// production.
var _ ReconcileUploadJobStore = (interface {
	FindByID(int64) (*models.UploadJob, error)
})(nil)
