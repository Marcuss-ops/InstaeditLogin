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

// ErrLivestreamRunNotFound is returned when an operation targets an unknown
// run or a run that is no longer owned by the caller.
var ErrLivestreamRunNotFound = errors.New("livestream run not found")

type LivestreamRunRepository struct {
	db *sql.DB
}

func NewLivestreamRunRepository(db *sql.DB) *LivestreamRunRepository {
	return &LivestreamRunRepository{db: db}
}

const livestreamRunColumns = `id, livestream_id, platform_account_id, generation,
status, youtube_broadcast_id, youtube_stream_id, configuration_version,
worker_id, lease_expires_at, heartbeat_at, last_frame_at, encoder_pid,
reconnect_count, attempt_count, started_at, live_at, ended_at, error_code,
error_message, last_error_code, last_error_message, created_at, updated_at`

// SQLClaimLivestreamRuns is exported so worker tests can pin the production
// claim shape. FOR UPDATE SKIP LOCKED lets independent worker replicas claim
// different runs without waiting on one another.
const SQLClaimLivestreamRuns = `WITH candidates AS (
    SELECT id
      FROM livestream_runs
     WHERE status IN ('preflighting', 'preparing', 'ready', 'scheduled',
                      'starting', 'waiting_for_ingest', 'testing', 'live',
                      'degraded', 'reconnecting', 'stopping')
       AND (lease_expires_at IS NULL OR lease_expires_at < NOW())
     ORDER BY created_at ASC, id ASC
     FOR UPDATE SKIP LOCKED
     LIMIT $1
)
UPDATE livestream_runs AS r
   SET worker_id        = $2,
       lease_expires_at = $3,
       heartbeat_at     = NOW(),
       attempt_count    = r.attempt_count + 1,
       updated_at       = NOW()
  FROM candidates
 WHERE r.id = candidates.id
RETURNING ` + livestreamRunColumns

// SQLHeartbeatLivestreamRun renews only a currently owned, unexpired lease.
// The ownership and expiry predicates make renewal a compare-and-swap: a
// stale worker cannot resurrect a lease after a reaper or another worker has
// taken it.
const SQLHeartbeatLivestreamRun = `UPDATE livestream_runs
   SET lease_expires_at = $1,
       heartbeat_at     = NOW(),
       updated_at       = NOW()
 WHERE id              = $2
   AND worker_id        = $3
   AND lease_expires_at > NOW()
RETURNING ` + livestreamRunColumns

// SQLAdvanceLivestreamRunConfigurationVersion increments the run's version
// only when the caller still observes expectedVersion. This prevents a
// worker from continuing with a stale configuration snapshot.
const SQLAdvanceLivestreamRunConfigurationVersion = `UPDATE livestream_runs
   SET configuration_version = configuration_version + 1,
       updated_at            = NOW()
 WHERE id                    = $1
   AND configuration_version = $2
RETURNING configuration_version`

// SQLUpdateLivestreamRunStatus updates observed state while retaining the
// worker lease and checking both ownership and the configuration snapshot.
const SQLUpdateLivestreamRunStatus = `UPDATE livestream_runs
   SET status     = $1,
       updated_at = NOW()
 WHERE id                    = $2
   AND worker_id              = $3
   AND configuration_version = $4
   AND lease_expires_at      > NOW()
RETURNING ` + livestreamRunColumns

// Create persists one run. generation must be positive: callers should use
// NextGeneration when creating a new execution. Keeping generation explicit
// makes retries and idempotent commands safe, while the unique index enforces
// one row per (livestream_id, generation).
func (r *LivestreamRunRepository) Create(ctx context.Context, run *models.LivestreamRun) error {
	if err := validateLivestreamRun(run); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO livestream_runs
		(id, livestream_id, platform_account_id, generation, status,
		 youtube_broadcast_id, youtube_stream_id, configuration_version,
		 worker_id, lease_expires_at, heartbeat_at, last_frame_at, encoder_pid,
		 reconnect_count, attempt_count, started_at, live_at, ended_at,
		 error_code, error_message, last_error_code, last_error_message)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
	        $14, $15, $16, $17, $18, $19, $20, $21, $22)`,
		run.ID, run.LivestreamID, run.PlatformAccountID, run.Generation, run.Status,
		run.YouTubeBroadcastID, run.YouTubeStreamID, run.ConfigurationVersion,
		run.WorkerID, run.LeaseExpiresAt, run.HeartbeatAt, run.LastFrameAt, run.EncoderPID,
		run.ReconnectCount, run.AttemptCount, run.StartedAt, run.LiveAt, run.EndedAt,
		run.ErrorCode, run.ErrorMessage, run.LastErrorCode, run.LastErrorMessage)
	if err != nil {
		return classifyLivestreamRunWriteError(err)
	}
	return nil
}

// NextGeneration returns the next generation number for a livestream. The
// MAX+1 calculation is intentionally read-only; Create remains the atomic
// write boundary and the unique index is the final concurrency guard. A
// caller that loses a concurrent insert should request the next generation
// again and retry its Create operation.
func (r *LivestreamRunRepository) NextGeneration(ctx context.Context, livestreamID string) (int64, error) {
	if livestreamID == "" {
		return 0, errors.New("livestream run NextGeneration: empty livestream ID")
	}
	var next int64
	if err := r.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation), 0) + 1
		   FROM livestream_runs WHERE livestream_id = $1`, livestreamID).Scan(&next); err != nil {
		return 0, fmt.Errorf("livestream run NextGeneration: %w", err)
	}
	return next, nil
}

// CreateNext allocates a generation and inserts a run in one transaction.
// The per-configuration advisory transaction lock serializes generation
// allocation for this livestream while the partial unique channel index still
// protects the cross-configuration active-run invariant.
func (r *LivestreamRunRepository) CreateNext(ctx context.Context, run *models.LivestreamRun) error {
	if run == nil {
		return errors.New("livestream run CreateNext: nil run")
	}
	if run.LivestreamID == "" {
		return errors.New("livestream run CreateNext: empty livestream ID")
	}
	if run.PlatformAccountID <= 0 || run.ID == "" {
		return errors.New("livestream run CreateNext: invalid run identity")
	}
	if !models.ValidLivestreamRunStatus(run.Status) {
		return fmt.Errorf("livestream run CreateNext: invalid status %q", run.Status)
	}
	if run.ConfigurationVersion <= 0 {
		run.ConfigurationVersion = 1
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("livestream run CreateNext begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Hashtext is stable across replicas and scopes the lock to this
	// reusable configuration, not to the whole livestream-run table.
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtext($1))`, run.LivestreamID); err != nil {
		return fmt.Errorf("livestream run CreateNext lock: %w", err)
	}
	var next int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation), 0) + 1
		   FROM livestream_runs WHERE livestream_id = $1`,
		run.LivestreamID).Scan(&next); err != nil {
		return fmt.Errorf("livestream run CreateNext generation: %w", err)
	}
	run.Generation = next
	if _, err := tx.ExecContext(ctx, `INSERT INTO livestream_runs
		(id, livestream_id, platform_account_id, generation, status,
		 youtube_broadcast_id, youtube_stream_id, configuration_version,
		 worker_id, lease_expires_at, heartbeat_at, last_frame_at, encoder_pid,
		 reconnect_count, attempt_count, started_at, live_at, ended_at,
		 error_code, error_message, last_error_code, last_error_message)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
	        $14, $15, $16, $17, $18, $19, $20, $21, $22)`,
		run.ID, run.LivestreamID, run.PlatformAccountID, run.Generation, run.Status,
		run.YouTubeBroadcastID, run.YouTubeStreamID, run.ConfigurationVersion,
		run.WorkerID, run.LeaseExpiresAt, run.HeartbeatAt, run.LastFrameAt, run.EncoderPID,
		run.ReconnectCount, run.AttemptCount, run.StartedAt, run.LiveAt, run.EndedAt,
		run.ErrorCode, run.ErrorMessage, run.LastErrorCode, run.LastErrorMessage); err != nil {
		return classifyLivestreamRunWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("livestream run CreateNext commit: %w", err)
	}
	return nil
}

// ClaimBatch atomically claims up to limit runnable runs. A claim does not
// change status: the reconciler may be resuming a run in any operational
// state. The returned rows carry the incremented attempt count and lease.
func (r *LivestreamRunRepository) ClaimBatch(ctx context.Context, workerID string, limit int, lease time.Duration) ([]*models.LivestreamRun, error) {
	if workerID == "" {
		return nil, errors.New("livestream run ClaimBatch: empty worker ID")
	}
	if limit <= 0 {
		return nil, nil
	}
	if lease <= 0 {
		return nil, fmt.Errorf("livestream run ClaimBatch: non-positive lease (%s)", lease)
	}
	leaseUntil := time.Now().Add(lease)
	rows, err := r.db.QueryContext(ctx, SQLClaimLivestreamRuns, limit, workerID, leaseUntil)
	if err != nil {
		return nil, fmt.Errorf("livestream run ClaimBatch: %w", err)
	}
	defer rows.Close()
	var out []*models.LivestreamRun
	for rows.Next() {
		run, scanErr := scanLivestreamRun(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("livestream run ClaimBatch scan: %w", scanErr)
		}
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("livestream run ClaimBatch rows: %w", err)
	}
	return out, nil
}

// Heartbeat renews a live lease, returning ErrLivestreamRunLeaseLost if the
// worker no longer owns an unexpired lease.
func (r *LivestreamRunRepository) Heartbeat(ctx context.Context, runID, workerID string, lease time.Duration) error {
	if runID == "" || workerID == "" {
		return errors.New("livestream run Heartbeat: run ID and worker ID are required")
	}
	if lease <= 0 {
		return fmt.Errorf("livestream run Heartbeat: non-positive lease (%s)", lease)
	}
	leaseUntil := time.Now().Add(lease)
	row := r.db.QueryRowContext(ctx, SQLHeartbeatLivestreamRun, leaseUntil, runID, workerID)
	var run models.LivestreamRun
	if err := scanLivestreamRunInto(row, &run); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: run_id=%s worker_id=%s", models.ErrLivestreamRunLeaseLost, runID, workerID)
		}
		return fmt.Errorf("livestream run Heartbeat: %w", err)
	}
	return nil
}

// AdvanceConfigurationVersion performs a compare-and-swap increment. It is
// intentionally independent of worker ownership so an API configuration
// update can invalidate a worker snapshot safely.
func (r *LivestreamRunRepository) AdvanceConfigurationVersion(ctx context.Context, runID string, expectedVersion int64) (int64, error) {
	if runID == "" || expectedVersion <= 0 {
		return 0, errors.New("livestream run AdvanceConfigurationVersion: invalid run ID or version")
	}
	var next int64
	if err := r.db.QueryRowContext(ctx, SQLAdvanceLivestreamRunConfigurationVersion, runID, expectedVersion).Scan(&next); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("%w: run_id=%s expected_version=%d", models.ErrLivestreamRunVersionConflict, runID, expectedVersion)
		}
		return 0, fmt.Errorf("livestream run AdvanceConfigurationVersion: %w", err)
	}
	return next, nil
}

// UpdateStatus applies an observed state transition only for the current
// worker lease and configuration snapshot.
func (r *LivestreamRunRepository) UpdateStatus(ctx context.Context, runID, workerID string, expectedVersion int64, status models.LivestreamRunStatus) (*models.LivestreamRun, error) {
	if runID == "" || workerID == "" || expectedVersion <= 0 {
		return nil, errors.New("livestream run UpdateStatus: invalid ownership or version")
	}
	if !models.ValidLivestreamRunStatus(status) {
		return nil, fmt.Errorf("livestream run UpdateStatus: invalid status %q", status)
	}
	row := r.db.QueryRowContext(ctx, SQLUpdateLivestreamRunStatus, status, runID, workerID, expectedVersion)
	run, err := scanLivestreamRun(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: run_id=%s", models.ErrLivestreamRunLeaseLost, runID)
		}
		return nil, fmt.Errorf("livestream run UpdateStatus: %w", err)
	}
	return run, nil
}

func (r *LivestreamRunRepository) FindByID(ctx context.Context, runID string) (*models.LivestreamRun, error) {
	if runID == "" {
		return nil, errors.New("livestream run FindByID: empty run ID")
	}
	row := r.db.QueryRowContext(ctx, `SELECT `+livestreamRunColumns+` FROM livestream_runs WHERE id = $1`, runID)
	run, err := scanLivestreamRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("livestream run FindByID: %w", err)
	}
	return run, nil
}

func (r *LivestreamRunRepository) ListByLivestream(ctx context.Context, livestreamID string, limit int) ([]*models.LivestreamRun, error) {
	if livestreamID == "" {
		return nil, errors.New("livestream run ListByLivestream: empty livestream ID")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+livestreamRunColumns+`
		FROM livestream_runs WHERE livestream_id = $1
		ORDER BY generation DESC, id DESC LIMIT $2`, livestreamID, limit)
	if err != nil {
		return nil, fmt.Errorf("livestream run ListByLivestream: %w", err)
	}
	defer rows.Close()
	var out []*models.LivestreamRun
	for rows.Next() {
		run, scanErr := scanLivestreamRun(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("livestream run ListByLivestream scan: %w", scanErr)
		}
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("livestream run ListByLivestream rows: %w", err)
	}
	return out, nil
}

func validateLivestreamRun(run *models.LivestreamRun) error {
	if run == nil {
		return errors.New("livestream run Create: nil run")
	}
	if run.ID == "" || run.LivestreamID == "" || run.PlatformAccountID <= 0 || run.Generation <= 0 {
		return errors.New("livestream run Create: invalid identity or generation")
	}
	if !models.ValidLivestreamRunStatus(run.Status) {
		return fmt.Errorf("livestream run Create: invalid status %q", run.Status)
	}
	if run.ConfigurationVersion <= 0 {
		return errors.New("livestream run Create: configuration version must be positive")
	}
	if run.ReconnectCount < 0 || run.AttemptCount < 0 {
		return errors.New("livestream run Create: counters cannot be negative")
	}
	return nil
}

func classifyLivestreamRunWriteError(err error) error {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) || pqErr.Code != "23505" {
		return fmt.Errorf("livestream run write: %w", err)
	}
	switch pqErr.Constraint {
	case "livestream_one_active_run_per_channel":
		return fmt.Errorf("%w: %s", models.ErrLivestreamRunActiveConflict, pqErr.Constraint)
	case "livestream_runs_generation_uq":
		return fmt.Errorf("%w: %s", models.ErrLivestreamRunGenerationConflict, pqErr.Constraint)
	default:
		return fmt.Errorf("livestream run write unique violation (%s): %w", pqErr.Constraint, err)
	}
}

func scanLivestreamRun(row interface{ Scan(...any) error }) (*models.LivestreamRun, error) {
	run := &models.LivestreamRun{}
	if err := scanLivestreamRunInto(row, run); err != nil {
		return nil, err
	}
	return run, nil
}

func scanLivestreamRunInto(row interface{ Scan(...any) error }, run *models.LivestreamRun) error {
	var (
		status, broadcastID, streamID, workerID  sql.NullString
		leaseExpiresAt, heartbeatAt, lastFrameAt sql.NullTime
		startedAt, liveAt, endedAt               sql.NullTime
	)
	err := row.Scan(
		&run.ID, &run.LivestreamID, &run.PlatformAccountID, &run.Generation,
		&status, &broadcastID, &streamID, &run.ConfigurationVersion,
		&workerID, &leaseExpiresAt, &heartbeatAt, &lastFrameAt, &run.EncoderPID,
		&run.ReconnectCount, &run.AttemptCount, &startedAt, &liveAt, &endedAt,
		&run.ErrorCode, &run.ErrorMessage, &run.LastErrorCode, &run.LastErrorMessage,
		&run.CreatedAt, &run.UpdatedAt,
	)
	if err != nil {
		return err
	}
	run.Status = models.LivestreamRunStatus(status.String)
	if broadcastID.Valid {
		run.YouTubeBroadcastID = &broadcastID.String
	}
	if streamID.Valid {
		run.YouTubeStreamID = &streamID.String
	}
	if workerID.Valid {
		run.WorkerID = &workerID.String
	}
	if leaseExpiresAt.Valid {
		t := leaseExpiresAt.Time
		run.LeaseExpiresAt = &t
	}
	if heartbeatAt.Valid {
		t := heartbeatAt.Time
		run.HeartbeatAt = &t
	}
	if lastFrameAt.Valid {
		t := lastFrameAt.Time
		run.LastFrameAt = &t
	}
	if startedAt.Valid {
		t := startedAt.Time
		run.StartedAt = &t
	}
	if liveAt.Valid {
		t := liveAt.Time
		run.LiveAt = &t
	}
	if endedAt.Valid {
		t := endedAt.Time
		run.EndedAt = &t
	}
	return nil
}
