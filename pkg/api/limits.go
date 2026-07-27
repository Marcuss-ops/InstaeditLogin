package api

import "time"

// ScheduleLimits is the narrow read-only view of WorkerConfig that the
// HTTP layer needs for publish-horizon enforcement + media-asset TTL
// computation. The bootstrap constructs this from cfg.Worker and passes
// it via WithScheduleLimits; the HTTP layer reads it through
// r.scheduleLimits. Keeping the type local to pkg/api (instead of
// passing the full *config.WorkerConfig) avoids pkg/api importing
// internal/config — the same layering rationale as UserStore /
// WorkspaceStore / MediaStore interfaces declared inline.
//
// Blocco #2 P0 — both fields are env-driven via WorkerConfig:
//
//	PublishHorizonDays        = PUBLISH_HORIZON_DAYS         (default 30)
//	VideoRetentionBufferDays  = VIDEO_RETENTION_BUFFER_DAYS  (default 7)
type ScheduleLimits struct {
	// PublishHorizonDays caps how far in the future a user/operator
	// can schedule a publish. Used in handleRescheduleUpload + the
	// batch V2 producer's heuristic projection. 0 disables
	// enforcement (default-defer; not exposed as an option to keep
	// the validation contract simple).
	PublishHorizonDays int
	// VideoRetentionBufferDays is the post-publish tail for
	// media_assets.expires_at. The 1-day min-floor is applied inside
	// computeMediaAssetLifetime unconditionally.
	VideoRetentionBufferDays int
}

// WithScheduleLimits wires the env-derived schedule limits into the
// Router. The setter pattern (vs. a constructor parameter) keeps
// NewRouter's signature stable across wire orderings — bootstrap can
// pass the dep AT THE END of the option chain without breaking the
// NewRouter public API.
func WithScheduleLimits(l ScheduleLimits) RouterOption {
	return func(r *Router) {
		r.scheduleLimits = l
	}
}

// computeMediaAssetLifetime is the buffer-aware media-asset TTL
// formula used by every media_asset CREATE site (presign,
// drive_import, upload_worker). Centralised here so a future
// operator bump to VIDEO_RETENTION_BUFFER_DAYS or PUBLISH_HORIZON_DAYS
// is picked up by EVERY creation path without grep-and-replace.
//
// Formula:
//   - when publishAt is set (scheduled content):
//     max(now+1d, publishAt + buffer)
//   - when publishAt is nil (publish-now flow):
//     max(now+1d, now + horizon)
//
// The 1-day min-floor protects against the "user scheduled in the
// past via clock skew" silent bug where a pure `publishAt + buffer`
// could already be expired at creation time → /complete returns 410.
// At worst the floor extends the asset's life by 1 day, which the
// operator can re-tune via the env var chain.
//
// Returns zero (time.Time{}) when r.scheduleLimits is the zero value
// (e.g. test fixtures that bypass WithScheduleLimits). Callers should
// fall through to a sane default in that case (NOT done here — the
// caller passes the result straight to time.Now().Add(ttl) which the
// daily-cleanup tolerates).
func (r *Router) computeMediaAssetLifetime(publishAt *time.Time) time.Time {
	const oneDay = 24 * time.Hour
	now := time.Now()
	horizon := r.scheduleLimits.PublishHorizonDays
	buffer := r.scheduleLimits.VideoRetentionBufferDays
	// Defensive defaults so dev environments that forget to wire
	// WithScheduleLimits still get a sensible TTL (mirrors the
	// historical mediaAssetLifetime=24h floor).
	if horizon <= 0 {
		horizon = 30
	}
	if buffer <= 0 {
		buffer = 7
	}
	if publishAt == nil {
		candidate := now.Add(time.Duration(horizon) * 24 * time.Hour)
		if candidate.Before(now.Add(oneDay)) {
			return now.Add(oneDay)
		}
		return candidate
	}
	candidate := publishAt.Add(time.Duration(buffer) * 24 * time.Hour)
	floor := now.Add(oneDay)
	if candidate.After(floor) {
		return candidate
	}
	return floor
}

// publishHorizonDays returns r.scheduleLimits.PublishHorizonDays with a
// safe fallback to preserve legacy handler contract when
// WithScheduleLimits isn't wired. Used by handleRescheduleUpload and
// the batch V2 producer's horizon comparison.
func (r *Router) publishHorizonDays() int {
	if r.scheduleLimits.PublishHorizonDays <= 0 {
		return 30
	}
	return r.scheduleLimits.PublishHorizonDays
}
