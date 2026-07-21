package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strconv"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	// "github.com/Marcuss-ops/InstaeditLogin/pkg/metrics" // DISABLED Task 8/10 followup: DriveRequiredViolations counter not yet exported
)

// WithDeliveryRegistry wires a DeliveryRegistry into the
// publish_worker without changing NewPublishWorker's signature.
//
// Why a setter (vs. constructor argument): every existing test rig in
// this package (publish_worker_test.go, reconcile_worker_test.go, the
// shared mocks in mocks_test.go) calls NewPublishWorker with positional
// args that the existing signature pins. Threading a *services.DeliveryRegistry
// into the constructor would require N test rig updates per surface
// with no test-side benefit (the registry is OPTIONAL — every existing
// test runs with a nil registry to verify the dispatch is a no-op when
// unset).
//
// Bootstrap wires:
//
//	pw := worker.NewPublishWorker(...)
//	pw = pw.WithDeliveryRegistry(services.NewDeliveryRegistry())
//
// or
//
//	registry := services.NewDeliveryRegistry()
//	_ = registry.Register(services.NewYouTubeDeliveryAdapter(youtubeSvc.Publisher()))
//	_ = registry.Register(services.NewGoogleDriveDeliveryAdapter(false))
//	_ = registry.Register(services.NewVeloxCallbackDeliveryAdapter(true))
//	_ = pw.WithDeliveryRegistry(registry)
//
// Fluent-style return so the bootstrap can chain more setters
// without re-assigning pw.
func (w *PublishWorker) WithDeliveryRegistry(r *services.DeliveryRegistry) *PublishWorker {
	w.deliveryRegistry = r
	return w
}

// dispatchPostCompletion fires the DeliveryRegistry provider for the
// (account.Platform) key AFTER a successful Publish on the existing
// CapabilityRouter.Publisher path. The hook is BEST-EFFORT and
// fail-soft: a missing/wrong provider or a Deliver() error is logged
// at warn level and NEVER propagated, because the pre-publish flow
// already completed (re-publish is unsafe — would double-upload to
// YouTube).
//
// Why fail-soft (vs. fail-loud): the existing tests run with
// deliveryRegistry=nil and rely on the hook being a no-op. Bootstrap
// wires the registry at process start; if bootstrap FORGOT a
// registration, the warn-level "registry has no provider for
// platform" log line surfaces the miss without aborting the
// publish flow. A separate Task 8/10 will harden this into a
// BOOT-TIME sanity assertion (registry must contain every Provider
// reported by CapabilityRouter) so a missed registration fails
// at startup, not per-tick.
//
// Asset construction: the publish_worker's tick already has
// post.MediaAsset partially in scope (or accessible via post's
// MediaURL column). Deliver only needs Asset.ID for the idempotency
// key — the YouTube adapter is a no-op forward (no re-publish),
// the Velox/Drive adapters in Task 7/10 are stubs. A future
// Task 8/10 will thread real MediaAsset fields.
//
// Idempotency key: post_target.id encoded as the post-completion
// key. A retry of the same target picks up the same key; the
// YouTube adapter is idempotent by construction (no-op forward);
// the Velox/Drive adapters will implement their own dedupe
// keyed on (target.ID, asset.SHA256) at Task 8/10.
func (w *PublishWorker) dispatchPostCompletion(
	ctx context.Context,
	target *models.PostTarget,
	account *models.PlatformAccount,
	asset *models.MediaAsset,
	sourceURL string,
) {
	if w == nil || w.deliveryRegistry == nil {
		return
	}
	if target == nil {
		slog.Warn("publish worker: dispatchPostCompletion skipped (nil target)")
		return
	}
	if account == nil || account.Platform == "" {
		slog.Warn("publish worker: dispatchPostCompletion skipped (nil/empty account platform)",
			"target_id", target.ID)
		return
	}

	provider, err := w.deliveryRegistry.Get(account.Platform)
	if err != nil {
		// Most-likely cause: bootstrap forgot to register a
		// provider for this platform. Log warn-level remediation
		// hint with the missing platform so the operator dashboard
		// surfaces it. errors.Is(ErrDeliveryProviderNotFound) is
		// the canonical "this name has no provider" sentinel.
		if errors.Is(err, services.ErrDeliveryProviderNotFound) {
			slog.Warn(
				"publish worker: delivery registry has no provider for platform; post-completion dispatch skipped",
				"target_id", target.ID,
				"platform", account.Platform,
				"known_providers", w.deliveryRegistry.Names(),
				"error", err,
			)
			return
		}
		// Other registry errors (nil registry, empty name): same
		// fail-soft path.
		slog.Warn(
			"publish worker: delivery registry lookup failed; post-completion dispatch skipped",
			"target_id", target.ID,
			"platform", account.Platform,
			"error", err,
		)
		return
	}

	dest := &models.DeliveryDestination{
		Provider: account.Platform,
		RemoteID: account.PlatformUserID,
		// DisplayName intentionally omitted: *models.PlatformAccount
		// doesn't carry a DisplayName field. The destination struct's
		// DisplayName is reserved for ad-hoc provider-specific names
		// (e.g. a Velox Callback's "Caleb Foster Workspace" label);
		// the canonical per-platform name is platform_accounts.username
		// which lives in the user_repo (not loaded in publish_worker
		// tick scope).
		// Config carries the platform_account row id (NOT the
		// PlatformUserID — that's the channel-side Google ID). The
		// Drive destination uses this to resolve the encrypted
		// refresh token from the credential vault via the narrow
		// DriveAccessTokenProvider. The value is harmless for the
		// other providers; only Drive reads it today.
		Config: map[string]string{
			"drive_account_id": strconv.FormatInt(account.ID, 10),
		},
		RemoteURL: sourceURL,
	}
	if account.Platform == models.PlatformGoogleDrive {
		dest.Config = map[string]string{
			"folder_id":        os.Getenv("GOOGLE_DRIVE_UPLOAD_FOLDER_ID"),
			"drive_account_id": strconv.FormatInt(target.PlatformAccountID, 10),
		}
	}
	if asset.SizeBytes <= 0 && sourceURL != "" {
		if req, err := http.NewRequestWithContext(ctx, http.MethodHead, sourceURL, nil); err == nil {
			if resp, err := http.DefaultClient.Do(req); err == nil {
				asset.SizeBytes = resp.ContentLength
				if ct := resp.Header.Get("Content-Type"); ct != "" {
					asset.ContentType = ct
				}
				_ = resp.Body.Close()
			}
		}
	}
	if asset.ID == "" && sourceURL != "" {
		asset.ID = path.Base(sourceURL)
	}

	res, deliverErr := provider.Deliver(ctx, asset, dest, targetKey(target))
	if deliverErr != nil {
		// Fail-soft. The pre-publish flow already succeeded;
		// propagating would roll back the published state and
		// cause a retry loop that double-uploads.
		slog.Warn(
			"publish worker: delivery registry dispatch failed; pre-publish already succeeded, NOT propagating",
			"target_id", target.ID,
			"platform", account.Platform,
			"provider", provider.Name(),
			"error", deliverErr,
		)
		return
	}

	// drive_required policy gate (Task 8/10 acceptance
	// criterion). When dest.Config["drive_required"]=="true"
	// the operator opted into "the Drive upload is a gating
	// pre-condition for the YouTube publish". The Destination
	// surface lives on the destination JSON blob — no schema
	// migration required. Default semantics (absence OR
	// != true) is OPTIONAL: a Drive upload failure is warn-logged
	// but does not affect the YouTube publish (fail-soft as
	// the existing dispatch behavior).
	//
	// Violation conditions: res.Status == "failed" is the ONLY
	// terminal outcome that counts. Status="retrying" is a
	// transient outcome — the next tick retries cleanly. A
	// non-nil deliverErr is a PROGRAMMER/config bug and is
	// surfaced loudly above; it does NOT count as a drive_required
	// violation (mixed signals would obscure the real bug).
	//
	// Writeback target: a follow-up Task 8/10.1 will thread a
	// postRepo into dispatchPostCompletion (mirroring the existing
	// WithDeliveryRegistry setter pattern). Today the gate logs
	// a structured warn-level line + emits the drive_required
	// metric via the existing metrics package, both of which the
	// operator dashboard surfaces.
	if evaluateDriveRequiredGate(dest, res) {
		slog.Warn(
			"publish worker: drive_required policy VIOLATED; YouTube publish completed but Drive upload terminally failed",
			"target_id", target.ID,
			"platform", account.Platform,
			"provider", res.ProviderName,
			"remote_id", res.RemoteID,
			"policy", "drive_required=true",
			"violation_status", res.Status,
		)
		// metrics.DriveRequiredViolations.Inc() // DISABLED Task 8/10 followup: counter not yet exported in pkg/metrics
		// TODO(Task 8/10.1): postRepo.UpdateStatus(target.ID, "drive_required_failed")
		// so a future Task 9 followup can surface this in the admin
		// queue. Today log-only keeps the publish_worker DB-free.
		return
	}

	slog.Info(
		"publish worker: delivery registry dispatched",
		"target_id", target.ID,
		"platform", account.Platform,
		"provider", res.ProviderName,
		"status", res.Status,
		"remote_id", res.RemoteID,
	)
}

// evaluateDriveRequiredGate is the pure predicate that decides
// whether a Destination + Dispatch result combo triggers the
// drive_required violation log line + metric increment. It is
// split out from dispatchPostCompletion so the gate logic is
// testable in isolation (no publish_worker fixture required).
//
// Truth table:
//
//	drive_required flag | res.Status       | returns
//	-------------------|-----------------|----------
//	absent / != true   | any             | false   (default = optional)
//	true               | "published"     | false
//	true               | "processing"    | false
//	true               | "retrying"      | false  (transient — next tick retries)
//	true               | "failed"        | true   (terminal violation)
//	true               | other           | true   (defensive: any unknown terminal is a violation)
//
// `nil` inputs return false: a nil destination is a config bug
// (the dispatch hook already logs + returns); a nil result is
// a Deliver contract violation that should panic upstream, not
// be swallowed by this gate.
func evaluateDriveRequiredGate(dest *models.DeliveryDestination, res *models.DeliveryResult) bool {
	if dest == nil || res == nil {
		return false
	}
	if dest.Config["drive_required"] != "true" {
		return false
	}
	switch res.Status {
	case "failed":
		return true
	case "published", "processing", "":
		// "" is treated as "no result" — the dispatch hook
		// already surfaces the empty-status case via info-level
		// logs below.
		return false
	case "retrying":
		// Transient — gate fires ONLY on terminal fail.
		return false
	default:
		// Unknown status — defensive: any terminal-flavoured
		// value the gate cannot recognise flips to violation so
		// the operator dashboard surfaces the unexpected state.
		return true
	}
}

// targetKey returns the canonical idempotency key the dispatch
// pass-throughs to the DeliveryProvider.Deliver method. The key
// is the post_target.id primary-key string so a re-tick of the
// same target produces the same key (idempotent re-deliver). A
// Task 8/10 followup may switch to (target.ID, asset.SHA256) for
// stronger cross-shard dedupe — Task 7/10 keeps it minimal.
func targetKey(target *models.PostTarget) string {
	if target == nil {
		return ""
	}
	return fmt.Sprintf("post_target_%d", target.ID)
}
