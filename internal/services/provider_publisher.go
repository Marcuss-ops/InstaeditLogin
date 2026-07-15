package services

import (
	"context"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// Publisher publishes content to the platform. Every provider that
// supports publishing implements this.
//
// The PublishWorker (internal/worker/publish_worker.go) looks up the
// Publisher via `router.Publisher(account.Platform)` and invokes
// Publish() on every tick where a queued target becomes ready.
// Providers that additionally implement AsyncPublisher (see
// provider_async_publisher.go) get their Publish() entry point called
// once for the StartPublish, then the worker's reconciler goroutine
// drives the async state machine via AsyncPublisher.Reconcile.
type Publisher interface {
	NameProvider

	// Publish publishes content and returns the platform media ID.
	Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error)
}
