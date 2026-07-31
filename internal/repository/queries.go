package repository

// queries.go centralises every SQL string used by post_repo.go (and its
// future method-split siblings post_create.go, post_query.go,
// post_update.go, post_schedule.go, post_dispatch.go) into a single file
// for migration-friendly grep:
//
//	$ grep -nE 'FROM [a-z_]+|JOIN [a-z_]+|INTO [a-z_]+|UPDATE [a-z_]+' \
//	    internal/repository/queries.go
//
// Naming: q<Verb><Entity>[<Qualifier>] in PascalCase. Constants are
// grouped under the post_repo_*.go file that owns the call site, so a
// developer grepping the source file also finds the matching constants
// under the same comment header.

// --- post_create.go ---

// P1 (migration 077) — qInsertPost now binds upload_job_id as $10 and
// uses `ON CONFLICT (upload_job_id) WHERE upload_job_id IS NOT NULL DO
// NOTHING` + `RETURNING id, created_at, upload_job_id`. The partial
// unique index `uq_posts_upload_job_id` (same WHERE predicate as the
// ON CONFLICT clause so Postgres can mirror-infer the index) enforces
// "one post per upload_job" so a worker retry reuses the existing row
// instead of creating a phantom post.
//
// ON CONFLICT inference: Postgres REQUIRES the `WHERE upload_job_id IS
// NOT NULL` predicate on ON CONFLICT to mirror the partial index's
// own WHERE clause (otherwise it raises 42P10 — "there is no unique or
// exclusion constraint matching the ON CONFLICT specification").
// Without the WHERE clause, the request fails at runtime. The
// mirrored predicate also makes the inference deterministic: the only
// candidate index is the partial one (no other unique constraint on
// upload_job_id exists), so the inference picks it unambiguously.
//
// On DO NOTHING firing, Postgres returns ZERO rows via RETURNING — so
// QueryRow().Scan(&id, &created_at, &upload_job_id) returns
// sql.ErrNoRows. PostRepository.Create treats ErrNoRows as the
// idempotent-retried path and re-fetches the existing row + its
// post_targets fan-out via qSelectPostByUploadJobID +
// qSelectTargetsByPost (still inside the same tx so the re-fetch sees
// its own uncommitted state). Skipping the target/outbox INSERTs on
// the conflict path is essential: those would 23505 on
// UNIQUE(post_id, platform_account_id).
const qInsertPost = `INSERT INTO posts (workspace_id, title, caption, media_url, ingest_after, publish_at, default_privacy_level, privacy_level, status, upload_job_id, media_asset_id, storage_object_key, bucket)
 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
 ON CONFLICT (upload_job_id) WHERE upload_job_id IS NOT NULL DO NOTHING
 RETURNING id, created_at, upload_job_id`

const qInsertPostTarget = `INSERT INTO post_targets (post_id, platform_account_id, status)
 VALUES ($1, $2, $3)
 RETURNING id`

const qInsertOutboxEvent = `INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
 VALUES ($1, $2, $3, $4::jsonb)`

// --- post_query.go ---

// P1 (migration 053) — schema-wide: every SELECT against posts now
// returns the two new privacy columns (privacy_level, default_privacy_level)
// so the publish_worker can apply the precedence cascade without an extra
// round-trip.
// P1 (migration 077) — every post-column-listing SELECT now also returns
// upload_job_id (the last column) so the worker / API layer can read
// the back-reference without a second round-trip. Column-order
// invariant callers depend on: `id, workspace_id, title, caption,
// media_url, ingest_after, publish_at, status, privacy_level,
// default_privacy_level, created_at, upload_job_id` — and the Scan
// arity in post_repo.go matches exactly. Whatever order the caller
// reads the row via Scan, the same ORDER BY must stay in sync here —
// post_repo_test.go's mock assertions live here.
const qSelectPostByID = `SELECT id, workspace_id, title, caption, media_url, ingest_after, publish_at, status, privacy_level, default_privacy_level, created_at, upload_job_id, media_asset_id, storage_object_key, bucket
 FROM posts
 WHERE id = $1`

// P1 (migration 077) — for the ON CONFLICT resolution path. Used by
// PostRepository.fetchExistingByUploadJobID to rehydrate the caller's
// Post struct when qInsertPost's `ON CONFLICT ... DO NOTHING` fired for
// a duplicate upload_job_id. Backed by the partial index
// `idx_posts_upload_job_id_lookup` (also `WHERE upload_job_id IS NOT
// NULL` predicate) so the lookup is O(log N) on the retry path.
//
// Column order matches the family of qSelectPost* (with upload_job_id
// appended at the end). The caller re-reads the row that already
// exists on disk and stamps the canonical id/created_at back onto the
// caller's pointer.
const qSelectPostByUploadJobID = `SELECT id, workspace_id, title, caption, media_url, ingest_after, publish_at, status, privacy_level, default_privacy_level, created_at, upload_job_id, media_asset_id, storage_object_key, bucket
 FROM posts
 WHERE upload_job_id = $1`

const qSelectPostsByWorkspace = `SELECT id, workspace_id, title, caption, media_url, ingest_after, publish_at, status, privacy_level, default_privacy_level, created_at, upload_job_id, media_asset_id, storage_object_key, bucket
 FROM posts
 WHERE workspace_id = $1
 ORDER BY created_at DESC`

const qSelectQueuedPosts = `SELECT id, workspace_id, title, caption, media_url, ingest_after, publish_at, status, privacy_level, default_privacy_level, created_at, upload_job_id, media_asset_id, storage_object_key, bucket
 FROM posts
 WHERE status = 'queued' AND (publish_at IS NULL OR publish_at <= $1)
 ORDER BY publish_at ASC NULLS FIRST`

const qSelectTargetsByPost = `SELECT id, post_id, platform_account_id, status,
	        COALESCE(platform_post_id, ''), COALESCE(error_message, ''), published_at,
	        COALESCE(provider_state, ''), COALESCE(container_id, ''),
	provider_idempotency_key, completed_at
	 FROM post_targets
	 WHERE post_id = $1
	 ORDER BY id ASC`

const qSelectPublishingTargets = `SELECT id, post_id, platform_account_id, status,
	        COALESCE(platform_post_id, ''), COALESCE(error_message, ''), published_at,
	        COALESCE(provider_state, ''), COALESCE(container_id, ''),
	provider_idempotency_key, completed_at
	 FROM post_targets
	 WHERE status = 'publishing' AND platform_post_id IS NOT NULL AND platform_post_id <> ''
	 ORDER BY id ASC`

const qSelectPendingTargets = `SELECT pt.id, pt.post_id, pt.platform_account_id, pt.status,
	        COALESCE(pt.platform_post_id, ''), COALESCE(pt.error_message, ''), pt.published_at,
	        COALESCE(pt.provider_state, ''), COALESCE(pt.container_id, ''),
	        pt.provider_idempotency_key, pt.completed_at
	 FROM post_targets pt
	 JOIN posts p ON p.id = pt.post_id
	 WHERE (pt.status = 'queued' OR pt.status = 'waiting_provider')
	   AND (p.publish_at IS NULL OR p.publish_at <= $1)
	 ORDER BY p.publish_at ASC NULLS FIRST`

// qSelectTargetByID — single post_target lookup for the GET
// /api/v1/post-targets/{id} polling endpoint (Taglio 5.1 step 2).
// Returns ALL retry-aware columns the polling frontend needs:
// attempt_count + next_attempt_at are projected as the API-level
// `next_retry_at` + convenience fields alongside the target lifecycle
// data. JSON tag for NextAttemptAt on the Go side stays
// `next_attempt_at`; the API layer renames it to `next_retry_at` via
// postTargetDetailResponse.MarshalJSON (the public-facing name is
// semantically clearer for "when will the platform retry" — see
// pkg/api/posts_handlers.go::handleGetSinglePostTarget).
//
// Posts are NOT joined here on purpose: the API layer needs the
// post_id + workspace_id of the parent post for ownership checks
// (workspace isolation must NOT come from a JOIN) so the handler
// calls PostRepository.FindByID separately. Two round-trips, one
// for security clarity.
const qSelectTargetByID = `SELECT id, post_id, platform_account_id, status,
	        COALESCE(platform_post_id, ''), COALESCE(error_message, ''), published_at,
	        COALESCE(provider_state, ''), COALESCE(container_id, ''),
	provider_idempotency_key, completed_at,
	attempt_count, next_attempt_at
	 FROM post_targets
	 WHERE id = $1`

// --- post_update.go ---

// P1 (migration 053) — qUpdatePost now writes the two privacy columns so
// the editor endpoint can persist a per-post privacy_level override in
// the same atomic UPDATE. order matches insertion above.
const qUpdatePost = `UPDATE posts
 SET title = $1, caption = $2, media_url = $3, publish_at = $4, privacy_level = $5, default_privacy_level = $6, status = $7, media_asset_id = $8, storage_object_key = $9, bucket = $10
 WHERE id = $11 AND workspace_id = $12`

const qUpdateTargetProviderIdempotencyKey = `UPDATE post_targets
 SET provider_idempotency_key = $1
 WHERE id = $2`

const qUpdateTargetStatus = `UPDATE post_targets
 SET status = $1, platform_post_id = $2, error_message = $3, published_at = $4,
     provider_state = $6, container_id = $7
 WHERE id = $5`

const qDeletePost = `DELETE FROM posts WHERE id = $1`

// --- post_schedule.go ---

const qPublishPostUpdateStatus = `UPDATE posts SET status = 'queued' WHERE id = $1`

const qPublishPostTargetsReset = `UPDATE post_targets SET status = 'queued', error_message = '' WHERE post_id = $1`

const qCancelPost = `UPDATE posts SET status = 'draft' WHERE id = $1`

const qRetryPostResetFailedTargets = `UPDATE post_targets SET status = 'queued', error_message = '' WHERE post_id = $1 AND status = 'failed'`

const qRetryTargetResetTarget = `UPDATE post_targets SET status = 'queued', error_message = '' WHERE id = $1`

const qRetryTargetUpdateParent = `UPDATE posts SET status = 'queued' WHERE id = (SELECT post_id FROM post_targets WHERE id = $1)`

const qClaimWaitingProviderTargetSelect = `SELECT id FROM post_targets
 WHERE id = $1 AND status = 'waiting_provider'
 FOR UPDATE SKIP LOCKED`

// --- post_dispatch.go ---

const qClaimQueuedTargetSelect = `SELECT id FROM post_targets
 WHERE id = $1 AND status = 'queued'
 FOR UPDATE SKIP LOCKED`

const qClaimQueuedTargetUpdate = `UPDATE post_targets SET status = 'publishing' WHERE id = $1`

const qClaimQueuedTargetWithLeaseUpdate = `UPDATE post_targets
 SET status = 'publishing',
     lease_owner_id = $2,
     leased_until = NOW() + ($3 || ' seconds')::INTERVAL,
     heartbeat_at = NOW()
 WHERE id = $1`

const qUpdatePublishProgress = `UPDATE post_targets
 SET upload_offset = $3,
     provider_state = $4,
     heartbeat_at = NOW(),
     leased_until = NOW() + ($5 || ' seconds')::INTERVAL
 WHERE id = $1 AND lease_owner_id = $2`

const qReleaseLease = `UPDATE post_targets
 SET lease_owner_id = NULL,
     leased_until = NULL,
     heartbeat_at = NULL
 WHERE id = $1 AND lease_owner_id = $2`

const qMarkDeadLetter = `UPDATE post_targets
 SET status = 'dlq',
     lease_owner_id = NULL,
     leased_until = NULL,
     heartbeat_at = NULL,
     error_message = $3,
     last_error_code = 'DLQ',
     completed_at = NOW()
 WHERE id = $1 AND lease_owner_id = $2`

const qMarkRetrying = `UPDATE post_targets
 SET attempt_count = attempt_count + 1,
     next_retry_at = $3,
     lease_owner_id = NULL,
     leased_until = NULL,
     heartbeat_at = NULL,
     error_message = $4
 WHERE id = $1 AND lease_owner_id = $2`

const qMarkRateLimited = `UPDATE post_targets
 SET next_retry_at = $3,
     rate_limit_reset_at = $3,
     lease_owner_id = NULL,
     leased_until = NULL,
     heartbeat_at = NULL,
     last_error_code = 'RATE_LIMITED'
 WHERE id = $1 AND lease_owner_id = $2`

const qReclaimExpiredLeases = `UPDATE post_targets
 SET status = 'queued',
     lease_owner_id = NULL,
     leased_until = NULL,
     heartbeat_at = NULL,
     next_retry_at = NOW()
 WHERE leased_until IS NOT NULL
   AND leased_until <= NOW()
   AND lease_owner_id IS NOT NULL
   AND lease_owner_id <> $1
   AND status IN ('publishing', 'queued')`

const qClaimPublishingTargetSelect = `SELECT id FROM post_targets
 WHERE id = $1 AND status = 'publishing' AND platform_post_id IS NOT NULL AND platform_post_id <> ''
 FOR UPDATE SKIP LOCKED`
