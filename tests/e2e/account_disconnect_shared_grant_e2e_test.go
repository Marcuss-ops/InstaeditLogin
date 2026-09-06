//go:build e2e

// Package e2e — shared-grant lifecycle proof (P1).
//
// TestDisconnectSharedGrant_DisconnectA_KeepsBSiblingPublishing_E2E
// exercises the P1 shared-grant contract end-to-end against a REAL
// Postgres (testcontainers) and the REAL production wiring:
//
//	real UserRepository        (DisconnectPlatformAccount + real SQL)
//	real PostRepository        (PublishPost via the /posts/{id}/publish API)
//	real CredentialVault       (encrypted token rows, real Revoke)
//	real auth.Manager          (JWT-protected routes)
//
// Scenario:
//
//	seed:  one Google grant (oauth_connection) + two YouTube channels
//	       A and B sharing it, one grant-scoped encrypted token row,
//	       group + workspace-channel memberships for BOTH, scheduled
//	       posts targeting A and B.
//	step 1:  GET  /api/v1/accounts            → both A and B valid.
//	step 2:  POST /api/v1/accounts/{A}/disconnect → 204.
//	         assert: A disconnected, B still active; A removed from
//	         group_accounts + workspace_channels; A's future post
//	         cancelled (draft); B's post untouched; token row PRESERVED
//	         (grant shared with B); NO remote revoke while sibling
//	         active; vault.GetRefreshToken(B) still decrypts.
//	step 3:  POST /api/v1/posts/{postB}/publish → B still publishes
//	         (target queued, HTTP 200) through the real API.
//	step 4:  POST /api/v1/accounts/{B}/disconnect → 204 (LAST channel).
//	         assert: remote revoke fired exactly ONCE (on the last
//	         channel), ALL token rows deleted (complete grant
//	         revocation), B disconnected; the oauth_connections audit
//	         row survives (only the permanent-delete endpoint removes
//	         it) and vault.GetRefreshToken(B) now fails.
//
// This is the database-backed companion to the sqlmock lifecycle tests
// (internal/repository/platform_account_shared_grant_test.go) and the
// handler tests (pkg/api/account_routes_shared_grant_test.go): it proves
// the three invariants against a real engine with real transactions.
package e2e

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
)

// sharedGrantRevokerService is the real production service shape with a
// recording Revoke. It embeds the package-wide YouTubeOAuthService stub
// (validate_account_e2e_test.go) so the router's WithYouTubeService
// discovery finds the narrower YouTubeRevoker capability at wiring time.
type sharedGrantRevokerService struct {
	*stubYouTubeOAuthService
	revokeCalls atomic.Int64
}

// Revoke records the remote provider revocation. The disconnect handler
// MUST invoke it exactly once, and only when the disconnected channel is
// the last active one on the grant.
func (s *sharedGrantRevokerService) Revoke(_ context.Context, _ string) error {
	s.revokeCalls.Add(1)
	return nil
}

// sharedGrantFixture carries every id the scenario needs.
type sharedGrantFixture struct {
	userID      int64
	workspaceID int64
	sessionID   int64
	connID      int64
	accountA    int64
	accountB    int64
	postA       int64
	postB       int64
}

// applySharedGrantLifecycleE2ESchemaExt extends the harness schema with
// the production columns/tables the REAL repositories touch during
// disconnect / publish:
//
//	platform_accounts  → connected_at, last_validated_at, last_refresh_at,
//	                     reauth_required_at, last_error_code,
//	                     last_error_message, metadata (JSONB)
//	group_accounts     → the disconnect transaction deletes memberships
//	workspace_channels → the disconnect transaction deletes publishable
//	                     destinations
//	post_targets       → error_message (production cancel/publish queries)
//	tokens             → UNIQUE (oauth_connection_id, token_type) backing
//	                     the production ON CONFLICT upsert
func applySharedGrantLifecycleE2ESchemaExt(t *testing.T, db *sql.DB) {
	t.Helper()
	stmts := []string{
		`ALTER TABLE platform_accounts ADD COLUMN IF NOT EXISTS connected_at TIMESTAMPTZ`,
		`ALTER TABLE platform_accounts ADD COLUMN IF NOT EXISTS last_validated_at TIMESTAMPTZ`,
		`ALTER TABLE platform_accounts ADD COLUMN IF NOT EXISTS last_refresh_at TIMESTAMPTZ`,
		`ALTER TABLE platform_accounts ADD COLUMN IF NOT EXISTS reauth_required_at TIMESTAMPTZ`,
		`ALTER TABLE platform_accounts ADD COLUMN IF NOT EXISTS last_error_code TEXT`,
		`ALTER TABLE platform_accounts ADD COLUMN IF NOT EXISTS last_error_message TEXT`,
		`ALTER TABLE platform_accounts ADD COLUMN IF NOT EXISTS metadata JSONB`,
		// groups: minimal parent table so group_accounts can keep the
		// production FK shape (migration 041).
		`CREATE TABLE IF NOT EXISTS groups (
			id           BIGSERIAL PRIMARY KEY,
			workspace_id BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
			name         TEXT NOT NULL,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS group_accounts (
			group_id    BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
			account_id  BIGINT NOT NULL REFERENCES platform_accounts(id) ON DELETE CASCADE,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (group_id, account_id)
		)`,
		`CREATE TABLE IF NOT EXISTS workspace_channels (
			workspace_id        BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
			platform_account_id BIGINT NOT NULL REFERENCES platform_accounts(id) ON DELETE CASCADE,
			group_name          TEXT,
			enabled             BOOLEAN NOT NULL DEFAULT TRUE,
			created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (workspace_id, platform_account_id)
		)`,
		`ALTER TABLE post_targets ADD COLUMN IF NOT EXISTS error_message TEXT NOT NULL DEFAULT ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_e2e_tokens_conn_type
			ON tokens (oauth_connection_id, token_type)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("applySharedGrantLifecycleE2ESchemaExt: %v\nstmt: %s", err, trimForError(s))
		}
	}
}

// seedSharedGrantE2E seeds the full shared-grant world:
// user/workspace/session, one oauth_connection (the Google grant), two
// active YouTube channels A and B sharing it, grant-scoped encrypted
// token rows via the REAL vault, group + workspace-channel memberships
// for both, and scheduled posts targeting each channel.
func seedSharedGrantE2E(t *testing.T, h *E2EHarness, vault *credentials.CredentialVault) sharedGrantFixture {
	t.Helper()
	db := h.pgDB

	var f sharedGrantFixture
	if err := db.QueryRow(`INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"sharedgrant+e2e@example.com").Scan(&f.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO workspaces (name, owner_id) VALUES ($1, $2) RETURNING id`,
		"Shared Grant E2E WS", f.userID).Scan(&f.workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO sessions (user_id, workspace_id, token_hash, expires_at)
		VALUES ($1, $2, $3, NOW() + INTERVAL '24 hours') RETURNING id`,
		f.userID, f.workspaceID, "shared-grant-token-hash-"+randomHex16()).Scan(&f.sessionID); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	// One Google grant, shared by A and B (migrations 084/085 model).
	if err := db.QueryRow(`INSERT INTO oauth_connections
		(user_id, provider, provider_subject_id, provider_resource_id, status, scopes, granted_scopes)
		VALUES ($1, 'youtube', 'e2e-google-sub-1', 'e2e-google-resource-1', 'active',
		        ARRAY['https://www.googleapis.com/auth/youtube.upload'], ARRAY['https://www.googleapis.com/auth/youtube.upload'])
		RETURNING id`,
		f.userID).Scan(&f.connID); err != nil {
		t.Fatalf("seed oauth_connection: %v", err)
	}

	seedChannel := func(name, platformUserID string) int64 {
		var id int64
		if err := db.QueryRow(`INSERT INTO platform_accounts
			(user_id, workspace_id, platform, platform_user_id, username, status, oauth_connection_id, created_at, updated_at)
			VALUES ($1, $2, 'youtube', $3, $4, 'active', $5, NOW(), NOW())
			RETURNING id`,
			f.userID, f.workspaceID, platformUserID, name, f.connID).Scan(&id); err != nil {
			t.Fatalf("seed channel %s: %v", name, err)
		}
		return id
	}
	f.accountA = seedChannel("Channel A", "UC_e2e_shared_grant_A")
	f.accountB = seedChannel("Channel B", "UC_e2e_shared_grant_B")

	// Grant-scoped encrypted token: Save(B) upserts the row Save(A)
	// created (production ON CONFLICT (oauth_connection_id, token_type)),
	// modelling the single shared grant token both channels use.
	for _, accountID := range []int64{f.accountA, f.accountB} {
		if err := vault.Save(context.Background(), accountID, &models.TokenData{
			AccessToken:  accessTokenFor(accountID, f),
			RefreshToken: "e2e-refresh-shared-grant",
			TokenType:    models.TokenTypeBearer,
			ExpiresIn:    3600,
			Scopes:       []string{"https://www.googleapis.com/auth/youtube.upload"},
		}); err != nil {
			t.Fatalf("vault.Save(account=%d): %v", accountID, err)
		}
	}

	// Group membership + publishable destination for BOTH channels.
	var groupID int64
	if err := db.QueryRow(`INSERT INTO groups (workspace_id, name) VALUES ($1, 'E2E Group') RETURNING id`,
		f.workspaceID).Scan(&groupID); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	for _, accountID := range []int64{f.accountA, f.accountB} {
		if _, err := db.Exec(`INSERT INTO group_accounts (group_id, account_id) VALUES ($1, $2)`, groupID, accountID); err != nil {
			t.Fatalf("seed group_accounts(account=%d): %v", accountID, err)
		}
		if _, err := db.Exec(`INSERT INTO workspace_channels (workspace_id, platform_account_id) VALUES ($1, $2)`,
			f.workspaceID, accountID); err != nil {
			t.Fatalf("seed workspace_channels(account=%d): %v", accountID, err)
		}
	}

	// Scheduled posts: one targeting A, one targeting B. After
	// disconnecting A, A's target must be cancelled to draft while B's
	// stays scheduled and publishable.
	seedPost := func(targetID int64) int64 {
		var postID int64
		if err := db.QueryRow(`INSERT INTO posts (user_id, workspace_id, title, caption, media_url, status, publish_at, created_at, updated_at)
			VALUES ($1, $2, 'e2e-shared-grant-post', '', 'https://example.com/video.mp4', 'scheduled', NOW() + INTERVAL '1 hour', NOW(), NOW())
			RETURNING id`,
			f.userID, f.workspaceID).Scan(&postID); err != nil {
			t.Fatalf("seed post: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO post_targets (post_id, platform_account_id, status, created_at, updated_at)
			VALUES ($1, $2, 'scheduled', NOW(), NOW())`, postID, targetID); err != nil {
			t.Fatalf("seed post_target(account=%d): %v", targetID, err)
		}
		return postID
	}
	f.postA = seedPost(f.accountA)
	f.postB = seedPost(f.accountB)

	t.Logf("seeded shared grant: user=%d ws=%d session=%d conn=%d A=%d B=%d postA=%d postB=%d",
		f.userID, f.workspaceID, f.sessionID, f.connID, f.accountA, f.accountB, f.postA, f.postB)
	return f
}

func accessTokenFor(accountID int64, f sharedGrantFixture) string {
	if accountID == f.accountA {
		return "e2e-access-A"
	}
	return "e2e-access-B"
}

// sharedGrantAuthedRequest runs a JWT-protected request through the real
// router with the same auth pattern the validate E2E harness uses.
func sharedGrantAuthedRequest(t *testing.T, router *api.Router, authMgr *auth.Manager, f sharedGrantFixture, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	token, _, _, err := authMgr.IssueAccess(f.userID, f.workspaceID, f.sessionID)
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.Setup().ServeHTTP(w, req)
	return w
}

// countRows is a tiny query helper for SQL assertions.
func countRows(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("countRows %q: %v", query, err)
	}
	return n
}

func TestDisconnectSharedGrant_DisconnectA_KeepsBSiblingPublishing_E2E(t *testing.T) {
	h := NewE2EHarness(t)
	if h == nil || h.pgDB == nil {
		t.Skip("testcontainers Postgres unavailable in this sandbox (Docker not reachable)")
	}
	defer h.Close()

	applySharedGrantLifecycleE2ESchemaExt(t, h.pgDB)

	// Real vault: encrypts and persists token rows exactly like production.
	encKey := make([]byte, 32)
	if _, err := rand.Read(encKey); err != nil {
		t.Fatalf("rand.Read(encryption key): %v", err)
	}
	encKeyB64 := base64.StdEncoding.EncodeToString(encKey)
	tokenRepo := repository.NewTokenRepository(h.pgDB)
	encryptor, err := crypto.NewEncryptor(1, map[uint32]string{1: encKeyB64})
	if err != nil {
		t.Fatalf("crypto.NewEncryptor: %v", err)
	}
	vault := credentials.NewCredentialVault(encryptor, h.pgDB, tokenRepo)

	f := seedSharedGrantE2E(t, h, vault)

	// Real repositories + real auth manager + recording provider revoker.
	userRepo := repository.NewUserRepository(h.pgDB)
	postRepo := repository.NewPostRepository(h.pgDB)
	authMgr := auth.NewManager(testJWTSecret, 15*time.Minute)
	revokerSvc := &sharedGrantRevokerService{stubYouTubeOAuthService: &stubYouTubeOAuthService{}}

	capRouter := services.NewCapabilityRouter()
	// handlePublishPostID resolves Post → Workspace ownership through the
	// workspace store; without it the publish endpoint 501s ("workspaces
	// not configured"), which is the correct fail-closed behavior for a
	// partial deployment but wrong for this full-lifecycle fixture.
	workspaceRepo := repository.NewWorkspaceRepository(h.pgDB)
	router := buildE2ERouter(
		capRouter, userRepo, authMgr,
		api.WithCredentialVault(vault),
		api.WithPostStore(postRepo),
		api.WithWorkspaceStore(workspaceRepo),
		api.WithYouTubeService(revokerSvc),
	)

	// ── Step 1: both channels valid and publishable ─────────────────
	w := sharedGrantAuthedRequest(t, router, authMgr, f, http.MethodGet, "/api/v1/accounts")
	if w.Code != http.StatusOK {
		t.Fatalf("step1 GET /accounts: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var listResp struct {
		Accounts []struct {
			ID            int64  `json:"id"`
			AccountState  string `json:"account_state"`
			IsPublishable bool   `json:"is_publishable"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("step1 decode /accounts: %v", err)
	}
	seen := map[int64]string{}
	seenPublishable := map[int64]bool{}
	for _, a := range listResp.Accounts {
		seen[a.ID] = a.AccountState
		seenPublishable[a.ID] = a.IsPublishable
	}
	if seen[f.accountA] != "valid" || seen[f.accountB] != "valid" {
		t.Fatalf("step1: both channels must be valid, got %v (A=%d B=%d)", seen, f.accountA, f.accountB)
	}
	if !seenPublishable[f.accountA] || !seenPublishable[f.accountB] {
		t.Errorf("step1: both channels must be publishable, got %v", seenPublishable)
	}

	// ── Step 2: disconnect A (sibling B still active) ───────────────
	w = sharedGrantAuthedRequest(t, router, authMgr, f, http.MethodPost,
		"/api/v1/accounts/"+itoa(f.accountA)+"/disconnect")
	if w.Code != http.StatusNoContent {
		t.Fatalf("step2 disconnect A: want 204, got %d body=%s", w.Code, w.Body.String())
	}

	assertStatus := func(accountID int64, want string) {
		t.Helper()
		var got string
		if err := h.pgDB.QueryRow(`SELECT status FROM platform_accounts WHERE id=$1`, accountID).Scan(&got); err != nil {
			t.Fatalf("read status(account=%d): %v", accountID, err)
		}
		if got != want {
			t.Errorf("status(account=%d): want %q, got %q", accountID, want, got)
		}
	}
	assertStatus(f.accountA, "disconnected")
	assertStatus(f.accountB, "active")

	// A removed from groups + publishable destinations; B untouched.
	if n := countRows(t, h.pgDB, `SELECT COUNT(*) FROM group_accounts WHERE account_id=$1`, f.accountA); n != 0 {
		t.Errorf("after disconnect A: A group memberships = %d, want 0", n)
	}
	if n := countRows(t, h.pgDB, `SELECT COUNT(*) FROM group_accounts WHERE account_id=$1`, f.accountB); n != 1 {
		t.Errorf("after disconnect A: B group memberships = %d, want 1", n)
	}
	if n := countRows(t, h.pgDB, `SELECT COUNT(*) FROM workspace_channels WHERE platform_account_id=$1`, f.accountA); n != 0 {
		t.Errorf("after disconnect A: A workspace_channels = %d, want 0", n)
	}
	if n := countRows(t, h.pgDB, `SELECT COUNT(*) FROM workspace_channels WHERE platform_account_id=$1`, f.accountB); n != 1 {
		t.Errorf("after disconnect A: B workspace_channels = %d, want 1", n)
	}

	// A's future job cancelled to draft; B's job still scheduled.
	var statusA, statusB string
	if err := h.pgDB.QueryRow(`SELECT status FROM post_targets WHERE platform_account_id=$1`, f.accountA).Scan(&statusA); err != nil {
		t.Fatalf("read post_target A: %v", err)
	}
	if err := h.pgDB.QueryRow(`SELECT status FROM post_targets WHERE platform_account_id=$1`, f.accountB).Scan(&statusB); err != nil {
		t.Fatalf("read post_target B: %v", err)
	}
	if statusA != "draft" {
		t.Errorf("after disconnect A: A post_target status = %q, want draft (future jobs cancelled)", statusA)
	}
	if statusB != "scheduled" {
		t.Errorf("after disconnect A: B post_target status = %q, want scheduled (sibling untouched)", statusB)
	}

	// Shared grant token PRESERVED while a sibling is active.
	if n := countRows(t, h.pgDB, `SELECT COUNT(*) FROM tokens WHERE oauth_connection_id=$1`, f.connID); n != 1 {
		t.Errorf("after disconnect A: token rows for grant = %d, want 1 (grant preserved for B)", n)
	}
	// No remote revocation while a sibling remains active.
	if got := revokerSvc.revokeCalls.Load(); got != 0 {
		t.Errorf("after disconnect A: remote revoke calls = %d, want 0 (sibling B still on grant)", got)
	}
	// B's credential still decrypts → B can still publish.
	if rt, err := vault.GetRefreshToken(context.Background(), f.accountB); err != nil || rt == "" {
		t.Errorf("after disconnect A: GetRefreshToken(B) = %q, err=%v; want a decryptable shared-grant token", rt, err)
	}

	// API surface after disconnect A: B stays visible + publishable, A is
	// hidden (disconnected → account_state deleted, filtered by default).
	w = sharedGrantAuthedRequest(t, router, authMgr, f, http.MethodGet, "/api/v1/accounts")
	if w.Code != http.StatusOK {
		t.Fatalf("after disconnect A: GET /accounts: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	listResp = struct {
		Accounts []struct {
			ID            int64  `json:"id"`
			AccountState  string `json:"account_state"`
			IsPublishable bool   `json:"is_publishable"`
		} `json:"accounts"`
	}{}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("after disconnect A: decode /accounts: %v", err)
	}
	seenAfter := map[int64]string{}
	for _, a := range listResp.Accounts {
		seenAfter[a.ID] = a.AccountState
	}
	if _, ok := seenAfter[f.accountA]; ok {
		t.Errorf("after disconnect A: A must be hidden from /accounts (account_state deleted), got state=%q", seenAfter[f.accountA])
	}
	if seenAfter[f.accountB] != "valid" {
		t.Errorf("after disconnect A: B must stay valid in /accounts, got %q", seenAfter[f.accountB])
	}
	for _, a := range listResp.Accounts {
		if a.ID == f.accountB && !a.IsPublishable {
			t.Errorf("after disconnect A: B must remain publishable in /accounts")
		}
	}

	// ── Step 3: B still publishes through the real API ──────────────
	w = sharedGrantAuthedRequest(t, router, authMgr, f, http.MethodPost,
		"/api/v1/posts/"+itoa(f.postB)+"/publish")
	if w.Code != http.StatusOK {
		t.Fatalf("step3 publish postB: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if err := h.pgDB.QueryRow(`SELECT status FROM post_targets WHERE platform_account_id=$1`, f.accountB).Scan(&statusB); err != nil {
		t.Fatalf("read post_target B after publish: %v", err)
	}
	if statusB != "queued" {
		t.Errorf("after publish: B post_target status = %q, want queued (publish pool can still claim B)", statusB)
	}
	// The parent post aggregate is recomputed from the queued target.
	var postBStatus string
	if err := h.pgDB.QueryRow(`SELECT status FROM posts WHERE id=$1`, f.postB).Scan(&postBStatus); err != nil {
		t.Fatalf("read posts.status(postB): %v", err)
	}
	if postBStatus != "queued" {
		t.Errorf("after publish: postB aggregate status = %q, want queued (resolver recomputed from B target)", postBStatus)
	}

	// ── Step 4: disconnect B (LAST channel on the grant) ────────────
	w = sharedGrantAuthedRequest(t, router, authMgr, f, http.MethodPost,
		"/api/v1/accounts/"+itoa(f.accountB)+"/disconnect")
	if w.Code != http.StatusNoContent {
		t.Fatalf("step4 disconnect B: want 204, got %d body=%s", w.Code, w.Body.String())
	}
	assertStatus(f.accountB, "disconnected")

	// Complete grant revocation: remote revoke fired EXACTLY once, on
	// the last channel, and every token row for the grant is gone.
	if got := revokerSvc.revokeCalls.Load(); got != 1 {
		t.Errorf("after disconnect B (last channel): remote revoke calls = %d, want 1", got)
	}
	if n := countRows(t, h.pgDB, `SELECT COUNT(*) FROM tokens WHERE oauth_connection_id=$1`, f.connID); n != 0 {
		t.Errorf("after disconnect B: token rows for grant = %d, want 0 (complete revocation)", n)
	}
	if _, err := vault.GetRefreshToken(context.Background(), f.accountB); err == nil {
		t.Errorf("after disconnect B: GetRefreshToken(B) must fail (grant fully revoked), got nil error")
	}

	// Soft-disconnect semantics: the oauth_connections audit row and the
	// platform_accounts rows survive (the permanent-delete endpoint
	// DELETE /accounts/{id}/data removes them instead).
	if n := countRows(t, h.pgDB, `SELECT COUNT(*) FROM oauth_connections WHERE id=$1`, f.connID); n != 1 {
		t.Errorf("after disconnect B: oauth_connections row count = %d, want 1 (kept for audit)", n)
	}

	t.Logf("SHARED GRANT E2E PASS — A=%d B=%d conn=%d: A disconnected first (grant preserved, B still published), B last (revokeCalls=1, tokens=0)",
		f.accountA, f.accountB, f.connID)
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
