package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// DriveAccessTokenProvider is the narrow contract
// GoogleDriveDestination uses to obtain an access token for a
// platform_account_id. Implementation:
//
//   - *DriveVaultTokenProvider below — production path; pulls
//     the encrypted refresh token from the vault, asks
//     GoogleDriveOAuthService.RefreshOAuthToken for a fresh
//     bearer, returns it.
//   - a test fake returning a fixed token (E2E httptest fixture).
//
// Narrow interface so the destination doesn't import
// services.GoogleDriveOAuthService directly (would create a
// dependency direction services → services and force test
// authors to construct a full OAuth service for every test).
//
// Why this contract exists at all: the destination needs a
// token to call /upload/drive/v3/files behind the operator's
// back. The vault is the canonical store for encrypted OAuth
// grants; the refresh path lives in GoogleDriveOAuthService per
// the existing provider pattern. Wrapping both halves behind
// this narrow interface means the destination has ONE place to
// look, and an alternative implementation (e.g. raw grant in
// the request context for a batch-script) plugs in without
// touching the destination code.
type DriveAccessTokenProvider interface {
	// GetAccessToken returns a fresh bearer access token for
	// the platform_account associated with platformAccountID
	// (the platform_accounts.id, NOT the user's id). Errors
	// only on programmer issues (empty id) or vault-level
	// failures (no refresh token, refresh-rejected-by-Google);
	// transient network/retryable errors are surfaced to the
	// destination so the chunk loop can react.
	GetAccessToken(ctx context.Context, platformAccountID int64) (string, error)
}

// NewDriveVaultTokenProvider wires the production path: a
// vault that holds the encrypted refresh token and the
// GoogleDriveOAuthService (read-only, NOT modified) that
// RefreshOAuthToken wraps. Tests inject a fake that skips
// both.
func NewDriveVaultTokenProvider(vault DriveTokenVault, oauth *GoogleDriveOAuthService) *DriveVaultTokenProvider {
	return &DriveVaultTokenProvider{vault: vault, oauth: oauth}
}

// DriveTokenVault is the narrow subset of internal/credentials.Vault
// the destination depends on. Defined here (parallel to
// YouTubeSessionStore) so the destination doesn't take a hard
// dependency on internal/credentials/vault.go and so tests can
// inject a stub without a real KMS/PG backing store.
//
//   - GetRefreshToken: decrypts the encrypted refresh token
//     blob for the platform_account_id.
//
// Future exposure (Task 9/10 hardening): add the account_id
// scope check so a stolen account_id from a different workspace
// cannot be used to mint an upload token. Today the upload
// scope is enforced by GoogleDriveOAuthService.RefreshOAuthToken// (the refresh-token bearer inherits the originally-granted
//     scopes, which are drive.readonly + userinfo.profile per the
//     Task 3/10 consent screen alignment documented in
//     docs/OAUTH-PRODUCTION.md Step 3; the exporter surface
//     requests the unrestricted `drive` write scope separately,
//     so this Importer source contract applies drive.readonly
//     and nothing else).
type DriveTokenVault interface {
	GetRefreshToken(ctx context.Context, platformAccountID int64) (string, error)
}

// DriveVaultTokenProvider is the production implementation of
// DriveAccessTokenProvider.
type DriveVaultTokenProvider struct {
	vault DriveTokenVault
	oauth *GoogleDriveOAuthService
}

// GetAccessToken resolves a platform_account_id to a fresh
// bearer token: pulls the encrypted refresh token from the
// vault, calls GoogleDriveOAuthService.RefreshOAuthToken, returns
// the bearer. Errors wrapping includes:
//
//   - empty platformAccountID → ErrDriveInvalidAccountID
//   - vault store missing / empty refreshToken → ErrDriveNoRefreshToken
//   - oauth refresh failure → original error wrapped
func (p *DriveVaultTokenProvider) GetAccessToken(ctx context.Context, platformAccountID int64) (string, error) {
	if platformAccountID <= 0 {
		return "", fmt.Errorf("%w: platformAccountID=%d", ErrDriveInvalidAccountID, platformAccountID)
	}
	if p == nil || p.vault == nil {
		return "", errors.New("DriveVaultTokenProvider.GetAccessToken: nil vault (wire at bootstrap)")
	}
	if p.oauth == nil {
		return "", errors.New("DriveVaultTokenProvider.GetAccessToken: nil oauth (wire at bootstrap)")
	}
	refreshToken, err := p.vault.GetRefreshToken(ctx, platformAccountID)
	if err != nil {
		return "", fmt.Errorf("DriveVaultTokenProvider.GetAccessToken: vault.GetRefreshToken: %w", err)
	}
	if refreshToken == "" {
		return "", fmt.Errorf("%w: platformAccountID=%d", ErrDriveNoRefreshToken, platformAccountID)
	}
	td, err := p.oauth.RefreshOAuthToken(ctx, refreshToken)
	if err != nil {
		return "", fmt.Errorf("DriveVaultTokenProvider.GetAccessToken: oauth.RefreshOAuthToken: %w", err)
	}
	if td == nil || td.AccessToken == "" {
		return "", fmt.Errorf("DriveVaultTokenProvider.GetAccessToken: oauth returned empty bearer (platformAccountID=%d)", platformAccountID)
	}
	return td.AccessToken, nil
}

// ErrDriveInvalidAccountID is the typed sentinel for an empty /
// non-positive platform_account_id. Surfaces distinctly from a
// vault lookup failure so the orchestration layer can map it to
// a 4xx "configuration error" without misclassifying it as a
// transient retry.
var ErrDriveInvalidAccountID = errors.New("ERR_DRIVE_INVALID_ACCOUNT_ID")

// ErrDriveNoRefreshToken is the typed sentinel for a vault row
// that exists but has no refresh token (e.g. an OAuth-only-online
// grant where Google never returned offline access). Surfaces as
// a 4xx "re-authorize required" — the destination wraps and
// returns a retryable DeliveryResult so the post-completion hook
// doesn't block the publish pipeline.
var ErrDriveNoRefreshToken = errors.New("ERR_DRIVE_NO_REFRESH_TOKEN")

// Compile-time assertion the production wiring stays
// pointed-at-the-right-interface. Caught by `go vet`, not at
// runtime.
var _ DriveAccessTokenProvider = (*DriveVaultTokenProvider)(nil)

// Compile-time anchor for models package import without breaking
// the implicit-imports rule. Used by future extensions (e.g.
// the destination reads account.Profile.PlatformUserID); today
// the marker is unused but drives `go vet` to flag a future refactor
// that drops the import incorrectly.
var _ = models.PlatformGoogleDrive
