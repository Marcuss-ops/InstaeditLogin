package credentials

// Encryption, token preparation, and persistence. This file preserves the
// existing CredentialVault save/rotate behavior without changing its API.

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// Save encrypts and persists an OAuth token while preserving omitted
// refresh-token metadata from the existing grant.
func (v *CredentialVault) Save(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	oauthConnectionID, err := v.oauthConnectionIDForAccount(ctx, platformAccountID)
	if err != nil {
		return err
	}
	return v.saveForOAuthConnection(ctx, oauthConnectionID, platformAccountID, tokenData, true)
}

func (v *CredentialVault) saveForOAuthConnection(ctx context.Context, oauthConnectionID, platformAccountID int64, tokenData *models.TokenData, preserveExisting bool) error {
	token, err := v.prepareTokenForOAuthConnection(ctx, oauthConnectionID, platformAccountID, tokenData, preserveExisting, nil)
	if err != nil {
		return err
	}
	if err := v.store.SaveToken(token); err != nil {
		return fmt.Errorf("vault: failed to persist token: %w", err)
	}
	return nil
}

func (v *CredentialVault) saveForOAuthConnectionTx(ctx context.Context, tx *sql.Tx, oauthConnectionID, platformAccountID int64, tokenData *models.TokenData, preserveExisting bool, existing *models.Token) error {
	token, err := v.prepareTokenForOAuthConnection(ctx, oauthConnectionID, platformAccountID, tokenData, preserveExisting, existing)
	if err != nil {
		return err
	}
	if err := v.store.SaveTokenTx(ctx, tx, token); err != nil {
		return fmt.Errorf("vault: failed to persist token in lock tx: %w", err)
	}
	return nil
}

func (v *CredentialVault) prepareTokenForOAuthConnection(ctx context.Context, oauthConnectionID, platformAccountID int64, tokenData *models.TokenData, preserveExisting bool, existingOverride *models.Token) (*models.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Google commonly omits refresh_token on subsequent authorizations.
	// Read the existing grant before pruning the previous row so a normal
	// reconnect can never replace a valid refresh token with NULL. Scopes
	// and refresh expiry are preserved for the same reason. Work on a
	// local copy: merging provider omissions must not mutate the
	// TokenData owned by the callback or refresh caller.
	merged := *tokenData
	tokenData = &merged
	// Treat only non-blank scope values as authoritative; malformed
	// empty entries from a provider must not erase a valid grant.
	tokenData.Scopes = nonEmptyScopeValues(tokenData.Scopes)
	incomingRefreshTokenEmpty := tokenData.RefreshToken == ""
	var preservedEncryptedRefresh []byte
	var preservedAccessExpiresAt *time.Time
	var preservedRefreshExpiresAt *time.Time
	if preserveExisting || existingOverride != nil {
		existing := existingOverride
		if existing == nil {
			var findErr error
			existing, findErr = v.store.FindLatestToken(oauthConnectionID, tokenData.TokenType)
			if findErr != nil {
				return nil, fmt.Errorf("vault: find existing token for grant: %w", findErr)
			}
		}
		if existing != nil {
			if tokenData.ExpiresIn <= 0 {
				if existingAccessExpiresAt := existing.AccessTokenExpiresAt; existingAccessExpiresAt != nil {
					preservedAccessExpiresAt = cloneTime(existingAccessExpiresAt)
				} else if existing.ExpiresAt != nil {
					preservedAccessExpiresAt = cloneTime(existing.ExpiresAt)
				}
			}
			if tokenData.RefreshTokenExpiresIn <= 0 {
				preservedRefreshExpiresAt = cloneTime(existing.RefreshTokenExpiresAt)
			}
			if tokenData.RefreshToken == "" && len(existing.EncryptedRefreshToken) > 0 {
				// Keep the original ciphertext as the source of truth. The
				// decrypted value is copied only so downstream metadata and
				// callers see the same grant; retaining the bytes directly
				// also guarantees that even an unusual empty plaintext can
				// never turn a valid stored ciphertext into NULL.
				preservedEncryptedRefresh = append([]byte(nil), existing.EncryptedRefreshToken...)
				refresh, decryptErr := v.encryptor.Decrypt(existing.EncryptedRefreshToken)
				if decryptErr != nil {
					return nil, fmt.Errorf("vault: preserve existing refresh token: %w", decryptErr)
				}
				tokenData.RefreshToken = refresh
			}
			if len(tokenData.Scopes) == 0 {
				scopes := existing.GrantedScopes
				if len(scopes) == 0 {
					scopes = existing.Scopes
				}
				tokenData.Scopes = nonEmptyScopeValues(scopes)
			}
			if tokenData.RefreshTokenExpiresIn <= 0 && existing.RefreshTokenExpiresAt != nil {
				remaining := existing.RefreshTokenExpiresAt.Sub(v.clock())
				if remaining > 0 {
					tokenData.RefreshTokenExpiresIn = int64(remaining / time.Second)
					if tokenData.RefreshTokenExpiresIn < 1 {
						tokenData.RefreshTokenExpiresIn = 1
					}
				}
			}
		}
	}
	encrypted, err := v.encryptor.Encrypt(tokenData.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("vault: failed to encrypt access token: %w", err)
	}
	var encryptedRefresh []byte
	if incomingRefreshTokenEmpty && len(preservedEncryptedRefresh) > 0 {
		// Preserve the exact existing envelope when the provider omitted
		// refresh_token. This avoids needless ciphertext rotation and,
		// more importantly, makes an empty callback value unable to clear
		// a valid encrypted grant.
		encryptedRefresh = preservedEncryptedRefresh
	} else if tokenData.RefreshToken != "" {
		encryptedRefresh, err = v.encryptor.Encrypt(tokenData.RefreshToken)
		if err != nil {
			return nil, fmt.Errorf("vault: failed to encrypt refresh token: %w", err)
		}
	} else if len(preservedEncryptedRefresh) > 0 {
		// Never replace an existing encrypted refresh grant with NULL
		// merely because Google omitted refresh_token in this response.
		encryptedRefresh = preservedEncryptedRefresh
	}
	var expiresAt time.Time
	if tokenData.ExpiresIn > 0 {
		expiresAt = v.clock().Add(time.Duration(tokenData.ExpiresIn) * time.Second)
	} else if preservedAccessExpiresAt != nil {
		expiresAt = *preservedAccessExpiresAt
	} else {
		// Keep the historical fallback for a token without an expiry,
		// but never replace a known persisted expiry with "now".
		expiresAt = v.clock()
	}
	var refreshExpiresAt *time.Time
	if tokenData.RefreshTokenExpiresIn > 0 {
		expires := v.clock().Add(time.Duration(tokenData.RefreshTokenExpiresIn) * time.Second)
		refreshExpiresAt = &expires
	} else {
		refreshExpiresAt = preservedRefreshExpiresAt
	}
	// Modern subject-keyed grants are shared across resources, so they
	// do not persist a channel id. Legacy providers retain the resource
	// hint for compatibility; the canonical credential identity remains
	// oauth_connection_id in both cases.
	platformAccountHint := platformAccountID
	if tokenData.ProviderSubjectID != "" {
		platformAccountHint = 0
	}
	return &models.Token{
		PlatformAccountID:     platformAccountHint,
		OAuthConnectionID:     oauthConnectionID,
		TokenType:             tokenData.TokenType,
		EncryptedAccessToken:  encrypted,
		EncryptedToken:        encrypted,
		EncryptedRefreshToken: encryptedRefresh,
		AccessTokenExpiresAt:  &expiresAt,
		ExpiresAt:             &expiresAt,
		RefreshTokenExpiresAt: refreshExpiresAt,
		Scopes:                tokenData.Scopes, GrantedScopes: tokenData.Scopes,
	}, nil
}

// Rotate is the compatibility-preserving semantic alias for Save.
func (v *CredentialVault) Rotate(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error {
	return v.Save(ctx, platformAccountID, tokenData)
}
