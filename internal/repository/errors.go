package repository

import "errors"

// Sentinel errors returned by PostRepository. The API layer maps these via
// errors.Is to HTTP status codes:
//   ErrPostNotFound        → 404 (post does not exist)
//   ErrPostUnauthorized    → 404 (tenant-isolation miss: post exists but is
//                                owned by a different workspace; 404
//                                rather than 403 to avoid leaking workspace
//                                existence to cross-tenant probes)
//   ErrPostTargetNotFound  → 404 (a stale post_target ID — the worker
//                                would otherwise see nil error and assume
//                                the status transition happened, leaving a
//                                ghost target in the pending queue)
//
// Foundation for `pkg/api/routes.go` mapping: the API layer wraps the
// canonical sentinel in its own response envelope; callers in the worker
// can still inspect err.Error() for log/debug context.
//
// Each sentinel is wrapped with fmt.Errorf("%w: ...", ErrXxx, id) so the
// error chain carries both the typed sentinel (for errors.Is matching) AND
// the operation context (for log lines and debuggability).
var (
	// ErrPostNotFound is returned when a post lookup by primary key finds
	// no row. Currently NOT raised by any PostRepository method — FindByID
	// returns (nil, nil) per repo convention. Declared now so the API
	// layer can wire its mapping code-path and future repo methods (e.g.
	// a strict FindByIDStrict variant) can adopt it without coordination.
	ErrPostNotFound = errors.New("post not found")

	// ErrPostUnauthorized is returned when a write operation (Update)
	// matches zero rows because either the post id is wrong OR the
	// workspace_id does not match. The two cases are indistinguishable
	// from a single UPDATE statement — we return 404 (not 403) to avoid
	// leaking row existence to cross-tenant probes.
	ErrPostUnauthorized = errors.New("post not owned by workspace")

	// ErrPostTargetNotFound is returned when post_target write operations
	// (e.g. UpdateStatus) match zero rows because the target id is stale
	// or invalid. Workers depend on this signal to drop otherwise-phantom
	// updates from the pending queue.
	ErrPostTargetNotFound = errors.New("post_target not found")

	// ErrUserNotFound is returned by UserRepository.Update when zero rows
	// match — the user id does not exist. Unlike PostRepository.Update,
	// UserRepository.Update is NOT tenant-scoped (no workspace_id clause
	// in the WHERE), so a 0-row match is unambiguous: the row is gone.
	// Surface as 404 at the API layer.
	ErrUserNotFound = errors.New("user not found")

	// ErrTokenNotFound is returned by TokenRepository DELETE methods
	// (DeleteToken, DeleteAllTokensForPlatformAccount) when zero rows
	// match. For DeleteToken the token id is stale; for
	// DeleteAllTokensForPlatformAccount the account simply had no tokens
	// (idempotent no-op). Callers in logout / revoke flows can use
	// errors.Is to treat this as non-fatal in the bulk case. Surface as
	// 404 at the API layer for the single-token path.
	ErrTokenNotFound = errors.New("token not found")
)
