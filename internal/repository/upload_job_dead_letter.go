package repository

import (
	"context"
	"fmt"
)

// MarkDeadLetter transitions the row to terminal failure (retry budget
// exhausted). status = 'dead_letter', error_code + error_message stamped,
// lease cleared, completed_at = NOW(). The worker calls this when
// attempt_count >= max_attempts — the row is out of retry budget and
// surfaces in the operator-triage dashboard.
//
// Same CAS protection as MarkCompleted / MarkFailed: a late delivery
// from a worker whose lease expired cannot overwrite a peer's
// terminal write.
func (r *UploadJobRepository) MarkDeadLetter(ctx context.Context, id int64, workerID, errorCode, errMessage string) error {
	res, err := r.db.ExecContext(ctx,
		SQLMarkDeadLetter,
		id, errMessage, errorCode, workerID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark upload job dead_letter: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d workerID=%s", ErrUploadJobLeaseLost, id, workerID)
	}
	return nil
}
