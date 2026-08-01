package credentials

import (
	"bytes"
	"context"
	"errors"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestVault_Save_Get_Revoke_RoundTrip(t *testing.T) {
	v, mock, store := newTestVault(t)
	ctx := context.Background()
	const accountID int64 = 1

	// P0#3: every public call resolves oauth_connection_id (identity mapping).
	expectOauthConnLookup(mock, accountID, accountID)
	// Save
	if err := v.Save(ctx, accountID, &models.TokenData{
		AccessToken:  "the-access",
		RefreshToken: "the-refresh",
		TokenType:    "bearer",
		ExpiresIn:    3600,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if store.saveCalls.Load() != 1 {
		t.Errorf("SaveToken calls: want 1, got %d", store.saveCalls.Load())
	}

	expectOauthConnLookup(mock, accountID, accountID)
	// Get — the mock's default FindLatestToken reads the just-saved
	// row from its state map, so no findLatestFn override is needed.
	// The Get path will also check the stored ExpiresAt; Save sets it
	// to NOW + ExpiresIn = NOW + 1h, which is fresh.
	got, err := v.Get(ctx, accountID, models.TokenTypeBearer)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessToken != "the-access" {
		t.Errorf("Get returned access token: want %q, got %q", "the-access", got.AccessToken)
	}

	expectOauthConnLookup(mock, accountID, accountID)
	// Revoke
	if err := v.Revoke(ctx, accountID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if store.deleteCalls.Load() != 1 {
		t.Errorf("DeleteAllTokensForOAuthConnection calls: want 1, got %d", store.deleteCalls.Load())
	}
	expectOauthConnLookup(mock, accountID, accountID)
	// After Revoke, Get must return a "no token" error (state cleared).
	if _, err := v.Get(ctx, accountID, models.TokenTypeBearer); err == nil {
		t.Error("Get after Revoke must return an error (state cleared)")
	}
}

func TestVault_Save_EmptyRefreshToken_PreservesExistingCiphertext(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 61

	oldRefreshCiphertext, err := v.encryptor.Encrypt("existing-refresh-token")
	if err != nil {
		t.Fatalf("encrypt existing refresh token: %v", err)
	}
	store.seedToken(&models.Token{
		PlatformAccountID:     accountID,
		OAuthConnectionID:     accountID,
		TokenType:             models.TokenTypeBearer,
		EncryptedRefreshToken: oldRefreshCiphertext,
	})

	expectOauthConnLookup(mock, accountID, accountID)
	if err := v.Save(context.Background(), accountID, &models.TokenData{
		AccessToken: "reconnected-access",
		TokenType:   models.TokenTypeBearer,
		ExpiresIn:   3600,
		// RefreshToken intentionally omitted by the provider.
	}); err != nil {
		t.Fatalf("Save reconnect: %v", err)
	}

	stored, err := store.FindLatestToken(accountID, models.TokenTypeBearer)
	if err != nil {
		t.Fatalf("find persisted reconnect token: %v", err)
	}
	if stored == nil || len(stored.EncryptedRefreshToken) == 0 {
		t.Fatal("reconnect must retain a non-empty encrypted refresh token")
	}
	if !bytes.Equal(stored.EncryptedRefreshToken, oldRefreshCiphertext) {
		t.Fatal("reconnect must preserve the existing refresh-token ciphertext exactly")
	}
	gotRefresh, err := v.encryptor.Decrypt(stored.EncryptedRefreshToken)
	if err != nil {
		t.Fatalf("decrypt preserved refresh token: %v", err)
	}
	if gotRefresh != "existing-refresh-token" {
		t.Errorf("preserved refresh token: want existing value, got %q", gotRefresh)
	}
}

func TestVault_PrepareToken_ScopeAndExpiryMerge(t *testing.T) {
	v, _, _ := newTestVault(t)
	const (
		oauthConnectionID int64 = 701
		platformAccountID int64 = 702
	)
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	v.SetClock(func() time.Time { return now })
	previousAccessExpiry := now.Add(45 * time.Minute)
	previousRefreshExpiry := now.Add(30 * 24 * time.Hour)

	oldRefresh, err := v.encryptor.Encrypt("existing-refresh")
	if err != nil {
		t.Fatalf("encrypt existing refresh token: %v", err)
	}
	input := &models.TokenData{
		AccessToken: "refreshed-without-scope",
		TokenType:   models.TokenTypeBearer,
		ExpiresIn:   3600,
	}
	originalScopes := append([]string(nil), input.Scopes...)
	originalRefresh := input.RefreshToken

	existing := &models.Token{
		OAuthConnectionID:     oauthConnectionID,
		TokenType:             models.TokenTypeBearer,
		EncryptedRefreshToken: oldRefresh,
		AccessTokenExpiresAt:  &previousAccessExpiry,
		ExpiresAt:             &previousAccessExpiry,
		RefreshTokenExpiresAt: &previousRefreshExpiry,
		GrantedScopes:         []string{"scope.read", "scope.write"},
	}

	withoutScope, err := v.prepareTokenForOAuthConnection(
		context.Background(), oauthConnectionID, platformAccountID,
		input, false, existing,
	)
	if err != nil {
		t.Fatalf("prepare token without scope: %v", err)
	}
	if got, want := strings.Join(withoutScope.GrantedScopes, ","), "scope.read,scope.write"; got != want {
		t.Fatalf("omitted scope: want %q preserved, got %q", want, got)
	}
	if !withoutScope.AccessTokenExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("access expiry: want %s from expires_in, got %s", now.Add(time.Hour), withoutScope.AccessTokenExpiresAt)
	}
	if !withoutScope.RefreshTokenExpiresAt.Equal(previousRefreshExpiry) {
		t.Fatalf("omitted refresh expiry: want %s preserved, got %s", previousRefreshExpiry, withoutScope.RefreshTokenExpiresAt)
	}
	if !bytes.Equal(withoutScope.EncryptedRefreshToken, oldRefresh) {
		t.Fatal("omitted refresh token must preserve the existing encrypted grant")
	}
	if !reflect.DeepEqual(input.Scopes, originalScopes) || input.RefreshToken != originalRefresh {
		t.Fatalf("prepare must not mutate input TokenData: scopes=%v refresh=%q", input.Scopes, input.RefreshToken)
	}

	withNewScope, err := v.prepareTokenForOAuthConnection(
		context.Background(), oauthConnectionID, platformAccountID,
		&models.TokenData{
			AccessToken:           "refreshed-with-new-scope",
			TokenType:             models.TokenTypeBearer,
			ExpiresIn:             1200,
			RefreshTokenExpiresIn: 7200,
			Scopes:                []string{"scope.read"},
		}, false, existing,
	)
	if err != nil {
		t.Fatalf("prepare token with new scope: %v", err)
	}
	if got, want := strings.Join(withNewScope.GrantedScopes, ","), "scope.read"; got != want {
		t.Fatalf("returned scope set: want replacement %q, got %q", want, got)
	}
	if !withNewScope.AccessTokenExpiresAt.Equal(now.Add(1200 * time.Second)) {
		t.Fatalf("new access expiry: want %s, got %s", now.Add(1200*time.Second), withNewScope.AccessTokenExpiresAt)
	}
	if !withNewScope.RefreshTokenExpiresAt.Equal(now.Add(7200 * time.Second)) {
		t.Fatalf("new refresh expiry: want %s, got %s", now.Add(7200*time.Second), withNewScope.RefreshTokenExpiresAt)
	}

	withoutBlankScope, err := v.prepareTokenForOAuthConnection(
		context.Background(), oauthConnectionID, platformAccountID,
		&models.TokenData{
			AccessToken: "refreshed-with-blank-scope",
			TokenType:   models.TokenTypeBearer,
			ExpiresIn:   1800,
			Scopes:      []string{" ", "\t"},
		}, false, existing,
	)
	if err != nil {
		t.Fatalf("prepare token with blank scopes: %v", err)
	}
	if got, want := strings.Join(withoutBlankScope.GrantedScopes, ","), "scope.read,scope.write"; got != want {
		t.Fatalf("blank scope values: want %q preserved, got %q", want, got)
	}

	withoutAccessExpiry, err := v.prepareTokenForOAuthConnection(
		context.Background(), oauthConnectionID, platformAccountID,
		&models.TokenData{
			AccessToken: "refresh-without-expires-in",
			TokenType:   models.TokenTypeBearer,
		}, false, existing,
	)
	if err != nil {
		t.Fatalf("prepare token without access expiry: %v", err)
	}
	if !withoutAccessExpiry.AccessTokenExpiresAt.Equal(previousAccessExpiry) {
		t.Fatalf("omitted access expiry: want %s preserved, got %s", previousAccessExpiry, withoutAccessExpiry.AccessTokenExpiresAt)
	}
}

func TestVault_Save_LegacyProviderRetainsPlatformAccountHint(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID, oauthConnectionID int64 = 17, 900
	var saved *models.Token
	store.saveTokenFn = func(token *models.Token) error {
		saved = token
		return nil
	}

	expectOauthConnLookup(mock, accountID, oauthConnectionID)
	if err := v.Save(context.Background(), accountID, &models.TokenData{
		AccessToken: "legacy-access",
		TokenType:   models.TokenTypeLongLived,
		ExpiresIn:   3600,
	}); err != nil {
		t.Fatalf("Save legacy provider token: %v", err)
	}
	if saved == nil {
		t.Fatal("Save did not pass a token to the store")
	}
	if saved.PlatformAccountID != accountID {
		t.Fatalf("legacy token platform_account_id: got %d want %d", saved.PlatformAccountID, accountID)
	}
	if saved.OAuthConnectionID != oauthConnectionID {
		t.Fatalf("legacy token oauth_connection_id: got %d want %d", saved.OAuthConnectionID, oauthConnectionID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

func TestVault_Save_ModernSubjectLeavesPlatformAccountHintNull(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID, oauthConnectionID int64 = 18, 901
	var saved *models.Token
	store.saveTokenFn = func(token *models.Token) error {
		saved = token
		return nil
	}

	expectOauthConnLookup(mock, accountID, oauthConnectionID)
	if err := v.Save(context.Background(), accountID, &models.TokenData{
		AccessToken:       "youtube-access",
		TokenType:         models.TokenTypeBearer,
		ProviderSubjectID: "google-subject",
		ExpiresIn:         3600,
	}); err != nil {
		t.Fatalf("Save modern YouTube token: %v", err)
	}
	if saved == nil {
		t.Fatal("Save did not pass a token to the store")
	}
	if saved.PlatformAccountID != 0 {
		t.Fatalf("modern grant token platform_account_id: got %d want 0/SQL NULL", saved.PlatformAccountID)
	}
	if saved.OAuthConnectionID != oauthConnectionID {
		t.Fatalf("modern grant token oauth_connection_id: got %d want %d", saved.OAuthConnectionID, oauthConnectionID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

func TestVault_Revoke_NotFound_TreatedAsSuccess(t *testing.T) {
	v, mock, store := newTestVault(t)
	expectOauthConnLookup(mock, 1, 1)
	store.deleteAllFn = func(int64) error {
		return errors.New("token not found: oauth_connection_id=1")
	}
	if err := v.Revoke(context.Background(), 1); err != nil {
		t.Errorf("Revoke must swallow 'token not found' (idempotent disconnect): got %v", err)
	}
}
