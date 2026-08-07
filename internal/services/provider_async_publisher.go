package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// AsyncPublisher models the four-step state machine for platforms whose
// publish is asynchronous (TikTok + Threads today; the interface is
// here so other async platforms can opt in without changing the worker).
//
// The flow is:
//
//  1. StartPublish       — initiate the publish, return publish_id,
//     return immediately. Stored on
//     post_target.platform_post_id + provider_state.
//  2. CheckPublishStatus — single status query, no polling. Returns the
//     platform's current state string
//     (PROCESSING_UPLOAD / PENDING_PUBLISH /
//     IN_REVIEW / PUBLISH_COMPLETE / FAILED).
//  3. ContinuePublish    — for PULL_FROM_FILE chunked upload, no-op for
//     PULL_FROM_URL.
//  4. Reconcile          — combines CheckPublishStatus + transition
//     decision: PUBLISH_COMPLETE → success
//     result; FAILED → error; in-flight →
//     (nil, nil) — try again next tick.
//
// Taglio 4.2: replaces the old synchronous polling loop inside the
// worker's tick with a separate reconciler goroutine. Publish()
// returns immediately with the publish_id; the reconciler calls
// Reconcile on every tick to advance the async state machine.
// ErrPublishTerminal marks a provider response whose explicit state says
// the asynchronous publish has permanently failed (for example FAILED,
// ERROR, or EXPIRED). The reconciler uses this marker to distinguish an
// authoritative provider terminal state from a transport error.
var ErrPublishTerminal = errors.New("publish reached terminal provider state")

// TerminalPublishError carries an explicit terminal state reported by a
// provider. It is intentionally separate from generic errors: a timeout or
// network reset must be retried, while this error must transition the target
// to failed immediately.
type TerminalPublishError struct {
	State string
	Err   error
}

func (e *TerminalPublishError) Error() string {
	if e == nil {
		return ErrPublishTerminal.Error()
	}
	if e.Err == nil {
		return fmt.Sprintf("publish terminal state: %s", e.State)
	}
	return fmt.Sprintf("publish terminal state %s: %v", e.State, e.Err)
}

func (e *TerminalPublishError) Unwrap() error {
	if e == nil {
		return ErrPublishTerminal
	}
	return errors.Join(ErrPublishTerminal, e.Err)
}

// NewTerminalPublishError constructs the canonical terminal-state error.
func NewTerminalPublishError(state string, err error) error {
	return &TerminalPublishError{State: state, Err: err}
}

// ErrPublishPermanent marks a provider-side reconciliation error that is
// known not to succeed by polling again (for example a channel mismatch).
var ErrPublishPermanent = errors.New("publish reconciliation permanent error")

// PermanentPublishError carries a non-retryable provider reconciliation
// failure while preserving the underlying cause for diagnostics.
type PermanentPublishError struct {
	Err error
}

func (e *PermanentPublishError) Error() string {
	if e == nil || e.Err == nil {
		return ErrPublishPermanent.Error()
	}
	return e.Err.Error()
}

func (e *PermanentPublishError) Unwrap() error {
	if e == nil {
		return ErrPublishPermanent
	}
	return errors.Join(ErrPublishPermanent, e.Err)
}

// NewPermanentPublishError constructs the canonical non-retryable error.
func NewPermanentPublishError(err error) error {
	return &PermanentPublishError{Err: err}
}

type AsyncPublisher interface {
	NameProvider

	// StartPublish initiates the async publish.
	StartPublish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (publishID string, state string, err error)

	// CheckPublishStatus does a SINGLE GET to the platform's status
	// endpoint. Returns the current state string. Does NOT poll.
	CheckPublishStatus(ctx context.Context, accessToken, publishID string) (state string, err error)

	// ContinuePublish is a placeholder for PULL_FROM_FILE chunked
	// upload flows. For PULL_FROM_URL (the default) it's a no-op that
	// returns nil — the platform fetches the video directly from the
	// URL. Provided for forward-compat with platforms that need
	// explicit chunked upload.
	ContinuePublish(ctx context.Context, accessToken, publishID string) error

	// Reconcile queries the platform and decides the transition:
	//   PUBLISH_COMPLETE → returns *PublishResult (success, terminal)
	//   explicit FAILED/ERROR/EXPIRED state → returns a TerminalPublishError
	//   permanent provider rejection → returns a PermanentPublishError
	//   transient transport/provider failure → returns the retryable error
	//   in-flight       → returns (nil, nil) — caller should poll later
	// The reconciler worker applies the common retry policy to these outcomes.
	Reconcile(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error)
}
