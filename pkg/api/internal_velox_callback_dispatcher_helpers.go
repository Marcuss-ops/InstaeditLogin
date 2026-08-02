package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// sleep applies the exponential-backoff + jitter delay for
// the given attempt (1-based). The per-attempt delay is:
//
//	delay = baseDelay * 2^(attempt-1) + uniform(jitterMin, jitterMax)
//
// attempt N delay totals (with defaults):
//
//	1 → 1s + jitter[100ms..500ms)
//	2 → 2s + jitter
//	3 → 4s + jitter
//	4 → 8s + jitter
//	5 → 16s + jitter (final: no sleep, just emit audit)
//
// ctx-cancellable. A cancelled ctx during sleep surfaces as
// context.Canceled / context.DeadlineExceeded via the
// returned error — the caller treats that as terminal failure.
func (d *VeloxCallbackDispatcher) sleep(ctx context.Context, attempt int) error {
	exp := d.baseDelay
	for i := 1; i < attempt; i++ {
		exp *= 2
	}
	span := int64(d.jitterMax - d.jitterMin)
	if span <= 0 {
		span = int64(d.jitterMax)
	}
	jitter := time.Duration(d.randSrc.Int63n(span))
	delay := exp + d.jitterMin + jitter

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(delay):
		return nil
	}
}

// signBody computes HMAC-SHA256 over "<unix_ts>.<body>" using
// the dispatcher's secret. Returns the lowercase hex digest
// WITHOUT the "sha256=" prefix — that's added when the header
// is rendered so the canonical hex form stays comparable with
// test expectations.
func (d *VeloxCallbackDispatcher) signBody(ts int64, body []byte) string {
	mac := hmac.New(sha256.New, d.secret)
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte{'.'})
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// emitAudit persists a single AuditLog row per Dispatch
// invocation regardless of retry count. Success → action
// AuditActionVeloxCallbackSent + result=success. Failure →
// AuditActionVeloxCallbackFailed + result=failure + metadata
// capturing last_status + attempts_used + event_id.
//
// auditStore nil → no-op + Warn log (the underlying POST
// outcome is unaffected — a missing audit row is recoverable
// from the worker's external_deliveries.status + last_error_*
// columns).
func (d *VeloxCallbackDispatcher) emitAudit(
	ctx context.Context,
	delivery *models.ExternalDelivery,
	event VeloxCallbackEvent,
	eventID string,
	attempts int,
	lastStatus int,
	lastErr error,
) {
	if d.auditStore == nil {
		d.logger.Warn("velox callback: auditStore nil; skipping audit emission",
			"event", event, "event_id", eventID, "attempts", attempts,
		)
		return
	}

	action := models.AuditActionVeloxCallbackSent
	result := models.AuditResultSuccess
	if lastErr != nil {
		action = models.AuditActionVeloxCallbackFailed
		result = models.AuditResultFailure
	}

	// Metadata is a models.Metadata map (map[string]any per
	// internal/models/user.go) — constructed directly with
	// string values so an audit_log_repo scan lands the
	// fields in their expected shape without a JSON
	// round-trip error masking any type mismatch.
	meta := models.Metadata{
		"external_delivery_id": delivery.ExternalDeliveryID,
		"callback_url":         derefString(delivery.CallbackURL),
		"event":                string(event),
		"event_id":             eventID,
		"attempts":             strconv.Itoa(attempts),
		"max_attempts":         strconv.Itoa(d.maxAttempts),
		"last_status":          strconv.Itoa(lastStatus),
	}
	if lastErr != nil {
		meta["error"] = lastErr.Error()
	}

	entry := &models.AuditLog{
		Action:       action,
		Result:       result,
		ResourceType: "external_delivery",
		// ResourceID stays 0 — ExternalDelivery.ID is a TEXT
		// PRIMARY KEY (ULID-shaped) and doesn't fit the int64
		// ResourceID column. The string id lives in metadata.
		Metadata: meta,
	}
	if err := d.auditStore.Append(ctx, entry); err != nil {
		d.logger.Error("velox callback: audit append failed (postmortem gap)",
			"event", event, "event_id", eventID, "attempts", attempts,
			"audit_error", err.Error(),
		)
	}
}

// derefString returns "" for nil pointers (the audit metadata
// column is non-null).
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// defaultVeloxEventID generates evt_<32-hex> for the
// X-Velox-Event-ID header. 16 random bytes from
// crypto/rand (sufficient for the dedup window; the id is
// NOT a security boundary).
func defaultVeloxEventID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is essentially impossible on
		// real hardware. Fall back to a deterministic nonce
		// so we don't panic — id uniqueness degrades but the
		// field is non-critical.
		for i := range b {
			b[i] = byte(i)
		}
	}
	return "evt_" + hex.EncodeToString(b)
}
