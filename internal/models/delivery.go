package models

// DeliveryDestination is the per-target descriptor the
// DeliveryRegistry (internal/services/delivery_registry.go)
// hands to a DeliveryProvider.Deliver call. It is the small,
// self-describing subset of the post + target state the provider
// needs to construct a platform-specific upload without holding a
// reference to the publisher's repository layer.
//
// Why an explicit struct rather than passing *PostTarget: post_targets
// is bound to platform_accounts (one account = one channel). The
// DeliveryDestination breaks that coupling — a single delivery can
// target any downstream system (a YouTube channel, a Shared Drive
// folder, a Velox callback URL) without the registry having to
// fan the target model out per provider.
//
// Provider-specific knobs (e.g. the Shared Drive folder_id for
// GoogleDriveDestination) ride on the Config JSON column as a
// free-form blob; providers that need stronger typing parse it
// at Deliver() time and surface typed validation errors.
type DeliveryDestination struct {
	// Provider is the delivery registry key (e.g. "youtube",
	// "google_drive", "velox_callback"). The registry's Get
	// uses this string verbatim — case-sensitive, no whitespace
	// normalisation. Must match Name() of the registered provider.
	Provider string

	// RemoteID is the platform-side identifier of the destination:
	//   - YouTube channel: the channel ID (UCxxxxxxxx)
	//   - Google Drive folder: the folder ID (1Abc…)
	//   - Velox callback: the platform_account_id (no remote ID)
	// Empty when the destination is purely URL-driven (Velox).
	RemoteID string

	// RemoteURL is optional. For VeloxCallback it carries the
	// callback URL the adapter should POST the completion
	// notification to. For YouTube channels it's empty (the
	// Provider derives the upload URL from the OAuth token +
	// remote ID combination).
	RemoteURL string

	// DisplayName is a human-readable hint for the operator
	// dashboard / log lines (e.g. "Caleb Foster (YouTube)").
	// Optional.
	DisplayName string

	// Config is a free-form provider-specific JSON blob:
	//   - YouTube: empty (no extra per-target config today).
	//   - Google Drive: {"folder_id":"…", "filename_template":"…"}
	//   - Velox: {"callback_url":"…", "metadata_title":"…"}
	// The struct is kept narrow so providers stay decoupled from
	// each other's knobs. Providers parse Config at Deliver()
	// time and surface typed errors on malformed input.
	Config map[string]string
}

// DeliveryResult is what a DeliveryProvider returns on a successful
// (or terminal-failure) Deliver() call. The post-completion flow
// in the publish_worker logs RemoteID + RemoteURL for operator audit
// and stamps Metadata onto the post_targets row.
//
// Status follows the same lifecycle conventions as
// models.PublishResult.Status so the publish_worker can treat the
// two as a union without inventing a parallel state machine:
//   "published"   — the delivery succeeded; post_targets.status
//                   flips to published and the lease is released.
//   "processing"  — the provider accepted the work but the
//                   terminal state lives elsewhere (e.g. an async
//                   video processing pipeline). Same shape as
//                   models.PublishResult.Status == "processing".
//   "retrying"    — a typed transient failure; the publisher
//                   schedules the next attempt via the existing
//                   upload-job retry budget. Metadata may carry
//                   the retry-after seconds.
//   "failed"      — terminal failure; post_targets.status stays
//                   failed and the row is dead-lettered.
//
// Providers MUST return a non-nil DeliveryResult on terminal
// outcomes. Transient retriable failures should still return a
// result with Status="retrying" so the worker doesn't have to
// special-case error returns separately from status returns.
type DeliveryResult struct {
	// ProviderName echoes the registered provider name so log
	// lines that aggregate across providers don't need to
	// re-identify the source.
	ProviderName string

	// Status is one of: "published", "processing", "retrying",
	// "failed". Same vocabulary as models.PublishResult.Status.
	Status string

	// RemoteID is the platform-side identifier of the delivered
	// artifact (YouTube video ID, Drive file ID, Velox delivery
	// row id). Empty for deliveries whose destination has no
	// remote ID (Velox callback).
	RemoteID string

	// RemoteURL is the human-facing link to the delivered artifact.
	// Empty for Velox callback (no URL).
	RemoteURL string

	// Metadata is a free-form provider-specific extra blob the
	// publish_worker can persist onto post_targets.metadata for
	// downstream replay / debug visibility. Keys are provider-
	// defined.
	Metadata map[string]string
}
