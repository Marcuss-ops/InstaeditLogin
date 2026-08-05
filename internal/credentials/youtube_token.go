package credentials

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// RenewYouTubeToken renews the canonical YouTube credential type first.
//
// YouTube's canonical token_type is models.TokenTypeBearer. The
// long_lived branch is a compatibility bridge for credentials written by
// older releases and must be removed after the normalization migration has
// been deployed everywhere. Neither the warning nor the returned error
// contains provider error text, because upstream OAuth responses can contain
// credential-adjacent fields.
func RenewYouTubeToken(
	ctx context.Context,
	vault VaultAPI,
	accountID int64,
	refresher TokenRefresher,
	logger *slog.Logger,
) (*models.OAuthToken, error) {
	canonical, canonicalErr := vault.Renew(ctx, accountID, models.TokenTypeBearer, refresher)
	if canonicalErr == nil {
		return canonical, nil
	}

	if errors.Is(canonicalErr, ErrInvalidGrant) {
		// Preserve only a typed, redacted classification for callers. The
		// provider response may contain credential-adjacent material and
		// must not cross the credential package boundary.
		return nil, ErrYouTubeInvalidGrant
	}
	if !errors.Is(canonicalErr, ErrModernGrantMissing) {
		return nil, ErrYouTubeTokenRenewal
	}

	if logger != nil {
		logger.Warn("youtube canonical token is unavailable; using temporary legacy fallback",
			"account_id", accountID,
			"canonical_type", models.TokenTypeBearer,
			"legacy_type", models.TokenTypeLongLived,
		)
	}

	legacy, legacyErr := vault.Renew(ctx, accountID, models.TokenTypeLongLived, refresher)
	if legacyErr == nil {
		return legacy, nil
	}
	if errors.Is(legacyErr, ErrInvalidGrant) {
		// The legacy compatibility row is still the same Google grant;
		// preserve the reauthorization signal instead of hiding it behind
		// the generic renewal error.
		return nil, ErrYouTubeInvalidGrant
	}
	return nil, fmt.Errorf("youtube token renewal failed for canonical and temporary legacy credentials: %w", ErrYouTubeTokenRenewal)
}

// Legacy fallback is intentionally selected only by the typed vault
// classification ErrModernGrantMissing. Provider-controlled text is never
// parsed to decide whether a different credential row may be used.

// ErrYouTubeTokenRenewal is intentionally generic. Callers may use
// errors.Is without logging provider response bodies or token material.
var ErrYouTubeTokenRenewal = errors.New("youtube token renewal failed")

// ErrYouTubeInvalidGrant is a redacted classification for Google's
// invalid_grant response. It intentionally contains no upstream body,
// token, email, or authorization details.
var ErrYouTubeInvalidGrant = errors.New("youtube OAuth grant requires reauthorization")
