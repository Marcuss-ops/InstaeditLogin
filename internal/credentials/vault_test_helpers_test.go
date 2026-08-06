package credentials

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type mockTokenStore struct {
	saveTokenFn         func(*models.Token) error
	findLatestFn        func(int64, string) (*models.Token, error)
	updateCiphertextsFn func(int64, []byte, []byte) error
	deleteAllFn         func(int64) error
	saveCalls           atomic.Int32
	seedCalls           atomic.Int32
	findCalls           atomic.Int32
	updateCalls         atomic.Int32
	deleteCalls         atomic.Int32

	// accountToConn[platformAccountID] = oauth_connection_id. Lazy
	// initialised at the first seedToken call; default = identity
	// (account X → connection X) so existing tests that build Token
	// rows with only PlatformAccountID set keep seeding against the
	// same effective key under both pre- and post-053 contracts.
	accountToConn map[int64]int64

	// state[oauthConnectionID][tokenType] = *models.Token. The two-level
	// map matches the production SQL's `WHERE oauth_connection_id = $1`
	// equality semantics introduced by migration 053: deleting for
	// connection 10 only removes connection 10's tokens, never
	// connection 1's.
	state map[int64]map[string]*models.Token
	mu    sync.RWMutex
}

func (m *mockTokenStore) seedToken(t *models.Token) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.ID == 0 {
		t.ID = 1000 + int64(m.seedCalls.Add(1))
	}
	key := m.resolveStorageKey(t)
	if m.state == nil {
		m.state = make(map[int64]map[string]*models.Token)
	}
	bucket, ok := m.state[key]
	if !ok {
		bucket = make(map[string]*models.Token)
		m.state[key] = bucket
	}
	bucket[t.TokenType] = t
}

func (m *mockTokenStore) resolveStorageKey(t *models.Token) int64 {
	if t.OAuthConnectionID != 0 {
		return t.OAuthConnectionID
	}
	if m.accountToConn == nil {
		m.accountToConn = make(map[int64]int64)
	}
	if existing, ok := m.accountToConn[t.PlatformAccountID]; ok {
		return existing
	}
	// Default: identity mapping. Existing tests that build tokens
	// with PlatformAccountID=N expect them to land in state[N].
	// Pinned at first lookup so later reads (FindLatestToken via
	// vault.Save→store.Save→next FindLatestToken) stay consistent.
	oid := t.PlatformAccountID
	m.accountToConn[t.PlatformAccountID] = oid
	return oid
}

func (m *mockTokenStore) SaveToken(t *models.Token) error {
	m.saveCalls.Add(1)
	if m.saveTokenFn != nil {
		return m.saveTokenFn(t)
	}
	t.ID = int64(m.saveCalls.Load())
	t.CreatedAt = time.Now()
	m.seedToken(t)
	return nil
}

func (m *mockTokenStore) SaveTokenTx(_ context.Context, _ *sql.Tx, t *models.Token) error {
	return m.SaveToken(t)
}

func (m *mockTokenStore) FindLatestToken(oauthConnectionID int64, tokenType string) (*models.Token, error) {
	m.findCalls.Add(1)
	if m.findLatestFn != nil {
		return m.findLatestFn(oauthConnectionID, tokenType)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if bucket, ok := m.state[oauthConnectionID]; ok {
		if t, ok := bucket[tokenType]; ok {
			return t, nil
		}
	}
	return nil, nil
}

func (m *mockTokenStore) DeleteAllTokensForOAuthConnection(oauthConnectionID int64) error {
	m.deleteCalls.Add(1)
	if m.deleteAllFn != nil {
		return m.deleteAllFn(oauthConnectionID)
	}
	// Exact int64 match — mirrors the production SQL
	// `DELETE FROM tokens WHERE oauth_connection_id = $1`. A nested
	// map makes this trivially safe against connection-id prefix
	// overlap (1 vs 10, 100 vs 1000, etc.).
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.state, oauthConnectionID)
	return nil
}

func (m *mockTokenStore) UpdateCiphertexts(tokenID int64, oldEncrypted, newEncrypted []byte) error {
	m.updateCalls.Add(1)
	if m.updateCiphertextsFn != nil {
		return m.updateCiphertextsFn(tokenID, oldEncrypted, newEncrypted)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, bucket := range m.state {
		for _, t := range bucket {
			if t.ID == tokenID {
				if !bytes.Equal(t.EncryptedToken, oldEncrypted) {
					return errors.New("ciphertext stale: another re-encrypt already applied (mock)")
				}
				t.EncryptedToken = newEncrypted
				return nil
			}
		}
	}
	return errors.New("ciphertext stale: row not found (mock)")
}

func newTestVault(t *testing.T) (*CredentialVault, sqlmock.Sqlmock, *mockTokenStore) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// 32-byte base64-encoded key for AES-256-GCM (deterministic for tests).
	// Decoded to: 32 ASCII bytes "0123456789abcdef0123456789abcdef"
	enc, err := crypto.NewEncryptor(1, map[uint32]string{1: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="})
	if err != nil {
		t.Fatalf("crypto.NewEncryptor: %v", err)
	}
	store := &mockTokenStore{}
	return NewCredentialVault(enc, db, store), mock, store
}

func expectOauthConnLookup(mock sqlmock.Sqlmock, platformAccountID, oauthConnectionID int64) {
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL`).
		WithArgs(platformAccountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(oauthConnectionID))
}

// expectOAuthClientKeyLookup installs the R4 pool-client-key resolution
// the Renew slow path issues inside the lock tx. The SQL must match
// resolveOAuthClientKey exactly (QueryMatcherEqual).
func expectOAuthClientKeyLookup(mock sqlmock.Sqlmock, oauthConnectionID int64, key string) {
	mock.ExpectQuery(`SELECT oc.oauth_client_key
		   FROM oauth_connections oc
		  WHERE oc.id = $1
		    AND oc.provider = 'youtube'`).
		WithArgs(oauthConnectionID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_client_key"}).AddRow(key))
}

// expectInvalidGrantMetricLookup installs the (subject, client-key)
// resolution recordInvalidGrantMetric issues on an invalid_grant
// detection. The SQL must match recordInvalidGrantMetric exactly
// (QueryMatcherEqual). A grant with an empty subject must never be
// counted, so the returned subject drives the metric assertion.
func expectInvalidGrantMetricLookup(mock sqlmock.Sqlmock, oauthConnectionID int64, key, subject string) {
	mock.ExpectQuery(`SELECT oc.oauth_client_key, oc.provider_subject_id
		   FROM oauth_connections oc
		  WHERE oc.id = $1
		    AND oc.provider = 'youtube'`).
		WithArgs(oauthConnectionID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_client_key", "provider_subject_id"}).AddRow(key, subject))
}

func newEncryptedToken(t *testing.T, v *CredentialVault, accountID int64, expiresIn time.Duration, refreshToken string) *models.Token {
	t.Helper()
	encAccess, err := v.encryptor.Encrypt("old-access-token")
	if err != nil {
		t.Fatalf("encrypt access: %v", err)
	}
	tok := &models.Token{
		PlatformAccountID: accountID,
		OAuthConnectionID: accountID, // P0#3: identity mapping for tests
		TokenType:         models.TokenTypeBearer,
		EncryptedToken:    encAccess,
		ExpiresAt:         ptrTime(time.Now().Add(expiresIn)),
	}
	if refreshToken != "" {
		encRefresh, err := v.encryptor.Encrypt(refreshToken)
		if err != nil {
			t.Fatalf("encrypt refresh: %v", err)
		}
		tok.EncryptedRefreshToken = encRefresh
	}
	return tok
}

func ptrTime(t time.Time) *time.Time { return &t }

func makeTestEncryptorWith2Keys(t *testing.T, key1B64, key2B64 string) *crypto.Encryptor {
	t.Helper()
	enc, err := crypto.NewEncryptor(2, map[uint32]string{1: key1B64, 2: key2B64})
	if err != nil {
		t.Fatalf("NewEncryptor (2-key): %v", err)
	}
	return enc
}
