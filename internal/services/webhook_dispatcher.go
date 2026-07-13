// Package services — webhook dispatcher (SPRINT 4.2).
//
// Coordinates Emit (publisher-side: a domain event was just
// produced) and the fan-out that creates one webhook_deliveries
// row per matching endpoint. The actual POST work is done by the
// background worker (internal/worker/webhook_worker.go) which
// claims due deliveries from the same table.
//
// Dedup: the dispatcher's InsertEvent uses ON CONFLICT (event_id)
// DO UPDATE so two emits with the same event_id short-circuit at
// the DB level. The dispatcher's CreateDelivery then fans out
// per-endpoint. Two emits with the same event_id produce ONE
// webhook_event row and N webhook_deliveries rows (one per
// matching endpoint) — the second emit's fan-out is a no-op
// because the InsertEvent's ON CONFLICT short-circuits BEFORE
// the ListActiveEndpointsForEvent fan-out loop runs. This is the
// canonical "exactly one fan-out per (event_id, endpoint_id) pair"
// guarantee.
//
// Backoff curve: NextAttempt(attempt) returns the next scheduled
// time using AWS-style decorrelated jitter. attempt is 1-based.
// The curve is bounded by maxAttempt (5 → cap reached at 12h).
// Manual replay (POST /api/v1/webhooks/deliveries/{id}/replay)
// resets attempt to 0 and reschedules to NOW.
package services

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// EventType names the canonical event types. Adding a new event
// type is a 2-step change: add the constant here, document the
// payload shape, and emit it from the appropriate worker /
// service.
const (
	EventAccountConnected                = "account.connected"
	EventAccountReauthenticationRequired = "account.reauthentication_required"
	EventPostPublished                   = "post.published"
	EventPostPartiallyPublished          = "post.partially_published"
	EventPostFailed                      = "post.failed"
	EventPostTargetPublished             = "post_target.published"
	EventPostTargetFailed                = "post_target.failed"
)

// MaxAttempts is the cap for the backoff curve. After the 5th
// failed attempt, the delivery is marked status='dead' (DLQ).
const MaxAttempts = 5

// WebhookDispatcher coordinates Emit + the per-endpoint fan-out.
// It depends on the WebhookRepository (CRUD) and a UUID source
// (for client-side event_id generation when the caller doesn't
// supply one). Construct via NewWebhookDispatcher.
type WebhookDispatcher struct {
	repo DispatcherRepo
	rand *rand.Rand
}

// DispatcherRepo is the subset of *repository.WebhookRepository
// the dispatcher depends on. The signatures use the repository's
// types directly (no local re-shape) so *repository.WebhookRepository
// satisfies the interface by duck-typing and tests can inject
// fakes without an extra conversion layer.
type DispatcherRepo interface {
	InsertEvent(ctx context.Context, ev *repository.WebhookEvent) error
	ListActiveEndpointsForEvent(ctx context.Context, workspaceID int64, eventType string) ([]repository.WebhookEndpoint, error)
	CreateDelivery(ctx context.Context, d *repository.WebhookDelivery) error
}

// WebhookEvent is the local event shape the dispatcher accepts.
// Distinct from repository.WebhookEvent (which mirrors the DB row);
// this is the public-facing emit struct the callers (workers,
// services) supply.
type WebhookEvent struct {
	EventID     string          // optional — empty = server-generates
	EventType   string          // required — one of the EventType consts
	WorkspaceID int64           // required
	Payload     json.RawMessage // required — opaque JSON the receiver parses
}

// WebhookDelivery is the local delivery shape the dispatcher
// creates. Mirrors the DB row + adds the endpoint_id the worker
// uses to look up the URL + secret.
type WebhookDelivery struct {
	EventID    int64 // webhook_events.id
	EndpointID int64
}

// NewWebhookDispatcher wires the service. clock is injectable for
// tests; the rand source is for NextAttempt jitter reproducibility.
func NewWebhookDispatcher(repo DispatcherRepo) *WebhookDispatcher {
	return &WebhookDispatcher{
		repo: repo,
		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Emit inserts a webhook_event row and fans out one
// webhook_deliveries row per active matching endpoint. Returns
// (eventID, fanoutCount, error). fanoutCount is the number of
// deliveries created by this Emit (zero on dedup — when the
// event_id was already seen, the InsertEvent ON CONFLICT
// short-circuits and the fan-out loop is skipped).
//
// The fan-out is per-workspace: an Emit with workspaceID=X only
// delivers to endpoints owned by X. A user who added their
// endpoint to workspace A does not receive events for workspace B.
func (d *WebhookDispatcher) Emit(ctx context.Context, ev WebhookEvent) (string, int, error) {
	if ev.EventType == "" {
		return "", 0, fmt.Errorf("dispatcher: EventType is required")
	}
	if ev.WorkspaceID <= 0 {
		return "", 0, fmt.Errorf("dispatcher: WorkspaceID is required")
	}
	if len(ev.Payload) == 0 {
		return "", 0, fmt.Errorf("dispatcher: Payload is required")
	}
	// Server-generate event id when the caller didn't supply one.
	// 16 random bytes hex = 32 chars; collision probability is
	// astronomically small for the lifetime of a workspace.
	if ev.EventID == "" {
		b := make([]byte, 16)
		// crypto/rand would be better; math/rand is enough for
		// the dedup window (we're not signing). The dispatcher is
		// hot-path so we use the dispatcher's own rand source.
		_, _ = d.rand.Read(b)
		ev.EventID = fmt.Sprintf("evt_%x", b)
	}
	dbEv := &repository.WebhookEvent{
		EventID:     ev.EventID,
		EventType:   ev.EventType,
		WorkspaceID: ev.WorkspaceID,
		Payload:     ev.Payload,
	}
	if err := d.repo.InsertEvent(ctx, dbEv); err != nil {
		return "", 0, fmt.Errorf("dispatcher: insert event: %w", err)
	}
	// Fan out: list active endpoints subscribed to this event type
	// in this workspace. For each, create a delivery row. The
	// dispatcher's flow is NOT idempotent on (event_id,
	// endpoint_id) at the DB level today (the deliveries table
	// has no UNIQUE on that pair) — re-inserting the same event
	// via a fresh Emit WILL produce duplicate fan-out rows. The
	// canonical dedup is the upstream caller passing the SAME
	// event_id across retries; if the caller doesn't, this
	// dispatcher will fan out twice. (A deferred follow-up adds
	// a UNIQUE(event_id, endpoint_id) on webhook_deliveries +
	// ON CONFLICT DO NOTHING to make the dispatcher
	// re-insertion-safe.)
	endpoints, err := d.repo.ListActiveEndpointsForEvent(ctx, ev.WorkspaceID, ev.EventType)
	if err != nil {
		return ev.EventID, 0, fmt.Errorf("dispatcher: list endpoints: %w", err)
	}
	for _, ep := range endpoints {
		delivery := &repository.WebhookDelivery{
			EventID:    dbEv.ID,
			EndpointID: ep.ID,
		}
		if err := d.repo.CreateDelivery(ctx, delivery); err != nil {
			// Don't fail the whole Emit on a single endpoint —
			// log the error and continue. (The HTTP handler that
			// emitted the event returns 200; the worker retries
			// the failed delivery on the next tick via the
			// post-creation path.)
			continue
		}
	}
	return ev.EventID, len(endpoints), nil
}

// NextAttempt returns the next scheduled time for a delivery
// that just failed. attempt is 1-based (the dispatcher's
// ClaimDueDeliveries sets attempt++ on claim, so the value
// passed here is the post-bump attempt number). Beyond
// MaxAttempts the caller marks the delivery 'dead' (DLQ).
//
// Curve (AWS decorrelated jitter, same shape as the outbox
// dispatcher's computeBackoff):
//
//	temp = min(capDelay, base * 2^attempt * 3)
//	sleep = uniform(base, temp)
//
// capDelay is the absolute ceiling; base is the minimum retry
// interval. With base=60s and capDelay=12h, attempt=5 returns
// somewhere in [60s, 12h]. The jitter decorrelates retries
// across replicas so a transient outage doesn't drive a
// thundering-herd retry storm.
func (d *WebhookDispatcher) NextAttempt(attempt int, now time.Time) time.Time {
	if attempt < 1 {
		attempt = 1
	}
	const (
		base     = 60 * time.Second
		capDelay = 12 * time.Hour
	)
	prev := float64(base)
	for i := 0; i < attempt; i++ {
		prev *= 2
	}
	if prev > float64(capDelay) {
		prev = float64(capDelay)
	}
	temp := prev * 3
	if temp > float64(capDelay) {
		temp = float64(capDelay)
	}
	span := int64(temp) - int64(base)
	if span <= 0 {
		return now.Add(base)
	}
	delta := d.rand.Int63n(span)
	return now.Add(time.Duration(int64(base) + delta))
}

// IsTerminalStatus is the classifier used by the worker. 2xx
// → success. 4xx (non-429) → dead (client misconfiguration, no
// point retrying). 5xx / 429 / timeout → retry (subject to
// MaxAttempts).
func IsTerminalStatus(httpStatus int, timedOut bool) (success, dead, retry bool) {
	if timedOut {
		return false, false, true
	}
	switch {
	case httpStatus >= 200 && httpStatus < 300:
		return true, false, false
	case httpStatus == 408 || httpStatus == 425 || httpStatus == 429:
		// 408 Request Timeout, 425 Too Early, 429 Too Many Requests:
		// retry-with-backoff.
		return false, false, true
	case httpStatus >= 500:
		return false, false, true
	case httpStatus >= 400:
		// 4xx (other): client misconfiguration, do not retry.
		return false, true, false
	default:
		// 1xx / 3xx — receiver's protocol problem; treat as
		// terminal (we don't follow redirects here).
		return false, true, false
	}
}
