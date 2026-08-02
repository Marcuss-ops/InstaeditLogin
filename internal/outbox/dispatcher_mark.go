package outbox

import (
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// markProcessed is the terminal-success arm of processOne: the
// side-effect ran cleanly and MarkProcessed must persist the
// durable acknowledgement. A MarkProcessed failure is partial
// persistence (H1): the lease stays held, the row is re-claimable,
// and the wrapped ErrPartialPersistence escalates visibility.
func (d *Dispatcher) markProcessed(ev *models.OutboxEvent, leaseID string, duration time.Duration) error {
	if err := d.cfg.OutboxStore.MarkProcessed(ev.ID, leaseID); err != nil {
		d.cfg.Logger.Error("outbox partial persistence: MarkProcessed failed AFTER side-effect success — lease will expire; next peer/tick re-claims to re-run idempotent adapter",
			"event_id", ev.ID, "duration", duration, "error", err)
		return fmt.Errorf("%w: MarkProcessed failed: %w", ErrPartialPersistence, err)
	}
	d.cfg.Logger.Info("outbox dispatcher processed event",
		"event_id", ev.ID, "duration", duration)
	return nil
}

// markDeadLetterTerminal is the ErrTerminal arm of processOne:
// the failure is unrecoverable (schema mismatch, payload too
// large, business-rule violation) → go straight to DLQ regardless
// of attempt count, do NOT retry.
func (d *Dispatcher) markDeadLetterTerminal(ev *models.OutboxEvent, leaseID string, duration time.Duration, processErr error) error {
	if err := d.cfg.OutboxStore.MarkDeadLetter(ev.ID, leaseID, processErr.Error()); err != nil {
		d.cfg.Logger.Error("outbox partial persistence: MarkDeadLetter (terminal) failed — lease will expire; next peer/tick re-runs idempotent adapter to re-DLQ",
			"event_id", ev.ID, "duration", duration, "error", err)
		return fmt.Errorf("%w: MarkDeadLetter (terminal) failed: %w", ErrPartialPersistence, err)
	}
	d.cfg.Logger.Warn("outbox dispatcher sent event to DLQ (terminal error)",
		"event_id", ev.ID, "error", processErr.Error())
	return nil
}

// markDeadLetterMaxAttempts is the transient-retries-exhausted arm
// of processOne: attempt count reached MaxAttempts → DLQ so an
// operator can triage the row.
func (d *Dispatcher) markDeadLetterMaxAttempts(ev *models.OutboxEvent, leaseID string, duration time.Duration, processErr error) error {
	if err := d.cfg.OutboxStore.MarkDeadLetter(ev.ID, leaseID,
		fmt.Sprintf("max attempts (%d) reached: %s", d.cfg.MaxAttempts, processErr.Error()),
	); err != nil {
		d.cfg.Logger.Error("outbox partial persistence: MarkDeadLetter (max attempts) failed — lease will expire; next peer/tick re-runs idempotent adapter to re-DLQ",
			"event_id", ev.ID, "duration", duration, "error", err)
		return fmt.Errorf("%w: MarkDeadLetter (max attempts) failed: %w", ErrPartialPersistence, err)
	}
	d.cfg.Logger.Warn("outbox dispatcher sent event to DLQ (max attempts)",
		"event_id", ev.ID, "attempts", ev.AttemptCount, "error", processErr.Error())
	return nil
}

// markFailedBackoff is the transient-failure arm of processOne:
// compute the decorrelated-jitter backoff for the next attempt and
// MarkFailed so the row is re-claimable after next_attempt_at.
func (d *Dispatcher) markFailedBackoff(ev *models.OutboxEvent, leaseID string, duration time.Duration, processErr error) error {
	backoff := d.computeBackoff(ev.AttemptCount)
	if err := d.cfg.OutboxStore.MarkFailed(ev.ID, leaseID, processErr.Error(), &backoff); err != nil {
		d.cfg.Logger.Error("outbox partial persistence: MarkFailed failed — lease will expire; next peer/tick re-runs idempotent adapter to re-schedule",
			"event_id", ev.ID, "duration", duration, "backoff", backoff, "error", err)
		return fmt.Errorf("%w: MarkFailed failed: %w", ErrPartialPersistence, err)
	}
	d.cfg.Logger.Info("outbox dispatcher retrying event",
		"event_id", ev.ID, "attempts", ev.AttemptCount, "backoff", backoff, "error", processErr.Error())
	return nil
}
