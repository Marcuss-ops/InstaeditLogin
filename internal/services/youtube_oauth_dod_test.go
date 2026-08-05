package services

// Definition of Done certification for the YouTube OAuth Client Pool.
//
// These tests pin the operator-facing guarantees of the pool feature
// with locally-runnable mocks/sqlmock (the true cross-process advisory
// lock concurrency proof lives in
// internal/credentials/vault_integration_test.go, which runs on a real
// Postgres in CI):
//
//  1. 100 simulated connections distribute 50/50 across the two pools,
//     the 101st assignment is deterministic (never random) and is
//     REFUSED once every pool is over the hard block threshold.
//  2. 100 expired grants refreshed concurrently each use their OWN
//     pool client (no pool A token ever hits client B), each grant is
//     refreshed exactly once, and a second Renew on a fresh grant is
//     served from the vault cache without touching the platform.
//  3. A process restart conserves oauth_client_key from the durable
//     grant and never re-selects a pool.
//  4. Disabling pool A's secret fails pool A grants CLOSED (config
//     error, no silent migration, no HTTP call, no secret in errors or
//     logs) while pool B keeps refreshing.
//  5. Channel-binding mismatch aborts BEFORE any DB write: no token
//     saved, no account activated.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ---- SQL fixtures (must match vault_refresh.go byte-for-byte; the
// harness uses QueryMatcherEqual). -----------------------------------------

const (
	dodProbeSQL = `SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL`

	dodLockSQL = `SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`

	dodAdvisorySQL = "SELECT pg_advisory_xact_lock($1)"

	dodKeySQL = `SELECT oc.oauth_client_key
		   FROM oauth_connections oc
		  WHERE oc.id = $1
		    AND oc.provider = 'youtube'`
)

// ---------------------------------------------------------------------------
// 1. Distribution: 100 connections → 50/50, 101st deterministic + blocked
// ---------------------------------------------------------------------------

// TestDoD_100Connections_Distribute50_50 simulates assigning 100 new
// connections through SelectForNewConnection with a live usage counter:
// every assignment lands on the least-loaded pool, the pools never
// differ by more than 1 at any point, and the final distribution is
// exactly 50/50 — never a random alternation, never a pool pushed past
// the recommended capacity while the other has headroom.
func TestDoD_100Connections_Distribute50_50(t *testing.T) {
	counter := &fakeOAuthClientUsageCounter{usage: map[string]int64{}}
	reg, err := NewYouTubeOAuthClientRegistry(testPoolClients(), WithYouTubeOAuthClientUsageCounter(counter))
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	ctx := context.Background()

	counts := map[string]int64{}
	for i := 0; i < 100; i++ {
		sel, err := reg.SelectForNewConnection(ctx, testPoolGoogleSubject)
		if err != nil {
			t.Fatalf("SelectForNewConnection(connection %d): %v", i+1, err)
		}
		counts[sel.Key]++
		// Keep the fake usage counter in sync so the NEXT selection
		// observes the grant this assignment just created (this is the
		// storage row the real counter would read after the upsert).
		counter.usage[sel.Key]++

		// Balance invariant: the two pools never drift by more than 1.
		diff := counts["youtube_pool_a"] - counts["youtube_pool_b"]
		if diff < -1 || diff > 1 {
			t.Fatalf("after %d connections the pools drifted out of balance: A=%d B=%d (diff %d)",
				i+1, counts["youtube_pool_a"], counts["youtube_pool_b"], diff)
		}
		// No pool may exceed the recommended 50 while the other still
		// has headroom (the assignment must prefer the emptier pool).
		if counts["youtube_pool_a"] > 50 && counts["youtube_pool_b"] < 50 {
			t.Fatalf("pool A pushed past 50 while pool B has headroom (A=%d B=%d)", counts["youtube_pool_a"], counts["youtube_pool_b"])
		}
		if counts["youtube_pool_b"] > 50 && counts["youtube_pool_a"] < 50 {
			t.Fatalf("pool B pushed past 50 while pool A has headroom (A=%d B=%d)", counts["youtube_pool_a"], counts["youtube_pool_b"])
		}
	}

	if counts["youtube_pool_a"] != 50 || counts["youtube_pool_b"] != 50 {
		t.Fatalf("100 connections must distribute exactly 50/50, got A=%d B=%d",
			counts["youtube_pool_a"], counts["youtube_pool_b"])
	}
}

// TestDoD_101stChannel_TieBreaksDeterministic pins the 101st
// assignment when both pools sit at 50/50: remaining capacity ties
// deterministically to the first registered client — never a random
// alternation (a random pick would flip-flop across retries and could
// burn a slot on the wrong pool).
func TestDoD_101stChannel_TieBreaksDeterministic(t *testing.T) {
	counter := &fakeOAuthClientUsageCounter{usage: map[string]int64{
		"youtube_pool_a": 50,
		"youtube_pool_b": 50,
	}}
	reg, err := NewYouTubeOAuthClientRegistry(testPoolClients(), WithYouTubeOAuthClientUsageCounter(counter))
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		sel, err := reg.SelectForNewConnection(ctx, testPoolGoogleSubject)
		if err != nil {
			t.Fatalf("SelectForNewConnection (attempt %d): %v", i+1, err)
		}
		if sel.Key != "youtube_pool_a" {
			t.Fatalf("101st at 50/50: tie must break to the first registered client (youtube_pool_a), got %q", sel.Key)
		}
	}
}

// TestDoD_101stChannel_AllPoolsOverCritical_Blocked pins the block
// policy: when EVERY pool client is over the critical threshold (90
// active grants), the 101st connection is REFUSED with
// ErrYouTubeOAuthClientPoolExhausted — never silently assigned to an
// over-capacity client (Google's hard cap is 100 per (account, client)).
func TestDoD_101stChannel_AllPoolsOverCritical_Blocked(t *testing.T) {
	counter := &fakeOAuthClientUsageCounter{usage: map[string]int64{
		"youtube_pool_a": 95,
		"youtube_pool_b": 95,
	}}
	reg, err := NewYouTubeOAuthClientRegistry(testPoolClients(), WithYouTubeOAuthClientUsageCounter(counter))
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	_, err = reg.SelectForNewConnection(context.Background(), testPoolGoogleSubject)
	if !errors.Is(err, ErrYouTubeOAuthClientPoolExhausted) {
		t.Fatalf("101st when both pools are blocked (>90): want ErrYouTubeOAuthClientPoolExhausted, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// 2. Concurrent refresh of 100 expired grants, each with its own client
// ---------------------------------------------------------------------------

// dodRefreshRequest is one captured call to the fake token endpoint.
type dodRefreshRequest struct {
	clientID     string
	clientSecret string
	refreshToken string
}

// dodRefreshRecorder is a concurrency-safe token endpoint double that
// records every refresh request and always answers with a fresh token.
type dodRefreshRecorder struct {
	mu       sync.Mutex
	requests []dodRefreshRequest
}

func (r *dodRefreshRecorder) serve() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		form, _ := url.ParseQuery(string(body))
		r.mu.Lock()
		r.requests = append(r.requests, dodRefreshRequest{
			clientID:     form.Get("client_id"),
			clientSecret: form.Get("client_secret"),
			refreshToken: form.Get("refresh_token"),
		})
		r.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "fresh-" + form.Get("refresh_token"),
			"token_type":   "bearer",
			"expires_in":   3600,
			"scope":        "youtube.upload youtube.readonly youtube.force-ssl",
		})
	})
	return httptest.NewServer(mux)
}

func (r *dodRefreshRecorder) snapshot() []dodRefreshRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]dodRefreshRequest(nil), r.requests...)
}

// dodGrantStore seeds an in-memory token store with one EXPIRED bearer
// grant carrying a decryptable refresh token.
func dodGrantStore(enc *crypto.Encryptor, accountID int64, refreshToken string) (*chainTokenStore, error) {
	store := &chainTokenStore{}
	expired := time.Now().Add(-time.Minute)
	encAccess, err := enc.Encrypt("stale-access")
	if err != nil {
		return nil, err
	}
	encRefresh, err := enc.Encrypt(refreshToken)
	if err != nil {
		return nil, err
	}
	store.token = &models.Token{
		PlatformAccountID:     accountID,
		OAuthConnectionID:     accountID,
		TokenType:             models.TokenTypeBearer,
		EncryptedToken:        encAccess,
		EncryptedRefreshToken: encRefresh,
		ExpiresAt:             &expired,
	}
	return store, nil
}

// dodQueueRenewExpectations queues the exact SQL sequence vault.Renew
// issues for one grant: fast-path probe, BEGIN, FOR UPDATE lookup,
// advisory lock, oauth_client_key resolution (asserted per grant!),
// COMMIT, post-commit re-read probe. The pool key returned by the
// "database" is the grant's OWN stored key.
func dodQueueRenewExpectations(mock sqlmock.Sqlmock, accountID int64, poolKey string) {
	mock.ExpectQuery(dodProbeSQL).
		WithArgs(accountID).WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectBegin()
	mock.ExpectQuery(dodLockSQL).
		WithArgs(accountID).WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectExec(dodAdvisorySQL).
		WithArgs(accountID).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(dodKeySQL).
		WithArgs(accountID).WillReturnRows(sqlmock.NewRows([]string{"oauth_client_key"}).AddRow(poolKey))
	mock.ExpectCommit()
	mock.ExpectQuery(dodProbeSQL).
		WithArgs(accountID).WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
}

// runDodGrantRefresh drives one grant through the FULL production
// chain — CredentialVault (sqlmock DB + in-memory store) → pool-wired
// YouTubeOAuthService → the shared token endpoint — and returns the
// store (so the caller can prove a second Renew is cache-served).
func runDodGrantRefresh(ctx context.Context, accountID int64, poolKey string, srv *httptest.Server) (*chainTokenStore, error) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		return nil, fmt.Errorf("sqlmock: %w", err)
	}
	defer db.Close()

	enc, err := crypto.NewEncryptor(1, map[uint32]string{1: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="})
	if err != nil {
		return nil, err
	}
	store, err := dodGrantStore(enc, accountID, fmt.Sprintf("rt-%s-%d", poolKey, accountID))
	if err != nil {
		return nil, err
	}
	vault := credentials.NewCredentialVault(enc, db, store)
	dodQueueRenewExpectations(mock, accountID, poolKey)

	svc := newPoolWiredService(srv)
	if _, err := credentials.RenewYouTubeToken(ctx, vault, accountID, svc.RefreshOAuthToken, slog.Default()); err != nil {
		return store, err
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		return store, err
	}
	return store, nil
}

// TestDoD_ConcurrentRefresh_100ExpiredGrants_OwnClient_NoDuplicates
// certifies the "refresh concorrente" DoD line locally: 100 expired
// grants (50 on pool A, 50 on pool B) are renewed concurrently, each
// through its own vault instance. The fake token endpoint records the
// client_id + refresh_token of every call, proving:
//
//   - every pool A grant was refreshed with client A and every pool B
//     grant with client B — a pool A token is NEVER sent to client B
//     (which would surface as invalid_client / invalid_grant);
//   - one worker per grant means each grant was refreshed exactly
//     once in this batch (duplicate prevention under real contention
//     is the advisory lock's job and is certified by the
//     Postgres-gated tests in internal/credentials);
//   - zero failures — the resolver caused no invalid_grant.
//
// After the batch, one grant is renewed a second time and the endpoint
// is NOT called again: the vault serves the fresh grant from its
// in-memory row (fast path) instead of re-refreshing.
func TestDoD_ConcurrentRefresh_100ExpiredGrants_OwnClient_NoDuplicates(t *testing.T) {
	recorder := &dodRefreshRecorder{}
	srv := recorder.serve()
	defer srv.Close()

	const grants = 100
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	stores := make([]*chainTokenStore, grants)
	errs := make([]error, grants)
	var wg sync.WaitGroup
	for i := 0; i < grants; i++ {
		poolKey := "youtube_pool_a"
		if i%2 == 1 {
			poolKey = "youtube_pool_b"
		}
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			stores[i], errs[i] = runDodGrantRefresh(ctx, int64(10000+i), poolKey, srv)
		}()
	}
	wg.Wait()

	// (1) Every grant refreshed successfully — no invalid_grant from the
	// resolver (a cross-pool refresh would have returned invalid_client).
	for i, err := range errs {
		if err != nil {
			t.Fatalf("grant %d (pool %s): %v", i, poolKeyLabel(i), err)
		}
	}

	// (2) Exactly one request per grant — 100 distinct refresh tokens.
	requests := recorder.snapshot()
	if len(requests) != grants {
		t.Fatalf("token endpoint calls: want %d (one per grant), got %d", grants, len(requests))
	}
	seen := map[string]bool{}
	for _, r := range requests {
		if seen[r.refreshToken] {
			t.Errorf("duplicate refresh for grant %s", r.refreshToken)
		}
		seen[r.refreshToken] = true
	}

	// (3) Each grant was refreshed with ITS OWN pool client AND its own
	// pool secret. The two pools carry DISTINCT secrets in this fixture
	// (testPoolSecret vs testPoolSecretB), so the literal DoD line
	// "no token A with secret B" is provable: a pool A grant sent to
	// the token endpoint with pool B's secret fails the assertions
	// below exactly like the invalid_client error Google would return.
	for _, r := range requests {
		wantID := testPoolClientAID
		wantSecret := testPoolSecret
		otherSecret := testPoolSecretB
		if strings.HasPrefix(r.refreshToken, "rt-youtube_pool_b-") {
			wantID = testPoolClientBID
			wantSecret = testPoolSecretB
			otherSecret = testPoolSecret
		}
		if r.clientID != wantID {
			t.Errorf("grant %s refreshed with client %q; want %q (its own pool — cross-pool refresh detected)",
				r.refreshToken, r.clientID, wantID)
		}
		if r.clientSecret != wantSecret {
			t.Errorf("grant %s: client_secret %q, want its own pool secret %q — a cross-pool secret would surface as invalid_client",
				r.refreshToken, r.clientSecret, wantSecret)
		}
		if r.clientSecret == otherSecret {
			t.Errorf("grant %s sent with the OTHER pool's secret %q: literal 'no token A with secret B' violation",
				r.refreshToken, r.clientSecret)
		}
	}

	// (4) No duplicate refresh: a second Renew on a freshly-refreshed
	// grant is served from the vault cache — the token endpoint must not
	// see another request.
	before := recorder.snapshot()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	enc, err := crypto.NewEncryptor(1, map[uint32]string{1: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="})
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	sampledAccountID := int64(10000)
	secondVault := credentials.NewCredentialVault(enc, db, stores[0])
	// Only the fast-path probe runs: the stored token is now fresh.
	mock.ExpectQuery(dodProbeSQL).
		WithArgs(sampledAccountID).WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(sampledAccountID))
	if _, err := secondVault.Renew(ctx, sampledAccountID, models.TokenTypeBearer, func(context.Context, string) (*models.TokenData, error) {
		t.Error("refresher called on a fresh grant: second Renew must be served from cache")
		return nil, nil
	}); err != nil {
		t.Fatalf("second Renew on fresh grant: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
	if got := recorder.snapshot(); len(got) != len(before) {
		t.Errorf("second Renew must not hit the token endpoint: calls went %d → %d", len(before), len(got))
	}
}

func poolKeyLabel(i int) string {
	if i%2 == 1 {
		return "youtube_pool_b"
	}
	return "youtube_pool_a"
}

// ---------------------------------------------------------------------------
// 3. Restart conserves oauth_client_key, never re-selects a pool
// ---------------------------------------------------------------------------

// TestDoD_Restart_ConservesOAuthClientKey_NoReassignment simulates a
// process restart: the vault, the pool registry and the service are all
// rebuilt from scratch, but the durable oauth_connections row still
// carries oauth_client_key=youtube_pool_b. The post-restart refresh
// must resolve that stored key and refresh with client B — a regression
// that re-selected a pool would have picked youtube_pool_a (the
// deterministic first client with no usage counter) and the test fails.
func TestDoD_Restart_ConservesOAuthClientKey_NoReassignment(t *testing.T) {
	const accountID int64 = 4242
	recorder := &dodRefreshRecorder{}
	srv := recorder.serve()
	defer srv.Close()

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	enc, err := crypto.NewEncryptor(1, map[uint32]string{1: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="})
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}

	// Pre-restart instance: grant issued by pool B, key persisted on the
	// connection row.
	store1, err := dodGrantStore(enc, accountID, "rt-youtube_pool_b-4242")
	if err != nil {
		t.Fatal(err)
	}
	dodQueueRenewExpectations(mock, accountID, "youtube_pool_b")
	vault1 := credentials.NewCredentialVault(enc, db, store1)
	if _, err := credentials.RenewYouTubeToken(context.Background(), vault1, accountID, newPoolWiredService(srv).RefreshOAuthToken, slog.Default()); err != nil {
		t.Fatalf("pre-restart renew: %v", err)
	}

	// RESTART: brand-new vault, brand-new pool registry + service, same
	// durable database (same sqlmock row still holds the key).
	store2, err := dodGrantStore(enc, accountID, "rt-youtube_pool_b-4242")
	if err != nil {
		t.Fatal(err)
	}
	dodQueueRenewExpectations(mock, accountID, "youtube_pool_b")
	vault2 := credentials.NewCredentialVault(enc, db, store2)
	if _, err := credentials.RenewYouTubeToken(context.Background(), vault2, accountID, newPoolWiredService(srv).RefreshOAuthToken, slog.Default()); err != nil {
		t.Fatalf("post-restart renew: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}

	requests := recorder.snapshot()
	if len(requests) != 2 {
		t.Fatalf("token endpoint calls: want 2 (one per instance), got %d", len(requests))
	}
	for i, r := range requests {
		if r.clientID != testPoolClientBID {
			t.Errorf("instance %d refreshed with client %q; want %q — the stored oauth_client_key must survive restart",
				i+1, r.clientID, testPoolClientBID)
		}
		if r.clientID == testPoolClientAID {
			t.Errorf("instance %d: a fresh pool selection would have picked client A (deterministic first); the key must come from the durable grant, not re-selection", i+1)
		}
	}
}

// ---------------------------------------------------------------------------
// 4. Pool A secret disabled: B unaffected, A fails closed, no secrets
// ---------------------------------------------------------------------------

// TestDoD_PoolASecretDisabled_BNeverAffected_NoSecretLeak simulates an
// operator disabling pool A (its secret is removed from config and the
// registry rebuilt with only pool B):
//
//   - Resolve("youtube_pool_a") fails (configuration error) without
//     leaking the secret;
//   - new connections deterministically land on pool B;
//   - pool B grants keep refreshing with client B;
//   - a pool A grant FAILS CLOSED (unknown key) — no silent migration
//     to pool B, no HTTP call, no secret in the error or in the logs.
func TestDoD_PoolASecretDisabled_BNeverAffected_NoSecretLeak(t *testing.T) {
	regB, err := NewYouTubeOAuthClientRegistry([]YouTubeOAuthClientConfig{
		{Key: "youtube_pool_b", ClientID: testPoolClientBID, ClientSecret: testPoolSecretB, RedirectURI: testPoolRedirectB},
	})
	if err != nil {
		t.Fatalf("registry (pool B only): %v", err)
	}

	// (1) Pool A is gone: Resolve fails, error never carries the secret.
	if _, err := regB.Resolve("youtube_pool_a"); !errors.Is(err, ErrYouTubeOAuthClientUnknown) {
		t.Fatalf("Resolve(youtube_pool_a) after disable: want ErrYouTubeOAuthClientUnknown, got %v", err)
	} else if strings.Contains(err.Error(), testPoolSecret) || strings.Contains(err.Error(), testPoolSecretB) {
		t.Fatalf("Resolve error leaked the client secret: %v", err)
	}

	// (2) New connections land on the remaining pool.
	sel, err := regB.SelectForNewConnection(context.Background(), testPoolGoogleSubject)
	if err != nil {
		t.Fatalf("SelectForNewConnection: %v", err)
	}
	if sel.Key != "youtube_pool_b" {
		t.Errorf("new connections after pool A disable: want youtube_pool_b, got %q", sel.Key)
	}

	// Capture every log line the service emits for both refreshes below:
	// no credential material may ever appear. NOTE: this swaps the
	// process-wide logger; safe because no test in this package runs
	// with t.Parallel() — the defer restores it unconditionally. If a
	// parallel test is ever added, scope this swap instead.
	var logBuf bytes.Buffer
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(oldDefault)

	recorder := &dodRefreshRecorder{}
	srv := recorder.serve()
	defer srv.Close()
	svc := newTestYouTubeService(srv)
	svc.SetYouTubeOAuthPool(regB)

	// (3) Pool B grants keep working.
	if _, err := svc.RefreshOAuthToken(credentials.WithOAuthClientKey(context.Background(), "youtube_pool_b"), "pool-b-refresh-token"); err != nil {
		t.Fatalf("pool B refresh after pool A disable: %v", err)
	}
	requests := recorder.snapshot()
	if len(requests) != 1 || requests[0].clientID != testPoolClientBID {
		t.Fatalf("pool B refresh: want 1 call with client B, got %+v", requests)
	}

	// (4) Pool A grants fail CLOSED: configuration error, no HTTP call,
	// no silent migration to B, no secret in error or logs.
	if _, err := svc.RefreshOAuthToken(credentials.WithOAuthClientKey(context.Background(), "youtube_pool_a"), "pool-a-refresh-token"); err == nil {
		t.Fatal("pool A grant must fail closed once its client is disabled")
	} else {
		if !strings.Contains(err.Error(), "unknown client key") {
			t.Errorf("error must identify the configuration failure; got %v", err)
		}
		if strings.Contains(err.Error(), testPoolSecret) || strings.Contains(err.Error(), testPoolSecretB) {
			t.Fatalf("error leaked the client secret: %v", err)
		}
	}
	if got := recorder.snapshot(); len(got) != 1 {
		t.Errorf("pool A grant must NOT reach the token endpoint (no token moved to B); calls=%d", len(got))
	}
	if got := logBuf.String(); strings.Contains(got, testPoolSecret) || strings.Contains(got, testPoolSecretB) {
		t.Fatalf("client secret appeared in the logs: %s", got)
	}
}

// ---------------------------------------------------------------------------
// 5. Channel-binding mismatch: no token saved, no channel activated
// ---------------------------------------------------------------------------

// TestDoD_ChannelBindingMismatch_NoTokenSaved_NoActivation certifies
// the channel-binding DoD line: a grant authorized for channel A bound
// to channel B's row must abort with ErrYouTubeChannelMismatch BEFORE
// any database work — the (perfectly valid) token is never encrypted,
// never saved, and the platform_account is never flipped to active.
func TestDoD_ChannelBindingMismatch_NoTokenSaved_NoActivation(t *testing.T) {
	svc, mock, binder, cleanup := newSvcHarness(t)
	defer cleanup()
	binder.validateErr = ErrYouTubeChannelMismatch

	const wrongChannel = "UCaaaaaaaaaaaaaaaaaaaaaZ"
	_, err := svc.AuthorizeChannel(context.Background(),
		1,
		wrongChannel,
		"youtube_pool_a",
		[]string{"https://www.googleapis.com/auth/youtube.upload"},
		&models.TokenData{
			AccessToken:  "bearer-for-channel-A", // valid token — must NOT be saved
			RefreshToken: "refresh-for-channel-A",
			TokenType:    models.TokenTypeBearer,
			ExpiresIn:    3600,
		},
	)
	if err == nil {
		t.Fatal("AuthorizeChannel must reject a channel-binding mismatch")
	}
	if !errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("error chain must include ErrYouTubeChannelMismatch; got %v", err)
	}
	if strings.Contains(err.Error(), "BeginTransaction") {
		t.Errorf("mismatch must abort before ANY DB work; got %v", err)
	}
	// Zero sqlmock expectations were set → any BEGIN/INSERT/UPDATE
	// issued by a regression would fail here.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v (no token saved, no account activation allowed)", err)
	}
	// The binder received the mismatched pair exactly once.
	if calls := binder.validateCalls.Load(); calls != 1 {
		t.Errorf("ValidateChannelBinding calls: want 1, got %d", calls)
	}
	if got := binder.lastExpected.Load().(string); got != wrongChannel {
		t.Errorf("binder received expected channel %q, want %q", got, wrongChannel)
	}
	if got := binder.lastAccessToken.Load().(string); got != "bearer-for-channel-A" {
		t.Errorf("binder received access token %q, want bearer-for-channel-A", got)
	}
}
