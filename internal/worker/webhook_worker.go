// Package worker — webhook_worker.go (SPRINT 4.2).
//
// Background goroutine that drains the webhook_deliveries table:
// every interval, claim a batch of due deliveries (status='pending'
// AND scheduled_at <= NOW()), POST each to its endpoint with
// HMAC-SHA256 signing, classify the response, and either mark
// success / mark-retry (reschedule) / mark-dead (DLQ).
//
// Mirrors the lifecycle shape of the publish driver, the
// reconciler, and the outbox dispatcher: one struct, one Run
// loop, ctx-cancellable. Multi-replica safety is delegated to
// WebhookRepository.ClaimDueDeliveries (SELECT FOR UPDATE SKIP
// LOCKED + UPDATE in a single tx).
//
// Retry-vs-DLQ classifier: internal/services/webhook_dispatcher.go
// IsTerminalStatus. 2xx → success; 4xx (non-408/425/429) → dead;
// 5xx / 408 / 425 / 429 / timeout → retry (subject to MaxAttempts).
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// DefaultWebhookInterval is the default tick interval for the
// webhook worker. Faster than the publish driver (30s) so an
// end-to-end delivery latency is bounded by a 1-2s ceiling under
// typical load. Operators can override via env in the future
// (Taglio-style cfg.Worker.WebhookWorkerIntervalSeconds).
const DefaultWebhookInterval = 5 * time.Second

// DefaultWebhookHTTPTimeout caps any single POST attempt. Past
// this, the request is treated as a timeout (retry, not DLQ).
// 30s is the typical SaaS webhook budget; longer is impolite.
const DefaultWebhookHTTPTimeout = 30 * time.Second

// DefaultWebhookBatchSize is the max deliveries claimed per tick.
// Keeps memory bounded + gives other replicas a chance to claim
// (the SKIP LOCKED skip-others is per-row, not per-tx).
const DefaultWebhookBatchSize = 25

// WebhookRepo is the subset of *repository.WebhookRepository the
// worker depends on. Defined inline so tests can inject a fake
// without importing internal/repository.
type WebhookRepo interface {
	ClaimDueDeliveries(ctx context.Context, limit int) ([]repository.WebhookDelivery, error)
	MarkSuccess(ctx context.Context, id int64, responseLog string) error
	MarkRetry(ctx context.Context, id int64, lastError, requestLog, responseLog string, nextAttemptAt time.Time) error
	MarkDead(ctx context.Context, id int64, lastError, requestLog, responseLog string) error
	FindEventByID(ctx context.Context, id int64) (*repository.WebhookEvent, error)
	FindEndpointByID(ctx context.Context, id int64) (*repository.WebhookEndpoint, error)
}

// WebhookWorker is the background goroutine that drains the
// webhook_deliveries table. Construct via NewWebhookWorker.
type WebhookWorker struct {
	repo       WebhookRepo
	signer     *services.WebhookSigner
	dispatcher *services.WebhookDispatcher
	httpClient *http.Client
	interval   time.Duration
	batchSize  int
	logger     *slog.Logger
	clock      func() time.Time
	// metrics counters
	processed int64
	retried   int64
	dead      int64
}

// NewWebhookWorker wires the dependencies. interval <= 0 falls
// back to DefaultWebhookInterval (5s). batchSize <= 0 falls
// back to DefaultWebhookBatchSize (25). httpClient with nil
// timeout defaults to DefaultWebhookHTTPTimeout (30s). nil
// logger inherits slog.Default(). signer/dispatcher are
// constructed internally; tests can use NewWebhookWorkerWithDeps
// to inject fakes.
func NewWebhookWorker(repo WebhookRepo, interval time.Duration) *WebhookWorker {
	if interval <= 0 {
		interval = DefaultWebhookInterval
	}
	return &WebhookWorker{
		repo:       repo,
		signer:     services.NewWebhookSigner(),
		dispatcher: services.NewWebhookDispatcher(nil), // unused on the worker hot path
		httpClient: &http.Client{Timeout: DefaultWebhookHTTPTimeout},
		interval:   interval,
		batchSize:  DefaultWebhookBatchSize,
		logger:     slog.Default(),
		clock:      time.Now,
	}
}

// NewWebhookWorkerWithDeps is the test constructor.
func NewWebhookWorkerWithDeps(
	repo WebhookRepo,
	signer *services.WebhookSigner,
	dispatcher *services.WebhookDispatcher,
	interval time.Duration,
	batchSize int,
	logger *slog.Logger,
) *WebhookWorker {
	if interval <= 0 {
		interval = DefaultWebhookInterval
	}
	if batchSize <= 0 {
		batchSize = DefaultWebhookBatchSize
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookWorker{
		repo:       repo,
		signer:     signer,
		dispatcher: dispatcher,
		httpClient: &http.Client{Timeout: DefaultWebhookHTTPTimeout},
		interval:   interval,
		batchSize:  batchSize,
		logger:     logger,
		clock:      time.Now,
	}
}

// Run blocks until ctx is cancelled. Initial tick runs before the
// first ticker tick. Returns ctx.Err() on shutdown.
func (w *WebhookWorker) Run(ctx context.Context) error {
	w.logger.Info("webhook worker started",
		"interval_seconds", w.interval.Seconds(),
		"batch_size", w.batchSize)
	defer w.logger.Info("webhook worker stopped")

	w.runOnce(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

// Processed returns the cumulative count of webhook deliveries
// processed across the worker's lifetime. Used by tests + the
// /api/v1/admin/webhook-stats endpoint (deferred).
func (w *WebhookWorker) Processed() int64 { return atomic.LoadInt64(&w.processed) }
func (w *WebhookWorker) Retried() int64   { return atomic.LoadInt64(&w.retried) }
func (w *WebhookWorker) Dead() int64      { return atomic.LoadInt64(&w.dead) }

func (w *WebhookWorker) runOnce(ctx context.Context) {
	deliveries, err := w.repo.ClaimDueDeliveries(ctx, w.batchSize)
	if err != nil {
		w.logger.Warn("webhook worker ClaimDueDeliveries error", "error", err)
		return
	}
	if len(deliveries) == 0 {
		return
	}
	for i := range deliveries {
		w.processOne(ctx, &deliveries[i])
	}
}

// processOne handles a single claimed delivery: load event +
// endpoint, sign + POST, classify, mark.
func (w *WebhookWorker) processOne(ctx context.Context, d *repository.WebhookDelivery) {
	// ClaimDueDeliveries already incremented the attempt counter
	// (so d.Attempt is the post-bump value).
	ev, err := w.repo.FindEventByID(ctx, d.EventID)
	if err != nil {
		w.markDead(d, fmt.Sprintf("load event %d: %v", d.EventID, err), "", "")
		return
	}
	ep, err := w.repo.FindEndpointByID(ctx, d.EndpointID)
	if err != nil {
		w.markDead(d, fmt.Sprintf("load endpoint %d: %v", d.EndpointID, err), "", "")
		return
	}
	// Sign + POST.
	ts, headers := w.signer.FormatHeaders([]byte(ep.Secret), ev.EventID, ev.Payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewReader(ev.Payload))
	if err != nil {
		w.markDead(d, fmt.Sprintf("build request: %v", err), "", "")
		return
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	requestLog := fmt.Sprintf("POST %s ts=%d event_id=%s", ep.URL, ts, ev.EventID)
	resp, err := w.httpClient.Do(req)
	if err != nil {
		// Transport error (timeout, DNS, connection refused) — retry.
		w.classifyFailure(d, 0, true, err.Error(), requestLog, "")
		return
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
	responseLog := fmt.Sprintf("HTTP %d %s", resp.StatusCode, string(bodyBytes))
	success, dead, retry := services.IsTerminalStatus(resp.StatusCode, false)
	switch {
	case success:
		if err := w.repo.MarkSuccess(ctx, d.ID, responseLog); err != nil {
			w.logger.Warn("webhook worker MarkSuccess error", "delivery_id", d.ID, "error", err)
		}
		atomic.AddInt64(&w.processed, 1)
		w.logger.Info("webhook delivered", "delivery_id", d.ID, "event_id", ev.EventID, "endpoint_id", d.EndpointID, "attempt", d.Attempt)
	case dead:
		w.markDead(d, fmt.Sprintf("HTTP %d (terminal)", resp.StatusCode), requestLog, responseLog)
	case retry:
		w.classifyFailure(d, resp.StatusCode, false, resp.Status, requestLog, responseLog)
	}
}

// classifyFailure handles the retry-or-DLQ decision for a
// transient failure (5xx, 408, 425, 429, or transport error).
// If attempt >= MaxAttempts, the delivery is marked dead (DLQ).
// Otherwise it is rescheduled per the NextAttempt backoff curve.
func (w *WebhookWorker) classifyFailure(d *repository.WebhookDelivery, httpStatus int, timedOut bool, errStr, requestLog, responseLog string) {
	if d.Attempt >= services.MaxAttempts {
		w.markDead(d, fmt.Sprintf("max attempts (%d) reached: HTTP %d %s (timeout=%v)", services.MaxAttempts, httpStatus, errStr, timedOut), requestLog, responseLog)
		return
	}
	nextAt := w.dispatcher.NextAttempt(d.Attempt, w.clock())
	if err := w.repo.MarkRetry(context.Background(), d.ID, errStr, requestLog, responseLog, nextAt); err != nil {
		w.logger.Warn("webhook worker MarkRetry error", "delivery_id", d.ID, "error", err)
	}
	atomic.AddInt64(&w.retried, 1)
	w.logger.Info("webhook retry scheduled", "delivery_id", d.ID, "attempt", d.Attempt, "next_at", nextAt, "http_status", httpStatus, "timed_out", timedOut)
}

// markDead is the terminal-failure path. Called for 4xx
// (non-408/425/429), max-attempts-exhausted, or repo-level errors
// (load event/endpoint/build request).
func (w *WebhookWorker) markDead(d *repository.WebhookDelivery, lastErr, requestLog, responseLog string) {
	if err := w.repo.MarkDead(context.Background(), d.ID, lastErr, requestLog, responseLog); err != nil {
		w.logger.Warn("webhook worker MarkDead error", "delivery_id", d.ID, "error", err)
	}
	atomic.AddInt64(&w.dead, 1)
	w.logger.Warn("webhook dead-lettered", "delivery_id", d.ID, "reason", lastErr)
}

// _ keeps encoding/json referenced if future versions add JSON
// request bodies (the current body comes pre-encoded as
// webhook_events.payload).
var _ = json.Marshal
