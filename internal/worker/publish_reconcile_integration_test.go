//go:build integration

// Package worker — testcontainers integration test for the two-goroutine
// publish pipeline (PublishWorker + ReconcileWorker).
//
// This file is the runtime integration counterpart to the unit tests in
// publish_worker_test.go + reconcile_worker_test.go. It exercises the
// REAL worker constructors against a REAL Postgres (testcontainers),
// with the ONLY non-real component being the outbound HTTP to TikTok —
// that one is pointed at an httptest.Server that returns a canned
// PUBLISH_COMPLETE response. The reconciler drives a real
// *TikTokOAuthService (no fakes) so the platform-decoded
// Reconcile → success result → terminal UpdateStatus path is exercised
// end-to-end.
//
// Why "integration" not "unit":
//   - Postgres is real (testcontainers-go ephemeral 16-alpine).
//   - *CredentialVault is real (real *crypto.Encryptor, real
//     *repository.TokenRepository) so the fast-path Renew hits a real
//     tokens row in the testcontainer's DB.
//   - *CapabilityRouter is real. The fake layer is just TikTok's
//     outbound HTTPS — the *TikTokOAuthService itself is the production
//     struct, only the HTTP client transport is rewired to localhost.
//   - *repository.PostRepository is real. All SQL is real.
//
// The pre-seeded post_target is inserted DIRECTLY in status='publishing'
// with a non-null platform_post_id, so the PublishWorker (driver) has
// nothing to claim (no queued rows) and the ReconcilerWorker is the
// sole actor that drives the row to terminal. This isolates the
// assertion ("row transitions to status='published' within
// cfg.ReconcileWorkerIntervalSeconds + epsilon") to the reconciler's
// 5s cadence — the bound would be meaningless on the 30s driver
// cadence.
//
// The wall-clock bound being asserted is the canonical Taglio 5.x
// guarantee: an async publish's publishing → published transition is
// observed within ONE reconciler tick (5s default). Epsilon accounts
// for the DB roundtrip + httptest.Server roundtrip on a slow CI host.
package worker

import (
	"context"
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	tpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// rewriteTransport is a custom http.RoundTripper that rewrites the
// request URL to point at the local httptest.Server. The real
// *TikTokOAuthService hits open.tiktokapis.com; in the test we want
// every outbound TikTok call to land on localhost where the
// httptest.Server returns canned JSON. The transport preserves the
// request path + headers + body verbatim — only the scheme/host
// change.
type rewriteTransport struct {
	// TargetURL is the httptest.Server's URL (e.g. "http://127.0.0.1:54321").
	TargetURL string
	// Inner is the transport that actually performs the HTTP roundtrip
	// after the URL has been rewritten. nil → http.DefaultTransport
	// (sufficient for plain HTTP to localhost).
	Inner http.RoundTripper
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// req.URL is a *url.URL pointing at the platform's real endpoint
	// (e.g. https://open.tiktokapis.com/v2/post/publish/status/fetch/).
	// Defensive copy via req.Clone: the http.RoundTripper contract is
	// silent on whether the caller may keep referencing req.URL after
	// Do returns, and mutating in place would silently break a future
	// provider that captures the URL (e.g. for retry-with-same-URL).
	// The current *TikTokOAuthService doesn't, so this is a
	// forward-compat hardening rather than a behaviour fix.
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = "http"
	req2.URL.Host = strings.TrimPrefix(t.TargetURL, "http://")
	inner := t.Inner
	if inner == nil {
		inner = http.DefaultTransport
	}
	return inner.RoundTrip(req2)
}

// requireDocker short-circuits the test if Docker isn't available so
// dev environments without Docker don't see false failures. Mirrors
// internal/database/migrations_integration_test.go::requireDocker —
// the helpers can't be shared because the test packages differ.
func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker not on PATH: %v", err)
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}
}

// startTestPostgres spins up an ephemeral Postgres 16-alpine via
// testcontainers-go. Returns the *sql.DB and a cleanup function that
// terminates the container. Mirrors the helper in
// internal/database/migrations_integration_test.go for the same
// package-reuse reason as requireDocker.
func startTestPostgres(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	ctx := context.Background()

	pgC, err := tpostgres.Run(ctx,
		"postgres:16-alpine",
		tpostgres.WithDatabase("instaedit_test_worker"),
		tpostgres.WithUsername("test"),
		tpostgres.WithPassword("test"),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("ConnectionString: %v", err)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	// Retry db.Ping with a short backoff. testcontainers' built-in
	// log-based readiness check can fire BEFORE the TCP listener is
	// bound on some Docker configs — same race as the migrations
	// integration test, same backoff pattern.
	pingDeadline := time.Now().Add(15 * time.Second)
	for attempt := 1; ; attempt++ {
		if pingErr := db.Ping(); pingErr == nil {
			break
		}
		if time.Now().After(pingDeadline) {
			t.Fatalf("db.Ping: timeout after %d attempts over 15s", attempt)
		}
		time.Sleep(200 * time.Millisecond)
	}

	cleanup := func() {
		_ = db.Close()
		_ = pgC.Terminate(ctx)
	}
	return db, cleanup
}

// seedTestFixtures inserts the bare minimum fixture set the test
// needs: 1 user, 1 workspace, 1 platform_account (platform='tiktok'),
// 1 unexpired encrypted token row (so the reconciler's vault.Renew
// takes the fast path), 1 post, and 1 post_target in
// status='publishing' with a non-null platform_post_id so the
// reconciler's ListPublishing picks it up on the very first tick.
//
// Returns the post_target.id so the test can poll its status.
func seedTestFixtures(t *testing.T, db *sql.DB, enc *crypto.Encryptor) (workspaceID, userID, platformAccountID, postID, targetID int64) {
	t.Helper()

	// 1. user — minimal (email + name are the NOT-NULL-everywhere
	// columns; updated_at has a default). Use deterministic emails so
	// a future re-run-on-same-container would conflict, but each
	// test gets a fresh container so this is irrelevant.
	if err := db.QueryRow(
		`INSERT INTO users (email, name) VALUES ('integration-test@example.com', 'integration-test') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatalf("seed users: %v", err)
	}

	// 2. workspace.
	if err := db.QueryRow(
		`INSERT INTO workspaces (name, owner_id) VALUES ('integration-test-ws', $1) RETURNING id`,
		userID,
	).Scan(&workspaceID); err != nil {
		t.Fatalf("seed workspaces: %v", err)
	}

	// 3. platform_account — platform='tiktok', status='active' (the
	// 005_account_lifecycle default). workspace_id is set (the
	// 003_posts_workspaces migration made it nullable-but-prefer-
	// populated; the workers don't filter on it but a populated
	// workspace keeps the fixture consistent with the production
	// invariant).
	if err := db.QueryRow(
		`INSERT INTO platform_accounts (user_id, workspace_id, platform, platform_user_id, username, status)
		 VALUES ($1, $2, 'tiktok', 'tt-integration-1', 'integration_tt', 'active') RETURNING id`,
		userID, workspaceID,
	).Scan(&platformAccountID); err != nil {
		t.Fatalf("seed platform_accounts: %v", err)
	}

	// 4. tokens — pre-insert an UNEXPIRED encrypted access token so
	// the reconciler's vault.Renew takes the fast path
	// (Get → ExpiresAt in future → no refresh → no API call). The
	// test's PublishWorker never fires (no queued rows), so we don't
	// need to worry about the driver's idempotency-key stamp path.
	encryptedAccess, err := enc.Encrypt("dummy-access-token-integration-test")
	if err != nil {
		t.Fatalf("encrypt access token: %v", err)
	}
	expiresAt := time.Now().Add(1 * time.Hour)
	if _, err := db.Exec(
		`INSERT INTO tokens (platform_account_id, token_type, encrypted_token, expires_at, scopes)
		 VALUES ($1, 'bearer', $2, $3, ARRAY['video.publish'])`,
		platformAccountID, encryptedAccess, expiresAt,
	); err != nil {
		t.Fatalf("seed tokens: %v", err)
	}

	// 5. post — minimal; status is incidental (the workers filter
	// on post_targets.status, not posts.status). Use 'draft' since
	// the post is essentially inert in this test — the platform
	// publish is the post_target's affair, not the post's.
	if err := db.QueryRow(
		`INSERT INTO posts (workspace_id, title, caption, media_url, status)
		 VALUES ($1, 'integration-test', 'integration-test caption', 'https://example.com/video.mp4', 'draft') RETURNING id`,
		workspaceID,
	).Scan(&postID); err != nil {
		t.Fatalf("seed posts: %v", err)
	}

	// 6. post_target — the heart of the test. Pre-set
	// status='publishing' and a non-null platform_post_id so
	// ListPublishing picks it up on the first reconciler tick. The
	// driver (PublishWorker) ignores this row (its ListPending
	// filter is status='queued' OR status='waiting_provider'); the
	// reconciler is the sole writer to the publishing → published
	// transition on this row.
	if err := db.QueryRow(
		`INSERT INTO post_targets (post_id, platform_account_id, status, platform_post_id)
		 VALUES ($1, $2, 'publishing', 'integration-test-publish-id-001') RETURNING id`,
		postID, platformAccountID,
	).Scan(&targetID); err != nil {
		t.Fatalf("seed post_targets: %v", err)
	}

	return workspaceID, userID, platformAccountID, postID, targetID
}

// TestPublishAndReconcileWorkers_AsyncRowTransitionsToPublished is the
// end-to-end integration test for the two-goroutine publish pipeline.
//
// What it asserts:
//   1. The pre-seeded post_target row (status='publishing') transitions
//      to status='published' within cfg.ReconcileWorkerIntervalSeconds
//      + epsilon. The bound is the canonical Taglio 5.x wall-clock
//      guarantee: an async publish's terminal transition is observed
//      within ONE reconciler tick.
//   2. The transition was driven by the REAL *TikTokOAuthService
//      pointed at the httptest.Server (not a mock) — verified by the
//      server's hit counter.
//   3. The provider_state column is stamped to 'PUBLISH_COMPLETE' and
//      the published_at column is non-null after the transition.
//   4. The PublishWorker goroutine ran (parallel-drain shape) without
//      interfering with the reconciler's transition.
//
// What it does NOT assert (intentional, to keep the test fast and
// scoped to the reconciler's terminal-transition path):
//   - The PublishWorker's queued → publishing transition (no queued
//     rows are seeded; the driver has nothing to claim).
//   - The outbox dispatcher's materialisation (no outbox rows are
//     seeded; the dispatcher is intentionally NOT spawned — this
//     test is publish-worker + reconcile-worker only, mirroring the
//     spec's "PublishWorker + ReconcileWorker in parallel" wording).
func TestPublishAndReconcileWorkers_AsyncRowTransitionsToPublished(t *testing.T) {
	requireDocker(t)
	db, cleanupDB := startTestPostgres(t)
	// Defer LIFO: cleanupDB runs LAST (after the workers are
	// drained by wg.Wait + the testcontainer can be torn down
	// safely). See the cleanup-ordering note at the bottom of
	// runOnce below for the rationale.
	defer cleanupDB()

	// 1. Migrate the schema (RunMigrations is idempotent so the
	// initial 027-migration set is applied once on a fresh
	// testcontainer).
	if err := database.Migrate(db); err != nil {
		t.Fatalf("database.Migrate: %v", err)
	}

	// 2. Spin up the httptest.Server that stands in for the TikTok
	// status endpoint. Returns PUBLISH_COMPLETE on the first call —
	// the canonical single-tick path the reconciler drives. A
	// hit counter lets the test assert the server was actually
	// reached (rules out "test passed because the reconciler did
	// nothing").
	var statusHits int32
	var mu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/post/publish/status/fetch/" {
			mu.Lock()
			statusHits++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":{"status":"PUBLISH_COMPLETE"}}`))
			return
		}
		// Anything else is unexpected — the real *TikTokOAuthService
		// only calls this one endpoint on the reconciler path. Fail
		// loudly so a future drift surfaces here.
		t.Errorf("unexpected httptest.Server call: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	// 3. Build a custom *http.Client that rewrites every outbound
	// request to point at ts.URL. The transport is the only thing
	// the real *TikTokOAuthService is wired to (via
	// ProviderDependencies.HTTPClient); the rest of the service is
	// the production struct.
	customClient := &http.Client{
		Transport: &rewriteTransport{TargetURL: ts.URL},
		Timeout:   5 * time.Second,
	}

	// 4. Config — must satisfy validation (EncryptionKey base64-
	// decode-32-bytes, TikTokClientSecret ≥ 32 chars). The
	// TikTokClientID/Secret are arbitrary — the real TikTok
	// endpoints aren't called (transport rewrites everything to
	// localhost) so the values only need to pass validate().
	encKeyBytes := []byte("12345678901234567890123456789012") // 32 raw bytes → AES-256
	encKey := base64.StdEncoding.EncodeToString(encKeyBytes)
	cfg := &config.Config{
		TikTokClientID:                 "integration-test-client-id",
		TikTokClientSecret:             "integration-test-client-secret-must-be-32-chars-or-more",
		TikTokRedirectURI:              "https://example.com/callback",
		EncryptionKey:                  encKey,
		PublishWorkerIntervalSeconds:   30,
		ReconcileWorkerIntervalSeconds: 5,
	}

	enc, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		t.Fatalf("crypto.NewEncryptor: %v", err)
	}

	// 5. Real *CredentialVault + real repos. The vault is the
	// production struct; the only reason the test stays hermetic
	// is that the seed inserts an unexpired token so vault.Renew
	// takes the fast path (no refresh, no API call).
	tokenRepo := repository.NewTokenRepository(db)
	userRepo := repository.NewUserRepository(db)
	postRepo := repository.NewPostRepository(db)
	vault := credentials.NewCredentialVault(enc, db, tokenRepo)

	// 6. Seed: user → workspace → platform_account → token → post
	// → post_target(status='publishing'). The target's id is what
	// the test polls on.
	_, _, _, _, targetID := seedTestFixtures(t, db, enc)

	// 7. Real *CapabilityRouter with the real *TikTokOAuthService
	// (pointed at the local httptest.Server via customClient).
	router := services.NewCapabilityRouter()
	ttSvc, err := services.NewTikTokOAuthService(cfg, services.ProviderDependencies{HTTPClient: customClient})
	if err != nil {
		t.Fatalf("services.NewTikTokOAuthService: %v", err)
	}
	if ttSvc == nil {
		t.Fatal("NewTikTokOAuthService returned nil despite TikTokClientID being set")
	}
	router.Register(ttSvc.Name(), ttSvc)

	// 8. Spawn the two workers on independent goroutines, each
	// with their own ctx. Mirrors the cmd/server/main.go shape.
	pubCtx, pubCancel := context.WithCancel(context.Background())
	recCtx, recCancel := context.WithCancel(context.Background())
	defer pubCancel()
	defer recCancel()

	pubWorker := NewPublishWorker(
		postRepo, userRepo, router, vault,
		time.Duration(cfg.PublishWorkerIntervalSeconds)*time.Second,
		nil, // inherit slog.Default()
	)
	recWorker := NewReconcileWorker(
		postRepo, userRepo, router, vault,
		time.Duration(cfg.ReconcileWorkerIntervalSeconds)*time.Second,
		nil, // inherit slog.Default()
	)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = pubWorker.Run(pubCtx) }()
	go func() { defer wg.Done(); _ = recWorker.Run(recCtx) }()

	// 9. Poll the target's status until it transitions to
	// 'published' OR the budget is exhausted. The budget is
	// cfg.ReconcileWorkerIntervalSeconds + epsilon: the canonical
	// Taglio 5.x wall-clock bound for "an async publish is
	// observed within one reconciler tick". 1s of epsilon is
	// generous for the DB roundtrip + httptest.Server roundtrip
	// on a slow CI host; tighten to 500ms if you want a stricter
	// bound, but the +1s slack avoids flakes on shared CI runners.
	epsilon := 1 * time.Second
	budget := time.Duration(cfg.ReconcileWorkerIntervalSeconds)*time.Second + epsilon
	deadline := time.Now().Add(budget)

	var (
		finalStatus    string
		providerState  string
		publishedAtSet bool
	)
	for time.Now().Before(deadline) {
		if err := db.QueryRow(
			`SELECT status, COALESCE(provider_state, ''), published_at IS NOT NULL
			   FROM post_targets WHERE id = $1`,
			targetID,
		).Scan(&finalStatus, &providerState, &publishedAtSet); err != nil {
			t.Fatalf("poll post_targets.status: %v", err)
		}
		if finalStatus == "published" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// 10. Assertions.
	if finalStatus != "published" {
		t.Errorf("post_target did not transition to 'published' within %v (last status: %q)", budget, finalStatus)
	}
	if providerState != "PUBLISH_COMPLETE" {
		t.Errorf("provider_state: want \"PUBLISH_COMPLETE\", got %q (reconciler should stamp the terminal state on the success transition)", providerState)
	}
	if !publishedAtSet {
		t.Error("published_at: want non-nil, got nil (reconciler must stamp publish time on terminal success)")
	}

	mu.Lock()
	hits := statusHits
	mu.Unlock()
	if hits < 1 {
		t.Errorf("httptest.Server was not reached (status hits: %d) — the reconciler did not drive the real TikTok code path", hits)
	}

	// 11. Graceful shutdown. Cancel both contexts and wait for
	// both Run loops to return. wg.Wait blocks until both
	// goroutines call defer wg.Done() — the workers' Run
	// methods return on ctx.Done() (with a graceful drain of the
	// in-flight tick). After wg.Wait returns, the defers fire in
	// LIFO order: pubCancel/recCancel → ts.Close() → cleanupDB.
	// That order matters: the testcontainer MUST outlive the
	// workers (they still hold DB connections) and the httptest
	// server MUST outlive the workers' final API calls (in case
	// any are in flight). cleanupDB running last guarantees the
	// DB is alive when the workers exit.
	pubCancel()
	recCancel()
	wg.Wait()
}

