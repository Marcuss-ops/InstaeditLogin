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

const qInsertPost = `INSERT INTO posts (workspace_id, title, caption, media_url, scheduled_at, status)
 VALUES ($1, $2, $3, $4, $5, $6)
 RETURNING id, created_at`

const qInsertPostTarget = `INSERT INTO post_targets (post_id, platform_account_id, status)
 VALUES ($1, $2, $3)
 RETURNING id`

const qInsertOutboxEvent = `INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
 VALUES ($1, $2, $3, $4::jsonb)`

// --- post_query.go ---

const qSelectPostByID = `SELECT id, workspace_id, title, caption, media_url, scheduled_at, status, created_at
 FROM posts
 WHERE id = $1`

const qSelectPostsByWorkspace = `SELECT id, workspace_id, title, caption, media_url, scheduled_at, status, created_at
 FROM posts
 WHERE workspace_id = $1
 ORDER BY created_at DESC`

const qSelectQueuedPosts = `SELECT id, workspace_id, title, caption, media_url, scheduled_at, status, created_at
 FROM posts
 WHERE status = 'queued' AND scheduled_at <= $1
 ORDER BY scheduled_at ASC`

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
		 WHERE (pt.status = 'queued' OR pt.status = 'waiting_provider') AND p.scheduled_at <= $1
		 ORDER BY p.scheduled_at ASC`

// --- post_update.go ---

const qUpdatePost = `UPDATE posts
 SET title = $1, caption = $2, media_url = $3, scheduled_at = $4, status = $5
 WHERE id = $6 AND workspace_id = $7`

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
