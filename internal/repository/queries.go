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
	provider_idempotency_key, completed_at, reconcile_attempt, next_reconcile_at
	 FROM post_targets
	 WHERE status = 'publishing'
	   AND platform_post_id IS NOT NULL
   AND platform_post_id <> ''
   AND next_reconcile_at <= NOW()
   AND (reconcile_owner_id IS NULL OR reconcile_until IS NULL OR reconcile_until <= NOW())
 ORDER BY next_reconcile_at ASC, id ASC
	 LIMIT $1`

const qSelectPendingTargets = `SELECT pt.id, pt.post_id, pt.platform_account_id, pt.status,
	        COALESCE(pt.platform_post_id, ''), COALESCE(pt.error_message, ''), pt.published_at,
	        COALESCE(pt.provider_state, ''), COALESCE(pt.container_id, ''),
	        pt.provider_idempotency_key, pt.completed_at
	 FROM post_targets pt
	 JOIN posts p ON p.id = pt.post_id
	 WHERE (pt.status = 'queued' OR pt.status = 'waiting_provider')
	   AND (p.publish_at IS NULL OR p.publish_at <= $1)
	   AND (pt.next_attempt_at IS NULL OR pt.next_attempt_at <= NOW())
	 ORDER BY p.publish_at ASC NULLS FIRST`

// qSelectPendingTargetsFair interleaves children from different parents so
// one post with a large fan-out cannot occupy the entire publish batch. The
// row number is the child position within each parent; ordering by it gives
// round-robin fairness while retaining publish-time and id determinism.
// Note: every COALESCE projection in the CTE must carry an explicit alias —
// Postgres names bare expression columns `coalesce`, which both duplicates
// the column name and breaks the outer SELECT's `platform_post_id` reference.
const qSelectPendingTargetsFair = `WITH pending AS (
	SELECT pt.id, pt.post_id, pt.platform_account_id, pt.status,
	       COALESCE(pt.platform_post_id, '') AS platform_post_id,
	       COALESCE(pt.error_message, '') AS error_message,
	       pt.published_at,
	       COALESCE(pt.provider_state, '') AS provider_state,
	       COALESCE(pt.container_id, '') AS container_id,
	       pt.provider_idempotency_key, pt.completed_at,
	       p.publish_at,
	       ROW_NUMBER() OVER (PARTITION BY pt.post_id ORDER BY pt.id ASC) AS child_position
	FROM post_targets pt
	JOIN posts p ON p.id = pt.post_id
	WHERE pt.status IN ('queued', 'waiting_provider', 'retrying')
	  AND (p.publish_at IS NULL OR p.publish_at <= $1)
	  AND (pt.next_attempt_at IS NULL OR pt.next_attempt_at <= NOW())
)
SELECT id, post_id, platform_account_id, status,
       platform_post_id, error_message, published_at,
       provider_state, container_id, provider_idempotency_key, completed_at
FROM pending
ORDER BY child_position ASC, publish_at ASC NULLS FIRST, post_id ASC, id ASC
LIMIT 100`

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

// Aggregate-status queries are kept together so the target transition and
// the reconciler repair sweep use the same locking and projection contract.
const qSelectPostIDByTarget = `SELECT post_id FROM post_targets WHERE id = $1`

const qLockTargetForAggregate = `SELECT id FROM post_targets WHERE id = $1 FOR UPDATE`

const qLockPostForAggregate = `SELECT id FROM posts WHERE id = $1 FOR UPDATE`

const qLockPostStatusForAggregate = `SELECT status FROM posts WHERE id = $1 FOR UPDATE`

const qSelectTargetStatusesByPost = `SELECT status FROM post_targets WHERE post_id = $1 ORDER BY id ASC`

const qSelectTargetStatusByID = `SELECT status FROM post_targets WHERE id = $1`

const qSelectTargetIDsByPost = `SELECT id FROM post_targets WHERE post_id = $1 ORDER BY id ASC FOR UPDATE`

const qSelectDirtyAggregatePostIDs = `SELECT post_id
 FROM post_aggregate_repair_queue
 ORDER BY queued_at ASC, post_id ASC
 LIMIT $1`

const qLockDirtyAggregatePost = `SELECT post_id
 FROM post_aggregate_repair_queue
 WHERE post_id = $1
 FOR UPDATE`

const qDeleteDirtyAggregatePost = `DELETE FROM post_aggregate_repair_queue WHERE post_id = $1`

const qSelectExpiredLeaseTargets = `SELECT id, post_id FROM post_targets
 WHERE leased_until IS NOT NULL
   AND leased_until <= NOW()
   AND lease_owner_id IS NOT NULL
   AND lease_owner_id <> $1
   AND status IN ('publishing', 'queued')
 ORDER BY id ASC
 FOR UPDATE SKIP LOCKED`

const qReclaimExpiredLeaseByID = `UPDATE post_targets
 SET status = 'queued',
     lease_owner_id = NULL,
     leased_until = NULL,
     heartbeat_at = NULL,
     next_retry_at = NOW()
 WHERE id = $1
   AND lease_owner_id IS NOT NULL
   AND lease_owner_id <> $2
   AND status IN ('publishing', 'queued')`

const qUpdatePostAggregateStatus = `UPDATE posts SET status = $1 WHERE id = $2`

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
 WHERE id = $5
   AND (status = $1 OR status NOT IN ('published', 'partially_published', 'failed', 'dlq'))`

const qUpdateTargetStatusWithLease = `UPDATE post_targets
 SET status = $1, platform_post_id = $2, error_message = $3, published_at = $4,
     provider_state = $6, container_id = $7,
     lease_owner_id = CASE WHEN $1 = 'publishing' THEN lease_owner_id ELSE NULL END,
     leased_until = CASE WHEN $1 = 'publishing' THEN leased_until ELSE NULL END,
     heartbeat_at = CASE WHEN $1 = 'publishing' THEN heartbeat_at ELSE NULL END
 WHERE id = $5
   AND lease_owner_id = $8
   AND status = 'publishing'`

// qUpdateTargetStatusWithReconcileLease is the reconciler terminal CAS.
// A stale replica cannot write after its lease expires, even if a successor
// has not claimed the row yet. Terminal transitions release the lease.
const qUpdateTargetStatusWithReconcileLease = `UPDATE post_targets
 SET status = $1, platform_post_id = $2, error_message = $3, published_at = $4,
     provider_state = $6, container_id = $7,
     reconcile_owner_id = NULL, reconcile_until = NULL, reconcile_heartbeat_at = NULL
 WHERE id = $5
   AND status = 'publishing'
   AND reconcile_owner_id = $8
   AND reconcile_until > NOW()`

const qDeletePost = `DELETE FROM posts WHERE id = $1`

// --- post_schedule.go ---

const qPublishPostUpdateStatus = `UPDATE posts SET status = 'queued' WHERE id = $1`

const qPublishPostTargetsReset = `UPDATE post_targets SET status = 'queued', error_message = '' WHERE post_id = $1`

// Cancelling resets only non-terminal targets. Terminal targets are preserved
// so a parent cannot be made draft while a destination is already published,
// failed, DLQ'd, or blocked on authentication; the shared aggregate resolver
// derives the resulting posts.status in the same transaction.
const qCancelPostTargetsReset = `UPDATE post_targets
 SET status = 'draft', error_message = ''
WHERE post_id = $1
  AND status NOT IN ('published', 'partially_published', 'failed', 'dlq')`

// qCancelFutureJobsForAccount resets every non-terminal post target of an
// account to 'draft' so scheduled/future jobs stop being publishable after a
// disconnect. RETURNING post_id feeds the parent aggregate recompute.
const qCancelFutureJobsForAccount = `UPDATE post_targets
    SET status = 'draft', error_message = ''
  WHERE platform_account_id = $1
    AND status NOT IN ('published', 'partially_published', 'failed', 'dlq')
  RETURNING post_id`

const qRetryPostResetFailedTargets = `UPDATE post_targets SET status = 'queued', error_message = '' WHERE post_id = $1 AND status = 'failed'`

const qRetryTargetResetTarget = `UPDATE post_targets SET status = 'queued', error_message = '' WHERE id = $1 AND status = 'failed'`

const qRetryTargetUpdateParent = `UPDATE posts SET status = 'queued' WHERE id = (SELECT post_id FROM post_targets WHERE id = $1)`

const qClaimWaitingProviderTargetSelect = `SELECT id FROM post_targets
 WHERE id = $1 AND status = 'waiting_provider'
 FOR UPDATE SKIP LOCKED`

// --- post_dispatch.go ---

const qClaimQueuedTargetSelect = `SELECT id FROM post_targets
 WHERE id = $1 AND status = 'queued'
 FOR UPDATE SKIP LOCKED`

const qClaimQueuedTargetUpdate = `UPDATE post_targets SET status = 'publishing' WHERE id = $1`

const qClaimQueuedTargetWithLeaseSelect = `SELECT id FROM post_targets
 WHERE id = $1
   AND status IN ('queued', 'waiting_provider', 'retrying')
   AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())
   AND (next_retry_at IS NULL OR next_retry_at <= NOW())
 FOR UPDATE SKIP LOCKED`

const qClaimQueuedTargetWithLeaseUpdate = `UPDATE post_targets
 SET status = 'publishing',
     lease_owner_id = $2,
     leased_until = NOW() + ($3 || ' seconds')::INTERVAL,
     heartbeat_at = NOW()
 WHERE id = $1
   AND status IN ('queued', 'waiting_provider', 'retrying')`

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

// qMarkRateLimitedRetry (OPEN GAP closure — see ARCHITECTURE.md §Rate
// limiting (d)) requeues a claimed target after the platform answered
// the FINAL publish call with 429/Retry-After. Unlike qMarkRetrying /
// qMarkRateLimited this does NOT CAS on lease_owner_id: the publish
// driver claims via ClaimQueuedTarget (no lease stamp), so a
// lease-CAS UPDATE would silently match zero rows and strand the row
// in 'publishing'. The `status = 'publishing'` guard plays the same
// ownership role — only the claim winner's row is in 'publishing'.
//
// status returns to 'queued' so the existing ListPending /
// ClaimQueuedTarget pickup path re-picks it once next_attempt_at
// elapses (ListPending filters next_attempt_at <= NOW()).
// attempt_count is bumped so the retry budget stays bounded.
const qMarkRateLimitedRetry = `UPDATE post_targets
 SET status = 'queued',
     attempt_count = attempt_count + 1,
     next_attempt_at = $2,
     rate_limit_reset_at = $2,
     error_message = $3,
     last_error_code = 'RATE_LIMITED'
 WHERE id = $1 AND status = 'publishing'`

const qMarkRateLimitedRetryWithLease = `UPDATE post_targets
 SET status = 'queued',
     attempt_count = attempt_count + 1,
     next_attempt_at = $3,
     rate_limit_reset_at = $3,
     error_message = $4,
     last_error_code = 'RATE_LIMITED',
     lease_owner_id = NULL,
     leased_until = NULL,
     heartbeat_at = NULL
 WHERE id = $1 AND lease_owner_id = $2 AND status = 'publishing'`

const qMarkRetrying = `UPDATE post_targets
 SET status = 'retrying',
     attempt_count = attempt_count + 1,
     next_retry_at = $3,
     next_attempt_at = $3,
     lease_owner_id = NULL,
     leased_until = NULL,
     heartbeat_at = NULL,
     error_message = $4
 WHERE id = $1 AND lease_owner_id = $2`

const qMarkRateLimited = `UPDATE post_targets
 SET status = 'queued',
     next_retry_at = $3,
     next_attempt_at = $3,
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
   AND status IN ('publishing', 'queued')
 RETURNING post_id`

// qClaimPublishingTargetSelect atomically claims one due publishing target.
// The CTE's FOR UPDATE SKIP LOCKED prevents replicas from waiting on or
// selecting the same row; the UPDATE stamps durable ownership before commit.
const qClaimPublishingTargetSelect = `WITH candidate AS (
 SELECT id
 FROM post_targets
 WHERE id = $1
   AND status = 'publishing'
   AND platform_post_id IS NOT NULL
   AND platform_post_id <> ''
   AND next_reconcile_at <= NOW()
   AND (reconcile_owner_id IS NULL OR reconcile_until IS NULL OR reconcile_until <= NOW())
 FOR UPDATE SKIP LOCKED
)
UPDATE post_targets pt
 SET reconcile_owner_id = $2,
     reconcile_until = NOW() + ($3 || ' seconds')::INTERVAL,
     reconcile_heartbeat_at = NOW()
 FROM candidate
 WHERE pt.id = candidate.id
 RETURNING pt.id`

const qHeartbeatReconcileTarget = `UPDATE post_targets
 SET reconcile_until = NOW() + ($3 || ' seconds')::INTERVAL,
     reconcile_heartbeat_at = NOW()
 WHERE id = $1
   AND reconcile_owner_id = $2
   AND reconcile_until > NOW()
   AND status = 'publishing'`

const qReleaseReconcileTarget = `UPDATE post_targets
 SET reconcile_owner_id = NULL,
     reconcile_until = NULL,
     reconcile_heartbeat_at = NULL
 WHERE id = $1
   AND reconcile_owner_id = $2
   AND reconcile_until > NOW()
   AND status = 'publishing'`

const qScheduleNextReconcile = `UPDATE post_targets
 SET reconcile_attempt = reconcile_attempt + 1,
     next_reconcile_at = $2,
     reconcile_owner_id = NULL,
     reconcile_until = NULL,
     reconcile_heartbeat_at = NULL
 WHERE id = $1
   AND reconcile_attempt = $3
   AND status = 'publishing'
   AND platform_post_id IS NOT NULL
   AND platform_post_id <> ''
   AND reconcile_owner_id = $4
   AND reconcile_until > NOW()`
