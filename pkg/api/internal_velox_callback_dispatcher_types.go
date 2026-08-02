package api

import (
	"context"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// VeloxCallbackEvent is the 7-value enum on the InstaEdit→Velox
// callback surface. The string values match the
// external_deliveries.status column names that trigger
// callbacks so a one-to-one mapping between status field on
// the row and the X-Velox-Event-ID header value holds (workers
// dispatch by status, no transform layer).
type VeloxCallbackEvent string

const (
	// VeloxCallbackArtifactVerified fires after the worker has
	// streamed the artifact through the Velox download_url
	// (size + SHA pass) and the asset is ready to be staged
	// into the InstaEdit ingest pipeline.
	VeloxCallbackArtifactVerified VeloxCallbackEvent = "artifact_verified"
	// VeloxCallbackQueued fires when the post has been created
	// in InstaEdit's posts table + is awaiting the publish_at
	// window.
	VeloxCallbackQueued VeloxCallbackEvent = "queued"
	// VeloxCallbackPublishing fires immediately before the
	// platform publish call (videos.insert / etc) is invoked.
	VeloxCallbackPublishing VeloxCallbackEvent = "publishing"
	// VeloxCallbackPublished fires when the platform publish
	// call returns 2xx + a platform-side media id/url is known.
	VeloxCallbackPublished VeloxCallbackEvent = "published"
	// VeloxCallbackBlockedAuth fires when the platform_account
	// transitions to reauth_required mid-pipeline; the
	// publish halts until the user re-links their account.
	VeloxCallbackBlockedAuth VeloxCallbackEvent = "blocked_auth"
	// VeloxCallbackFailed fires when an attempt exhausted its
	// retries with a retryable error (network, 5xx within
	// budget). Distinct from dead_letter: a failed callback
	// hasn't exhausted the dispatcher's retry budget — the
	// audit row says so deterministically.
	VeloxCallbackFailed VeloxCallbackEvent = "failed"
	// VeloxCallbackDeadLetter fires after the dispatcher's
	// max_attempts has been exhausted (default 5). The audit
	// row carries attempts_used + last_status for forensics.
	VeloxCallbackDeadLetter VeloxCallbackEvent = "dead_letter"
)

// IsTerminalSuccess returns true for the 4 events that
// represent progress-or-completion. Used by the audit log
// decision tree (success → AuditActionVeloxCallbackSent,
// failure → AuditActionVeloxCallbackFailed).
func (e VeloxCallbackEvent) IsTerminalSuccess() bool {
	switch e {
	case VeloxCallbackArtifactVerified,
		VeloxCallbackQueued,
		VeloxCallbackPublishing,
		VeloxCallbackPublished:
		return true
	}
	return false
}

// VeloxCallbackPayload is the canonical JSON body posted to
// the Velox callback_url. Field names match the architectural
// doc verbatim (lowercase snake_case, no camelCase aliases).
// Pointer-typed fields are nil when the transition doesn't
// carry that data — e.g. artifact_verified has no
// platform_media_id yet.
type VeloxCallbackPayload struct {
	EventID            string     `json:"event_id"`
	SocialDeliveryID   string     `json:"social_delivery_id"`
	ExternalDeliveryID string     `json:"external_delivery_id"`
	Status             string     `json:"status"`
	PlatformMediaID    *string    `json:"platform_media_id,omitempty"`
	PlatformURL        *string    `json:"platform_url,omitempty"`
	PublishedAt        *time.Time `json:"published_at,omitempty"`
	ErrorCode          *string    `json:"error_code,omitempty"`
	ErrorMessage       *string    `json:"error_message,omitempty"`
}

// VeloxCallbackAuditStore is the narrow audit-log slot the
// dispatcher uses to persist its outcome. The real impl is
// *repository.AuditLogRepository (Append method), wired via
// internal/bootstrap/wire. Deferring the concrete wiring to
// bootstrap keeps pkg/api off an internal/repository import —
// the test fakes satisfy this interface inline.
type VeloxCallbackAuditStore interface {
	Append(ctx context.Context, entry *models.AuditLog) error
}

// Dispatcher tuning constants — overridable in
// NewVeloxCallbackDispatcher (test-injectable). Doc strings
// spell out the rationale so a future operator-chosen env
// config (VELOX_CALLBACK_MAX_ATTEMPTS, etc.) just maps these
// to env keys.
const (
	// DefaultVeloxCallbackMaxAttempts caps the POST-attempt
	// budget. 5 was the operator-chosen default per the
	// architectural doc and matches the dead-letter budget
	// used by the legacy webhook_dispatcher. Operators can
	// raise it for receivers with longer recovery windows.
	DefaultVeloxCallbackMaxAttempts = 5
	// DefaultVeloxCallbackBaseDelay is the per-attempt base
	// interval; exponential doubling arrives at attempt N as
	// (BaseDelay * 2^(N-1)). 1s base + doubling yields a
	// cumulative delay budget of ~31s for 5 attempts + ~2s
	// of jitter across all attempts — the dispatcher's
	// tail-latency budget from first-attempt failure to
	// dead-letter is well under a minute.
	DefaultVeloxCallbackBaseDelay = 1 * time.Second
	// DefaultVeloxCallbackJitterMin + Max shape the uniform
	// jitter range applied to each backoff. The narrow 100-500ms
	// range decorrelates retries across the dispatcher fleet
	// without delaying audit emission too long. Wider jitter
	// is unnecessary here (5xx retries on the same receiver
	// are unlikely to recover within seconds of recovery time).
	DefaultVeloxCallbackJitterMin = 100 * time.Millisecond
	DefaultVeloxCallbackJitterMax = 500 * time.Millisecond
	// DefaultVeloxCallbackRequestTimeout caps a single POST.
	// 15s is generous for an HMAC-signed JSON POST even on a
	// slow link. Combine with the per-attempt retry budget
	// (5 * 15s = 75s upper bound for worst-case exhaustion).
	DefaultVeloxCallbackRequestTimeout = 15 * time.Second
)
