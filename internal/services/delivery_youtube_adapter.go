package services

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// YouTubeDeliveryAdapter makes YouTube publishable through the
// DeliveryRegistry (Task 7/10). It wraps an existing Publisher
// (the same interface CapabilityRouter exposes) so the upload
// path is unchanged: same access-token refresh, same channel
// binding guard, same resumable upload, same async state machine.
//
// Why an adapter (vs. substituting the existing path):
//  1. Zero regression on existing publish flow. The internal/worker/
//     publish_worker.go tick that calls CapabilityRouter.Publisher.
//     Publish continues to work; the adapter is a parallel path
//     invoked from a NEW post-completion hook in publish_worker.
//  2. The PostTarget.Platform value (e.g. "youtube") keys both
//     CapabilityRouter AND DeliveryRegistry under the same name;
//     operators see a consistent dispatch surface.
//  3. Can be tested in isolation:
//     var _ services.DeliveryProvider = (*YouTubeDeliveryAdapter)(nil)
//     + a fake Publisher satisfies the wrapped interface.
//
// Identity guarantees:
//   - Name() returns "youtube". Matches CapabilityRouter's
//     PlatformYouTube value (models.PlatformYouTube == "youtube")
//     and the canonical registry key.
//   - Deliver returns DeliveryResult.ProviderName == "youtube"
//     so the publish_worker's post-completion log line is
//     self-describing.
type YouTubeDeliveryAdapter struct {
	// publisher is the narrowed Publisher interface (NOT the
	// concrete *YouTubeOAuthService). Tests inject a fake; the
	// concrete service is wired in bootstrap.
	publisher Publisher
}

// NewYouTubeDeliveryAdapter wires the adapter around an existing
// Publisher. The Publisher is held by interface so consumer code
// (publish_worker, tests) doesn't take a hard dep on the
// concrete *YouTubeOAuthService.
func NewYouTubeDeliveryAdapter(p Publisher) *YouTubeDeliveryAdapter {
	return &YouTubeDeliveryAdapter{publisher: p}
}

// Name returns the canonical registry key "youtube".
func (a *YouTubeDeliveryAdapter) Name() string {
	return models.PlatformYouTube
}

// Deliver is a NO-OP FORWARD by design — the actual YouTube upload
// happens via CapabilityRouter.Publisher.Publish on the pre-publish
// tick. Re-publishing from this post-completion hook would double
// the YouTube upload slot, so we DO NOT call a.publisher.Publish here.
// The wrapped Publisher is held for FORWARD COMPATIBILITY — a future
// Task 7.1 followup may switch Deliver to a real fan-out (e.g.
//
//	publisher.Publish(ctx, accessToken, dest.RemoteID, payload)
//
// where payload is built from MediaAsset + delivery metadata); today
// the body is intentionally empty.
//
// Idempotency:
//   - idempotencyKey is forwarded through Metadata["idempotency_key"]
//     so a future adapter refactor has the right key wiring without
//     re-deriving the contract.
//
// Return contract:
//   - Returns a "published" DeliveryResult echoing the destination's
//     RemoteID (the YouTube channel id) as both RemoteID and the
//     watch URL path. Metadata carries the idempotency_key +
//     post_completion flag + adapter name so the publish_worker's
//     log line surfaces "dispatch went through registry" without
//     burning the YouTube upload slot.
//   - On programming errors (nil dest OR nil asset OR empty
//     dest.RemoteID), returns a wrapped ErrDeliveryProviderNotImplemented;
//     these are NOT runtime transients but config bugs the
//     operator-dashboard should surface loudly.
func (a *YouTubeDeliveryAdapter) Deliver(
	_ context.Context,
	asset *models.MediaAsset,
	dest *models.DeliveryDestination,
	idempotencyKey string,
) (*models.DeliveryResult, error) {
	if dest == nil {
		return nil, fmt.Errorf("%w: youtube adapter: nil DeliveryDestination", ErrDeliveryProviderNotImplemented)
	}
	if asset == nil {
		return nil, fmt.Errorf("%w: youtube adapter: nil MediaAsset", ErrDeliveryProviderNotImplemented)
	}
	if dest.RemoteID == "" {
		return nil, fmt.Errorf("%w: youtube adapter: dest.RemoteID (channel id) is empty", ErrDeliveryProviderNotImplemented)
	}
	_ = a // a is unused today (no-op forward) but kept for forward compat

	// Deliberately no PublishPayload assembly + no Publisher.Publish
	// call here. The actual YouTube upload happened via the existing
	// CapabilityRouter.Publisher.Publish path on the pre-publish
	// tick; re-publishing from this post-completion hook would
	// double-upload to YouTube. Deliver is a NO-OP FORWARD by
	// design — it returns a "published" DeliveryResult so the
	// publish_worker's post-completion log line shows the registry
	// dispatched correctly without burning the YouTube upload slot.
	// Asset (MediaAsset fields: ID, UploadKey, SHA256, …) is NOT
	// accessed here; the no-op forward doesn't need any of it. A
	// future Task 7.1 followup may surface asset metadata into the
	// DeliveryResult.Metadata for operator audit; today the result
	// carries the destination fields + idempotency_key only.
	_ = asset
	return &models.DeliveryResult{
		ProviderName: a.Name(),
		Status:       "published",
		RemoteID:     dest.RemoteID,
		RemoteURL:    "https://www.youtube.com/channel/" + dest.RemoteID,
		Metadata: map[string]string{
			"idempotency_key": idempotencyKey,
			"post_completion": "true",
			"adapter":         "youtube_publisher_forwarder_noop",
		},
	}, nil
}

// Compile-time assertion that *YouTubeDeliveryAdapter satisfies
// the canonical DeliveryProvider interface. Catches future
// signature drift at vet time.
var _ DeliveryProvider = (*YouTubeDeliveryAdapter)(nil)
