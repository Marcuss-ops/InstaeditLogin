package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// UpdateStatus transitions a delivery row to a new status alongside
// optional error metadata (last_error_code + last_error_message) and
// optional platform identifiers (platform_media_id + platform_url).
// All fields except id and status are nullable pointers — nil
// preserves the existing value via COALESCE.
//
// Sets completed_at = NOW() automatically when transitioning to a
// terminal state (Published / Failed / DeadLetter / BlockedAuth).
// Non-terminal state transitions leave completed_at untouched.
//
// The CAS contract: zero rows affected means the row was deleted
// between the caller-side Lookup and this Update (rare; possible
// only via a manual operator DELETE). Returns
// ErrExternalDeliveryNotFound wrapped with id context.
//
// This method does NOT take an advisory lock — concurrent state
// transitions are not idempotent in the same way the Insert is;
// the worker that wins is the worker that gets rows_affected = 1.
// State-machine correctness should be enforced one level up
// (publish_worker's state transition guard in
// ingest_fsm_state.go); this repo is the SQL surface.
func (r *ExternalDeliveryRepository) UpdateStatus(ctx context.Context, id string, newStatus models.ExternalDeliveryStatus, lastErrorCode, lastErrorMessage, platformMediaID, platformURL *string) error {
	if id == "" {
		return errors.New("external delivery UpdateStatus: empty id")
	}
	if newStatus == "" {
		return errors.New("external delivery UpdateStatus: empty newStatus")
	}

	// COALESCE-friendly nil-resolving for the optional fields.
	var codeArg, msgArg, midArg, purlArg interface{}
	if lastErrorCode != nil {
		codeArg = *lastErrorCode
	}
	if lastErrorMessage != nil {
		msgArg = *lastErrorMessage
	}
	if platformMediaID != nil {
		midArg = *platformMediaID
	}
	if platformURL != nil {
		purlArg = *platformURL
	}

	res, err := r.db.ExecContext(ctx,
		`UPDATE external_deliveries
		 SET status              = $2,
		     last_error_code     = COALESCE($3, last_error_code),
		     last_error_message  = COALESCE($4, last_error_message),
		     platform_media_id   = COALESCE($5, platform_media_id),
		     platform_url        = COALESCE($6, platform_url),
		     updated_at          = NOW(),
		     completed_at        = CASE
		         WHEN $2 IN ('published', 'failed', 'dead_letter', 'blocked_auth')
		              AND completed_at IS NULL
		         THEN NOW()
		         ELSE completed_at
		     END
		 WHERE id = $1`,
		id, string(newStatus),
		codeArg, msgArg, midArg, purlArg,
	)
	if err != nil {
		return fmt.Errorf("external delivery UpdateStatus: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("external delivery UpdateStatus rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s", ErrExternalDeliveryNotFound, id)
	}
	return nil
}

// MarkRetry releases the lease on a delivery and schedules a retry. The
// worker calls this when processing fails with a transient error. If the
// delivery has exhausted its retry budget, the row is instead marked as
// dead-letter via MarkDeadLetter.
func (r *ExternalDeliveryRepository) MarkRetry(ctx context.Context, id string, nextAttemptAt time.Time, errorCode, errorMessage string) error {
	if id == "" {
		return errors.New("external delivery MarkRetry: empty id")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE external_deliveries
		 SET status             = 'retry_wait',
		     next_attempt_at    = $2,
		     lease_expires_at   = NULL,
		     leased_by_worker_id = NULL,
		     last_error_code    = COALESCE($3, last_error_code),
		     last_error_message = COALESCE($4, last_error_message),
		     updated_at         = NOW()
		 WHERE id = $1`,
		id, nextAttemptAt, errorCode, errorMessage,
	)
	if err != nil {
		return fmt.Errorf("external delivery MarkRetry: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("external delivery MarkRetry rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s", ErrExternalDeliveryNotFound, id)
	}
	return nil
}

// MarkFailed moves a delivery to the terminal failed state, clears its
// lease, and records the failure reason. Used for non-retryable processing
// errors.
func (r *ExternalDeliveryRepository) MarkFailed(ctx context.Context, id string, errorCode, errorMessage string) error {
	if id == "" {
		return errors.New("external delivery MarkFailed: empty id")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE external_deliveries
		 SET status             = 'failed',
		     lease_expires_at   = NULL,
		     leased_by_worker_id = NULL,
		     next_attempt_at    = NULL,
		     last_error_code    = COALESCE($2, last_error_code),
		     last_error_message = COALESCE($3, last_error_message),
		     completed_at       = COALESCE(completed_at, NOW()),
		     updated_at         = NOW()
		 WHERE id = $1`,
		id, errorCode, errorMessage,
	)
	if err != nil {
		return fmt.Errorf("external delivery MarkFailed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("external delivery MarkFailed rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s", ErrExternalDeliveryNotFound, id)
	}
	return nil
}

// MarkBlockedAuth moves a delivery to the terminal blocked_auth state,
// clears its lease, and records the failure reason. Used when the delivery
// cannot proceed without operator intervention (e.g. metadata-only payload).
func (r *ExternalDeliveryRepository) MarkBlockedAuth(ctx context.Context, id string, errorCode, errorMessage string) error {
	if id == "" {
		return errors.New("external delivery MarkBlockedAuth: empty id")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE external_deliveries
		 SET status             = 'blocked_auth',
		     lease_expires_at   = NULL,
		     leased_by_worker_id = NULL,
		     next_attempt_at    = NULL,
		     last_error_code    = COALESCE($2, last_error_code),
		     last_error_message = COALESCE($3, last_error_message),
		     completed_at       = COALESCE(completed_at, NOW()),
		     updated_at         = NOW()
		 WHERE id = $1`,
		id, errorCode, errorMessage,
	)
	if err != nil {
		return fmt.Errorf("external delivery MarkBlockedAuth: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("external delivery MarkBlockedAuth rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s", ErrExternalDeliveryNotFound, id)
	}
	return nil
}

// MarkDeadLetter moves a delivery to the terminal dead_letter state, clears
// its lease, and records the failure reason. Called when the retry budget is
// exhausted.
func (r *ExternalDeliveryRepository) MarkDeadLetter(ctx context.Context, id string, errorCode, errorMessage string) error {
	if id == "" {
		return errors.New("external delivery MarkDeadLetter: empty id")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE external_deliveries
		 SET status             = 'dead_letter',
		     lease_expires_at   = NULL,
		     leased_by_worker_id = NULL,
		     next_attempt_at    = NULL,
		     last_error_code    = COALESCE($2, last_error_code),
		     last_error_message = COALESCE($3, last_error_message),
		     completed_at       = COALESCE(completed_at, NOW()),
		     updated_at         = NOW()
		 WHERE id = $1`,
		id, errorCode, errorMessage,
	)
	if err != nil {
		return fmt.Errorf("external delivery MarkDeadLetter: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("external delivery MarkDeadLetter rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s", ErrExternalDeliveryNotFound, id)
	}
	return nil
}
