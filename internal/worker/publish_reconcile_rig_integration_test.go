//go:build integration

// Shared integration-test rig for the two-goroutine publish pipeline
// (PublishWorker + ReconcileWorker) tests. The test bodies live in
// publish_reconcile_integration_test.go; this file owns the
// Testcontainers + Postgres migrations + encryption + vault + repo
// wiring + fixture seeding + LIFO teardown ordering so the tests can
// stay focused on the state-machine assertions.
package worker

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
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

// rig bundles the wired-up integration test environment (real DB +
// repos + vault + router + cfg + enc) for use by both tests. Single
// ownership of all dependencies at the rig construction site — the
// runWorkerPair helper reads PostRepo + UserRepo from here rather
// than re-constructing them. The fields are intentionally lowercase
// (package-internal); only this file uses them.
type rig struct {
	DB       *sql.DB
	Router   *services.CapabilityRouter
	Vault    credentials.VaultAPI
	PostRepo *repository.PostRepository
	UserRepo *repository.UserRepository
	CFG      *config.Config
	Enc      *crypto.Encryptor
	TargetID int64 // id of the pre-seeded post_target row
}

// setupWorkerRig wires up the integration test environment in the
// canonical ordering: Postgres + cleanup registration → migrations
// → encrypt → httptest.Server + cleanup registration → customClient
// → vault → repos → seed fixtures → router + TikTok capability.
//
// The teardown chain registered via t.Cleanup runs in LIFO order:
// the test's defer'd pair.Shutdown() drains workers FIRST, then
// ts.Close, then cleanupDB. The strict ordering matters — a worker
// could be in the middle of an HTTP call to ts when a fatal fires
// elsewhere, and we want the worker to finish its tick BEFORE the
// httptest.Server is closed and BEFORE the testcontainer is
// terminated.
//
// handlerBuilder receives the shared hit counter (*atomic.Int32)
// so a state-machine-style handler can branch on the per-call
// sequence number (the InFlight test uses this; the happy-path
// test ignores it).
//
// The helper fatally-fails the test on any setup failure. Partial
// resources are torn down via the t.Cleanup chain registered up to
// the failure point — there's no leaked testcontainer or httptest
// server on a failed setup.
func setupWorkerRig(t *testing.T, cfg *config.Config, handlerBuilder func(*atomic.Int32) http.Handler) *rig {
	t.Helper()
	// Postgres (testcontainer). Register DB cleanup FIRST so even
	// fatal failures during Migrate / NewEncryptor / NewServer /
	// seed / router setup tear down the testcontainer. Subsequent
	// ts.Close is registered AFTER this so t.Cleanup runs in LIFO
	// order: ts.Close → cleanupDB.
	db, cleanupDB := postgres.StartTestPostgres(t, postgres.WithDatabase("instaedit_test_worker"))
	t.Cleanup(cleanupDB)

	if err := database.Migrate(db); err != nil {
		t.Fatalf("database.Migrate: %v", err)
	}

	enc, err := crypto.NewEncryptor(1, map[uint32]string{1: cfg.EncryptionKey})
	if err != nil {
		t.Fatalf("crypto.NewEncryptor: %v", err)
	}

	// httptest.Server — registered AFTER cleanupDB so the LIFO
	// teardown order is: ts.Close → cleanupDB.
	var hits atomic.Int32
	ts := httptest.NewServer(handlerBuilder(&hits))
	t.Cleanup(ts.Close)

	customClient := &http.Client{
		Transport: &rewriteTransport{TargetURL: ts.URL, Inner: http.DefaultTransport},
		Timeout:   5 * time.Second,
	}

	tokenRepo := repository.NewTokenRepository(db)
	postRepo := repository.NewPostRepository(db)
	userRepo := repository.NewUserRepository(db)
	vault := credentials.NewCredentialVault(enc, db, tokenRepo)

	_, _, _, _, targetID := seedTestFixtures(t, db, enc)

	router := services.NewCapabilityRouter()
	ttSvc, err := services.NewTikTokOAuthService(cfg, services.ProviderDependencies{HTTPClient: customClient})
	if err != nil {
		t.Fatalf("services.NewTikTokOAuthService: %v", err)
	}
	if ttSvc == nil {
		t.Fatal("NewTikTokOAuthService returned nil despite TikTokClientID being set")
	}
	router.Register(ttSvc.Name(), ttSvc)

	return &rig{
		DB:       db,
		Router:   router,
		Vault:    vault,
		PostRepo: postRepo,
		UserRepo: userRepo,
		CFG:      cfg,
		Enc:      enc,
		TargetID: targetID,
	}
}

// workerPair owns the parallel goroutines spawned for PublishWorker
// + ReconcileWorker. Mirrors the the canonical worker wiring shape — both
// workers run as independent background goroutines with cancellable
// contexts, and shutdown is parallel (WaitGroup-drained) so neither
// worker forces the other to wait.
type workerPair struct {
	pubCancel context.CancelFunc
	recCancel context.CancelFunc
	wg        *sync.WaitGroup
}

// Shutdown cancels both contexts and waits for both goroutines to
// exit. Mirrors the canonical worker wiring's parallel-drain shutdown
// (sync.WaitGroup + per-leaf 15s inner timeouts) — bounded by the
// slowest Run drain (sub-second on healthy paths; capped at ~15s
// on the parallel-drain hard ceiling).
//
// IMPORTANT: callers MUST `defer pair.Shutdown()` so the worker
// drain happens BEFORE t.Cleanup callbacks (httptest.Server close
// + testcontainer terminate) — otherwise a worker's in-flight HTTP
// call could race the testcontainer teardown, producing an "EOF on
// retired connection" that masks the real cause. t.Fatal in the
// test body still fires the defer (Go's runtime.Goexit runs
// deferred funcs before terminating the goroutine), so workers are
// always drained on fatal paths.
func (p *workerPair) Shutdown() {
	p.pubCancel()
	p.recCancel()
	p.wg.Wait()
}

// runWorkerPair spawns PublishWorker + ReconcileWorker on parallel
// goroutines, wired to the rig's dependencies. The workers' tick
// intervals come from rig.CFG.Worker.PublishWorkerIntervalSeconds (default
// 30s, but not material here since ListPending is empty) and
// rig.CFG.Worker.ReconcileWorkerIntervalSeconds (default 5s — drives both
// tests' wall-clock bounds).
//
// Each worker gets its own cancellable ctx with the production
// per-goroutine shape (the canonical worker wiring creates separate ctxs so
// a Cancel call on one doesn't tear down the other). The workers'
// Run methods return on ctx.Done() with a graceful drain of their
// in-flight tick.
//
// PostRepo + UserRepo are read from the rig (single construction
// site) — both *PostRepository and *UserRepository satisfy the
// workers' narrow interface sets via duck-typing at the call
// site. *repository.PostRepository implements PublisherPostStore
// (used by the driver) AND ReconcilePostStore (used by the
// reconciler) without any type glue.
func runWorkerPair(rig *rig) *workerPair {
	pubCtx, pubCancel := context.WithCancel(context.Background())
	recCtx, recCancel := context.WithCancel(context.Background())

	pubWorker := NewPublishWorker(
		rig.PostRepo, rig.UserRepo, rig.Router, rig.Vault,
		nil, // no resolver in integration tests; executePublish falls back to MediaURL
		"test-worker-id",
		nil, // no MemoryLimiter needed in integration tests
		time.Duration(rig.CFG.Worker.PublishWorkerIntervalSeconds)*time.Second,
		nil, // inherit slog.Default() (matches the canonical worker wiring wiring)
	)
	recWorker := NewReconcileWorker(
		rig.PostRepo, rig.UserRepo, rig.Router, rig.Vault,
		"test-worker-id",
		nil, // no MemoryLimiter needed in integration tests
		time.Duration(rig.CFG.Worker.ReconcileWorkerIntervalSeconds)*time.Second,
		nil,
	)

	wg := &sync.WaitGroup{}
	wg.Add(2)
	go func() { defer wg.Done(); _ = pubWorker.Run(pubCtx) }()
	go func() { defer wg.Done(); _ = recWorker.Run(recCtx) }()

	return &workerPair{
		pubCancel: pubCancel,
		recCancel: recCancel,
		wg:        wg,
	}
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

	// 1. user — minimal.
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

	// 3. platform_account — platform='tiktok', status='active'.
	if err := db.QueryRow(
		`INSERT INTO platform_accounts (user_id, workspace_id, platform, platform_user_id, username, status)
		 VALUES ($1, $2, 'tiktok', 'tt-integration-1', 'integration_tt', 'active') RETURNING id`,
		userID, workspaceID,
	).Scan(&platformAccountID); err != nil {
		t.Fatalf("seed platform_accounts: %v", err)
	}

	// 4. tokens — pre-insert an UNEXPIRED encrypted access token so
	// the reconciler's vault.Renew takes the fast path.
	// Migration 053 requires every token row to carry an
	// oauth_connection_id, so first create the connection for the seed
	// platform account.
	var oauthConnID int64
	// Migration 084 replaced migration 043's UNIQUE (user_id, provider,
	// provider_resource_id) table constraint with partial unique indexes,
	// so the historical ON CONFLICT target no longer exists (42P10). A
	// fresh testcontainer DB never conflicts; fall back to a lookup so the
	// seed stays idempotent regardless.
	if err := db.QueryRow(
		`WITH pa AS (SELECT user_id, platform, platform_user_id FROM platform_accounts WHERE id = $1)
		 INSERT INTO oauth_connections (user_id, provider, provider_resource_id)
		 SELECT user_id, platform, platform_user_id FROM pa
		 ON CONFLICT DO NOTHING
		 RETURNING id`,
		platformAccountID,
	).Scan(&oauthConnID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("seed oauth_connection: %v", err)
		}
		if err := db.QueryRow(
			`SELECT oc.id FROM oauth_connections oc
			 JOIN platform_accounts pa
			   ON pa.user_id = oc.user_id AND pa.platform = oc.provider
			  AND pa.platform_user_id = oc.provider_resource_id
			 WHERE pa.id = $1`,
			platformAccountID,
		).Scan(&oauthConnID); err != nil {
			t.Fatalf("seed oauth_connection lookup: %v", err)
		}
	}
	if _, err := db.Exec(
		`UPDATE platform_accounts SET oauth_connection_id = $1 WHERE id = $2`,
		oauthConnID, platformAccountID,
	); err != nil {
		t.Fatalf("seed platform_account oauth link: %v", err)
	}

	encryptedAccess, err := enc.Encrypt("dummy-access-token-integration-test")
	if err != nil {
		t.Fatalf("encrypt access token: %v", err)
	}
	expiresAt := time.Now().Add(1 * time.Hour)
	if _, err := db.Exec(
		`INSERT INTO tokens (platform_account_id, oauth_connection_id, token_type, encrypted_token, expires_at, scopes)
		 VALUES ($1, $2, 'bearer', $3, $4, ARRAY['video.publish'])`,
		platformAccountID, oauthConnID, encryptedAccess, expiresAt,
	); err != nil {
		t.Fatalf("seed tokens: %v", err)
	}

	// 5. post — minimal.
	if err := db.QueryRow(
		// ──────────────────────────────────────────────────────────────────
		//  Seed depends on canonical migration DEFAULTs for the `posts` table.
		//
		//  Every NOT NULL column that this INSERT does not explicitly supply
		//  has a DEFAULT clause in the canonical migrations listed below:
		//   - 003_posts_workspaces.sql: status DEFAULT 'draft', created_at DEFAULT NOW()
		//   - 012_async_threads_support.sql: version DEFAULT 1, updated_at DEFAULT NOW()
		//     (idempotency_key is nullable — NULL when omitted)
		//   - 049b_posts_ingest_after_publish_at.sql: ingest_after DEFAULT NOW()
		//     (publish_at nullable — NULL when omitted)
		//   - 053_upload_jobs_and_posts_default_privacy_level.sql:
		//     default_privacy_level DEFAULT '', privacy_level DEFAULT ''
		//   - The `title`, `caption`, `media_url`, and original `scheduled_at`
		//     columns are explicitly nullable (no DEFAULT) — NULL when omitted.
		//
		//  DO NOT add a NOT NULL column WITHOUT a DEFAULT clause to any future
		//  migration on the posts table — this seed will fail loud at
		//  integration-test runtime. Either use a DEFAULT or update THIS seed
		//  to enumerate the new column explicitly.
		//
		//  See the cited migrations for the authoritative column-level DEFAULT
		//  map; the doc-comment above is a contract assertion. The 3 file
		//  seeds all depend on the same canonical schema + migrations and
		//  share this comment.
		// ──────────────────────────────────────────────────────────────────
		`INSERT INTO posts (workspace_id, title, caption, media_url, status)
		 VALUES ($1, 'integration-test', 'integration-test caption', 'https://example.com/video.mp4', 'draft') RETURNING id`,
		workspaceID,
	).Scan(&postID); err != nil {
		t.Fatalf("seed posts: %v", err)
	}

	// 6. post_target — status='publishing', non-null platform_post_id
	// so ListPublishing picks it up on the first reconciler tick.
	if err := db.QueryRow(
		`INSERT INTO post_targets (post_id, platform_account_id, status, platform_post_id)
		 VALUES ($1, $2, 'publishing', 'integration-test-publish-id-001') RETURNING id`,
		postID, platformAccountID,
	).Scan(&targetID); err != nil {
		t.Fatalf("seed post_targets: %v", err)
	}

	return workspaceID, userID, platformAccountID, postID, targetID
}

// makeTestConfig builds a config.Config that passes the crypto + tiktok
// validation gates without any platform endpoints being real.
// EncryptionKey is a base64-encoded 32-byte raw key (AES-256). The
// TikTokClientID/Secret are arbitrary — they only need to be
// long enough to satisfy validate(); no real TikTok endpoint is
// called because the rewriteTransport redirects every outbound
// request to the test's httptest.Server.
func makeTestConfig(publishInterval, reconcileInterval int) *config.Config {
	encKeyBytes := []byte("12345678901234567890123456789012") // 32 raw bytes → AES-256
	encKey := base64.StdEncoding.EncodeToString(encKeyBytes)
	return &config.Config{
		Auth: config.AuthConfig{
			TikTokClientID:     "integration-test-client-id",
			TikTokClientSecret: "integration-test-client-secret-must-be-32-chars-or-more",
			TikTokRedirectURI:  "https://example.com/callback",
		},
		EncryptionKey: encKey,
		Worker: config.WorkerConfig{
			PublishWorkerIntervalSeconds:   publishInterval,
			ReconcileWorkerIntervalSeconds: reconcileInterval,
		},
	}
}

// (readTargetStatus was removed in the WaitReadyMatch refactor;
// both test polling loops now inline the QueryRow+Scan via a
// WaitReadyMatch closure, which returns (false, err) on probe
// error so a transient DB blip doesn't terminate the test.)
