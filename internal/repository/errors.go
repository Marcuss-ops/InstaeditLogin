package repository

import "errors"

// Sentinel errors returned by the repository layer. The pkg/api layer maps
// these via errors.Is to HTTP status codes:
//
//   ErrPostNotFound           → 404 (post does not exist)
//   ErrUnauthorized / 404 (tenant-isolation miss: post exists but is
//                                owned by a different workspace; maps to
//                                403 by current API contract for admin
//                                debugging; a future hardening pass could
//                                switch this to 404 to prevent
//                                workspace-existence leaks across tenants)
//   ErrPostTargetNotFound     → 404 (a stale post_target ID — the worker
//                                would otherwise see nil error and assume
//                                the status transition happened, leaving a
//                                ghost target in the pending queue)
//   ErrUserNotFound           → 404 (user id does not exist)
//   ErrTokenNotFound          → 404 (token id stale / account has no tokens)
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
)
