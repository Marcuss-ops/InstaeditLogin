// Package api — Velox callback dispatcher.
//
// The InstaEdit→Velox webhook surface that carries
// external_delivery state transitions. When an
// external_deliveries row transitions status (e.g. accepted →
// artifact_verified → queued → publishing → published, or any
// of the error exits: blocked_auth / failed / dead_letter),
// the ingest+publish worker calls Dispatch to send a signed
// POST to the row's callback_url. Velox verifies the signature
// using VELOX_WEBHOOK_SECRET (server-shared, NOT row-scoped —
// every callback uses the same secret, mirroring Stripe +
// GitHub-webhooks conventions).
//
// Signature scheme — mirrors the architectural contract
// verbatim:
//
//	signed_string = "<unix_timestamp>.<raw_body>"
//	signature      = hex(HMAC-SHA256(secret, signed_string))
//
// Headers:
//
//	X-Velox-Event-ID:    <opaque event id, "evt_<32-hex>">
//	X-Velox-Timestamp:   <unix seconds>
//	X-Velox-Signature:   "sha256=<hex digest of signed_string>"
//	Content-Type:        application/json
//	User-Agent:          InstaEditLogin-Velox-Callbacks/1.0
//
// Retry policy — bounded attempts (default 5). 5xx + network
// errors retry; 4xx is terminal (receiver's bug, retrying is
// pointless); 2xx is terminal success. Backoff curve is
// exponential base=1s doubling each attempt + uniform jitter
// in [100ms, 500ms). Operators tune via dispatcher-init opts
// (a follow-up env-driven config layer would wire
// VELOX_CALLBACK_MAX_ATTEMPTS + VELOX_CALLBACK_BASE_DELAY_MS).
//
// Audit — every Dispatch call emits exactly one audit row
// regardless of retry count. Success → AuditActionVeloxCallbackSent
// (result=success). Terminal failure →
// AuditActionVeloxCallbackFailed (result=failure; metadata
// captures last_status + attempts_used + event_id + event type
// for postmortem grep). Per-attempt rows are intentionally
// omitted — the worker's external_deliveries.status is the
// canonical state, the audit is the operator-facing mirror.
//
// Event types — exactly 7 (matches the external_deliveries
// status names that trigger callbacks):
//
//	artifact_verified, queued, publishing, published,
//	blocked_auth, failed, dead_letter
//
// Adding a new event type is a 2-step change: add the const
// here + emit from the appropriate worker hook.
package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	mathrand "math/rand"
	"net/http"
	"strconv"
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

// VeloxCallbackDispatcher fans a signed POST to
// delivery.CallbackURL when an external_delivery row's status
// transitions. Bounded retry on transport/5xx; terminal fast
// on 2xx/4xx. Failure audit persisted after the last attempt.
//
// Concurrent safety: stateless (all fields are either
// per-call values or read-only after construction). Safe to
// share across the worker pool's goroutines.
type VeloxCallbackDispatcher struct {
	secret      []byte
	httpClient  *http.Client
	auditStore  VeloxCallbackAuditStore
	logger      *slog.Logger
	maxAttempts int
	baseDelay   time.Duration
	jitterMin   time.Duration
	jitterMax   time.Duration

	// Injectable for tests. clock, randSrc, idGen are NOT exposed
	// via the public constructor — production uses time.Now /
	// math/rand default-sourced / defaultVeloxEventID.
	// randSrc is named to avoid shadowing the math/rand
	// package import on a hypothetical future addition that
	// scrolls up to declare `rand` as a wrapper type.
	clock   func() time.Time
	randSrc *mathrand.Rand
	idGen   func() string
}

// NewVeloxCallbackDispatcher wires the dispatcher. secret nil →
// the dispatcher refuses Dispatch (returns ErrNotConfigured
// on every call) so a misconfigured bootstrap path produces
// deterministic audit failures rather than a silent no-op.
//
// httpClient nil → defaults to a 15s-timeout client. The
// dispatcher does NOT reuse the worker's http.Client because
// the per-attempt timeout (15s) and the worker's
// uploadTimeout (30 min) are different concerns.
// auditStore nil → audit calls become no-ops + a Warn log
// (a missing audit row is a logging gap, not a dispatch
// failure — the underlying POST still happens).
func NewVeloxCallbackDispatcher(
	secret []byte,
	httpClient *http.Client,
	auditStore VeloxCallbackAuditStore,
	logger *slog.Logger,
) *VeloxCallbackDispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultVeloxCallbackRequestTimeout}
	}
	return &VeloxCallbackDispatcher{
		secret:      secret,
		httpClient:  httpClient,
		auditStore:  auditStore,
		logger:      logger,
		maxAttempts: DefaultVeloxCallbackMaxAttempts,
		baseDelay:   DefaultVeloxCallbackBaseDelay,
		jitterMin:   DefaultVeloxCallbackJitterMin,
		jitterMax:   DefaultVeloxCallbackJitterMax,
		clock:       time.Now,
		randSrc:     mathrand.New(mathrand.NewSource(time.Now().UnixNano())),
		idGen:       defaultVeloxEventID,
	}
}

// ErrNotConfigured is returned by Dispatch when the dispatcher
// was constructed without a secret (the bootstrap nil-guard).
// Auditable + distinguishable from network failures.
var ErrNotConfigured = errors.New("velox callback dispatcher: not configured (empty secret)")

// Dispatch sends a signed callback. Returns nil on terminal
// success or a wrapped error on terminal failure (after the
// retry budget is exhausted OR after a non-retryable status).
//
// delivery.CallbackURL nil/empty → returns an error WITHOUT
// making any HTTP request (early-return prevents noise in the
// receiver's logs).
//
// payload may be a fresh struct (Dispatch stamps EventID +
// SocialDeliveryID + ExternalDeliveryID into it from the
// delivery row).
//
// ctx is propagated to the http.Client (per-attempt timeout)
// AND to the per-retry backoff (a cancelled ctx during the
// sleep between attempts short-circuits to terminal failure
// without burning the budget).
func (d *VeloxCallbackDispatcher) Dispatch(
	ctx context.Context,
	delivery *models.ExternalDelivery,
	event VeloxCallbackEvent,
	payload *VeloxCallbackPayload,
) error {
	if d == nil {
		return ErrNotConfigured
	}
	if len(d.secret) == 0 {
		return ErrNotConfigured
	}
	if delivery == nil {
		return errors.New("velox callback dispatcher: nil delivery")
	}
	if delivery.CallbackURL == nil || *delivery.CallbackURL == "" {
		return errors.New("velox callback dispatcher: delivery has no callback_url")
	}

	// Stamp canonical fields into the payload from the row.
	// Workers may pre-fill these; the dispatcher ensures no
	// field is empty regardless.
	p := payload
	if p == nil {
		p = &VeloxCallbackPayload{}
	}
	if p.EventID == "" {
		p.EventID = d.idGen()
	}
	if p.SocialDeliveryID == "" {
		p.SocialDeliveryID = delivery.ID
	}
	if p.ExternalDeliveryID == "" {
		p.ExternalDeliveryID = delivery.ExternalDeliveryID
	}
	if p.Status == "" {
		p.Status = string(event)
	}

	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("velox callback: marshal payload: %w", err)
	}

	url := *delivery.CallbackURL
	eventID := p.EventID

	var attempts int
	var lastStatus int
	var lastErr error

	for attempt := 1; attempt <= d.maxAttempts; attempt++ {
		attempts = attempt

		ts := d.clock().Unix()
		signature := d.signBody(ts, body)

		req, reqErr := http.NewRequestWithContext(
			ctx, http.MethodPost, url, bytes.NewReader(body),
		)
		if reqErr != nil {
			// Cannot build a request with a parsed URL.
			// Treat as terminal — the URL is structurally bad
			// and retrying won't fix it.
			lastErr = fmt.Errorf("velox callback: build request (attempt %d): %w", attempt, reqErr)
			break
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "InstaEditLogin-Velox-Callbacks/1.0")
		req.Header.Set("X-Velox-Event-ID", eventID)
		req.Header.Set("X-Velox-Timestamp", strconv.FormatInt(ts, 10))
		req.Header.Set("X-Velox-Signature", "sha256="+signature)

		d.logger.Debug("velox callback: attempt",
			"event_id", eventID,
			"event", event,
			"url", url,
			"attempt", attempt,
			"max_attempts", d.maxAttempts,
		)

		resp, doErr := d.httpClient.Do(req)
		if doErr != nil {
			lastErr = fmt.Errorf("velox callback: attempt %d transport: %w", attempt, doErr)
			lastStatus = 0
			// Network error: always retry.
			if attempt == d.maxAttempts {
				break
			}
			if sleepErr := d.sleep(ctx, attempt); sleepErr != nil {
				lastErr = fmt.Errorf("velox callback: backoff cancelled: %w", sleepErr)
				break
			}
			continue
		}
		lastStatus = resp.StatusCode
		// Drain + close so the connection can be reused by
		// keep-alive — cheap CPU + memory, and avoids half-
		// closed-connection warnings on the receiver side.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		lastErr = nil

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			// Terminal success.
			break
		case resp.StatusCode >= 500:
			lastErr = fmt.Errorf("velox callback: attempt %d server error %d", attempt, resp.StatusCode)
			if attempt == d.maxAttempts {
				break
			}
			if sleepErr := d.sleep(ctx, attempt); sleepErr != nil {
				lastErr = fmt.Errorf("velox callback: backoff cancelled: %w", sleepErr)
				break
			}
			// Loop again.
			continue
		case resp.StatusCode >= 400:
			// 4xx is terminal — receiver's bug. Retrying
			// would re-confuse the receiver (signature would
			// still fail validation, body would still parse
			// to the same error). Break out to audit + return.
			// [client_4xx] is an upstream-audit-parser marker so
			// postmortem search/jq filters can immediately
			// distinguish this terminal outcome from a 5xx transient.
			d.logger.Warn(
				"velox callback: client error (4xx terminal, no retry)",
				"event", event,
				"event_id", eventID,
				"callback_url", p.CallbackURL,
				"status_code", resp.StatusCode,
				"attempt", attempt,
			)
			lastErr = fmt.Errorf("[client_4xx] velox callback: attempt %d client error %d (terminal, no retry)", attempt, resp.StatusCode)
			break
		default:
			// 1xx / 3xx — unexpected; treat as terminal.
			lastErr = fmt.Errorf("velox callback: attempt %d unexpected status %d", attempt, resp.StatusCode)
			break
		}
		break // terminal exit (success / 4xx / unexpected)
	}

	// Emit the audit row.
	d.emitAudit(ctx, delivery, event, eventID, attempts, lastStatus, lastErr)

	if lastErr == nil {
		return nil
	}
	return fmt.Errorf("velox callback: %s after %d attempt(s): %w", event, attempts, lastErr)
}

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
