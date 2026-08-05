//go:build e2e

// Package e2e bundles the Task 9/10 Definition of Done end-to-end
// suite. The harness spins up Postgres via testcontainers-go and
// three in-process httptest fakes for Google Drive, YouTube, and
// Velox. Each subtest under TestPipelineE2E exercises one
// acceptance criterion from the source document.
//
// Build tag: tests in this package are gated behind `-tags=e2e`
// so `go test ./...` does NOT run them by default (Docker +
// ~3-5 s of container spin-up is not part of the developer inner
// loop). Operators / CI invoke `make test-e2e`.
package e2e

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx stdlib driver for sql.Open("pgx", ...)
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/runtime"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/stores"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/vault"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
)

// statusResumeIncomplete is the YouTube resumable-upload protocol's
// mid-stream response code. Go's stdlib has no constant for it
// (https://developers.google.com/youtube/v3/guides/resumable_uploads
// states: "After each chunk upload, the server returns HTTP 308
// Resume Incomplete"). Defined centrally so handler + client agree.
const statusResumeIncomplete = 308

// E2EHarness is the shared fixture for TestPipelineE2E. It spins up
// Postgres via testcontainers-go (already in go.mod) and exposes
// in-process httptest fakes for Drive, YouTube, and Velox. The 11
// t.Run subtests under TestPipelineE2E reuse this harness.
//
// Spec divergence note: the Task 9/10 source document asks for
// docker-compose with MinIO + Postgres + fakes. We drop the MinIO
// testcontainer to keep the e2e suite dependency-light and ship
// it as a tracked follow-up (Task 9.10 follow-up). In-process
// verify-policy + internal/services/storage_test.go cover the S3
// write/read path; only ~10 lines of additional test plumbing
// are pending once we decide to add MinIO + aws-sdk-v2.
// buildE2ERouter centralizes the mandatory test dependencies required by
// api.MustNewRouter. Test-specific options are appended after the defaults so
// a test can replace any dependency without rebuilding the full option list.
func buildE2ERouter(
	capRouter *services.CapabilityRouter,
	userStore api.UserStore,
	authMgr *auth.Manager,
	opts ...api.RouterOption,
) *api.Router {
	defaults := []api.RouterOption{
		api.WithCredentialVault(vault.NewFakeVault()),
		api.WithChannelAuthorizer(&e2EDefaultChannelAuthorizer{}),
		api.WithOneTimeCodeStore(api.NewInMemoryOneTimeCodeStore(60 * time.Second)),
		api.WithIdempotencyStore(stores.NewFakeIdempotencyStore()),
		api.WithConnectLinkNonceStore(stores.NewFakeConnectLinkNonceStore()),
	}

	return api.MustNewRouter(
		capRouter,
		userStore,
		authMgr,
		"https://app.example.com",
		[]string{"https://app.example.com"},
		append(defaults, opts...)...,
	)
}

// e2EDefaultChannelAuthorizer is a safe no-op default for E2E routes that do
// not exercise OAuth finalization. OAuth-focused tests provide an explicit
// authorizer override to retain their assertions and persistence behavior.
type e2EDefaultChannelAuthorizer struct{}

func (*e2EDefaultChannelAuthorizer) AuthorizeChannel(
	context.Context,
	int64,
	string,
	string,
	[]string,
	...*models.TokenData,
) (int64, error) {
	return 0, nil
}

type E2EHarness struct {
	t *testing.T

	pgContainer testcontainers.Container
	pgDB        *sql.DB
	pgURL       string

	driveFake   *fakeDriveServer
	youTubeFake *fakeYouTubeServer
	veloxFake   *fakeVeloxServer

	HTTPClient *http.Client
}

// NewE2EHarness spins up a Postgres container + applies the e2e
// schema bootstrap + boots the 3 httptest fakes. Returns nil on
// Docker-unavailable so the runner can `t.Skip` cleanly instead of
// failing.
func NewE2EHarness(t *testing.T) *E2EHarness {
	t.Helper()

	// Docker-availability guard: keeps this harness symmetric with
	// internal/testutil/postgres.go + redis.go so a dev laptop
	// without a running daemon t.Skip's instead of hanging the
	// suite on the first ConnectedTaskFailed.
	runtime.RequireDocker(t)

	h := &E2EHarness{t: t}
	h.driveFake = newFakeDriveServer()
	h.youTubeFake = newFakeYouTubeServer()
	h.veloxFake = newFakeVeloxServer()

	// Outer context bumped 120s -> 240s to absorb two failure modes
	// observed in CI:
	//   - testcontainers-go/modules/postgres v0.43.0 default
	//     WaitForLog startup timeout is 60s and races the
	//     container's TCP listener on busy ubuntu-latest runners.
	//   - In a "connection reset by peer" recovery, the second
	//     container start can hit the same race and pull the
	//     startup-tail past 2 minutes.
	// The explicit WithWaitStrategy below uses a 180s startup budget
	// which is the inner ceiling; 240s is the safety margin for the
	// outer ctx (Run + ConnectionString + WaitReady).
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	pgC, err := tcpostgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:17-alpine"),
		tcpostgres.WithDatabase("instaedit_e2e"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		// No explicit WithWaitStrategy override: the modules/postgres
		// default WaitForLog("database system is ready to accept
		// connections") already covers postgres's canonical "ready"
		// signal. The failure mode observed under CI was NOT a
		// missing-log race but a TCP-listener race AFTER the log
		// fired — absorbed by the runtime.WaitReady db.PingContext
		// poll below (the second-stage readiness probe). Customizing
		// the strategy here would only obfuscate the real fix path
		// (WaitReady) without addressing the bit that matters.
		// Keeping postgres:17-alpine pinned because
		// TestMigrations_OrderIndependent (integration.yml comment
		// line 220+) intentionally exercises post-17 features; do
		// NOT downgrade to 16.
	)
	if err != nil {
		t.Skipf("testcontainers: cannot start Postgres (Docker unavailable?): %v", err)
		return nil
	}
	h.pgContainer = pgC

	pgURL, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("postgres connection string: %v", err)
	}
	h.pgURL = pgURL

	db, err := sql.Open("pgx", pgURL)
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("sql.Open: %v", err)
	}
	// Pin the pool to 4 conns: testcontainers-go postgres with
	// ~1 listener + ~4 simultaneous INSERTs is plenty.
	db.SetMaxOpenConns(4)
	h.pgDB = db

	// Second-stage readiness probe: the explicit WaitStrategy above
	// already passed (the "database system is ready" log fired),
	// but testcontainers-go's log-based signal races the TCP
	// listener being able to honour a real connection. Absorb the
	// race with runtime.WaitReady's canonical 30s/200ms contract
	// (matching internal/testutil/postgres.go's "instance ready =
	// log + ping" two-stage model with a slightly wider e2e
	// budget because the TCP listener race is wider on the e2e
	// suite's auth-bearing image than on the lighter unit-helper
	// image).
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 30*time.Second)
	runtime.WaitReady(t, func() error { return db.PingContext(pingCtx) },
		30*time.Second, 200*time.Millisecond)
	pingCancel()

	if err := applyE2ESchema(h.pgDB); err != nil {
		_ = h.pgDB.Close()
		_ = pgC.Terminate(context.Background())
		t.Fatalf("applyE2ESchema: %v", err)
	}

	h.HTTPClient = &http.Client{
		Transport: rewriteRoundTripper(h.driveFake.URL, h.youTubeFake.URL),
		Timeout:   30 * time.Second,
	}

	t.Logf("E2EHarness ready: postgres=%s drive=%s youtube=%s velox=%s",
		pgURL, h.driveFake.URL, h.youTubeFake.URL, h.veloxFake.URL)
	return h
}

// Close brings down containers + closes the sql.DB + fake servers.
// Safe to call multiple times.
func (h *E2EHarness) Close() {
	if h == nil {
		return
	}
	if h.pgDB != nil {
		_ = h.pgDB.Close()
	}
	if h.pgContainer != nil {
		_ = h.pgContainer.Terminate(context.Background())
	}
	if h.driveFake != nil {
		h.driveFake.Close()
	}
	if h.youTubeFake != nil {
		h.youTubeFake.Close()
	}
	if h.veloxFake != nil {
		h.veloxFake.Close()
	}
}

// ResetFakes wipes the per-subtest mutable state on the fakes.
func (h *E2EHarness) ResetFakes() {
	h.driveFake.Reset()
	h.youTubeFake.Reset()
	h.veloxFake.Reset()
}
