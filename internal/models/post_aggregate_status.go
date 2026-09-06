package models

import "fmt"

// PostAggregateStatusResolver derives a parent post's status from the
// complete set of its target statuses. It is deliberately pure so every
// persistence path (publish worker, async reconciler, retries, and repair
// sweeps) uses exactly the same precedence rules.
type PostAggregateStatusResolver struct{}

// NewPostAggregateStatusResolver returns the shared aggregate resolver.
func NewPostAggregateStatusResolver() PostAggregateStatusResolver {
	return PostAggregateStatusResolver{}
}

// Resolve returns the deterministic aggregate status for targets.
//
// The precedence is intentionally expressed as business rules rather than
// enum ordering:
//   - no targets or all draft       -> draft
//   - all published                -> published
//   - any publishing               -> publishing
//   - any published/partial result -> partially_published (once no target is active)
//   - any retrying                 -> retrying
//   - any waiting_provider         -> waiting_provider
//   - any queued                   -> queued
//   - all remaining targets failed -> failed
//
// dlq and blocked_auth are terminal failures for aggregation. A target set
// containing a published target is partial even when the other targets are
// still queued or in progress; this keeps the parent from claiming that all
// destinations have completed successfully.
func (PostAggregateStatusResolver) Resolve(targets []PostTarget) (PostStatus, error) {
	if len(targets) == 0 {
		return PostStatusDraft, nil
	}

	allPublished := true
	anyPublished := false
	anyPartial := false
	anyPublishing := false
	anyRetrying := false
	anyWaitingProvider := false
	anyQueued := false
	allDraft := true
	allFailed := true

	for _, target := range targets {
		switch target.Status {
		case PostStatusPublished:
			anyPublished = true
			allDraft = false
			allFailed = false
		case PostStatusPartiallyPublished:
			anyPartial = true
			allPublished = false
			allDraft = false
			allFailed = false
		case PostStatusPublishing:
			anyPublishing = true
			allPublished = false
			allDraft = false
			allFailed = false
		case PostStatusRetrying:
			anyRetrying = true
			allPublished = false
			allDraft = false
			allFailed = false
		case PostStatusWaitingProvider:
			anyWaitingProvider = true
			allPublished = false
			allDraft = false
			allFailed = false
		case PostStatusQueued:
			anyQueued = true
			allPublished = false
			allDraft = false
			allFailed = false
		case PostStatusDraft:
			allPublished = false
			allFailed = false
		case PostStatusFailed, PostStatusDLQ, PostStatus("dead_letter"), PostStatusBlockedAuth,
			PostStatusDriveRequiredFailed:
			allPublished = false
			allDraft = false
		default:
			return "", fmt.Errorf("post aggregate: unknown target status %q", target.Status)
		}
	}

	if allPublished {
		return PostStatusPublished, nil
	}
	// An active target takes precedence over a completed target. This is
	// important for fan-outs such as {published, publishing}: the parent
	// must remain publishing until every destination reaches a terminal
	// outcome.
	if anyPublishing {
		return PostStatusPublishing, nil
	}
	if anyRetrying {
		return PostStatusRetrying, nil
	}
	if anyWaitingProvider {
		return PostStatusWaitingProvider, nil
	}
	if anyQueued {
		return PostStatusQueued, nil
	}
	if anyPublished || anyPartial {
		return PostStatusPartiallyPublished, nil
	}
	if allFailed {
		return PostStatusFailed, nil
	}
	if allDraft {
		return PostStatusDraft, nil
	}

	// The only remaining mixed terminal/initial combination is a failed
	// target beside a draft target. Failure is the useful aggregate state;
	// it prevents the parent from appearing editable/draft after a target
	// has already reached a terminal error.
	return PostStatusFailed, nil
}
