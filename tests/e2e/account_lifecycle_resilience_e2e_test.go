//go:build e2e

package e2e

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
)

// TestAccountLifecycle_ConcurrentLastDisconnectAndRestartPersistence uses
// real Postgres transactions and the real router. Two requests race to
// disconnect the last channel on a grant; advisory locking must make exactly
// one request perform the remote revoke, while both retries remain idempotent.
// Reopening the DB connection afterwards simulates a worker/API restart and
// proves the committed terminal state survives process-local state loss.
func TestAccountLifecycle_ConcurrentLastDisconnectAndRestartPersistence(t *testing.T) {
	h := NewE2EHarness(t)
	if h == nil || h.pgDB == nil {
		t.Skip("testcontainers Postgres unavailable")
	}
	defer h.Close()
	applySharedGrantLifecycleE2ESchemaExt(t, h.pgDB)

	encKey := make([]byte, 32)
	if _, err := rand.Read(encKey); err != nil {
		t.Fatalf("encryption key: %v", err)
	}
	encKeyB64 := base64.StdEncoding.EncodeToString(encKey)
	encryptor, err := crypto.NewEncryptor(1, map[uint32]string{1: encKeyB64})
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	tokenRepo := repository.NewTokenRepository(h.pgDB)
	vault := credentials.NewCredentialVault(encryptor, h.pgDB, tokenRepo)
	ctx := context.Background()
	f := seedSharedGrantE2E(t, h, vault)
	authMgr := auth.NewManager(testJWTSecret, 15*time.Minute)
	revoker := &sharedGrantRevokerService{stubYouTubeOAuthService: &stubYouTubeOAuthService{}}
	router := buildE2ERouter(services.NewCapabilityRouter(), repository.NewUserRepository(h.pgDB), authMgr,
		api.WithCredentialVault(vault), api.WithYouTubeService(revoker))

	// Disconnect A first so B is the last active channel. This also verifies
	// the shared grant remains usable before the concurrent last-channel race.
	if w := sharedGrantAuthedRequest(t, router, authMgr, f, http.MethodPost, "/api/v1/accounts/"+itoa(f.accountA)+"/disconnect"); w.Code != http.StatusNoContent {
		t.Fatalf("disconnect A: got %d", w.Code)
	}

	// Route wiring is initialization, not request work. Build the handler
	// once before starting concurrent requests; Setup serializes accidental
	// repeated callers, while this test focuses on the lifecycle operation's
	// transaction/advisory-lock concurrency.
	handler := router.Setup()

	var wg sync.WaitGroup
	type response struct {
		code int
		err  error
	}
	responses := make(chan response, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, _, _, err := authMgr.IssueAccess(f.userID, f.workspaceID, f.sessionID)
			if err != nil {
				responses <- response{err: err}
				return
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+itoa(f.accountB)+"/disconnect", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			responses <- response{code: w.Code}
		}()
	}
	wg.Wait()
	close(responses)
	for result := range responses {
		if result.err != nil {
			t.Errorf("concurrent last disconnect request setup: %v", result.err)
			continue
		}
		if result.code != http.StatusNoContent {
			t.Errorf("concurrent last disconnect: got HTTP %d, want 204", result.code)
		}
	}
	if got := revoker.revokeCalls.Load(); got != 1 {
		t.Fatalf("concurrent last disconnect: remote revoke calls=%d, want exactly 1", got)
	}
	if n := countRows(t, h.pgDB, `SELECT COUNT(*) FROM tokens WHERE oauth_connection_id=$1`, f.connID); n != 0 {
		t.Fatalf("concurrent last disconnect: grant tokens=%d, want 0", n)
	}

	// Simulate a worker restart: discard the original sql.DB and reopen a
	// fresh connection to the still-running Postgres container. No in-memory
	// retry/cache state is allowed to resurrect the disconnected channels.
	if err := h.pgDB.Close(); err != nil {
		t.Fatalf("close DB for restart simulation: %v", err)
	}
	restartedDB, err := sql.Open("pgx", h.pgURL)
	if err != nil {
		t.Fatalf("reopen DB after restart: %v", err)
	}
	defer restartedDB.Close()
	if err := restartedDB.PingContext(ctx); err != nil {
		t.Fatalf("ping DB after restart: %v", err)
	}
	var statusA, statusB, grantStatus string
	if err := restartedDB.QueryRowContext(ctx, `SELECT status FROM platform_accounts WHERE id=$1`, f.accountA).Scan(&statusA); err != nil {
		t.Fatal(err)
	}
	if err := restartedDB.QueryRowContext(ctx, `SELECT status FROM platform_accounts WHERE id=$1`, f.accountB).Scan(&statusB); err != nil {
		t.Fatal(err)
	}
	if err := restartedDB.QueryRowContext(ctx, `SELECT status FROM oauth_connections WHERE id=$1`, f.connID).Scan(&grantStatus); err != nil {
		t.Fatal(err)
	}
	if statusA != "disconnected" || statusB != "disconnected" || grantStatus != "disconnected" {
		t.Fatalf("state after restart: A=%q B=%q grant=%q; want all disconnected", statusA, statusB, grantStatus)
	}
}
