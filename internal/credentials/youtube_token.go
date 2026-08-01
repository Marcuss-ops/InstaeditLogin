package credentials

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

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

	if strings.Contains(strings.ToLower(canonicalErr.Error()), "invalid_grant") {
		// Preserve only a typed, redacted classification for callers. The
		// provider response may contain credential-adjacent material and
		// must not cross the credential package boundary.
		return nil, ErrYouTubeInvalidGrant
	}
	if !isMissingYouTubeCanonicalToken(canonicalErr) {
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
	if strings.Contains(strings.ToLower(legacyErr.Error()), "invalid_grant") {
		// The legacy compatibility row is still the same Google grant;
		// preserve the reauthorization signal instead of hiding it behind
		// the generic renewal error.
		return nil, ErrYouTubeInvalidGrant
	}
	return nil, fmt.Errorf("youtube token renewal failed for canonical and temporary legacy credentials: %w", ErrYouTubeTokenRenewal)
}

// isMissingYouTubeCanonicalToken deliberately recognizes only the vault's
// token-absence errors. Infrastructure, decryption, expiry, and provider
// errors must not trigger a legacy lookup because doing so would mask the
// actual incident and could select an unrelated credential.
func isMissingYouTubeCanonicalToken(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no token for account") ||
		strings.Contains(message, "no stored token for account")
}

// ErrYouTubeTokenRenewal is intentionally generic. Callers may use
// errors.Is without logging provider response bodies or token material.
var ErrYouTubeTokenRenewal = errors.New("youtube token renewal failed")

// ErrYouTubeInvalidGrant is a redacted classification for Google's
// invalid_grant response. It intentionally contains no upstream body,
// token, email, or authorization details.
var ErrYouTubeInvalidGrant = errors.New("youtube OAuth grant requires reauthorization")
