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
// Adding a new event type is a 2-step change: add the const in
// internal_velox_callback_dispatcher_types.go + emit from the
// appropriate worker hook.
//
// File layout (split per concern, 2026-08):
//
//	internal_velox_callback_dispatcher.go         — dispatcher struct +
//	                                                constructor + Dispatch (this file)
//	internal_velox_callback_dispatcher_types.go   — VeloxCallbackEvent enum,
//	                                                payload, audit store, tuning consts
//	internal_velox_callback_dispatcher_helpers.go — sleep / signBody /
//	                                                emitAudit / derefString /
//	                                                defaultVeloxEventID
package api

import (
	"bytes"
	"context"
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
	if p.ContractVersion == "" {
		p.ContractVersion = "velox.delivery.event.v1"
	}
	if p.SocialDeliveryID == "" {
		p.SocialDeliveryID = delivery.ID
	}
	if p.DeliveryID == "" {
		p.DeliveryID = delivery.ExternalDeliveryID
	}
	if p.Sequence <= 0 {
		p.Sequence = int64(delivery.AttemptCount)
		if p.Sequence <= 0 {
			p.Sequence = 1
		}
	}
	if p.OccurredAt == nil {
		now := d.clock()
		p.OccurredAt = &now
	}
	if p.ExternalDeliveryID == "" {
		p.ExternalDeliveryID = delivery.ExternalDeliveryID
	}
	if p.Status == "" {
		p.Status = string(event)
	}
	if p.Phase == "" {
		p.Phase = string(event)
	}
	if p.RemoteID == "" && delivery.PlatformMediaID != nil {
		p.RemoteID = *delivery.PlatformMediaID
	}
	if p.RemoteURL == "" && delivery.PlatformURL != nil {
		p.RemoteURL = *delivery.PlatformURL
	}
	if p.ErrorCode == nil && delivery.LastErrorCode != nil {
		value := *delivery.LastErrorCode
		p.ErrorCode = &value
	}
	if p.ErrorMessage == nil && delivery.LastErrorMessage != nil {
		value := *delivery.LastErrorMessage
		p.ErrorMessage = &value
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
		// Canonical event headers. Keep the old X-Velox-* aliases during
		// the bounded migration window so already deployed receivers can
		// continue to consume retries.
		req.Header.Set("X-Event-ID", eventID)
		req.Header.Set("X-Timestamp", strconv.FormatInt(ts, 10))
		req.Header.Set("X-Signature", "sha256="+signature)

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
				"callback_url", derefString(delivery.CallbackURL),
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
