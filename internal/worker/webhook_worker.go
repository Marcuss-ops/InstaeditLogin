package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

const (
	DefaultWebhookInterval          = 5 * time.Second
	DefaultWebhookHTTPTimeout       = 30 * time.Second
	DefaultWebhookBatchSize         = 25
	DefaultWebhookConcurrency       = 4
	DefaultWebhookLeaseTTL          = 60 * time.Second
	DefaultWebhookHeartbeatInterval = 20 * time.Second
)

// WebhookRepo is the durable lease-aware delivery surface. Every write after
// ClaimDueDeliveries is fenced by the returned lease_id.
type WebhookRepo interface {
	ClaimDueDeliveries(ctx context.Context, limit int, leaseTTL time.Duration) ([]repository.WebhookDelivery, error)
	HeartbeatLease(ctx context.Context, id int64, leaseID string, leaseTTL time.Duration) error
	MarkSuccess(ctx context.Context, id int64, leaseID, responseLog string) error
	MarkRetry(ctx context.Context, id int64, leaseID, lastError, requestLog, responseLog string, nextAttemptAt time.Time) error
	MarkDead(ctx context.Context, id int64, leaseID, lastError, requestLog, responseLog string) error
	FindEventByID(ctx context.Context, id int64) (*repository.WebhookEvent, error)
	FindEndpointByID(ctx context.Context, id int64) (*repository.WebhookEndpoint, error)
}

type WebhookWorkerOptions struct {
	Interval          time.Duration
	BatchSize         int
	Concurrency       int
	HTTPTimeout       time.Duration
	LeaseTTL          time.Duration
	HeartbeatInterval time.Duration
	Logger            *slog.Logger
	HTTPClient        *http.Client
}

type WebhookWorker struct {
	repo              WebhookRepo
	signer            *services.WebhookSigner
	dispatcher        *services.WebhookDispatcher
	httpClient        *http.Client
	httpTimeout       time.Duration
	interval          time.Duration
	batchSize         int
	concurrency       int
	leaseTTL          time.Duration
	heartbeatInterval time.Duration
	logger            *slog.Logger
	clock             func() time.Time
	processed         int64
	retried           int64
	dead              int64
}

func NewWebhookWorker(repo WebhookRepo, interval time.Duration) *WebhookWorker {
	return NewWebhookWorkerWithOptions(repo, WebhookWorkerOptions{Interval: interval})
}

// NewWebhookWorkerWithOptions applies safe defaults and enforces that a lease
// outlives one HTTP attempt. A heartbeat runs well before the lease expires.
func NewWebhookWorkerWithOptions(repo WebhookRepo, opts WebhookWorkerOptions) *WebhookWorker {
	if opts.Interval <= 0 {
		opts.Interval = DefaultWebhookInterval
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = DefaultWebhookBatchSize
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = DefaultWebhookConcurrency
	}
	if opts.HTTPTimeout <= 0 {
		opts.HTTPTimeout = DefaultWebhookHTTPTimeout
	}
	if opts.LeaseTTL <= 0 {
		opts.LeaseTTL = DefaultWebhookLeaseTTL
	}
	minimumLease := opts.HTTPTimeout + 10*time.Second
	if opts.LeaseTTL < minimumLease {
		opts.LeaseTTL = minimumLease
	}
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = opts.LeaseTTL / 3
	}
	if opts.HeartbeatInterval >= opts.LeaseTTL {
		opts.HeartbeatInterval = opts.LeaseTTL / 3
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = services.NewHTTPClientWithTimeout(opts.HTTPTimeout)
	}
	return &WebhookWorker{
		repo: repo, signer: services.NewWebhookSigner(),
		dispatcher: services.NewWebhookDispatcher(nil), httpClient: opts.HTTPClient,
		httpTimeout: opts.HTTPTimeout,
		interval:    opts.Interval, batchSize: opts.BatchSize, concurrency: opts.Concurrency,
		leaseTTL: opts.LeaseTTL, heartbeatInterval: opts.HeartbeatInterval,
		logger: opts.Logger, clock: time.Now,
	}
}

// NewWebhookWorkerWithDeps is retained for existing tests and callers.
func NewWebhookWorkerWithDeps(repo WebhookRepo, signer *services.WebhookSigner, dispatcher *services.WebhookDispatcher, interval time.Duration, batchSize int, logger *slog.Logger) *WebhookWorker {
	w := NewWebhookWorkerWithOptions(repo, WebhookWorkerOptions{Interval: interval, BatchSize: batchSize, Logger: logger})
	if signer != nil {
		w.signer = signer
	}
	if dispatcher != nil {
		w.dispatcher = dispatcher
	}
	return w
}

func (w *WebhookWorker) WithConcurrency(n int) *WebhookWorker {
	if n > 0 {
		w.concurrency = n
	}
	return w
}
func (w *WebhookWorker) WithLeaseTTL(ttl time.Duration) *WebhookWorker {
	if ttl > 0 {
		minimum := w.httpTimeout + 10*time.Second
		if ttl < minimum {
			ttl = minimum
		}
		w.leaseTTL = ttl
		if w.heartbeatInterval >= ttl {
			w.heartbeatInterval = ttl / 3
		}
	}
	return w
}

func (w *WebhookWorker) Run(ctx context.Context) error {
	w.logger.Info("webhook worker started", "interval_seconds", w.interval.Seconds(), "batch_size", w.batchSize, "concurrency", w.concurrency, "lease_seconds", w.leaseTTL.Seconds(), "heartbeat_seconds", w.heartbeatInterval.Seconds())
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

func (w *WebhookWorker) Processed() int64 { return atomic.LoadInt64(&w.processed) }
func (w *WebhookWorker) Retried() int64   { return atomic.LoadInt64(&w.retried) }
func (w *WebhookWorker) Dead() int64      { return atomic.LoadInt64(&w.dead) }

func (w *WebhookWorker) runOnce(ctx context.Context) {
	// Never claim rows that would wait behind a local semaphore. This keeps
	// every claimed lease actively processing and prevents lease expiry before
	// the HTTP request starts. The bounded batch remains useful when the
	// configured concurrency is larger than the batch size.
	limit := w.concurrency
	if limit > w.batchSize {
		limit = w.batchSize
	}
	deliveries, err := w.repo.ClaimDueDeliveries(ctx, limit, w.leaseTTL)
	if err != nil {
		w.logger.Warn("webhook worker ClaimDueDeliveries error", "error", err)
		return
	}
	if len(deliveries) == 0 {
		return
	}
	var wg sync.WaitGroup
	for i := range deliveries {
		wg.Add(1)
		d := &deliveries[i]
		go func() { defer wg.Done(); w.processOne(ctx, d) }()
	}
	wg.Wait()
}

func (w *WebhookWorker) processOne(ctx context.Context, d *repository.WebhookDelivery) {
	if d.LeaseID == "" {
		w.logger.Warn("webhook worker received delivery without lease", "delivery_id", d.ID)
		return
	}
	hbCtx, cancel := context.WithCancel(context.Background())
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		ticker := time.NewTicker(w.heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				if err := w.repo.HeartbeatLease(hbCtx, d.ID, d.LeaseID, w.leaseTTL); err != nil {
					if !errors.Is(err, repository.ErrWebhookLeaseLost) {
						w.logger.Warn("webhook heartbeat failed", "delivery_id", d.ID, "error", err)
					}
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-hbDone }()

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
	// Enforce the configured timeout even when tests or callers inject a
	// client with Timeout==0. The lease margin is calculated from this same
	// deadline, so a slow request cannot outlive ownership indefinitely.
	requestCtx, cancelRequest := context.WithTimeout(req.Context(), w.httpTimeout)
	defer cancelRequest()
	req = req.WithContext(requestCtx)
	resp, err := w.httpClient.Do(req)
	if err != nil {
		w.routeFailure(d, services.ClassifyErrorFor("webhook", "delivery", err), err.Error(), requestLog, "")
		return
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
	responseLog := fmt.Sprintf("HTTP %d %s", resp.StatusCode, string(bodyBytes))
	if classification := services.ClassifyHTTPStatus("webhook", "delivery", resp.StatusCode, services.ParseThrottleHeaders(resp.Header)); classification == nil {
		if err := w.repo.MarkSuccess(context.Background(), d.ID, d.LeaseID, responseLog); err != nil {
			w.logger.Warn("webhook worker MarkSuccess error", "delivery_id", d.ID, "error", err)
			return
		}
		atomic.AddInt64(&w.processed, 1)
	} else {
		w.routeFailure(d, classification, resp.Status, requestLog, responseLog)
	}
}

// routeFailure applies the shared provider-independent classification to one
// delivery. Auth and permanent errors go directly to the DLQ; transient and
// rate-limited errors are retried until MaxAttempts. A provider Retry-After
// hint wins over the local jitter schedule.
func (w *WebhookWorker) routeFailure(d *repository.WebhookDelivery, classification *services.NormalizedError, detail, requestLog, responseLog string) {
	if classification == nil {
		classification = services.ClassifyError(errors.New("webhook delivery failure"))
	}
	lastError := classification.Code
	if detail != "" {
		lastError = fmt.Sprintf("%s: %s", classification.Code, detail)
	}
	if !classification.Retryable || classification.Kind == services.ErrorKindAuth || classification.Kind == services.ErrorKindPermanent {
		w.markDead(d, lastError, requestLog, responseLog)
		return
	}
	if d.Attempt >= services.MaxAttempts {
		w.markDead(d, fmt.Sprintf("max attempts (%d) reached: %s", services.MaxAttempts, lastError), requestLog, responseLog)
		return
	}
	nextAt := w.dispatcher.NextAttempt(d.Attempt, w.clock())
	if classification.RetryAfter > 0 {
		nextAt = w.clock().Add(classification.RetryAfter)
	}
	if err := w.repo.MarkRetry(context.Background(), d.ID, d.LeaseID, lastError, requestLog, responseLog, nextAt); err != nil {
		w.logger.Warn("webhook worker MarkRetry error", "delivery_id", d.ID, "error", err)
		return
	}
	atomic.AddInt64(&w.retried, 1)
}

func (w *WebhookWorker) markDead(d *repository.WebhookDelivery, lastErr, requestLog, responseLog string) {
	if err := w.repo.MarkDead(context.Background(), d.ID, d.LeaseID, lastErr, requestLog, responseLog); err != nil {
		w.logger.Warn("webhook worker MarkDead error", "delivery_id", d.ID, "error", err)
		return
	}
	atomic.AddInt64(&w.dead, 1)
}
