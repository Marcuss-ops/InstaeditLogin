// Package worker — Velox ingest state-machine.
//
// Wraps the canonical transition graph defined on
// models.ExternalDeliveryStatus (transitionMap + CanTransitionTo +
// CanonicalResume) with the persistence side-effect (external_
// delivery_repo.UpdateStatus). Each Transition call:
//
//  1. Validates the (from, to) pair via CanTransitionTo
//  2. Persists via the supplied ExternalDeliveryStore
//
// Architecturally: the SQL CHECK constraint on the status column
// enforces the value set (11 strings), the model layer enforces
// the transition graph, this FSM adds the
// "load-row → guard → persist" sequencing that workers would
// otherwise re-implement per-call site. The DB respects any legal
// value pair; the FSM refuses any illegal pair BEFORE the DB.
//
// Terminal states (Published / Failed / DeadLetter) are NO-OUT.
// The FSM surfaces them via ErrTerminal. DeadLetter is the only
// terminal state that has an IN edge (from RetryWait when the
// budget is exhausted); BlockedAuth/Failed are reachable from any
// pre-terminal state per the architectural spec.
//
// Retryable side states (RetryWait / BlockedAuth) keep a single
// reentrance edge (per the dual-target retry_wait → {downloading,
// queued} canonical decision + the blocked_auth → queued admin-
// reconnect edge). The Resume helper bridges the dual-target
// decision by calling CanonicalResume(downloadURLValid) and
// dispatching to the chosen target.
//
// Error classification: bold the typo'd state name from the
// original spec "bocked_auth" → "blocked_auth" (mirrors the
// existing models.ExternalDeliveryStatusBlockedAuth constant).
// The Go-layer guard catches any caller-supplied misspelling at
// compile time.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ExternalDeliveryStore is the narrow persistence contract the FSM
// requires. The real impl is *repository.ExternalDeliveryRepository
// (UpdateStatus method); tests inject an in-process fake.
//
// Defined inline so pkg/api + tests can swap the impl without
// pulling the full repo surface into the worker package.
type ExternalDeliveryStore interface {
	UpdateStatus(
		ctx context.Context,
		id string,
		newStatus models.ExternalDeliveryStatus,
		lastErrorCode,
		lastErrorMessage,
		platformMediaID,
		platformURL *string,
	) error
}

// ErrIllegalTransition is returned when a Transition call proposes
// a (from, to) pair that's NOT in the transitionMap. Workers MUST
// treat this as a logic bug: the call-site either picked a typo'd
// state name (rare; the enum is typed) or proposed a transition the
// spec intentionally disallows (e.g. retry_wait → artifact_verified
// skipping the re-fetch).
//
// Per the operator DBA caveat in
// internal/models/external_delivery.go: the SQL CHECK constraint
// validates the value set but NOT the transition graph, so an
// absent guard at this layer would surface as a row update that
// succeeds and only later appears as a dashboard anomaly. The
// two-layer defence (this ErrIllegalTransition pre-check + SQL
// CHECK value validation) is the architectural contract.
var ErrIllegalTransition = errors.New("ingest FSM: illegal transition for this state")

// ErrTerminal is returned when a Transition targets a terminal
// state as a SOURCE (no outgoing transitions are allowed from
// Published / Failed / DeadLetter). BlockedAuth is NOT terminal
// in this FSM — per transitionMap, blocked_auth → queued is the
// admin-reconnect edge. The name ErrTerminal reflects the
// models.ExternalDeliveryStatus.IsTerminal classifier, which
// groups published + failed + dead_letter + blocked_auth as
// "no further automatic transitions expected"; the FSM
// distinguishes BlockedAuth's admin-reconnect edge by allowing
// ONLY blocked_auth → queued (not blocked_auth → published
// directly).
var ErrTerminal = errors.New("ingest FSM: source state is terminal")

// IngestFSM wraps the persistence-side calls behind the
// transition-graph guard. All exported methods are safe for
// concurrent use across worker pool goroutines; the struct holds
// no per-call mutable state.
//
// Lifecycle:
//
//	fsm := NewIngestFSM(extDelivRepo, slog.Default())
//	err := fsm.ToArtifactVerified(ctx, "sdel_01J", models.ExternalDeliveryStatusDownloading)
//	if errors.Is(err, ErrIllegalTransition) { worker dead-letter via ToDeadLetter(...) }
//	if errors.Is(err, ErrTerminal) { worker drops silently — peer already terminal-stamped }
type IngestFSM struct {
	store  ExternalDeliveryStore
	logger *slog.Logger
}

// NewIngestFSM wires the FSM. logger nil-safe. The store is the
// only required dependency; the FSM's correctness does NOT depend
// on a clock or a random source (CanonicalResume's behavior is
// deterministic from its parameter).
func NewIngestFSM(store ExternalDeliveryStore, logger *slog.Logger) *IngestFSM {
	if logger == nil {
		logger = slog.Default()
	}
	return &IngestFSM{store: store, logger: logger}
}

// Transition is the core method: validates the proposed transition
// against transitionMap then persists via UpdateStatus. Returns
// ErrIllegalTransition when (from, to) is not in the graph; the
// DB is NOT touched in that case. Returns the wrapped
// UpdateStatus error when the DB write fails (callers SHOULD
// classify via errors.Is + the canonical sentinel if they need
// to retry on transient DB errors).
//
// The optional *string parameters (errorCode, errorMessage, mediaID,
// mediaURL) are COALESCE'd by the repo — nil preserves the
// existing column value. Pass nil for clean happy-path advances;
// pass &"code" + &"msg" for error exits; pass &"dQw4w9WgXcQ" for
// the published transition.
func (f *IngestFSM) Transition(
	ctx context.Context,
	deliveryID string,
	from,
	to models.ExternalDeliveryStatus,
	errCode,
	errMsg,
	mediaID,
	mediaURL *string,
) error {
	if deliveryID == "" {
		return errors.New("ingest FSM: empty deliveryID")
	}
	if from == "" || to == "" {
		return fmt.Errorf("ingest FSM: empty from/to (from=%q to=%q)", from, to)
	}
	// Special-case terminal-source rejection with a distinct
	// sentinel: blockers (MostILY typo'd downstream code) want a
	// direct error rather than a generic illegal-transition
	// message that conflates "source is terminal" with "pair is
	// misspelled".
	if from.IsTerminal() && from != models.ExternalDeliveryStatusBlockedAuth {
		f.logger.Debug("ingest FSM: rejected transition from terminal",
			"delivery_id", deliveryID, "from", from, "to", to,
		)
		return fmt.Errorf("%w: %s → %s", ErrTerminal, from, to)
	}
	if !from.CanTransitionTo(to) {
		f.logger.Warn("ingest FSM: rejected illegal transition",
			"delivery_id", deliveryID, "from", from, "to", to,
		)
		return fmt.Errorf("%w: %s → %s", ErrIllegalTransition, from, to)
	}
	if err := f.store.UpdateStatus(ctx, deliveryID, to, errCode, errMsg, mediaID, mediaURL); err != nil {
		f.logger.Error("ingest FSM: persist failed",
			"delivery_id", deliveryID, "from", from, "to", to, "error", err,
		)
		return fmt.Errorf("ingest FSM: persist transition %s → %s: %w", from, to, err)
	}
	f.logger.Debug("ingest FSM: transition ok",
		"delivery_id", deliveryID, "from", from, "to", to,
	)
	return nil
}

// =====================================================================
// HAPPY-PATH CONVENIENCE METHODS
// =====================================================================
//
// Each method bakes in the canonical 1-step successor for the
// publish pipeline. Workers call these instead of Transition
// directly when the next-status is known (the common case). The
// `from` parameter tells the FSM the assumed current state so
// CanTransitionTo can validate before the DB write; a wrong
// assumption surfaces as ErrIllegalTransition instead of silently
// skipping the guard.

// ToDownloading: accepted → downloading (worker starts the
// HEAD/GET against the Velox download_url).
func (f *IngestFSM) ToDownloading(ctx context.Context, deliveryID string, from models.ExternalDeliveryStatus) error {
	return f.Transition(ctx, deliveryID, from, models.ExternalDeliveryStatusDownloading, nil, nil, nil, nil)
}

// ToArtifactVerified: downloading → artifact_verified (sha256 +
// size + mime all match, file promoted into InstaEdit storage).
func (f *IngestFSM) ToArtifactVerified(ctx context.Context, deliveryID string, from models.ExternalDeliveryStatus) error {
	return f.Transition(ctx, deliveryID, from, models.ExternalDeliveryStatusArtifactVerified, nil, nil, nil, nil)
}

// ToIngestCompleted: artifact_verified → ingest_completed
// (upload_job created + asset_id stamped, publish pool eligible).
func (f *IngestFSM) ToIngestCompleted(ctx context.Context, deliveryID string, from models.ExternalDeliveryStatus) error {
	return f.Transition(ctx, deliveryID, from, models.ExternalDeliveryStatusIngestCompleted, nil, nil, nil, nil)
}

// ToQueued: ingest_completed → queued (publish pool claimed the
// row + publish_at window opened).
func (f *IngestFSM) ToQueued(ctx context.Context, deliveryID string, from models.ExternalDeliveryStatus) error {
	return f.Transition(ctx, deliveryID, from, models.ExternalDeliveryStatusQueued, nil, nil, nil, nil)
}

// ToPublishing: queued → publishing (YouTube videos.insert or
// analogous for non-YouTube providers is in flight).
func (f *IngestFSM) ToPublishing(ctx context.Context, deliveryID string, from models.ExternalDeliveryStatus) error {
	return f.Transition(ctx, deliveryID, from, models.ExternalDeliveryStatusPublishing, nil, nil, nil, nil)
}

// ToPublished: publishing → published (terminal success). Caller
// passes mediaID + mediaURL (both must be non-nil to populate the
// platform_media_id + platform_url columns; the COALESCE in
// UpdateStatus preserves prior values if BOTH are nil, but a
// published transition without media_id is a logic bug — the
// YouTube id is what the operator's dashboard needs to render
// the post).
func (f *IngestFSM) ToPublished(ctx context.Context, deliveryID string, from models.ExternalDeliveryStatus, mediaID, mediaURL *string) error {
	return f.Transition(ctx, deliveryID, from, models.ExternalDeliveryStatusPublished, nil, nil, mediaID, mediaURL)
}

// =====================================================================
// ERROR / SIDE-STATE EXIT METHODS
// =====================================================================

// ToRetryWait: any pre-terminal → retry_wait (transient, backoff).
// Valid sources: accepted, downloading, artifact_verified,
// ingest_completed, queued, publishing (per transitionMap).
//
// The canonical srcStatus passed by the worker is the LAST-observed
// pre-error state of the row (NOT a guess). A wrong guess turns
// into ErrIllegalTransition which the worker should treat as a
// "peer-stale-state — re-read the row" signal.
func (f *IngestFSM) ToRetryWait(ctx context.Context, deliveryID string, from models.ExternalDeliveryStatus, code, msg string) error {
	return f.Transition(ctx, deliveryID, from, models.ExternalDeliveryStatusRetryWait,
		strPtr(code), strPtr(msg), nil, nil)
}

// ToFailed: any pre-terminal → failed (terminal non-recoverable).
// Reserved for 4xx / permanent errors (SHA mismatch, MIME
// mismatch, malformed JSON). The terminal stamp fires
// completed_at=NOW() automatically via the repo's CASE clause.
func (f *IngestFSM) ToFailed(ctx context.Context, deliveryID string, from models.ExternalDeliveryStatus, code, msg string) error {
	return f.Transition(ctx, deliveryID, from, models.ExternalDeliveryStatusFailed,
		strPtr(code), strPtr(msg), nil, nil)
}

// ToBlockedAuth: any pre-terminal → blocked_auth (platform_account
// reauth_required; worker halts). The row is classified as
// terminal for the worker's claim CTE (per IsTerminal); only the
// admin-reconnect handler transitions it BACK to queued.
func (f *IngestFSM) ToBlockedAuth(ctx context.Context, deliveryID string, from models.ExternalDeliveryStatus, code, msg string) error {
	return f.Transition(ctx, deliveryID, from, models.ExternalDeliveryStatusBlockedAuth,
		strPtr(code), strPtr(msg), nil, nil)
}

// ToDeadLetter: retry_wait → dead_letter (retry budget exhausted).
// Per transitionMap, dead_letter is reachable ONLY from
// retry_wait — NOT directly from downloading/queued/etc. The
// worker SHOULD issue ToRetryWait first when retry_count exhausts,
// then ToDeadLetter on the subsequent tick (so the operator
// audit log captures the intermediate backoff state).
func (f *IngestFSM) ToDeadLetter(ctx context.Context, deliveryID string, from models.ExternalDeliveryStatus, code, msg string) error {
	return f.Transition(ctx, deliveryID, from, models.ExternalDeliveryStatusDeadLetter,
		strPtr(code), strPtr(msg), nil, nil)
}

// =====================================================================
// RESUME + RECOVERY METHODS
// =====================================================================

// Resume picks one of the two legal retry_wait resume targets
// (download_url_valid=true → queued, false → downloading) via
// models.CanonicalResume, then dispatches the transition. Returns
// a non-nil error when the source state is NOT retry_wait —
// recovery from a non-retry_wait state must use Transition
// (or one of the convenience methods) directly.
//
// Worker MUST verify the external_delivery's DownloadURL != nil
// before calling Resume(true) — CanonicalResume has no visibility
// into the URL pointer; passing the metadata-only delivery
// through CanonicalResume(true) → queued would be a semantic bug
// (no artifact exists to skip re-fetch).
func (f *IngestFSM) Resume(ctx context.Context, deliveryID string, from models.ExternalDeliveryStatus, downloadURLValid bool) error {
	target := from.CanonicalResume(downloadURLValid)
	if target == "" {
		return fmt.Errorf("ingest FSM: Resume called on non-retry_wait state %q", from)
	}
	return f.Transition(ctx, deliveryID, from, target, nil, nil, nil, nil)
}

// ToQueuedFromBlockedAuth (admin-reconnect edge): blocked_auth →
// queued. Called by the admin-reconnect handler (NOT the worker
// pool) once the platform_account reauth completes. Mirrors the
// single edge in transitionMap[blocked_auth] = {queued: true}.
//
// Named with the source in the method name (vs. the 1-step
// convention happy-path above) because there's exactly ONE legal
// successor — a generic ToQueued(from) would force the worker to
// pass an explicit from that's redundant info when from IS
// blocked_auth.
func (f *IngestFSM) ToQueuedFromBlockedAuth(ctx context.Context, deliveryID string) error {
	return f.Transition(ctx, deliveryID, models.ExternalDeliveryStatusBlockedAuth, models.ExternalDeliveryStatusQueued, nil, nil, nil, nil)
}

// strPtr is the typed helper for the *string parameters on
// Transition — Go doesn't auto-coerce a string literal to *string,
// so the helper keeps the call sites readable.
func strPtr(s string) *string { return &s }
