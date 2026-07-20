package services

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// VeloxCallbackDeliveryAdapter is the post-completion Velox-callback
// adapter registered under the "velox_callback" key on
// DeliveryRegistry. It surfaces a wrapper contract that records
// the dispatch intent; the actual HTTP POST to the Velox
// callback URL is delegated to the existing
// internal_velox_callback_dispatcher.go wiring (no behaviour change
// at the HTTP layer — Task 7/10 is a registry-layer refactor).
//
// Identity:
//   - Name() returns "velox_callback". Distinct from the
//     DeliveryProvider convention's underscored name because the
//     Velox callback is a notification channel, not a publish
//     destination — the underscore signals "side-channel" to
//     operators reading the registry's Names() list at startup.
//
// Why this exists (vs. continuing to call the dispatcher
// directly): the existing internal_velox_callback_dispatcher.go
// is a low-level "fire a callback" primitive with no registration
// surface. Publishing it through the DeliveryRegistry gives it
// the same dispatch-by-name contract as YouTube / GoogleDrive so
// a future "all deliveries fail uniformly because Velox is
// down" operator dashboard table can group the failures by
// registry key without per-provider bespoke plumbing.
type VeloxCallbackDeliveryAdapter struct {
	// enabled toggles dispatch on/off without removing the
	// registration. Used by tests to assert that the registry
	// dispatches to the adapter without needing a live HTTP
	// transport. Production wires enabled=true.
	//
	// Zero-value (false) is the conservative default; bootstrap
	// explicitly sets true when a Velox callback URL is
	// configured for the workspace.
	enabled bool
}

// NewVeloxCallbackDeliveryAdapter returns the adapter. Pass
// enabled=true in production bootstrap; tests pass false + a
// fake to assert the dispatch without HTTP side effects.
func NewVeloxCallbackDeliveryAdapter(enabled bool) *VeloxCallbackDeliveryAdapter {
	return &VeloxCallbackDeliveryAdapter{enabled: enabled}
}

// Name returns the canonical registry key "velox_callback".
func (a *VeloxCallbackDeliveryAdapter) Name() string {
	return "velox_callback"
}

// Deliver records the post-completion intent for the Velox
// callback. The actual HTTP fire is delegated to
// internal_velox_callback_dispatcher.go (existing wiring);
// this method is the canonical registry entry point so the
// publish_worker's post-completion hook can dispatch by name.
//
// Behaviour today (Task 7/10):
//   - enabled == false: returns a "no-op" DeliveryResult
//     with Status="processing" so the publish_worker records
//     "delivered to velox_callback registry" without making an
//     HTTP call (used by tests + dry-runs).
//   - enabled == true: returns a "no-op" DeliveryResult with
//     Status="processing" today; the actual callback firing
//     is wired through internal_velox_callback_dispatcher.go's
//     existing flow (a Task 7.1/Task 9 followup will swap
//     this stub for the real HTTP fire so the registration
//     adapter owns the dispatch end-to-end).
//
// Return contract:
//   - Returns a non-nil DeliveryResult with Status="processing"
//     on the enabled path so the publish_worker treats it as
//     "accepted but terminal state lives elsewhere" (the same
//     shape as the existing async YouTube publish path).
//   - Returns nil + ErrDeliveryProviderNotImplemented if a
//     delivery has no RemoteURL (a programming/config error,
//     not a runtime transient — surfaces loudly to the operator
//     dashboard rather than silently swallowing).
func (a *VeloxCallbackDeliveryAdapter) Deliver(
	ctx context.Context,
	asset *models.MediaAsset,
	dest *models.DeliveryDestination,
	idempotencyKey string,
) (*models.DeliveryResult, error) {
	_ = ctx
	if dest == nil {
		return nil, fmt.Errorf("%w: velox_callback adapter: nil DeliveryDestination", ErrDeliveryProviderNotImplemented)
	}
	if asset == nil {
		return nil, fmt.Errorf("%w: velox_callback adapter: nil MediaAsset", ErrDeliveryProviderNotImplemented)
	}
	if dest.RemoteURL == "" {
		return nil, fmt.Errorf(
			"%w: velox_callback adapter: dest.RemoteURL (callback URL) is empty; "+
				"the VeloxCallback destination config must include callback_url",
			ErrDeliveryProviderNotImplemented,
		)
	}

	// Stub path: enabled=false OR enabled=true both surface a
	// "processing" DeliveryResult. The actual HTTP fire lives
	// in internal_velox_callback_dispatcher.go; a Task 7.1
	// followup will rewrite this body to call that
	// dispatcher's entry point so the registry owns the
	// end-to-end dispatch.
	_ = a.enabled
	_ = idempotencyKey

	return &models.DeliveryResult{
		ProviderName: a.Name(),
		Status:       "processing",
		RemoteURL:    dest.RemoteURL,
		Metadata: map[string]string{
			"idempotency_key": idempotencyKey,
			"post_completion": "true",
			"adapter":         "velox_callback_stub",
			"callback_url":    dest.RemoteURL,
		},
	}, nil
}

// Compile-time assertion that *VeloxCallbackDeliveryAdapter satisfies
// the canonical DeliveryProvider interface. Catches future signature
// drift at vet time.
var _ DeliveryProvider = (*VeloxCallbackDeliveryAdapter)(nil)
