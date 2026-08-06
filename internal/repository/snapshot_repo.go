package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// AccountResourceSnapshot represents a cached snapshot of a remote
// platform resource (channel stats, profile, branding). Stored in
// account_resource_snapshots and refreshed asynchronously when stale.
type AccountResourceSnapshot struct {
	PlatformAccountID int64
	ResourceType      string
	Profile           map[string]interface{}
	Statistics        map[string]interface{}
	Status            map[string]interface{}
	Content           map[string]interface{}
	ProviderETag      string
	FetchedAt         time.Time
	UpdatedAt         time.Time
}

// SnapshotRepository handles CRUD for account_resource_snapshots.
type SnapshotRepository struct {
	db *sql.DB
}

func NewSnapshotRepository(db *sql.DB) *SnapshotRepository {
	return &SnapshotRepository{db: db}
}

// GetSnapshot returns the cached snapshot for a platform account, or nil
// if no snapshot exists.
func (r *SnapshotRepository) GetSnapshot(platformAccountID int64) (*AccountResourceSnapshot, error) {
	row := r.db.QueryRow(
		`SELECT platform_account_id, resource_type, profile, statistics, status, content,
		        provider_etag, fetched_at, updated_at
		 FROM account_resource_snapshots
		 WHERE platform_account_id = $1`,
		platformAccountID,
	)

	snap := &AccountResourceSnapshot{}
	var profileJSON, statsJSON, statusJSON, contentJSON []byte
	err := row.Scan(
		&snap.PlatformAccountID, &snap.ResourceType,
		&profileJSON, &statsJSON, &statusJSON, &contentJSON,
		&snap.ProviderETag, &snap.FetchedAt, &snap.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get snapshot: %w", err)
	}
	if err := decodeSnapshotJSON(snap, profileJSON, statsJSON, statusJSON, contentJSON); err != nil {
		return nil, err
	}
	return snap, nil
}

func decodeSnapshotJSON(snap *AccountResourceSnapshot, profileJSON, statsJSON, statusJSON, contentJSON []byte) error {
	if err := json.Unmarshal(profileJSON, &snap.Profile); err != nil {
		return fmt.Errorf("unmarshal profile: %w", err)
	}
	if err := json.Unmarshal(statsJSON, &snap.Statistics); err != nil {
		return fmt.Errorf("unmarshal statistics: %w", err)
	}
	if err := json.Unmarshal(statusJSON, &snap.Status); err != nil {
		return fmt.Errorf("unmarshal status: %w", err)
	}
	if err := json.Unmarshal(contentJSON, &snap.Content); err != nil {
		return fmt.Errorf("unmarshal content: %w", err)
	}
	return nil
}

// UpsertSnapshot creates or updates a snapshot. A successful refresh clears
// both the pending marker and the claim lease atomically.
func (r *SnapshotRepository) UpsertSnapshot(snap *AccountResourceSnapshot) error {
	profileJSON, err := json.Marshal(snap.Profile)
	if err != nil {
		return fmt.Errorf("marshal profile: %w", err)
	}
	statsJSON, err := json.Marshal(snap.Statistics)
	if err != nil {
		return fmt.Errorf("marshal statistics: %w", err)
	}
	statusJSON, err := json.Marshal(snap.Status)
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}
	contentJSON, err := json.Marshal(snap.Content)
	if err != nil {
		return fmt.Errorf("marshal content: %w", err)
	}

	_, err = r.db.Exec(
		`INSERT INTO account_resource_snapshots
		    (platform_account_id, resource_type, profile, statistics, status, content,
		     provider_etag, fetched_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		 ON CONFLICT (platform_account_id) DO UPDATE SET
		    resource_type = EXCLUDED.resource_type,
		    profile       = EXCLUDED.profile,
		    statistics    = EXCLUDED.statistics,
		    status        = EXCLUDED.status,
		    content       = EXCLUDED.content,
		    provider_etag = EXCLUDED.provider_etag,
		    fetched_at    = EXCLUDED.fetched_at,
		    updated_at    = NOW(),
		    refresh_pending_at = NULL,
		    refresh_claimed_until = NULL,
		    refresh_attempts = 0,
		    refresh_last_error = NULL`,
		snap.PlatformAccountID, snap.ResourceType,
		profileJSON, statsJSON, statusJSON, contentJSON,
		snap.ProviderETag, snap.FetchedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert snapshot: %w", err)
	}
	return nil
}

// MarkSnapshotRefreshPending durably enqueues an account. It also creates a
// placeholder row for accounts that have never been fetched, which makes the
// queue claimable and prevents every worker tick from selecting the same
// missing row indefinitely.
func (r *SnapshotRepository) MarkSnapshotRefreshPending(platformAccountID int64, now time.Time) error {
	return r.MarkSnapshotsRefreshPending([]int64{platformAccountID}, now)
}

// MarkSnapshotsRefreshPending enqueues multiple accounts with one SQL
// statement. The read path uses this to keep account-list refresh marking
// constant in round-trips rather than issuing one write per stale account.
func (r *SnapshotRepository) MarkSnapshotsRefreshPending(platformAccountIDs []int64, now time.Time) error {
	if len(platformAccountIDs) == 0 {
		return nil
	}
	args := make([]interface{}, 0, len(platformAccountIDs)*2)
	values := make([]string, 0, len(platformAccountIDs))
	for i, accountID := range platformAccountIDs {
		accountParam := i*2 + 1
		timeParam := i*2 + 2
		values = append(values, fmt.Sprintf("($%d, 'pending', '{}'::jsonb, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb, to_timestamp(0), NOW(), $%d, 0)", accountParam, timeParam))
		args = append(args, accountID, now)
	}
	query := fmt.Sprintf(`INSERT INTO account_resource_snapshots
		    (platform_account_id, resource_type, profile, statistics, status, content,
		     fetched_at, updated_at, refresh_pending_at, refresh_attempts)
		 VALUES %s
		 ON CONFLICT (platform_account_id) DO UPDATE SET
		    refresh_pending_at = LEAST(
		        COALESCE(account_resource_snapshots.refresh_pending_at, EXCLUDED.refresh_pending_at),
		        EXCLUDED.refresh_pending_at
		    ),
		    refresh_claimed_until = CASE
		        WHEN account_resource_snapshots.refresh_claimed_until > NOW()
		        THEN account_resource_snapshots.refresh_claimed_until
		        ELSE NULL
		    END`, strings.Join(values, ", "))
	if _, err := r.db.Exec(query, args...); err != nil {
		return fmt.Errorf("mark snapshots refresh pending: %w", err)
	}
	return nil
}

// MarkAllSnapshotRefreshesPending durably enqueues every non-deleted,
// non-disconnected account owned by userID for a background refresh in a
// SINGLE statement — the backend for the "refresh all channels" action.
// Returns the number of accounts enqueued (rows inserted or re-stamped).
//
// Explicit user-action semantics: the pending time is stamped NOW
// (overriding any worker backoff) so a deliberately requested refresh is
// never silently swallowed. The sweep worker still bounds the actual
// provider fan-out to a small concurrency (see SnapshotRefreshSweepWorker),
// so this never translates into N simultaneous YouTube calls.
func (r *SnapshotRepository) MarkAllSnapshotRefreshesPending(userID int64, now time.Time) (int64, error) {
	res, err := r.db.Exec(
		`INSERT INTO account_resource_snapshots
		    (platform_account_id, resource_type, profile, statistics, status, content,
		     fetched_at, updated_at, refresh_pending_at, refresh_attempts)
		 SELECT pa.id, 'pending', '{}'::jsonb, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb,
		        to_timestamp(0), NOW(), $2, 0
		   FROM platform_accounts pa
		  WHERE pa.user_id = $1
		    AND pa.status NOT IN ('deleted', 'disconnected')
		 ON CONFLICT (platform_account_id) DO UPDATE SET
		    refresh_pending_at = EXCLUDED.refresh_pending_at,
		    refresh_claimed_until = CASE
		        WHEN account_resource_snapshots.refresh_claimed_until > NOW()
		        THEN account_resource_snapshots.refresh_claimed_until
		        ELSE NULL
		    END`,
		userID, now,
	)
	if err != nil {
		return 0, fmt.Errorf("mark all snapshot refreshes pending: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("mark all snapshot refreshes pending rows: %w", err)
	}
	return n, nil
}

func (r *SnapshotRepository) DeleteSnapshot(platformAccountID int64) error {
	_, err := r.db.Exec(`DELETE FROM account_resource_snapshots WHERE platform_account_id = $1`, platformAccountID)
	if err != nil {
		return fmt.Errorf("delete snapshot: %w", err)
	}
	return nil
}

func (r *SnapshotRepository) IsSnapshotStale(platformAccountID int64, maxAge time.Duration) (bool, error) {
	snap, err := r.GetSnapshot(platformAccountID)
	if err != nil {
		return false, err
	}
	if snap == nil {
		return true, nil
	}
	return time.Since(snap.FetchedAt) > maxAge, nil
}

// PendingSnapshotRefresh is one account claimed for background refresh.
type PendingSnapshotRefresh struct {
	PlatformAccountID int64
	Platform          string
	PlatformUserID    string
	Username          string
	Attempts          int
}

const SnapshotRefreshBatchLimit = 25
const SnapshotRefreshClaimLease = 2 * time.Minute

// ClaimPendingSnapshotRefreshes atomically claims due rows with
// FOR UPDATE SKIP LOCKED. Multiple API/worker replicas therefore cannot
// refresh the same account concurrently.
func (r *SnapshotRepository) ClaimPendingSnapshotRefreshes(ctx context.Context, limit int, lease time.Duration) ([]PendingSnapshotRefresh, error) {
	if limit <= 0 {
		limit = SnapshotRefreshBatchLimit
	}
	if limit > SnapshotRefreshBatchLimit {
		limit = SnapshotRefreshBatchLimit
	}
	if lease <= 0 {
		lease = SnapshotRefreshClaimLease
	}
	const query = `WITH candidates AS (
		SELECT ars.platform_account_id
		  FROM account_resource_snapshots ars
		  JOIN platform_accounts pa ON pa.id = ars.platform_account_id
		 WHERE pa.status NOT IN ('deleted', 'disconnected')
		   AND ars.refresh_pending_at IS NOT NULL
		   AND ars.refresh_pending_at <= NOW()
		   AND (ars.refresh_claimed_until IS NULL OR ars.refresh_claimed_until <= NOW())
		 ORDER BY ars.refresh_pending_at, ars.platform_account_id
		 FOR UPDATE OF ars SKIP LOCKED
		 LIMIT $1
	)
	UPDATE account_resource_snapshots ars
	   SET refresh_claimed_until = NOW() + $2::interval
	  FROM candidates c
	 WHERE ars.platform_account_id = c.platform_account_id
	RETURNING ars.platform_account_id, ars.refresh_attempts,
	          (SELECT pa.platform FROM platform_accounts pa WHERE pa.id = ars.platform_account_id),
	          (SELECT pa.platform_user_id FROM platform_accounts pa WHERE pa.id = ars.platform_account_id),
	          (SELECT pa.username FROM platform_accounts pa WHERE pa.id = ars.platform_account_id)`

	rows, err := r.db.QueryContext(ctx, query, limit, fmt.Sprintf("%f seconds", lease.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("claim pending snapshot refreshes: %w", err)
	}
	defer rows.Close()
	out := make([]PendingSnapshotRefresh, 0, limit)
	for rows.Next() {
		var p PendingSnapshotRefresh
		if err := rows.Scan(&p.PlatformAccountID, &p.Attempts, &p.Platform, &p.PlatformUserID, &p.Username); err != nil {
			return nil, fmt.Errorf("scan claimed snapshot refresh: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claimed snapshot refreshes: %w", err)
	}
	return out, nil
}

// ListPendingSnapshotRefreshes remains as a compatibility/read-only port for
// diagnostics and older callers. Production workers use the atomic claim
// above; only due rows are returned.
func (r *SnapshotRepository) ListPendingSnapshotRefreshes(ctx context.Context, limit int) ([]PendingSnapshotRefresh, error) {
	if limit <= 0 || limit > SnapshotRefreshBatchLimit {
		limit = SnapshotRefreshBatchLimit
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT pa.id, pa.platform, pa.platform_user_id, pa.username, ars.refresh_attempts
		 FROM platform_accounts pa
		 JOIN account_resource_snapshots ars ON ars.platform_account_id = pa.id
		 WHERE pa.status NOT IN ('deleted', 'disconnected')
		   AND ars.refresh_pending_at IS NOT NULL
		   AND ars.refresh_pending_at <= NOW()
		   AND (ars.refresh_claimed_until IS NULL OR ars.refresh_claimed_until <= NOW())
		 ORDER BY ars.refresh_pending_at, pa.id
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending snapshot refreshes: %w", err)
	}
	defer rows.Close()
	out := make([]PendingSnapshotRefresh, 0)
	for rows.Next() {
		var p PendingSnapshotRefresh
		if err := rows.Scan(&p.PlatformAccountID, &p.Platform, &p.PlatformUserID, &p.Username, &p.Attempts); err != nil {
			return nil, fmt.Errorf("scan pending snapshot refresh: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending snapshot refreshes: %w", err)
	}
	return out, nil
}

// RescheduleSnapshotRefresh releases the claim and applies durable
// exponential backoff. The error is intentionally bounded to avoid storing
// provider bodies or credentials in PostgreSQL.
func (r *SnapshotRepository) RescheduleSnapshotRefresh(ctx context.Context, accountID int64, next time.Time, errText string) error {
	if len(errText) > 500 {
		errText = errText[:500]
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE account_resource_snapshots
		    SET refresh_pending_at = NULLIF($2, to_timestamp(0)),
		        refresh_claimed_until = NULL,
		        refresh_attempts = LEAST(refresh_attempts + 1, 20),
		        refresh_last_error = $3,
		        updated_at = NOW()
		  WHERE platform_account_id = $1`, accountID, next, errText)
	if err != nil {
		return fmt.Errorf("reschedule snapshot refresh: %w", err)
	}
	return nil
}
