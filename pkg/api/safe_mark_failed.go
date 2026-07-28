package api

import (
	"context"
	"log/slog"
)

// markFailedter is the minimal contract any media-asset store must
// satisfy to be passed to safeMarkFailed. Defined here (not in
// repository) so pkg/api stays a provider of adapter helpers and the
// repository package is not polluted with API-shape concerns.
//
// The 2-method surface mirrors the historical MediaStore interface
// declared in pkg/api/media_handlers.go: the diagnose-friendly variant
// MarkFailedWithReason logs more context than MarkFailed but both
// outcomes need the same defensive secondary-error handling.
type markFailedter interface {
	MarkFailed(id, reason string) error
	MarkFailedWithReason(id, reason string, cause error) error
}

// safeMarkFailed replaces the silent `_ = r.mediaStore.MarkFailed(...)`
// and `_ = r.mediaStore.MarkFailedWithReason(...)` pattern that
// historically littered pkg/api/media_handlers.go and
// pkg/api/drive_import_handlers.go.
//
// Why it exists (audit PR-1, doc-comment is the canonical record):
//
//   - The pre-fix shape silently discarded the secondary error. If
//     the DB write of the failure-transition itself errored (DB blip,
//     timeout, advisory-lock contention), the post_target never
//     transitioned out of processing/queued — a "zombie" state that
//     the worker loop kept retrying forever, believing the row was
//     still in-flight.
//
//   - safeMarkFailed is best-effort: it calls the right underlying
//     method (MarkFailed or MarkFailedWithReason based on whether
//     `cause` is non-nil) and, IF the secondary call fails, emits an
//     ERROR-level structured log so the failure-mode is visible in
//     logs + scrapeable by error-rate metrics. The function does
//     NOT return an error: callers continue their normal control
//     flow (which is the same contract the silent `_ = ...` had —
//     just with observability).
//
//   - The helper accepts a context to thread through any future
//     traceparent wiring + to satisfy the canonical
//     log/slog.ErrorContext convention. Callers pass the request
//     context (r.Context() in HTTP handlers) so the structured
//     fields include request_id, traceparent, etc.
//
// Usage:
//
//	safeMarkFailed(r.Context(), slog.Default(), r.mediaStore,
//	    asset.ID, "sha256 required")                       // → MarkFailed
//	safeMarkFailed(r.Context(), slog.Default(), r.mediaStore,
//	    asset.ID, "S3 upload failed", err)                // → MarkFailedWithReason
//
// The previous PR-2 step's audit notes that 'cause' switching is the
// the only input that distinguishes the two underlying methods;
// callers should pick one consciously. The 6th argument is variadic
// `cause ...error` rather than a fixed `cause error` so the 5-arg
// shape (`safeMarkFailed(<ctx>, <log>, <store>, id, reason)`) compiles
// for the `_ = store.MarkFailed(id, reason)`-shaped sites in
// media_handlers.go WITHOUT requiring each caller to pass `nil`. The
// 6-arg shape preserves the chained-error semantics for the
// `_ = store.MarkFailedWithReason(id, reason, err)`-shaped sites in
// drive_import_handlers.go.
//
// We could have split into two named helpers (safeMarkFailed vs
// safeMarkFailedWithReason) but the audit notes that the cause-discriminator
// is conceptually a single operation with optional causal metadata; a
// variadic shape keeps the call site tidy in both cases.
func safeMarkFailed(ctx context.Context, log *slog.Logger, store markFailedter, id, reason string, cause ...error) {
	if log == nil {
		// log is nil-safe — the helper degrades to silent-fail
		// only if the caller explicitly opted in by passing nil.
		// The default call sites pass slog.Default() via r.logger
		// or a per-test value.
		return
	}
	var causeErr error
	if len(cause) > 0 {
		causeErr = cause[0]
	}
	var err error
	if causeErr != nil {
		err = store.MarkFailedWithReason(id, reason, causeErr)
	} else {
		err = store.MarkFailed(id, reason)
	}
	if err != nil {
		// Zombie state risk: the secondary DB write of the
		// failure-transition itself failed. The post will remain
		// in a non-terminal state and the worker loop will retry.
		// Surface this loudly so operators can correlate with
		// dashboard spikes of "stuck" targets. ERROR level is
		// deliberate — the silent `_ = ...` pattern was masked
		// at this exact code-path for ~2 years.
		log.ErrorContext(ctx, "safeMarkFailed: zombie state risk — could not persist MarkFailed transition",
			"asset_id", id,
			"reason", reason,
			"cause", causeErr,
			"error", err,
		)
	}
}
