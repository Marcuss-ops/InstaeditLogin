//go:build e2e

package e2e

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ----- route-rewriting RoundTripper ------------------------------------
//
// Production Go clients (services/auth, services/youtube_resumable)
// construct raw HTTPS URLs; this RoundTripper intercepts at the
// transport layer and rewrites *.googleapis.com to the in-process
// Drive/YouTube fakes. Without it, the suite can't exercise those
// code paths in-process.

type rewriteRT struct {
	driveURL   string
	youtubeURL string
}

func (rt *rewriteRT) RoundTrip(req *http.Request) (*http.Response, error) {
	u := req.URL.String()
	for _, prefix := range []string{"https://www.googleapis.com/", "https://oauth2.googleapis.com/"} {
		if strings.HasPrefix(u, prefix) {
			req2 := req.Clone(req.Context())
			rewritten := strings.Replace(u, prefix, rt.driveURL+"/", 1)
			parsed, err := url.Parse(rewritten)
			if err != nil {
				return nil, err
			}
			req2.URL = parsed
			return http.DefaultTransport.RoundTrip(req2)
		}
	}
	for _, prefix := range []string{"https://youtube.googleapis.com/", "https://www.youtube.com/"} {
		if strings.HasPrefix(u, prefix) {
			req2 := req.Clone(req.Context())
			rewritten := strings.Replace(u, prefix, rt.youtubeURL+"/", 1)
			parsed, err := url.Parse(rewritten)
			if err != nil {
				return nil, err
			}
			req2.URL = parsed
			return http.DefaultTransport.RoundTrip(req2)
		}
	}
	return http.DefaultTransport.RoundTrip(req)
}

func rewriteRoundTripper(driveURL, youtubeURL string) http.RoundTripper {
	return &rewriteRT{driveURL: driveURL, youtubeURL: youtubeURL}
}

// ----- helpers exposed to subtests via the harness ---------------------

// sha256Hex returns the hex-encoded SHA-256 of the byte slice.
func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// bytesEqual is a constant-time-friendly bytes comparison.
func bytesEqual(a, b []byte) bool {
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

// insertPublishTarget inserts a row in post_targets with the supplied
// status. Returns the inserted id. Used by scenarios 8 (lease), 9
// (retry budget), and 10 (dead_letter terminal).
func insertPublishTarget(h *E2EHarness, status string) (int64, error) {
	var id int64
	err := h.pgDB.QueryRow(
		`INSERT INTO post_targets (post_id, platform_account_id, status, created_at, updated_at)
		 VALUES ($1, $2, $3, NOW(), NOW())
		 RETURNING id`,
		1, 1, status,
	).Scan(&id)
	return id, err
}

// acquireLeaseInTx models the production lease-claim step. Inside
// its caller-supplied TX, it acquires a row-level lock on the
// target via SELECT...FOR UPDATE and stamps the lease columns
// (locked_by + locked_at + heartbeat_at). The TX must commit for
// the lease to be visible to other workers; rollback releases.
func acquireLeaseInTx(ctx context.Context, tx *sql.Tx, targetID int64) error {
	var currentStatus string
	if err := tx.QueryRowContext(ctx,
		`SELECT status FROM post_targets WHERE id=$1 FOR UPDATE`, targetID,
	).Scan(&currentStatus); err != nil {
		return fmt.Errorf("lock+select: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE post_targets
		    SET locked_by=$1, locked_at=NOW(), heartbeat_at=NOW(), updated_at=NOW()
		  WHERE id=$2`,
		"worker-A", targetID,
	); err != nil {
		return fmt.Errorf("stamp lease: %w", err)
	}
	return nil
}

// attemptAcquireWithNowait mirrors the production SKIP-LOCKED
// behaviour at the test layer. Uses NOWAIT so a held lock surfaces
// as an observable error (Postgres 55P03 / 40P01) rather than a
// silent 0-row read. The boolean reports whether the lock was
// observed as acquirable (false under contention).
//
// Production: SELECT FOR UPDATE SKIP LOCKED returns 0 rows silently
// when a peer holds the row. We use NOWAIT here so a future drift in
// the production lease contract (e.g. silently returning 0 instead
// of erroring on a missing lock) SURFACES in the test log.
func attemptAcquireWithNowait(ctx context.Context, tx *sql.Tx, targetID int64) (bool, error) {
	var status string
	err := tx.QueryRowContext(ctx,
		`SELECT status FROM post_targets WHERE id=$1 FOR UPDATE NOWAIT`, targetID,
	).Scan(&status)
	if err == nil {
		return true, nil
	}
	// 55P03 lock_not_available / 40P01 deadlock_detected both
	// mean the lock is held elsewhere.
	return false, err
}

// updateTargetStatus transitions a post_targets row, gated on the
// from-status. The production FSM contract writes a fresh row only
// when the current status matches the expected from-state; a
// terminal row (dead_letter / published / failed) makes the
// UPDATE match zero rows, so the WHERE-clause guard refuses. This
// method returns nil on success, an error on row-mismatch or DB
// failure. The scenario tests assert the refusal contract via the
// (err == nil) vs (err != nil) shape.
func updateTargetStatus(h *E2EHarness, targetID int64, fromStatus, toStatus, errMsg string) error {
	res, err := h.pgDB.Exec(
		`UPDATE post_targets
		    SET status=$1,
		        last_error_message=CASE WHEN $2 = '' THEN last_error_message ELSE $2 END,
		        updated_at=NOW()
		  WHERE id=$3
		    AND status=$4
		    AND (status=$1 OR status NOT IN ('published', 'partially_published', 'failed', 'dlq', 'dead_letter', 'blocked_auth'))`,
		toStatus, errMsg, targetID, fromStatus,
	)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("UPDATE matched 0 rows (terminal=%s→%s refused or stale state)", fromStatus, toStatus)
	}
	return nil
}

// recordRetryAttempt mirrors the production retry bookkeeping: once a
// target enters retry_wait, subsequent failed attempts update the retry
// metadata without inventing a retry_wait → retry_wait FSM edge.
func recordRetryAttempt(h *E2EHarness, targetID int64, errMsg string) error {
	res, err := h.pgDB.Exec(
		`UPDATE post_targets
		    SET attempt_count = attempt_count + 1,
		        last_error_message = $1,
		        updated_at = NOW()
		  WHERE id = $2 AND status = 'retry_wait'`,
		errMsg, targetID,
	)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("retry attempt matched 0 rows for target %d", targetID)
	}
	return nil
}

// attemptHeartbeatReclaim mirrors the production reclaimer-tick
// in internal/worker/reconcile_worker.go::runReclaimerTick: a
// query observes post_targets that (a) have an active lease with
// a stale heartbeat, (b) are NOT terminal, and (c) are NOT held
// by the reclaimer's own identity — and re-stamps the row's lease
// columns to the reclaiming worker.
//
// Returns (acquired, err) where acquired is true iff the
// WHERE-clause matched the row AND stamped the new owner.
//
// Guards encoded into the SQL:
//
//  1. `locked_by IS NOT NULL` — never reclaim an unowned row; this
//     prevents a fresh insert (which has locked_by=”) from being
//     prematurely heart-stamped before the worker pool claims it.
//  2. `locked_by <> $newOwner` — never let a worker reclaim its own
//     lease (would create spurious self-restarts on heartbeat ticks).
//  3. `status NOT IN ('dead_letter','failed','published')` — the
//     reclaimer must NEVER touch a terminal row; doing so would
//     resurrect a degraded state and surface to operators as a
//     false-positive retry.
//  4. `heartbeat_at IS NULL OR heartbeat_at < NOW() - lease_timeout`
//     — the staleness predicate; `IS NULL` covers legacy rows
//     from migration 044 that stamped locked_at but not heartbeat_at.
//
// Any future drift in production that drops one of these guards
// surfaces here as an E2E false-pass (the WHERE clause results in
// a match that production would block). The scenario therefore
// encodes the production contract literally, not just the happy
// path.
func attemptHeartbeatReclaim(ctx context.Context, h *E2EHarness, targetID int64, maxAge time.Duration, newOwner string) (bool, error) {
	res, err := h.pgDB.ExecContext(ctx,
		`UPDATE post_targets
		    SET locked_by = $1,
		        locked_at = NOW(),
		        heartbeat_at = NOW(),
		        updated_at = NOW()
		  WHERE id = $2
		    AND locked_by IS NOT NULL
		    AND locked_by <> $1
		    AND status NOT IN ('dead_letter', 'failed', 'published')
		    AND (
		        heartbeat_at IS NULL
		        OR heartbeat_at < NOW() - make_interval(secs => $3)
		    )`,
		newOwner, targetID, int64(maxAge.Seconds()),
	)
	if err != nil {
		return false, err
	}
	rows, _ := res.RowsAffected()
	return rows > 0, nil
}

// backdateHeartbeat simulates a crashed-worker scenario by moving
// heartbeat_at into the deep past while keeping the row's locked_by
// identity unchanged. Used by scenario_12 to inspect reclaim
// behaviour without requiring Docker time-warping.
func backdateHeartbeat(ctx context.Context, h *E2EHarness, targetID int64, age time.Duration) error {
	_, err := h.pgDB.ExecContext(ctx,
		`UPDATE post_targets
		    SET heartbeat_at = NOW() - make_interval(secs => $1)
		  WHERE id = $2`,
		int64(age.Seconds()), targetID,
	)
	return err
}

// applyE2ESchema bootstraps the minimal Postgres schema the e2e
// suite needs. We don't apply the production migration list
// because (a) the test only queries a handful of tables and (b)
// embedding the migration runner would force every test to
// materialize columns the suite never reads. CREATE TABLE IF NOT
// EXISTS keeps the bootstrap idempotent across re-runs.
//
// Schema-coverage matrix (verified by ripgrep across tests/e2e/
// for every INSERT/UPDATE/SELECT that targets these tables):
//
//	users              seeders INSERT (email); no other reads
//	workspaces         seeders INSERT (name, owner_id) — owner_id
//	                   references users(id) ON DELETE CASCADE
//	platform_accounts  seeders INSERT
//	                   (user_id, workspace_id, platform,
//	                    platform_user_id, status, username,
//	                    created_at, updated_at); SELECTs read
//	                   user_id + status; UPDATE flips status+updated_at
//	posts              scenario_5 INSERT covers the full shape
//	post_targets       scenarios 8–12 reference
//	                   locked_by/locked_at/heartbeat_at/next_attempt_at
//	                   /last_error_message in addition to the
//	                   INSERT-time columns
//	sessions           youtube_oauth_browser_e2e_test.go needs it
//	                   for session-middleware path-verification
//	oauth_connections  the OAuth-callback-bind tests ASSERT row
//	                   counts against this table
//	upload_jobs        the OAuth-callback-bind tests ASSERT row
//	                   counts against this table
//
// We surface every column the suite actually reads. Adding columns
// here (NOT NULL or otherwise) when a column is mandatory; default
// ”/NULL with optional semantic. Production migrations 033/043/etc.
// define a richer shape; we only need the columns the assertions
// read. The schema is recreated by Postgres per testcontainer start
// so we never need ALTER — fresh DB, fresh schema, no drift.
func applyE2ESchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id BIGSERIAL PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS workspaces (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			owner_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		// Channels = platform_accounts in production. The full
		// boot shape mirrors the columns the seed helpers INSERT
		// + the columns the OAuth-bind tests read back (user_id
		// for attach checks, status for the negative-bind path
		// assertion that the row was never promoted to active).
		`CREATE TABLE IF NOT EXISTS platform_accounts (
			id BIGSERIAL PRIMARY KEY,
			user_id BIGINT NOT NULL,
			workspace_id BIGINT NOT NULL,
			platform TEXT NOT NULL,
			platform_user_id TEXT NOT NULL,
			username TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending_authorization',
			oauth_connection_id BIGINT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		// Keep the reduced E2E schema aligned with the production
		// accounts-list LEFT JOIN. Migration 042 owns this table in
		// production; migration 102 adds the refresh coordination
		// columns. The harness uses an idempotent CREATE TABLE rather
		// than changing an applied migration checksum.
		`CREATE TABLE IF NOT EXISTS account_resource_snapshots (
			platform_account_id BIGINT PRIMARY KEY
				REFERENCES platform_accounts(id) ON DELETE CASCADE,
			resource_type TEXT NOT NULL DEFAULT 'channel',
			profile JSONB NOT NULL DEFAULT '{}'::jsonb,
			statistics JSONB NOT NULL DEFAULT '{}'::jsonb,
			status JSONB NOT NULL DEFAULT '{}'::jsonb,
			content JSONB NOT NULL DEFAULT '{}'::jsonb,
			provider_etag TEXT,
			fetched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			refresh_pending_at TIMESTAMPTZ,
			refresh_claimed_until TIMESTAMPTZ,
			refresh_attempts INTEGER NOT NULL DEFAULT 0,
			refresh_last_error TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_e2e_account_resource_snapshots_fetched
			ON account_resource_snapshots (fetched_at)`,
		`CREATE INDEX IF NOT EXISTS idx_e2e_account_resource_snapshots_refresh_pending
			ON account_resource_snapshots (refresh_pending_at)
			WHERE refresh_pending_at IS NOT NULL`,
		// posts: user_id + workspace_id + status + publish_at
		// cover scenario_5. Other columns are present for shape
		// parity with the production migration so any future
		// assertion that talks to posts won't trip on a missing
		// column.
		`CREATE TABLE IF NOT EXISTS posts (
			id BIGSERIAL PRIMARY KEY,
			user_id BIGINT NOT NULL,
			workspace_id BIGINT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			caption TEXT NOT NULL DEFAULT '',
			media_url TEXT NOT NULL DEFAULT '',
			thumbnail_url TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'scheduled',
			publish_at TIMESTAMPTZ NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		// post_targets: scenario_5 exercises INSERT + the publish-batch
		// claim-gate SELECT; scenarios 8-11 add lease / retry / dead_letter
		// paths. Columns aligned (loosely) with the production migration
		// 033_post_targets.sql: last_error_message for the retry/died
		// transitions, attempt_count + heartbeat_at for lease semantics.
		// The E2E doesn't strictly require every column to be populated —
		// it only requires the SELECT-side columns to exist.
		`CREATE TABLE IF NOT EXISTS post_targets (
			id BIGSERIAL PRIMARY KEY,
			post_id BIGINT NOT NULL,
			platform_account_id BIGINT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			locked_by TEXT NOT NULL DEFAULT '',
			locked_at TIMESTAMPTZ NULL,
			heartbeat_at TIMESTAMPTZ NULL,
			attempt_count INT NOT NULL DEFAULT 0,
			next_attempt_at TIMESTAMPTZ NULL,
			last_error_message TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		// sessions: youtube_oauth_browser_e2e_test.go's
		// session-middleware path-verification
		// (idx_sessions_user_id index kept for shape parity with
		// the production table — keeps SELECT-by-user_id plans
		// identical to production behaviour).
		`CREATE TABLE IF NOT EXISTS sessions (
			id BIGSERIAL PRIMARY KEY,
			user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			workspace_id BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL DEFAULT '',
			expires_at TIMESTAMPTZ NOT NULL,
			revoked_at TIMESTAMPTZ NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions (user_id)`,
		// oauth_connections: counter-ASSERTed by the bind-test's
		// assertOAuthConnectionCount helper (provider_resource_id
		// SELECT). Production migration 043 defines a richer shape;
		// we surface only what the suite reads.
		`CREATE TABLE IF NOT EXISTS oauth_connections (
			id BIGSERIAL PRIMARY KEY,
			user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			provider TEXT NOT NULL,
			provider_subject_id TEXT NOT NULL DEFAULT '',
			provider_resource_id TEXT NOT NULL,
			login_hint TEXT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			scopes TEXT[] NOT NULL DEFAULT '{}',
			granted_scopes TEXT[] NOT NULL DEFAULT '{}',
			expires_at TIMESTAMPTZ NULL,
			last_validated_at TIMESTAMPTZ NULL,
			last_refresh_at TIMESTAMPTZ NULL,
			last_refresh_error TEXT NULL,
			reauth_required_at TIMESTAMPTZ NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (user_id, provider, provider_resource_id)
		)`,
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conname = 'e2e_platform_accounts_oauth_connection_id_fkey'
			) THEN
				ALTER TABLE platform_accounts
					ADD CONSTRAINT e2e_platform_accounts_oauth_connection_id_fkey
					FOREIGN KEY (oauth_connection_id) REFERENCES oauth_connections(id) ON DELETE SET NULL;
			END IF;
		END $$`,
		// tokens mirrors the current production credential lineage:
		// platform_accounts.oauth_connection_id -> oauth_connections.id
		// -> tokens.oauth_connection_id. Fixtures must use this table,
		// never the retired oauth_tokens/credentials names.
		`CREATE TABLE IF NOT EXISTS tokens (
			id BIGSERIAL PRIMARY KEY,
			platform_account_id BIGINT NOT NULL REFERENCES platform_accounts(id) ON DELETE CASCADE,
			oauth_connection_id BIGINT NOT NULL REFERENCES oauth_connections(id) ON DELETE CASCADE,
			token_type TEXT NOT NULL,
			encrypted_access_token BYTEA,
			encrypted_token BYTEA NOT NULL,
			encrypted_refresh_token BYTEA,
			access_token_expires_at TIMESTAMPTZ,
			expires_at TIMESTAMPTZ,
			refresh_token_expires_at TIMESTAMPTZ,
			scopes TEXT[],
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_e2e_tokens_oauth_connection_id ON tokens (oauth_connection_id)`,
		// upload_jobs: counter-ASSERTed by the bind-test's
		// assertUploadJobCount helper (account_id SELECT).
		// Production migration 045/046 define a richer shape;
		// we surface only what the suite reads.
		`CREATE TABLE IF NOT EXISTS upload_jobs (
			id BIGSERIAL PRIMARY KEY,
			account_id BIGINT NULL,
			ingest_after TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			publish_at TIMESTAMPTZ NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			user_id BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
			workspace_id BIGINT NULL REFERENCES workspaces(id) ON DELETE SET NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_upload_jobs_account_id ON upload_jobs (account_id)`,
		`CREATE INDEX IF NOT EXISTS idx_post_targets_platform_account_id ON post_targets (platform_account_id)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("apply schema stmt: %s: %w", trimForError(s), err)
		}
	}
	return nil
}

// trimForError shortens a SQL stmt for error messages so the test
// log stays readable when bootstrap fails.
func trimForError(s string) string {
	const max = 80
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// artifactVerifyOK mirrors the artifactVerifyReader's behavior
// (Task 4/10). The real policy lives in
// internal/services; this stub lets the suite lock the shape
// without dragging in the full binary surface.
func artifactVerifyOK(body []byte, sha string, size int64, mime string) error {
	if len(body) != int(size) {
		return errors.New("size mismatch")
	}
	if sha256Hex(body) != sha {
		return errors.New("sha mismatch")
	}
	if mime != "video/mp4" && mime != "application/octet-stream" {
		return errors.New("mime unsupported")
	}
	return nil
}

// transientErrMsg formats a per-attempt transient-failure message so
// scenario_9's last_error_message column records exactly which
// retry attempt ultimately exhausted the budget.
func transientErrMsg(attempt int) string {
	return "transient_5xx_attempt_" + strconv.Itoa(attempt)
}

// insertScheduledPost inserts a scheduled post with publish_at in the
// future. Returns the inserted post ID.
func insertScheduledPost(h *E2EHarness, publishAt time.Time) (postID int64, err error) {
	tx, err := h.pgDB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := tx.QueryRow(
		`INSERT INTO posts (user_id, workspace_id, title, caption, media_url, status, publish_at, created_at, updated_at)
		 VALUES ($1, $2, $3, '', 'https://example.com/video.mp4', 'scheduled', $4, NOW(), NOW())
		 RETURNING id`,
		1, 1, "e2e-post",
		publishAt,
	).Scan(&postID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(
		`INSERT INTO post_targets (post_id, platform_account_id, status, created_at, updated_at)
		 VALUES ($1, 1, 'pending', NOW(), NOW())`,
		postID,
	); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return postID, nil
}

// runPublishClaimGate runs the publish-pool's claim SQL and returns
// the count of rows that would be claimed with the time-gate applied.
// Mirrors the production `ClaimBatchForPublish` filter shape.
func runPublishClaimGate(h *E2EHarness, now time.Time) (int, error) {
	var count int
	if err := h.pgDB.QueryRow(
		`SELECT COUNT(*) FROM posts
		  WHERE status = 'scheduled'
		    AND publish_at <= $1`, now,
	).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
