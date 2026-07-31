package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ContentPipelineEntry is the single composite row returned by
// ContentPipelineRepository.GetPipeline. It carries every field the
// GET /api/v1/content/{id}/pipeline handler renders into the
// `drive`, `storage`, and `targets[]` blocks of the response. One
// round-trip per lock-stepped SQL statement keeps the dashboard's
// timeline refresh cheap (a user could have 30+ targets/posts and
// pay only ~6 round-trips total instead of 6*N).
//
// Where this is *not* used:
//   - per-target writes (MarkYouTubeUploaded, MarkThumbnailReady) —
//     the YouTube target publication repo owns those atomic
//     transitions.
//   - single-resource lookups (Post.FindByID, MediaAsset.FindByID) —
//     the existing repos already expose them and the pipeline handler
//     reuses those entry points for non-fan-out code paths.
type ContentPipelineEntry struct {
	Post      *models.Post
	Targets   []*models.PostTarget
	UploadJob *models.UploadJob // nil-safe; nil when no upload_job linked the post
	Asset     *models.MediaAsset
	// YouTubePubs maps post_target_id → publication row. The map is
	// populated for every target the post has; missing keys are
	// non-YouTube targets (the schema allows fans-out to non-YouTube
	// platforms in the future, today every target is a platform_accounts
	// and every target MAY have a corresponding youtube_target_publications
	// row once the upload worker stamps it). The handler renders missing
	// keys as a target row with empty youtube_* fields.
	YouTubePubs map[int64]*models.YouTubeTargetPublication
	// Accounts maps platform_account_id → account row used to
	// resolve `channel_name` (== platform_accounts.username) for
	// the response. Pre-built map so the handler doesn't do linear
	// scans over the targets[] array.
	Accounts map[int64]*models.PlatformAccount
}

// ContentPipelineStore is the persistence contract the API handler
// depends on. Local interface so tests inject an in-memory fake;
// production wiring passes *repository.ContentPipelineRepository
// (created in internal/bootstrap/app.go).
type ContentPipelineStore interface {
	GetPipeline(ctx context.Context, workspaceID, postID int64) (*ContentPipelineEntry, error)
}

// ErrContentPipelineNotFound is the sentinel returned when the post
// lookup yields zero rows OR the post belongs to a different
// workspace (the SQL predicate `id = $2 AND workspace_id = $1`
// collapses both cases into one — no information leak). The API
// handler maps errors.Is to HTTP 404.
var ErrContentPipelineNotFound = errors.New("content pipeline: post not found or not in workspace")

// ContentPipelineRepository is the consolidated fanned-out read
// for the dashboard pipeline view (Blocco #0 Carosello). The hand-
// rolled SQL here intentionally mirrors the existing per-table
// reads in posts / upload_jobs / media_assets — keeping them
// inline lets the handler consume ONE repository rather than
// wiring five separate deps.
type ContentPipelineRepository struct {
	db *sql.DB
}

// NewContentPipelineRepository returns a Repository bound to db.
// Wire this exactly once at bootstrap.
func NewContentPipelineRepository(db *sql.DB) *ContentPipelineRepository {
	return &ContentPipelineRepository{db: db}
}

// GetPipeline returns the composite entry for (workspaceID, postID).
// (nil, ErrContentPipelineNotFound) when the post row is missing or
// belongs to a different workspace. Returns (entry, nil) when the
// post exists — even when ALL downstream tables (upload_jobs,
// media_assets, youtube_target_publications) are empty; the entry
// then carries nil/empty maps so the handler renders a "Drive still
// running / YT upload not started" timeline.
//
// SQL strategy: 4 round-trips regardless of target fan-out size:
//  1. posts              WHERE id = $2 AND workspace_id = $1
//  2. post_targets WHERE post_id = $postID  + youtube_target_publications
//     WHERE post_target_id = ANY($1::bigint[])
//  3. platform_accounts  WHERE id = ANY($1::bigint[]) AND workspace_id = $2
//  4. upload_jobs WHERE post_id = $postID ORDER BY id ASC LIMIT 1
//  5. media_assets       WHERE id = $assetID
//
// The post_targets + youtube_target_publications pair is one query
// (the targets fan-out also produces the publication-row primary-
// key set, so the platform_accounts fan-out keys off the same
// unioned id set). upload_jobs + media_assets are read after the
// targets query because their keys (upload_job.asset_id, the post
// itself) depend on step 1's return.
func (r *ContentPipelineRepository) GetPipeline(
	ctx context.Context,
	workspaceID, postID int64,
) (*ContentPipelineEntry, error) {
	if workspaceID <= 0 || postID <= 0 {
		return nil, fmt.Errorf("content pipeline GetPipeline: workspaceID and postID must be positive (got ws=%d, post=%d)", workspaceID, postID)
	}

	// Step 1: post row (workspace-scoped).
	p, err := r.findPost(ctx, workspaceID, postID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrContentPipelineNotFound
	}

	entry := &ContentPipelineEntry{Post: p}

	// Step 2: post_targets fan-out.
	pubsByTarget, targets, allAccountIDs, err := r.findTargetsAndPubs(ctx, postID)
	if err != nil {
		return nil, err
	}
	entry.Targets = targets
	entry.YouTubePubs = pubsByTarget

	// Step 3: platform_accounts fan-out (workspace-scoped at the SQL layer).
	accounts, err := r.findAccounts(ctx, workspaceID, allAccountIDs)
	if err != nil {
		return nil, err
	}
	entry.Accounts = accounts

	// Step 4: upload_job (the FIRST one linked to this post).
	uj, err := r.findUploadJob(ctx, postID)
	if err != nil {
		return nil, err
	}
	entry.UploadJob = uj

	// Step 5: media_asset lookup via upload_jobs.asset_id (when present).
	if uj != nil && uj.AssetID != nil && *uj.AssetID != "" {
		asset, err := r.findMediaAsset(ctx, *uj.AssetID)
		if err != nil {
			return nil, err
		}
		entry.Asset = asset
	}

	return entry, nil
}

// findPost is the workspace-scoped posts lookup (defence-in-depth —
// the route also intersects against identity.WorkspaceIDs()).
func (r *ContentPipelineRepository) findPost(
	ctx context.Context,
	workspaceID, postID int64,
) (*models.Post, error) {
	p := &models.Post{}
	err := r.db.QueryRowContext(ctx,
		`SELECT `+contentPipelineSelectColumns+`
		 FROM posts
		 WHERE id = $2 AND workspace_id = $1`,
		workspaceID, postID,
	).Scan(&p.ID, &p.WorkspaceID, &p.Title, &p.Caption, &p.MediaURL,
		&p.IngestAfter, &p.PublishAt, &p.Status,
		&p.PrivacyLevel, &p.DefaultPrivacyLevel,
		&p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("content pipeline posts: %w", err)
	}
	return p, nil
}

// findTargetsAndPubs reads post_targets WHERE post_id=$1 AND joins the
// one-round trip with the youtube_target_publications fan-out keyed
// on post_target_id = ANY(). Returns:
//   - pubsByTarget: post_target_id → publication row (zero entries
//     when no YT pub has been linked yet; the response renders a
//     "Drive done, YT upload not started" timeline in that case)
//   - targets: ordered list of post_targets rows
//   - allAccountIDs: the union of distinct platform_account_id values
//     across all targets (consumed by findAccounts)
//
// We use a single fan-out on the platform_account_ids — NOT one
// round-trip per target — because Drive batches can fan out to 20+
// channels and per-row SELECTs would serialise. The GIN index on
// post_targets(post_id) + the btree PK on platform_accounts.id keep
// both legs index-only at scale.
func (r *ContentPipelineRepository) findTargetsAndPubs(
	ctx context.Context,
	postID int64,
) (map[int64]*models.YouTubeTargetPublication, []*models.PostTarget, []int64, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, post_id, platform_account_id, status,
		        attempt_count, next_attempt_at, platform_post_id, remote_post_id, remote_post_url,
		        error_message, published_at, completed_at, provider_state, container_id,
		        last_error_code, provider_idempotency_key, version, created_at, updated_at
		 FROM post_targets
		 WHERE post_id = $1
		 ORDER BY id ASC`, postID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("content pipeline post_targets: %w", err)
	}
	defer rows.Close()

	var (
		targets       []*models.PostTarget
		postTargetIDs []int64
		accountIDs    []int64
		accountSet    = map[int64]bool{}
	)
	for rows.Next() {
		t := &models.PostTarget{}
		var (
			nextAttempt     sql.NullTime
			providerIdemKey sql.NullString
			publishedAt     sql.NullTime
			completedAt     sql.NullTime
		)
		if err := rows.Scan(
			&t.ID, &t.PostID, &t.PlatformAccountID, &t.Status,
			&t.AttemptCount, &nextAttempt, &t.PlatformPostID, &t.RemotePostID, &t.RemotePostURL,
			&t.ErrorMessage, &publishedAt, &completedAt, &t.ProviderState, &t.ContainerID,
			&t.LastErrorCode, &providerIdemKey, &t.Version, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, nil, nil, fmt.Errorf("content pipeline post_targets scan: %w", err)
		}
		if nextAttempt.Valid {
			tt := nextAttempt.Time
			t.NextAttemptAt = &tt
		}
		if publishedAt.Valid {
			tt := publishedAt.Time
			t.PublishedAt = &tt
		}
		if completedAt.Valid {
			tt := completedAt.Time
			t.CompletedAt = &tt
		}
		if providerIdemKey.Valid {
			v := providerIdemKey.String
			t.ProviderIdempotencyKey = &v
		}
		targets = append(targets, t)
		postTargetIDs = append(postTargetIDs, t.ID)
		if !accountSet[t.PlatformAccountID] {
			accountSet[t.PlatformAccountID] = true
			accountIDs = append(accountIDs, t.PlatformAccountID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("content pipeline post_targets rows: %w", err)
	}

	// YT pub fan-out. Empty input → empty map (single arg check; saves one round-trip).
	pubsByTarget := map[int64]*models.YouTubeTargetPublication{}
	if len(postTargetIDs) > 0 {
		pubs, err := r.findYouTubePubs(ctx, postTargetIDs)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, pub := range pubs {
			pubsByTarget[pub.PostTargetID] = pub
		}
	}

	return pubsByTarget, targets, accountIDs, nil
}

// findYouTubePubs reads the youtube_target_publications fan-out
// for the given post_target_ids. Uses the same column projection
// as the YouTubeTargetPublicationRepository.
func (r *ContentPipelineRepository) findYouTubePubs(
	ctx context.Context,
	postTargetIDs []int64,
) ([]*models.YouTubeTargetPublication, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+ytTargetPubsSelectColumns+`
		 FROM youtube_target_publications
		 WHERE post_target_id = ANY($1::bigint[])
		 ORDER BY id ASC`, pq.Array(postTargetIDs))
	if err != nil {
		return nil, fmt.Errorf("content pipeline youtube_target_publications: %w", err)
	}
	defer rows.Close()

	var out []*models.YouTubeTargetPublication
	for rows.Next() {
		pub := &models.YouTubeTargetPublication{}
		if err := scanYouTubeTargetPublication(rows, pub); err != nil {
			return nil, fmt.Errorf("content pipeline youtube_target_publications scan: %w", err)
		}
		out = append(out, pub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("content pipeline youtube_target_publications rows: %w", err)
	}
	return out, nil
}

// findAccounts returns platform_accounts rows for the supplied IDs,
// workspace-scoped at the SQL layer. Returns an empty map (not nil,
// not error) when allAccountIDs is empty.
func (r *ContentPipelineRepository) findAccounts(
	ctx context.Context,
	workspaceID int64,
	allAccountIDs []int64,
) (map[int64]*models.PlatformAccount, error) {
	out := map[int64]*models.PlatformAccount{}
	if len(allAccountIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, platform, platform_user_id, username, status, connected_at, last_validated_at, last_refresh_at, reauth_required_at
		 FROM platform_accounts
		 WHERE workspace_id = $1
		   AND id = ANY($2::bigint[])`,
		workspaceID, pq.Array(allAccountIDs))
	if err != nil {
		return nil, fmt.Errorf("content pipeline platform_accounts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		acct := &models.PlatformAccount{}
		var (
			connectedAt      sql.NullTime
			lastValidatedAt  sql.NullTime
			lastRefreshAt    sql.NullTime
			reauthRequiredAt sql.NullTime
		)
		if err := rows.Scan(
			&acct.ID, &acct.UserID, &acct.Platform, &acct.PlatformUserID,
			&acct.Username, &acct.Status,
			&connectedAt, &lastValidatedAt, &lastRefreshAt, &reauthRequiredAt,
		); err != nil {
			return nil, fmt.Errorf("content pipeline platform_accounts scan: %w", err)
		}
		if connectedAt.Valid {
			tt := connectedAt.Time
			acct.ConnectedAt = &tt
		}
		if lastValidatedAt.Valid {
			tt := lastValidatedAt.Time
			acct.LastValidatedAt = &tt
		}
		if lastRefreshAt.Valid {
			tt := lastRefreshAt.Time
			acct.LastRefreshAt = &tt
		}
		if reauthRequiredAt.Valid {
			tt := reauthRequiredAt.Time
			acct.ReauthRequiredAt = &tt
		}
		out[acct.ID] = acct
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("content pipeline platform_accounts rows: %w", err)
	}
	return out, nil
}

// findUploadJob returns the FIRST upload_jobs row whose post_id
// matches the supplied postID. (nil, nil) — distinct from a real
// error — when no row exists.
func (r *ContentPipelineRepository) findUploadJob(
	ctx context.Context,
	postID int64,
) (*models.UploadJob, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, workspace_id, source_type, source_id, drive_account_id, folder_id, title, caption,
		        targets, status, error_message, post_id, asset_id, ingest_after, publish_at, created_at, updated_at,
		        attempt_count, max_attempts, next_attempt_at, lease_owner, lease_expires_at, heartbeat_at,
		        progress_bytes, total_bytes, error_code, priority, started_at, completed_at,
		        youtube_session_uri, youtube_session_offset, youtube_session_expires_at, youtube_chunk_size, youtube_last_chunk_at,
		        default_privacy_level
		 FROM upload_jobs
		 WHERE post_id = $1
		 ORDER BY id ASC
		 LIMIT 1`, postID)
	job, err := scanUploadJob(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("content pipeline upload_jobs: %w", err)
	}
	return job, nil
}

// findMediaAsset returns the media_assets row by id (PK). Returns
// (nil, nil) when no row matches OR the row's id is the zero-value.
func (r *ContentPipelineRepository) findMediaAsset(
	ctx context.Context,
	assetID string,
) (*models.MediaAsset, error) {
	a := &models.MediaAsset{}
	if assetID == "" {
		return nil, nil
	}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, upload_key, bucket, content_type, size_bytes, status, sha256,
		        COALESCE(error_message, ''), expires_at, created_at, updated_at
		 FROM media_assets
		 WHERE id = $1`, assetID).Scan(
		&a.ID, &a.UserID, &a.UploadKey, &a.Bucket, &a.ContentType, &a.SizeBytes, &a.Status, &a.SHA256,
		&a.ErrorMessage, &a.ExpiresAt, &a.CreatedAt, &a.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("content pipeline media_assets: %w", err)
	}
	return a, nil
}

// Compile-time assertion that the imports stay in sync with our
// column-list helpers. Built once at link time; cheap.
var (
	_ = time.Time{}      // models.Post.UpdatedAt is time.Time via the scan list
	_ = sql.NullString{} // platform_accounts.reauth_required_at, etc.
)
