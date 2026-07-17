package services

import (
	"context"

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
	//   FAILED          → returns error (terminal)
	//   in-flight       → returns (nil, nil) — caller should retry later
	// The reconciler goroutine in the worker calls this on every tick.
	Reconcile(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error)
}
