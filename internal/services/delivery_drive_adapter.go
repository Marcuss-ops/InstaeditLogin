package services

import (
	"context"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// GoogleDriveDeliveryAdapter is the thin registry carrier for the
// Google Drive destination. The Task 7/10 hot stub returned
// ErrDeliveryProviderNotImplemented; Task 8/10 replaces its body
// with a delegation to *GoogleDriveDestination (the work-horse:
// resumable upload + chunked PUTs + app-property idempotency +
// crash recovery). The adapter is kept as the registry entry point
// so Task 7/10's bootstrap wiring (a single Registry.Register call)
// stays unchanged — Task 8/10 simply widens the constructor's
// dependencies.
//
// Why an adapter (vs. registering the destination directly):
//  1. Blast radius: bootstrap continues to call
//     registry.Register(NewGoogleDriveDeliveryAdapter(...))
//     without churn. Future feature toggles (dry-run mode, MFAE
//     retry strategy swaps, etc.) ride on the adapter without
//     touching the destination workhorse.
//  2. Identity: the adapter's Name() returns models.PlatformGoogleDrive
//     ("google-drive") matching account.Platform verbatim —
//     the publish_worker dispatch lookup lands on this provider
//     transparently. An underscored "google_drive" key would NOT
//     be found (see publish_worker_delivery.go dispatch hook).
//  3. Compile-time assertion: the var-underscore line catches
//     future refactors that break the DeliveryProvider contract
//     at vet time, not at runtime.
//
// Construction is via NewGoogleDriveDeliveryAdapter: takes the
// destination (produced by NewGoogleDriveDestination in the same
// bootstrap scope) and forwards Deliver calls to it. Bootstrap:
//
//   dst, _ := NewGoogleDriveDestination(sessionRepo, tokenProvider, encryptor, httpClient, 16*1024*1024)
//   _ = registry.Register(NewGoogleDriveDeliveryAdapter(dst))
//
// If the destination constructor returns an error (e.g. chunk size
// below Drive's 256 KiB minimum), the adapter construction refuses.
// The bootstrap fails loudly rather than registering a half-wired
// provider that explodes on the first Deliver call.
type GoogleDriveDeliveryAdapter struct {
	destination *GoogleDriveDestination
}

// NewGoogleDriveDeliveryAdapter wraps the destination. The
// destination must be non-nil (the canonical case: produced by
// GoogleDriveDestination.NewGoogleDriveDestination in the same
// bootstrap scope). Passing a nil destination returns an error so
// a future refactor that wires the adapter before the destination
// fails loudly at startup, not per-tick.
//
// Signature changed (Task 8/10): the Task 7/10 stub took a
// `bool enabled` argument. The new signature takes a concrete
// *GoogleDriveDestination so the adapter's Deliver forwards
// directly with no extra gating logic — feature toggles ride on
// the destination struct, not on the adapter constructor.
func NewGoogleDriveDeliveryAdapter(dst *GoogleDriveDestination) (*GoogleDriveDeliveryAdapter, error) {
	if dst == nil {
		return nil, errDeliveryDriveAdapterNilDestination
	}
	return &GoogleDriveDeliveryAdapter{destination: dst}, nil
}

// errDeliveryDriveAdapterNilDestination is a package-private sentinel
// for the constructor's nil-refusal. Operators see the error in
// bootstrap logs; the registry never sees the broken adapter.
var errDeliveryDriveAdapterNilDestination = newDeliveryDriveAdapterError("nil destination")

// deliveryDriveAdapterError is the typed error type for the adapter
// constructor. Internal-only; the registry surfaces the message
// verbatim in the wrap chain.
type deliveryDriveAdapterError struct {
	reason string
}

func (e deliveryDriveAdapterError) Error() string {
	return "google drive delivery adapter: " + e.reason
}

func newDeliveryDriveAdapterError(reason string) error {
	return deliveryDriveAdapterError{reason: reason}
}

// Name returns the canonical registry key "google-drive" (via
// models.PlatformGoogleDrive). The publish_worker dispatch hook
// looks up the provider by account.Platform, which is "google-drive"
// (the platform_accounts.platform string returned by
// GoogleDriveOAuthService.Name()). An underscored "google_drive"
// variant would NOT match — the lookup would log a warn-level
// "registry has no provider for platform" and skip the dispatch.
//
// Runtime consistency between Name() and the canonical constant is
// pinned by TestGoogleDriveDestination_Name_MatchesPlatformGoogleDrive
// in delivery_drive_destination_test.go. A drift catches at test
// time (vet doesn't catch constant-as-string assertion).
func (a *GoogleDriveDeliveryAdapter) Name() string {
	return models.PlatformGoogleDrive
}

// Deliver forwards to the underlying destination. The hook
// already passes (asset, dest, idempotencyKey); the adapter adds
// no behaviour of its own. Defensive nil-check (the constructor
// refuses nil so this is unreachable in production but safe).
func (a *GoogleDriveDeliveryAdapter) Deliver(
	ctx context.Context,
	asset *models.MediaAsset,
	dest *models.DeliveryDestination,
	idempotencyKey string,
) (*models.DeliveryResult, error) {
	if a == nil || a.destination == nil {
		return nil, errDeliveryDriveAdapterNilDestination
	}
	return a.destination.Deliver(ctx, asset, dest, idempotencyKey)
}

// Compile-time assertion: *GoogleDriveDeliveryAdapter satisfies
// DeliveryProvider. Triggers at vet time if the interface drifts.
var _ DeliveryProvider = (*GoogleDriveDeliveryAdapter)(nil)
