package services

import "context"

// ErrYouTubeChannelMismatch is the sentinel error returned by
// YouTubeChannelBinder.ValidateChannelBinding when the channel id
// reported by YouTube's channels.list?mine=true does not match the
// expected channel id (platform_accounts.platform_user_id).
//
// The error indicates a structural problem with the OAuth grant:
// either the grant is bound to a different channel than what we
// believe, or the grant has lost its channel binding (revoked,
// re-rotated by Google, or hijacked). It is NOT a transient failure
// — retrying without fixing the grant will hit the same mismatch.
//
// Callers (publish worker) detect this with errors.Is(err,
// services.ErrYouTubeChannelMismatch) and on detection mark the
// platform_account with status='reauth_required' + reauth_required_at
// so the operator's UI prompts the user to reconnect the channel.
// Transient errors (5xx, network timeout) are returned wrapped but
// NOT use this sentinel; the worker must NOT flag reauth on those.
var ErrYouTubeChannelMismatch = errorString("youtube channel binding mismatch: grant is not bound to the expected channel id")

// errorString is a tiny unexported type that implements error so the
// sentinel can be compared with errors.Is and also have sentence-
// style log formatting via Error() without depending on
// errors.errorString (which is unexported in the stdlib).
type errorString string

func (e errorString) Error() string { return string(e) }

// Is (defense-in-depth, reviewer recommendation) lets the sentinel
// stay detectable through wrap chains that obscure Unwrap. Without
// this method, errors.Is walks fmt.wrapError.Unwrap() and finds
// equality with the package-level ErrYouTubeChannelMismatch only by
// string-value coincidence. With this method, ANY wrapper in the
// chain that loses its target reference will still match because e
// itself implements Is.
func (e errorString) Is(target error) bool {
	return target == ErrYouTubeChannelMismatch
}

// YouTubeChannelBinder is the capability interface for services that
// can verify an OAuth grant is currently bound to a specific
// YouTube channel id, before publishing content to that channel.
//
// Why a separate interface (not folded into OAuthProvider): the
// check is read-side (channels.list GET), platform-specific
// (YouTube-only today, no other provider needs it), and naturally
// distinct from token refresh. Folding it into OAuthProvider would
// force every other provider to add a no-op method.
//
// The PublishWorker (internal/worker/publish_worker.go) looks up
// the binder via `router.Get(name).(YouTubeChannelBinder)` rather
// than via a dedicated router accessor — the router's Get returns
// the raw provider instance, and the type assertion is a single
// line at the call site. This matches the existing convention for
// platform-specific helpers (Validate / Revoke on per-provider
// structs) that aren't on any named capability interface.
//
// Implementations must:
//   - Use a fresh access token. The worker has already called
//     vault.Renew before invoking this method, so the supplied
//     accessToken is post-renew. Do NOT re-refresh internally
//     (would double the OAuth quota).
//   - Return ErrYouTubeChannelMismatch wrapped via fmt.Errorf("%w: ...")
//     when the expected channel id is not present in the grant's
//     channel set.
//   - Wrap non-mismatch transient errors plainly (no ErrYouTubeChannelMismatch
//     in the chain) so the worker can distinguish "reauth needed" from
//     "retry later".
type YouTubeChannelBinder interface {
	NameProvider
	// ValidateChannelBinding verifies that the supplied access token's
	// OAuth grant is bound to the YouTube channel identified by
	// expectedChannelID. Returns:
	//   - nil: grant is bound to the expected channel; proceed with upload.
	//   - error wrapping ErrYouTubeChannelMismatch: grant is NOT bound
	//     to the expected channel; the worker should mark the
	//     platform_account reauth_required and refuse the upload.
	//   - other error: transient failure (5xx, network, decode error);
	//     the worker should retry on a later tick.
	ValidateChannelBinding(ctx context.Context, accessToken, expectedChannelID string) error
}
