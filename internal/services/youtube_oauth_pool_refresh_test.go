package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// captureTokenBody builds a token endpoint that records the refresh
// request's client_id / client_secret and returns a fresh token pair.
func captureTokenBody(t *testing.T, gotClientID, gotSecret *string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		*gotClientID = form.Get("client_id")
		*gotSecret = form.Get("client_secret")
		if form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type: want refresh_token, got %q", form.Get("grant_type"))
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "refreshed-access",
			"token_type":   "bearer",
			"expires_in":   3600,
			"scope":        "youtube.upload youtube.readonly youtube.force-ssl",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newPoolWiredService builds a YouTubeOAuthService with the two-client
// pool registry wired (SetYouTubeOAuthPool) against the given server.
func newPoolWiredService(srv *httptest.Server) *YouTubeOAuthService {
	svc := newTestYouTubeService(srv)
	reg, err := NewYouTubeOAuthClientRegistry(testPoolClients())
	if err != nil {
		panic("test pool registry: " + err.Error())
	}
	svc.SetYouTubeOAuthPool(reg)
	return svc
}

// TestYouTubeRefresh_WithPoolBKey_UsesPoolBClient certifies the R4
// chain oauth_client_key → Resolve(key) → refresh: a grant stamped with
// oauth_client_key=youtube_pool_b must be refreshed with client B's
// client_id + client_secret — NEVER with the legacy single client or
// pool A.
func TestYouTubeRefresh_WithPoolBKey_UsesPoolBClient(t *testing.T) {
	var gotClientID, gotSecret string
	srv := captureTokenBody(t, &gotClientID, &gotSecret)
	svc := newPoolWiredService(srv)

	ctx := credentials.WithOAuthClientKey(context.Background(), "youtube_pool_b")
	result, err := svc.RefreshOAuthToken(ctx, "pool-b-refresh-token")
	if err != nil {
		t.Fatalf("RefreshOAuthToken: %v", err)
	}

	if gotClientID != testPoolClientBID {
		t.Errorf("refresh client_id: want %q (pool B), got %q", testPoolClientBID, gotClientID)
	}
	if gotSecret != testPoolSecret {
		t.Errorf("refresh client_secret: want the pool secret, got %q", gotSecret)
	}
	if result == nil || result.AccessToken != "refreshed-access" {
		t.Errorf("refresh result access token: want refreshed-access, got %+v", result)
	}
}

// TestYouTubeRefresh_WithPoolAKey_NeverUsesPoolB pins the anti-cross-
// pool guarantee: a pool A grant must be refreshed with client A's
// credentials — a body carrying pool B's client_id would be the exact
// invalid_client failure the pool exists to prevent.
func TestYouTubeRefresh_WithPoolAKey_NeverUsesPoolB(t *testing.T) {
	var gotClientID, gotSecret string
	srv := captureTokenBody(t, &gotClientID, &gotSecret)
	svc := newPoolWiredService(srv)

	ctx := credentials.WithOAuthClientKey(context.Background(), "youtube_pool_a")
	if _, err := svc.RefreshOAuthToken(ctx, "pool-a-refresh-token"); err != nil {
		t.Fatalf("RefreshOAuthToken: %v", err)
	}

	if gotClientID != testPoolClientAID {
		t.Errorf("refresh client_id: want %q (pool A), got %q — pool A token must NEVER be refreshed with client B", testPoolClientAID, gotClientID)
	}
	if gotClientID == testPoolClientBID {
		t.Fatal("pool A grant refreshed with pool B client: cross-pool refresh detected")
	}
}

// TestYouTubeRefresh_UnknownStampedKey_FailsClosed certifies the
// fail-closed contract: a stamped oauth_client_key the registry does
// not know must ERROR — the refresher must never silently fall back to
// a different client (which would corrupt the grant's client binding).
func TestYouTubeRefresh_UnknownStampedKey_FailsClosed(t *testing.T) {
	called := false
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newPoolWiredService(srv)
	ctx := credentials.WithOAuthClientKey(context.Background(), "youtube_pool_z")
	_, err := svc.RefreshOAuthToken(ctx, "some-token")
	if err == nil {
		t.Fatal("RefreshOAuthToken with unknown stamped key: want fail-closed error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown client key") {
		t.Errorf("error must identify the unknown key; got %v", err)
	}
	if strings.Contains(err.Error(), testPoolSecret) {
		t.Errorf("error must never leak the pool secret: %v", err)
	}
	if called {
		t.Error("token endpoint must NOT be called when the client cannot be resolved (fail-closed)")
	}
}

// TestYouTubeRefresh_NoStampedKey_LegacyClient certifies that a caller
// without a stamped pool key (non-vault callers, tests, canary) keeps
// the legacy single-client refresh path untouched.
func TestYouTubeRefresh_NoStampedKey_LegacyClient(t *testing.T) {
	var gotClientID string
	srv := captureTokenBody(t, &gotClientID, new(string))
	svc := newPoolWiredService(srv) // pool wired, but NO key in ctx

	if _, err := svc.RefreshOAuthToken(context.Background(), "legacy-token"); err != nil {
		t.Fatalf("RefreshOAuthToken without stamped key: %v", err)
	}
	if gotClientID != svc.cfg.Auth.YouTubeClientID {
		t.Errorf("no stamped key: want legacy client_id %q, got %q", svc.cfg.Auth.YouTubeClientID, gotClientID)
	}
}

// TestYouTubeRefresh_NilPool_LegacyClient certifies the un-wired
// deployment path: a service without a pool registry refreshes with the
// legacy single client even when a key is stamped (pre-pool rows carry
// the legacy label).
func TestYouTubeRefresh_NilPool_LegacyClient(t *testing.T) {
	var gotClientID string
	srv := captureTokenBody(t, &gotClientID, new(string))
	svc := newTestYouTubeService(srv) // no SetYouTubeOAuthPool call

	ctx := credentials.WithOAuthClientKey(context.Background(), "youtube_pool_a")
	if _, err := svc.RefreshOAuthToken(ctx, "legacy-token"); err != nil {
		t.Fatalf("RefreshOAuthToken with nil pool: %v", err)
	}
	if gotClientID != svc.cfg.Auth.YouTubeClientID {
		t.Errorf("nil pool: want legacy client_id %q, got %q", svc.cfg.Auth.YouTubeClientID, gotClientID)
	}
}

// ---------------------------------------------------------------------------
// End-to-end chain certification: platform_account_id → oauth_connection_id
// → oauth_client_key (vault) → Resolve(key) (registry) → refresh (service).
// ---------------------------------------------------------------------------

// chainTokenStore is a minimal credentials.TokenStore for the chain
// test: it holds one token row and persists the refreshed row so the
// vault's post-commit re-read observes the fresh token.
type chainTokenStore struct {
	token *models.Token
}

func (s *chainTokenStore) SaveToken(t *models.Token) error { s.token = t; return nil }

func (s *chainTokenStore) SaveTokenTx(_ context.Context, _ *sql.Tx, t *models.Token) error {
	s.token = t
	return nil
}

func (s *chainTokenStore) FindLatestToken(_ int64, tokenType string) (*models.Token, error) {
	if s.token != nil && s.token.TokenType == tokenType {
		return s.token, nil
	}
	return nil, nil
}

func (s *chainTokenStore) UpdateCiphertexts(_ int64, _, _ []byte) error { return nil }
func (s *chainTokenStore) DeleteAllTokensForOAuthConnection(_ int64) error {
	s.token = nil
	return nil
}

var _ credentials.TokenStore = (*chainTokenStore)(nil)

// TestYouTubeRefresh_Chain_VaultToServicePool wires a real
// CredentialVault (sqlmock DB + in-memory TokenStore) to a
// pool-wired YouTubeOAuthService and runs RenewYouTubeToken, asserting
// the token endpoint receives the credentials of the grant's pool
// client. This certifies the full production chain: the vault resolves
// oauth_client_key from the connection (platform_account_id →
// oauth_connection_id → oauth_client_key), stamps it on ctx, and the
// service Resolves the key and refreshes with that client — never a
// different one. Pool A must never be refreshed with client B and vice
// versa.
func TestYouTubeRefresh_Chain_VaultToServicePool(t *testing.T) {
	cases := []struct {
		name            string
		stampedKey      string
		wantClientID    string
		wantNotClientID string
	}{
		{"pool_a_uses_client_a", "youtube_pool_a", testPoolClientAID, testPoolClientBID},
		{"pool_b_uses_client_b", "youtube_pool_b", testPoolClientBID, testPoolClientAID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const accountID int64 = 9001

			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			if err != nil {
				t.Fatalf("sqlmock.New: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })

			enc, err := crypto.NewEncryptor(1, map[uint32]string{1: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="})
			if err != nil {
				t.Fatalf("crypto.NewEncryptor: %v", err)
			}

			// Seed an expired bearer grant with a decryptable refresh token.
			expired := time.Now().Add(-time.Minute)
			store := &chainTokenStore{}
			vault := credentials.NewCredentialVault(enc, db, store)
			encAccess, _ := enc.Encrypt("stale-access")
			encRefresh, _ := enc.Encrypt(tc.stampedKey + "-refresh-token")
			store.token = &models.Token{
				PlatformAccountID:     accountID,
				OAuthConnectionID:     accountID,
				TokenType:             models.TokenTypeBearer,
				EncryptedToken:        encAccess,
				EncryptedRefreshToken: encRefresh,
				ExpiresAt:             &expired,
			}

			var gotClientID, gotSecret string
			srv := captureTokenBody(t, &gotClientID, &gotSecret)
			svc := newPoolWiredService(srv)

			// SQL chain: fast-path probe, BEGIN, in-tx oauth_connection
			// lookup, advisory lock, oauth_client_key resolution, COMMIT,
			// post-commit re-read probe.
			mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL`).
				WithArgs(accountID).WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).
				WithArgs(accountID).WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
			mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
				WithArgs(accountID).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(`SELECT oc.oauth_client_key
		   FROM oauth_connections oc
		  WHERE oc.id = $1
		    AND oc.provider = 'youtube'`).
				WithArgs(accountID).WillReturnRows(sqlmock.NewRows([]string{"oauth_client_key"}).AddRow(tc.stampedKey))
			mock.ExpectCommit()
			mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL`).
				WithArgs(accountID).WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))

			_, err = credentials.RenewYouTubeToken(context.Background(), vault, accountID, svc.RefreshOAuthToken, slog.Default())
			if err != nil {
				t.Fatalf("RenewYouTubeToken: %v", err)
			}

			if gotClientID != tc.wantClientID {
				t.Errorf("chain refresh client_id: want %q (grant stamped %q), got %q", tc.wantClientID, tc.stampedKey, gotClientID)
			}
			if gotClientID == tc.wantNotClientID {
				t.Errorf("grant %q refreshed with the OTHER pool client: cross-pool refresh detected", tc.stampedKey)
			}
			if gotSecret != testPoolSecret {
				t.Errorf("chain refresh client_secret: want pool secret, got %q", gotSecret)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("sqlmock expectations: %v", err)
			}
		})
	}
}
