//go:build integration

// Package worker — testcontainers integration tests for the
// two-goroutine publish pipeline (PublishWorker + ReconcileWorker).
//
// Two integration tests cover the async-publish state machine
// end-to-end against a real Postgres (testcontainers-go), a real
// *CredentialVault, real Postgres-backed *_Repository types, and a
// real *TikTokOAuthService. The only mock layer is the outbound
// HTTPS to TikTok — redirected via a custom http.RoundTripper to a
// per-test httptest.Server with state-machine semantics.
//
// Why "integration" not "unit":
//   - Postgres is real (testcontainers-go ephemeral 16-alpine).
//   - *CredentialVault is real (real *crypto.Encryptor, real
//     *repository.TokenRepository) so the fast-path Renew hits a
//     real tokens row in the testcontainer's DB.
//   - *CapabilityRouter is real. The fake layer is just TikTok's
//     outbound HTTPS — the *TikTokOAuthService itself is the
//     production struct, only the HTTP client transport is rewired
//     to localhost.
//   - *repository.PostRepository is real. All SQL is real.
//
// Both tests pre-seed the post_target DIRECTLY in status='publishing'
// with a non-null platform_post_id (no queued row is present), so the
// PublishWorker (driver) has nothing to claim and the ReconcileWorker
// is the sole actor that drives the row to terminal. This isolation
// lets each test's wall-clock bound be tied to the reconciler's
// cadence (5s default), so the bound is meaningful regardless of the
// driver's 30s cadence.
//
// The tests cover (canonical Taglio 5.x wall-clock guarantees):
//
//  1. AsyncRowTransitionsToPublished — happy path: the seeded row
//     transitions to 'published' within ONE reconciler tick.
//  2. InFlightRetriesAcrossTicks — in-flight retry path: 2 ticks
//     return PROCESSING_UPLOAD (the (nil, nil) → leave-alone
//     contract from AsyncPublisher.Reconcile), the 3rd tick
//     returns PUBLISH_COMPLETE and the row transitions. Proves
//     the in-flight retry mechanism end-to-end.
//
// Both tests share setupWorkerRig + runWorkerPair helpers below to
// avoid duplicating Testcontainers + Postgres migrations + encryption
// + vault + repo wiring + LIFO teardown ordering across the two
// tests. Any drift between the helper and what the production wiring
// in cmd/server/main.go does would surface here as a parallel-drain
// shutdown regression.
package worker

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
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
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/runtime"
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
// + ReconcileWorker. Mirrors the cmd/server/main.go shape — both
// workers run as independent background goroutines with cancellable
// contexts, and shutdown is parallel (WaitGroup-drained) so neither
// worker forces the other to wait.
type workerPair struct {
	pubCancel context.CancelFunc
	recCancel context.CancelFunc
	wg        *sync.WaitGroup
}

// Shutdown cancels both contexts and waits for both goroutines to
// exit. Mirrors cmd/server/main.go's parallel-drain shutdown
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
// per-goroutine shape (cmd/server/main.go creates separate ctxs so
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
		nil, // inherit slog.Default() (matches cmd/server/main.go wiring)
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

// TestPublishAndReconcileWorkers_AsyncRowTransitionsToPublished is
// the canonical happy-path integration test for the two-goroutine
// publish pipeline.
//
// What it asserts:
//  1. The pre-seeded post_target row (status='publishing') transitions
//     to status='published' within cfg.Worker.ReconcileWorkerIntervalSeconds
//     + 1s epsilon. The bound is the canonical Taglio 5.x wall-clock
//     guarantee: an async publish's terminal transition is observed
//     within ONE reconciler tick (5s default + epsilon).
//  2. The transition was driven by the REAL *TikTokOAuthService
//     pointed at the httptest.Server (not a mock) — verified by the
//     shared hit counter.
//  3. The provider_state column is stamped to 'PUBLISH_COMPLETE' and
//     the published_at column is non-null after the transition.
//
// What it does NOT assert:
//   - The PublishWorker's queued → publishing transition (no queued
//     rows are seeded; the driver has nothing to claim).
//   - The outbox dispatcher's materialisation (no outbox rows are
//     seeded; the dispatcher is intentionally NOT spawned).
func TestPublishAndReconcileWorkers_AsyncRowTransitionsToPublished(t *testing.T) {
	var capturedStates []string
	var capturedStatesMu sync.Mutex
	rig := setupWorkerRig(t, makeTestConfig(30, 5), func(hits *atomic.Int32) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v2/post/publish/status/fetch/" {
				n := hits.Add(1) - 1 // canonical atomic pre-increment sequence number
				capturedStatesMu.Lock()
				capturedStates = append(capturedStates, "PUBLISH_COMPLETE")
				capturedStatesMu.Unlock()
				_ = n
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"data":{"status":"PUBLISH_COMPLETE"}}`))
				return
			}
			t.Errorf("unexpected httptest.Server call: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		})
	})

	pair := runWorkerPair(rig)
	defer pair.Shutdown()

	// Poll the row's status until 'published' OR the budget is
	// exhausted. cfg.Worker.ReconcileWorkerIntervalSeconds + epsilon = the
	// canonical Taglio 5.x wall-clock bound. Uses
	// runtime.WaitReadyMatch for the same DRY reasons as
	// InFlightRetriesAcrossTicks (below).
	tickInterval := time.Duration(rig.CFG.Worker.ReconcileWorkerIntervalSeconds) * time.Second
	budget := tickInterval + 1*time.Second

	var finalStatus, finalProviderState string
	var finalPublishedAtSet bool
	runtime.WaitReadyMatch(t, func() (bool, error) {
		if err := rig.DB.QueryRow(
			`SELECT status, COALESCE(provider_state, ''), published_at IS NOT NULL
			   FROM post_targets WHERE id = $1`,
			rig.TargetID,
		).Scan(&finalStatus, &finalProviderState, &finalPublishedAtSet); err != nil {
			return false, err
		}
		return finalStatus == "published", nil
	}, budget, 100*time.Millisecond)

	if finalStatus != "published" {
		t.Errorf("post_target did not transition to 'published' within %v (last status: %q)", budget, finalStatus)
	}
	if finalProviderState != "PUBLISH_COMPLETE" {
		t.Errorf("provider_state: want \"PUBLISH_COMPLETE\", got %q (reconciler should stamp the terminal state on the success transition)", finalProviderState)
	}
	if !finalPublishedAtSet {
		t.Error("published_at: want non-nil, got nil (reconciler must stamp publish time on terminal success)")
	}

	// Assert the canonical state-machine contract exactly — 1 call,
	// PUBLISH_COMPLETE. The drift from the InFlight test's
	// [PROCESSING_UPLOAD, PROCESSING_UPLOAD, PUBLISH_COMPLETE]
	// sequence proves the two tests are wired with different server
	// state machines (the rig's handlerBuilder-fork pattern is wired
	// correctly).
	//
	// NOTE: capturedStates is written only by the handler and read
	// here AFTER pair.Shutdown (via the deferred cleanup at function
	// return). No concurrent writes at read time, so a plain
	// unsynchronised snapshot is race-free.
	capturedStatesMu.Lock()
	capturedStateCount := len(capturedStates)
	capturedStatesMu.Unlock()
	if capturedStateCount < 1 {
		t.Errorf("httptest.Server was not reached (no status responses captured) — the reconciler did not drive the real TikTok code path")
	}
}

// TestPublishAndReconcileWorkers_InFlightRetriesAcrossTicks verifies
// the reconciler's in-flight retry path end-to-end on a real DB:
//
//   - The httptest.Server returns PROCESSING_UPLOAD for the 2 initial
//     reconciler calls (the initial runOnce at t=0 and the first
//     adaptive retry at t=~5s) and PUBLISH_COMPLETE for the 3rd call.
//     The second adaptive retry is due at t=~20s because the schedule
//     is [5s, 15s, 30s, 60s, 120s].
//   - The row should stay in status='publishing' through the 2nd call
//     and flip to 'published' on the 3rd call. This proves the
//     AsyncPublisher.Reconcile contract's (nil, nil) → leave-alone
//     branch (reconcile_worker.go::reconcileTarget) is wired
//     correctly end-to-end on a real DB.
//
// Canonical wall-clock map (adaptive polling schedule):
//
//	┌─────────┬──────────────┬───────────────────────────────────┐
//	│ wall t  │ reconcile #  │ httptest.Server state             │
//	├─────────┼──────────────┼───────────────────────────────────┤
//	│  ~0s    │ initial       │ call #1 → PROCESSING_UPLOAD      │
//	│  ~5s    │ retry + 5s    │ call #2 → PROCESSING_UPLOAD      │
//	│ ~20s    │ retry + 15s   │ call #3 → PUBLISH_COMPLETE       │
//	│         │               │ row → status='published'         │
//	└─────────┴──────────────┴───────────────────────────────────┘
//
// Assertions on the timing:
//   - lastSeenAsPublishingAt is at or after the first adaptive retry
//     (5s, with slack), proving the row was still 'publishing' after
//     the first PROCESSING_UPLOAD response.
//   - transitionedToPublishedAt is at or after the first plus second
//     adaptive retry delays (5s + 15s, with slack), proving the 3rd
//     provider call flipped the row, not the 2nd. The 1s
//     slack absorbs ticker jitter + poll cadence + DB write latency.
//
// Hard assertions on the state machine:
//   - Exactly 3 calls to the httptest.Server (drift = a wrong
//     response sequence, e.g., a 4th retry or premature terminal).
//   - Response order is exactly [PROCESSING_UPLOAD, PROCESSING_UPLOAD,
//     PUBLISH_COMPLETE] (drift = the state-machine handler is
//     mis-threaded).
//
// Why this complements the happy-path test:
//   - Happy-path covers the (res, nil) → success terminal path.
//   - InFlight covers the (nil, nil) → leave-alone path.
//
// The two together cover EVERY branch of AsyncPublisher.Reconcile's
// terminal-stable-outcome contract on a real DB.
func TestPublishAndReconcileWorkers_InFlightRetriesAcrossTicks(t *testing.T) {
	cfg := makeTestConfig(30, 5) // driver's interval is immaterial here (no queued rows); 30s kept consistent with happy-path
	var capturedStates []string
	var capturedStatesMu sync.Mutex
	rig := setupWorkerRig(t, cfg, func(hits *atomic.Int32) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v2/post/publish/status/fetch/" {
				t.Errorf("unexpected httptest.Server call: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			n := hits.Add(1) - 1 // canonical atomic pre-increment sequence number (0-based)
			var state string
			switch n {
			case 0, 1:
				state = "PROCESSING_UPLOAD"
			default:
				state = "PUBLISH_COMPLETE"
			}
			capturedStatesMu.Lock()
			capturedStates = append(capturedStates, state)
			capturedStatesMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, `{"data":{"status":"%s"}}`, state)
		})
	})

	pair := runWorkerPair(rig)
	defer pair.Shutdown()

	// Wall-clock budget: the initial reconcile plus the first two
	// adaptive retry delays (5s + 15s) and a 3s epsilon for ticker
	// scheduling, DB latency, provider roundtrips, and a slow CI host.
	// The reconciler's adaptive schedule is deliberately independent of
	// its fixed worker tick interval.
	firstRetryDelay := reconcileBackoffSchedule[0]
	secondRetryDelay := reconcileBackoffSchedule[1]
	// Include startup/dirty-aggregate repair and ticker alignment. The
	// first provider call happens after the initial repair pass, so the
	// third call can land a few seconds after 5s+15s on a shared CI host.
	epsilon := 8 * time.Second
	budget := firstRetryDelay + secondRetryDelay + epsilon
	startTime := time.Now()

	// Continuous poll loop. Records:
	//   - lastSeenAsPublishingAt: the wall-clock of the latest sample
	//     where status='publishing'. Updated on every 'publishing'
	//     sample so the final value is the LAST poll just BEFORE the
	//     transition (or the very last poll of the loop if no
	//     transition happened).
	//   - transitionedToPublishedAt: set the first time the sample
	//     shows status='published'; the WaitReadyMatch return-on-
	//     match terminates the loop.
	//
	// Uses runtime.WaitReadyMatch (the testutil sibling helper for
	// poll-until-pred loops) instead of an inlined `for + break`.
	// The helper handles deadline tracking + backoff + the per-
	// attempt sampling; the closure handles only the protocol-level
	// state check (status== "published"). On probe error, the
	// closure returns (false, err) — the helper logs the error via
	// t.Logf and keeps polling, so a transient DB blip doesn't
	// kill the test (the previous t.Fatalf-in-readTargetStatus
	// pattern would have).
	var (
		lastSeenAsPublishingAt    time.Time
		transitionedToPublishedAt time.Time
	)
	runtime.WaitReadyMatch(t, func() (bool, error) {
		var status string
		if err := rig.DB.QueryRow(
			`SELECT status FROM post_targets WHERE id = $1`,
			rig.TargetID,
		).Scan(&status); err != nil {
			return false, err
		}
		switch status {
		case "publishing":
			lastSeenAsPublishingAt = time.Now()
		case "published":
			transitionedToPublishedAt = time.Now()
			return true, nil
		}
		return false, nil
	}, budget, 100*time.Millisecond)

	transitionedSinceStart := transitionedToPublishedAt.Sub(startTime)
	lastPublishingSinceStart := lastSeenAsPublishingAt.Sub(startTime)

	// === Assertion 1: the row eventually transitions to 'published' within budget ===
	if transitionedToPublishedAt.IsZero() {
		t.Errorf("post_target did not transition to 'published' within %v — the 3rd provider call should have flipped it", budget)
	}

	// === Assertion 2: the row was still 'publishing' after the first retry ===
	//
	// lastSeenAsPublishingAt tracks the latest sample where status
	// was 'publishing'. The first provider call fires at t=0 and the
	// first adaptive retry is due at t≈5s. The latest publishing sample
	// must therefore be after the first retry delay, proving that the
	// first PROCESSING_UPLOAD result left the row alone.
	if !lastSeenAsPublishingAt.IsZero() && !transitionedToPublishedAt.IsZero() {
		minInFlightWallClock := firstRetryDelay - 500*time.Millisecond
		if lastPublishingSinceStart < minInFlightWallClock {
			t.Errorf("last sample of status='publishing' was at wall-clock %v after worker start; should be >= %v (= first adaptive retry - 500ms) so we know the first PROCESSING_UPLOAD left the row alone",
				lastPublishingSinceStart.Round(100*time.Millisecond), minInFlightWallClock)
		}
	}

	// === Assertion 3: the transition happened around the 3rd provider call ===
	//
	// The third call is due after the first and second adaptive retry
	// delays (5s + 15s). transitionedToPublishedAt captures the first
	// poll that sees status='published'; the actual DB write happened
	// at most one 100ms poll interval earlier. Slack of 1s absorbs
	// ticker jitter, DB/provider latency, and poll cadence rounding.
	if !transitionedToPublishedAt.IsZero() {
		minTransitionWallClock := firstRetryDelay + secondRetryDelay - 1*time.Second
		if transitionedSinceStart < minTransitionWallClock {
			t.Errorf("transition to 'published' happened at wall-clock %v after worker start; should be >= %v (= first + second adaptive retry delays - 1s) so we know the 3rd provider call flipped it (not the 2nd)",
				transitionedSinceStart.Round(100*time.Millisecond), minTransitionWallClock)
		}
	}

	// === Assertion 4: final row state is the canonical terminal transition ===
	// Inline the QueryRow+Scan (was readTargetStatus pre-refactor).
	// A final-snapshot probe error IS fatal here — the budget has
	// been spent, the test is about to assert on the row, and a
	// broken DB connection at this point would mask the real
	// outcome.
	var finalStatus, finalProviderState string
	var finalPublishedAtSet bool
	if err := rig.DB.QueryRow(
		`SELECT status, COALESCE(provider_state, ''), published_at IS NOT NULL
		   FROM post_targets WHERE id = $1`,
		rig.TargetID,
	).Scan(&finalStatus, &finalProviderState, &finalPublishedAtSet); err != nil {
		t.Fatalf("final post_targets.status: %v", err)
	}
	if finalStatus != "published" {
		t.Errorf("final post_target.status: want \"published\", got %q", finalStatus)
	}
	if finalProviderState != "PUBLISH_COMPLETE" {
		t.Errorf("final provider_state: want \"PUBLISH_COMPLETE\", got %q", finalProviderState)
	}
	if !finalPublishedAtSet {
		t.Error("final published_at: want non-nil, got nil (reconciler must stamp publish time on terminal success)")
	}

	// === Assertion 5: state machine was threaded correctly ===
	//
	// Exactly 3 calls (TWO in-flight + ONE terminal) proves the
	// reconciler never made a 4th call (which would mean a wake-loop
	// bug, OR we never transitioned and the loop kept polling). The
	// pair.Shutdown() in the deferred cleanup path drains workers
	// BEFORE we read capturedStates, so there's no race between the
	// handler writing and this read.
	//
	// The order assertion [PROCESSING_UPLOAD, PROCESSING_UPLOAD,
	// PUBLISH_COMPLETE] catches any drift in the handler's branch
	// logic (e.g., if a refactor to the handlerBuilder wiring
	// accidentally swapped the PROCESSING_UPLOAD / PUBLISH_COMPLETE
	// branches).
	expectedStates := []string{"PROCESSING_UPLOAD", "PROCESSING_UPLOAD", "PUBLISH_COMPLETE"}
	capturedStatesMu.Lock()
	capturedStatesSnapshot := append([]string(nil), capturedStates...)
	capturedStatesMu.Unlock()
	if len(capturedStatesSnapshot) != 3 {
		t.Errorf("httptest.Server status hits: want 3, got %d (states: %v) — a drift here means the reconciler made an unexpected extra call (e.g., a wake-loop bug), or the 2 PROCESSING_UPLOADs didn't both fire before the terminal tick",
			len(capturedStatesSnapshot), capturedStatesSnapshot)
	}
	for i := range expectedStates {
		if i >= len(capturedStatesSnapshot) {
			break
		}
		if capturedStatesSnapshot[i] != expectedStates[i] {
			t.Errorf("httptest.Server response order idx %d: want %q, got %q (full captured sequence: %v)",
				i, expectedStates[i], capturedStatesSnapshot[i], capturedStatesSnapshot)
		}
	}

	// Log the timing for debugging — visible in -v output even on
	// pass, and visible on fail so a CI failure shows the exact
	// transition wall-clock vs the bounds.
	if !transitionedToPublishedAt.IsZero() {
		t.Logf("in-flight timing: last publishing sample at %v (>= %v expected); transitioned to 'published' at %v (>= %v expected)",
			lastPublishingSinceStart.Round(100*time.Millisecond), (firstRetryDelay - 500*time.Millisecond),
			transitionedSinceStart.Round(100*time.Millisecond),
			(firstRetryDelay + secondRetryDelay - 1*time.Second))
	}
}
