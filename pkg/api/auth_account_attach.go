package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// markOAuthRefreshTokenRequired is the single callback-side transition for
// an authorization that produced an access token but no offline refresh grant.
// AuthorizeChannel deliberately rolls back the first pending authorization so
// no unusable token row is committed; this method then makes the recovery state
// durable and lets the reconnect CTA request prompt=consent. Reconnects from an
// already-authorized account never take this path because the existing grant is
// preserved by the vault/repository layers.
func (r *Router) markOAuthRefreshTokenRequired(ctx context.Context, account *models.PlatformAccount) error {
	if account == nil {
		return fmt.Errorf("missing platform account while marking refresh token reauthorization")
	}
	if r.userRepo == nil {
		return fmt.Errorf("user repository not configured while marking refresh token reauthorization")
	}
	return r.userRepo.MarkReauthRequired(ctx, account.ID, "refresh_token_required", "YouTube did not return an offline refresh token; reconnect with consent")
}

func platformAccountIDForLog(account *models.PlatformAccount) int64 {
	if account == nil {
		return 0
	}
	return account.ID
}

// connectLinkConsumeReason maps the known connect-link nonce
// repository sentinel errors to a short reason string used in logs
// and the connect_link_consume_total metric. It returns an empty
// string for any other error so callers can fall through to a
// generic 500 response.
func connectLinkConsumeReason(err error) string {
	switch {
	case errors.Is(err, repository.ErrNonceMissing):
		return "missing"
	case errors.Is(err, repository.ErrNonceExpired):
		return "expired"
	case errors.Is(err, repository.ErrNonceConsumed):
		return "consumed"
	default:
		return ""
	}
}

// attachDiscoveredAccounts is used by handleCallback for providers that
// expose AccountDiscoverer (Facebook Pages, YouTube Channels). It creates
// one PlatformAccount per discovered account and persists tokens.
//
// Token strategy per provider:
//   - YouTube: every discovered channel receives the root OAuth bearer
//     token (the same token is shared across all channels from one grant).
//     SupplementalTokens is nil/empty for YouTube.
//   - Facebook Pages: each Page carries a SupplementalToken
//     (TokenTypePageAccess) with the per-Page Page Access Token, plus the
//     root long-lived user token stored as TokenTypeLongLived on every
//     discovered page (so refresh can re-exchange from any page).
//
// The generalized flow:
//  1. Discover accounts via the provider's DiscoverAccounts.
//  2. For each DiscoveredAccount, AttachPlatformAccount (idempotent).
//  3. Save metadata from DiscoveredAccount.Metadata on the account row.
//  4. Save the root token on every discovered account.
//  5. Save every DiscoveredAccount.SupplementalTokens entry as an
//     additional token in the vault. This replaces the old provider-
//     specific hack that checked for Metadata["page_access_token"].
//
// ErrYouTubeAmbiguousAuthorization is returned by attachDiscoveredAccounts
// when a YouTube OAuth grant's channels.list(mine=true) returns >1
// channel AND no expected_channel_id was supplied at login time.
//
// P0: a single Google account can own multiple YouTube channels
// (Brand Accounts, multi-channel networks). YouTube's OAuth grant is
// bound to ONE channel per Brand-Account selection at consent time.
// Cloning the root bearer token across every channel silently
// violates Google's YouTube Data API contract and misroutes uploads
// to whatever channel the grant happens to target. The operator must
// re-authorize via /api/v1/auth/youtube/login with
// ?expected_channel_id=UC... so channels.list can be filtered to a
// single channel before any token is saved. Handler maps this to
// HTTP 409 Conflict so the SPA can ask the operator to disambiguate.
var ErrYouTubeAmbiguousAuthorization = errors.New("youtube authorization is ambiguous: re-authorize with expected_channel_id")

// ErrYouTubeChannelMismatch is returned when expected_channel_id was
// supplied but channels.list(mine=true) does NOT contain that ID. The
// operator authenticated the wrong Google account, mistyped the ID,
// or a Brand Account was added since the inventory was imported. We
// refuse to attach ANY account because saving the root token on a
// different channel would silently misroute uploads. Handler maps
// this to HTTP 409 Conflict.
var ErrYouTubeChannelMismatch = errors.New("youtube authorized channel does not match expected channel")

func (r *Router) attachDiscoveredAccounts(ctx context.Context, userID int64, provider string, discoverer services.AccountDiscoverer, tokenData *models.TokenData, expectedChannelID string) (*models.PlatformAccount, error) {
	accounts, err := discoverer.DiscoverAccounts(ctx, tokenData.AccessToken, "")
	if err != nil {
		return nil, fmt.Errorf("discover accounts: %w", err)
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no accounts discovered for provider %s", provider)
	}

	// YouTube enforces a 1:1 OAuth-grant-to-channel mapping. The
	// root bearer token is bound to whichever Brand Account the
	// operator selected in Google's consent screen; cloning it
	// across every channel silently misroutes uploads. Other
	// AccountDiscoverer providers (Facebook Pages, Instagram
	// Business Accounts) intentionally fan the root token out to
	// every discovered account — that path stays unchanged.
	if provider == models.PlatformYouTube {
		if expectedChannelID != "" {
			filtered := accounts[:0]
			matched := 0
			for _, acc := range accounts {
				if acc.Profile.PlatformUserID == expectedChannelID {
					filtered = append(filtered, acc)
					matched++
				}
			}
			if matched == 0 {
				return nil, fmt.Errorf("%w: %q is not in channels.list(mine=true) result", ErrYouTubeChannelMismatch, expectedChannelID)
			}
			if matched > 1 {
				// Defensive against channels.list returning duplicates
				// for the same resource; the first match wins.
				filtered = filtered[:1]
			}
			accounts = filtered
		} else if len(accounts) != 1 {
			return nil, fmt.Errorf("%w: channels.list returned %d channels for this grant", ErrYouTubeAmbiguousAuthorization, len(accounts))
		}
	}

	var first *models.PlatformAccount
	for _, acc := range accounts {
		profile := &models.PlatformProfile{
			PlatformUserID: acc.Profile.PlatformUserID,
			Username:       acc.Profile.Username,
		}
		created, err := r.userRepo.AttachPlatformAccount(userID, profile, provider)
		if err != nil {
			if errors.Is(err, repository.ErrAccountAlreadyLinked) {
				// Already linked to this user — load the existing row so
				// we can update its token below.
				existing, findErr := r.userRepo.FindPlatformAccount(provider, acc.Profile.PlatformUserID)
				if findErr != nil {
					return nil, fmt.Errorf("find existing account: %w", findErr)
				}
				if existing == nil {
					return nil, fmt.Errorf("account already linked but not found")
				}
				created = existing
			} else {
				return nil, fmt.Errorf("attach account %s: %w", acc.Profile.PlatformUserID, err)
			}
		}

		if first == nil {
			first = created
		}

		// Persist metadata from discovery (handle, avatar, stats, etc.)
		if len(acc.Metadata) > 0 {
			if created.Metadata == nil {
				created.Metadata = make(models.Metadata)
			}
			for k, v := range acc.Metadata {
				// Do not overwrite existing metadata keys.
				if _, exists := created.Metadata[k]; !exists {
					created.Metadata[k] = v
				}
			}
			if err := r.userRepo.UpdatePlatformAccount(created); err != nil {
				return nil, fmt.Errorf("update metadata for account %d: %w", created.ID, err)
			}
		}

		// P2 — admin connect-link: Task 1/10 atomic flip. The
		// previous two-call sequence (FinalizeAttach + vault.Save
		// + supplemental vault.Save) could leave the platform_account
		// row in status='active' WITHOUT a tokens row if the vault
		// save failed AFTER FinalizeAttach committed. The new
		// services.ChannelAuthorizer.AuthorizeChannel merges those
		// writes into ONE transaction inside services/
		// channel_authorization.go: any failure rolls every write
		// back, keeping the platform_account row in its pre-call
		// state (typically 'pending_authorization').
		// Equivalent codes behaviour preserved:
		//   - ErrYouTubeChannelMismatch → 422 (via the binder
		//     guard inside AuthorizeChannel)
		//   - Eligibility-gate reject → 422 (status not in
		//     pending_authorization / active / reauth_required)
		//   - DB write failure → 5xx (wrapped, retryable)
		// The principal token + every supplemental token are
		// persisted inside the SAME tx so a Page Access Token
		// (Facebook) failure rolls back its principal user token
		// write AND the oauth_connections row too.
		channelTokens := make([]*models.TokenData, 0, 1+len(acc.SupplementalTokens))
		channelTokens = append(channelTokens, tokenData)
		channelTokens = append(channelTokens, acc.SupplementalTokens...)
		if r.authorizer == nil {
			// Fail-fast on misconfiguration (symmetric to the
			// non-discoverer branch). Mirrors the postStore /
			// workspaceStore nil-guard pattern. Without this,
			// a misconfigured main.go (missing
			// WithChannelAuthorizer) would silently leave every
			// discovered-discoverer account stuck at
			// pending_authorization with no encrypted token
			// row, even though AttachPlatformAccount's commit
			// looks successful. The fail-fast 500 surfaces the
			// wiring mistake at first-callback time.
			return nil, errors.New("channel authorizer not configured")
		}
		if _, err := r.authorizer.AuthorizeChannel(ctx, created.ID, expectedChannelID, tokenData.Scopes, channelTokens...); err != nil {
			// Return the account alongside authorization errors so the
			// callback can persist reauth_required for failures that are
			// specific to this newly attached row (notably a missing
			// first-connection refresh token). The account is still not
			// active because AuthorizeChannel failed before its promotion.
			return first, fmt.Errorf("authorize channel for account %d: %w", created.ID, err)
		}
	}

	return first, nil
}
