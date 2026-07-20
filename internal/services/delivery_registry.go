package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// DeliveryProvider is the post-completion content-delivery abstraction
// introduced by Task 7/10. One provider per "destination surface":
// YouTube (publish to a channel), Google Drive (upload a copy to a
// folder), Velox callback (notify the originating system that the
// post completed).
//
// Contract:
//   - Name() returns the registry key — case-sensitive, stable across
//     releases. DEFAULT keys: "youtube", "google_drive",
//     "velox_callback". Custom providers pick their own name; the
//     convention is "<platform>" or "<vendor>_<feature>".
//   - Deliver() performs the platform-specific delivery for ONE
//     (asset, destination, idempotency-key) tuple. Returns a
//     non-nil *models.DeliveryResult on terminal outcomes
//     (published, processing, retrying, failed). Returns an error
//     only on PROGRAMMER errors (nil ctx, malformed destination
//     config) — runtime transient failures should surface as a
//     result with Status="retrying".
//   - Idempotency: a stable idempotencyKey (typically the
//     post_target.id or a Delivey-asset SHA-256-hash) MUST be
//     honoured end-to-end: a retry of the same key + same asset
//     returns the SAME DeliveryResult without side effects
//     (no duplicate uploads, no duplicate callback fires).
//
// Lookup is via DeliveryRegistry.Get(name). The publish_worker
// post-completion hook (internal/worker/publish_worker.go) threads
// the platform_account.platform string (or, in a future Task 8/10
// followup, an explicit post_targets.provider column) into Get
// at dispatch time.
//
// How to add a new provider (3 lines):
//  1. Implement the interface on a struct that already has the
//     platform's HTTP client + repo wiring:
//     func (p *MyPlatformDeliveryAdapter) Name() string { return "my_platform" }
//     func (p *MyPlatformDeliveryAdapter) Deliver(
//     ctx context.Context, asset *models.MediaAsset,
//     dest *models.DeliveryDestination, idempotencyKey string,
//     ) (*models.DeliveryResult, error) { … }
//  2. Compile-time assertion (catches Name() drift during vet):
//     var _ services.DeliveryProvider = (*MyPlatformDeliveryAdapter)(nil)
//  3. Register at bootstrap (internal/bootstrap/app.go):
//     registry := services.NewDeliveryRegistry()
//     registry.Register(myAdapter)
//     Name collisions return ErrDeliveryProviderAlreadyRegistered
//     so a future refactor that introduces a duplicate fails fast
//     in CI rather than silently overwriting.
type DeliveryProvider interface {
	Name() string
	Deliver(ctx context.Context, asset *models.MediaAsset, dest *models.DeliveryDestination, idempotencyKey string) (*models.DeliveryResult, error)
}

// ErrDeliveryProviderNotFound is the typed sentinel Get returns
// when no provider is registered under name. Handlers / workers
// can errors.Is against it to distinguish "this provider WAS
// implemented but isn't wired at startup" (a config bug)
// from "this provider doesn't exist" (a feature gap). The
// publish_worker treats both as transient at the post-completion
// hook: skips the deliver call + logs a warn-level remediation
// hint so the operator dashboard surfaces the gap.
var ErrDeliveryProviderNotFound = errors.New("ERR_DELIVERY_PROVIDER_NOT_FOUND")

// ErrDeliveryProviderAlreadyRegistered is the typed sentinel
// Register returns when the same name is registered twice.
// Fail-fast: silently overwriting would let a future refactor
// thread the wrong adapter into the dispatch path; surfacing
// the duplicate at startup time keeps the registry noise-free
// in production and loud at CI time.
var ErrDeliveryProviderAlreadyRegistered = errors.New("ERR_DELIVERY_PROVIDER_ALREADY_REGISTERED")

// ErrDeliveryProviderNotImplemented is the typed sentinel
// providers return when the registration IS wired but the
// dispatch path hasn't shipped yet (Task 8/10 in-flight
// for GoogleDriveDestination). Keep the sentinel package-
// private so a different provider choosing their own
// "not implemented" wording doesn't accidentally match the
// registry's lookup path. The publish_worker treats it as
// a soft-skip + warn-level log, NOT a hard failure, because
// the pre-publish flow ALREADY delivered the content via the
// existing CapabilityRouter.Publisher path (the registry is
// a post-completion ADDITION).
var ErrDeliveryProviderNotImplemented = errors.New("ERR_DELIVERY_PROVIDER_NOT_IMPLEMENTED")

// DeliveryRegistry holds a thread-safe map of DeliveryProvider
// keyed by Name(). Construction is via NewDeliveryRegistry; no
// public constructor other than that. The intended wiring:
//   - internal/bootstrap/app.go: NewDeliveryRegistry() then 3
//     .Register() calls + services.WithDeliveryRegistry(registry)
//     plumbing through to PublishWorker.
//   - tests: register 2 fake providers and assert dispatch-by-name
//     (the canonical acceptance test for Task 7/10).
type DeliveryRegistry struct {
	providers map[string]DeliveryProvider
}

// NewDeliveryRegistry returns an empty registry. Capacity is
// unbounded — the expected registry size is 3-5 providers,
// the map is small enough that pre-allocation is unnecessary.
func NewDeliveryRegistry() *DeliveryRegistry {
	return &DeliveryRegistry{
		providers: make(map[string]DeliveryProvider),
	}
}

// Register stores p under p.Name(). Returns
// ErrDeliveryProviderAlreadyRegistered if p.Name() is already
// taken — fail-fast, see the sentinel godoc for why.
//
// Re-registering with a NEW provider under the SAME name
// silently overwrites in map semantics; we refuse that by
// design so a typo in bootstrap (e.g. Register(youtubeA) +
// Register(youtubeB) where both Name() == "youtube") doesn't
// quietly drop youtubeA on the floor.
func (r *DeliveryRegistry) Register(p DeliveryProvider) error {
	if p == nil {
		return fmt.Errorf("%w: nil provider", ErrDeliveryProviderNotImplemented)
	}
	name := p.Name()
	if name == "" {
		return fmt.Errorf("%w: provider Name() must be non-empty", ErrDeliveryProviderAlreadyRegistered)
	}
	if existing, ok := r.providers[name]; ok && existing != nil {
		return fmt.Errorf("%w: %q already registered by %T", ErrDeliveryProviderAlreadyRegistered, name, existing)
	}
	r.providers[name] = p
	return nil
}

// Get returns the provider registered under name. Returns
// ErrDeliveryProviderNotFound (wrapped) if no provider is
// registered; the publish_worker catches this via errors.Is
// and logs a warn-level remediation hint without aborting
// the post-completion flow.
func (r *DeliveryRegistry) Get(name string) (DeliveryProvider, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: empty provider name", ErrDeliveryProviderNotFound)
	}
	p, ok := r.providers[name]
	if !ok || p == nil {
		return nil, fmt.Errorf("%w: %q not registered (known providers: %v)", ErrDeliveryProviderNotFound, name, r.Names())
	}
	return p, nil
}

// Names returns the registered provider names in unspecified
// order (Go map iteration). Useful for bootstrap-time sanity
// logging and the ErrDeliveryProviderNotFound remediation hint.
func (r *DeliveryRegistry) Names() []string {
	out := make([]string, 0, len(r.providers))
	for name := range r.providers {
		out = append(out, name)
	}
	return out
}

// Len returns the number of registered providers. Useful for
// tests (asserting capacity-bound assertions without depending
// on Names() ordering).
func (r *DeliveryRegistry) Len() int {
	return len(r.providers)
}

// Compile-time assertion that ErrDeliveryProviderNotImplemented
// is a valid sentinel (no nil-bug at runtime). Cheap; catches
// future refactors that accidentally turn the var into a func.
var _ error = ErrDeliveryProviderNotImplemented
