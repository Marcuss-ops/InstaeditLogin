package repository

import "errors"

// Sentinel errors returned by the repository layer. The pkg/api layer maps
// these via errors.Is to HTTP status codes:
//
//	ErrPostNotFound           → 404 (post does not exist)
//	ErrUnauthorized / 404 (tenant-isolation miss: post exists but is
//	                             owned by a different workspace; maps to
//	                             403 by current API contract for admin
//	                             debugging; a future hardening pass could
//	                             switch this to 404 to prevent
//	                             workspace-existence leaks across tenants)
//	ErrPostTargetNotFound     → 404 (a stale post_target ID — the worker
//	                             would otherwise see nil error and assume
//	                             the status transition happened, leaving a
//	                             ghost target in the pending queue)
//	ErrUserNotFound           → 404 (user id does not exist)
//	ErrTokenNotFound          → 404 (token id stale / account has no tokens)
//
// Foundation for `pkg/api/{workspaces,posts}.go` mapping: the API layer wraps
// the canonical sentinel in its own response envelope; callers in internal
// workers can still inspect err.Error() for log/debug context.
//
// Each sentinel is wrapped with fmt.Errorf("%w: ...", ErrXxx, id) so the
// error chain carries both the typed sentinel (for errors.Is matching) AND
// the operation context (for log lines and debuggability).
var (
	// ErrPostNotFound is returned when a post lookup by primary key finds
	// no row. Currently NOT raised by any PostRepository method — FindByID
	// returns (nil, nil) per repo convention. Declared so the API layer
	// can wire its mapping code-path and future repo methods (e.g. a strict
	// FindByIDStrict variant) can adopt it without coordination.
	ErrPostNotFound = errors.New("post not found")

	// ErrPostUnauthorized is returned when a write operation (Update)
	// matches zero rows because either the post id is wrong OR the
	// workspace_id does not match. The two cases are indistinguishable
	// from a single UPDATE statement. Per the current pkg/api contract,
	// the API layer maps this sentinel to HTTP 403 — the operator has
	// chosen to surface "exists but not yours" so admins can debug.
	// A future hardening pass could switch this to 404 to prevent
	// workspace-existence leaks across tenants (see pkg/api/posts.go
	// mapRepoError for the current policy).
	ErrPostUnauthorized = errors.New("post not owned by workspace")

	// ErrPostTargetNotFound is returned when post_target write operations
	// (e.g. UpdateStatus) match zero rows because the target id is stale
	// or invalid. Workers depend on this signal to drop otherwise-phantom
	// updates from the pending queue.
	ErrPostTargetNotFound = errors.New("post_target not found")

	// ErrUserNotFound is returned by UserRepository.Update when zero rows
	// match — the user id does not exist. Unlike PostRepository.Update,
	// UserRepository.Update is NOT tenant-scoped (no workspace_id clause),
	// so a 0-row match is unambiguous: the row is gone.
	// Surface as 404 at the API layer.
	ErrUserNotFound = errors.New("user not found")

	// ErrTokenNotFound is returned by TokenRepository DELETE methods
	// (DeleteToken, DeleteAllTokensForPlatformAccount) when zero rows
	// match. For DeleteToken the token id is stale; for
	// DeleteAllTokensForPlatformAccount the account simply had no tokens
	// (idempotent no-op). Callers in logout / revoke flows can use
	// errors.Is to treat this as non-fatal in the bulk case.
	ErrTokenNotFound = errors.New("token not found")

	// ErrWorkspaceNotFound is returned by WorkspaceRepository.Delete when
	// zero rows match the workspace id. Mirrors the post_repo pattern
	// introduced in fix(repo): surface rows-affected=0 as a real error so
	// the API layer can map to 404 via errors.Is without leaking query
	// details through string matching.
	ErrWorkspaceNotFound = errors.New("workspace not found")

	// ErrIdempotencyConflict is returned when a client retries a POST with
	// the same Idempotency-Key but a different request body (hash mismatch).
	// The API layer maps this to HTTP 409 Conflict per the Stripe idempotency
	// convention.
	ErrIdempotencyConflict = errors.New("idempotency key conflict: same key, different body")

	// ErrMediaAssetNotFound is returned when a media asset lookup by UUID
	// finds no row (or zero rows affected on update). Taglio 3.2.
	ErrMediaAssetNotFound = errors.New("media asset not found")

	// ErrPostTargetDuplicate is returned when a Create or Save would
	// INSERT a duplicate post_target row violating the
	// UNIQUE(post_id, platform_account_id) defense-in-depth constraint
	// added by migration 022 (Taglio 4.7 LEVEL 2). The API layer
	// maps this to HTTP 409 (the second CreatePost hit the fan-out
	// already landed). NOT a precondition failure — clients can recover
	// by GETting the post and continuing from the existing fan-out.
	ErrPostTargetDuplicate = errors.New("post target already exists for this post + platform account")

	// ErrProviderIdempotencyConflict is returned when a save
	// (typically SetProviderIdempotencyKey or Create on a target
	// already keyed) would violate the partial
	// UNIQUE(platform_account_id, provider_idempotency_key) constraint
	// from migration 022. Maps to HTTP 409 at the API layer. Note:
	// in the worker's normal publish flow, this should not fire —
	// the worker writes the key ONCE per retry cycle on the same row,
	// and the partial UNIQUE excludes NULLs so the migration can't
	// regress on pre-existing data. The error is the safety net for
	// degenerate runbook INSERTs and unintended duplicate-key stamps.
	ErrProviderIdempotencyConflict = errors.New("provider idempotency key conflict: account already has a target with this key")
)
