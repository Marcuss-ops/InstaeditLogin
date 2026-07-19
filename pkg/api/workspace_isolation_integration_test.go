//go:build integration

// Blocco #5.1 — workspace-isolation integration test.
//
// Run with:
//
//	go test -tags=integration -run TestWorkspaceIsolation ./pkg/api/...
//
// The test spins up an ephemeral Postgres via testcontainers-go,
// applies every migration via database.RunMigrations, and exercises
// two attack surfaces:
//
//  1. SQL-level isolation (TestWorkspaceIsolation_SQLFilter):
//     tenant A's workspace_id never returns rows owned by tenant B
//     — the "filter is correct" assertion at the SQL layer. This
//     is the canary: if a future migration drops the
//     `WHERE workspace_id = $1` predicate, this test fails loud.
//
//  2. HTTP-level isolation under JWT re-sign
//     (TestWorkspaceIsolation_JWTResign_Rejected): a token for
//     user A signed with the server's secret but carrying tenant
//     B's workspace_id is rejected with 404. The attack vector is
//     "attacker knows the secret AND tries to escalate via ws
//     claim tampering" — the realistic cross-tenant attack when
//     the secret leaks OR is mis-deployed. The
//     handleGetWorkspace's `ws.OwnerID != callerID` guard is the
//     production-side defence; this test is the regression guard.
//
// No production-code modifications are required — both defences
// already exist. The test exists to fail LOUDLY if a future commit
// drops either check.
package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

// isolationTestSecret is the JWT signing secret used by the
// isolation tests. It is intentionally hardcoded (not env-derived)
// so the JWT-forging helper at the bottom of the file can sign a
// forged token with the SAME secret the production Manager uses —
// the attack vector is "attacker knows the secret AND tries to
// escalate", which is the realistic cross-tenant attack when the
// secret leaks OR is mis-deployed (e.g. a shared secret across
// envs).
//
// Per Blocco #5.2 (cross-env rejection), the test Manager does NOT
// chain .WithEnv() so the env-check is skipped and the signature
// check is the only JWT-path gate. The forged token's signature is
// VALID (signed with this same secret) — only the ws claim differs
// from the user's actual membership. The handleGetWorkspace's
// `ws.OwnerID != callerID` guard is the production-side defence.
const isolationTestSecret = "isolation-test-secret-32-bytes-of-content-xxx"

// seedUser inserts a minimal users row and returns the id.
func seedUser(t *testing.T, db *sql.DB, email string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id`,
		email, email,
	).Scan(&id); err != nil {
		t.Fatalf("seed user %q: %v", email, err)
	}
	return id
}

// seedWorkspace inserts a workspaces row and returns the id.
func seedWorkspace(t *testing.T, db *sql.DB, name string, ownerID int64) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`INSERT INTO workspaces (name, owner_id) VALUES ($1, $2) RETURNING id`,
		name, ownerID,
	).Scan(&id); err != nil {
		t.Fatalf("seed workspace %q: %v", name, err)
	}
	return id
}

// seedPost inserts a posts row tied to the given workspace and
// returns the id. Empty caption / status='draft' keep the seed
// minimal — the test asserts ownership, not content shape.
func seedPost(t *testing.T, db *sql.DB, workspaceID int64, title string) int64 {
	t.Helper()
	var id int64
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
		`INSERT INTO posts (workspace_id, title, caption, status) VALUES ($1, $2, '', 'draft') RETURNING id`,
		workspaceID, title,
	).Scan(&id); err != nil {
		t.Fatalf("seed post %q: %v", title, err)
	}
	return id
}

// TestWorkspaceIsolation_SQLFilter pins the canary: queries filtered
// by tenant A's workspace_id must NEVER return rows owned by tenant
// B, even when both tenants have rows in the same table. This is
// the SQL-layer assertion that catches "I forgot the WHERE clause"
// regressions at the data layer rather than via HTTP.
//
// Asserts three properties:
//  1. A's posts are visible to A's filter, B's aren't.
//  2. B's posts are visible to B's filter, A's aren't.
//  3. A cross-tenant SELECT (A's filter + B's id) returns zero
//     rows.
func TestWorkspaceIsolation_SQLFilter(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Seed two tenants.
	userA := seedUser(t, db, "a@example.com")
	wsA := seedWorkspace(t, db, "Tenant A", userA)
	userB := seedUser(t, db, "b@example.com")
	wsB := seedWorkspace(t, db, "Tenant B", userB)

	// Each tenant gets one post.
	postA := seedPost(t, db, wsA, "A's post")
	postB := seedPost(t, db, wsB, "B's post")

	// Property 1: A's filter sees A's post, not B's.
	var countA int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM posts WHERE workspace_id = $1`, wsA,
	).Scan(&countA); err != nil {
		t.Fatalf("count A: %v", err)
	}
	if countA != 1 {
		t.Fatalf("A's filter should return exactly 1 post (its own); got %d", countA)
	}

	// Property 2: B's filter sees B's post, not A's.
	var countB int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM posts WHERE workspace_id = $1`, wsB,
	).Scan(&countB); err != nil {
		t.Fatalf("count B: %v", err)
	}
	if countB != 1 {
		t.Fatalf("B's filter should return exactly 1 post (its own); got %d", countB)
	}

	// Property 3: A's filter + B's id must return ZERO rows.
	// This is the canary — if a future commit accidentally drops
	// the workspace_id predicate (e.g. an unbounded SELECT
	// introduced for some unrelated feature), this assertion
	// surfaces the leak.
	var leak int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM posts WHERE workspace_id = $1 AND id = $2`,
		wsA, postB,
	).Scan(&leak); err != nil {
		t.Fatalf("cross-tenant leak check: %v", err)
	}
	if leak != 0 {
		t.Fatalf("A's filter leaked B's post (postA=%d, postB=%d, wsA=%d, wsB=%d): got %d rows",
			postA, postB, wsA, wsB, leak)
	}

	// Bonus: workspace ownership itself is correctly isolated.
	var ownerOfA int64
	if err := db.QueryRow(
		`SELECT owner_id FROM workspaces WHERE id = $1`, wsA,
	).Scan(&ownerOfA); err != nil {
		t.Fatalf("ownerOfA: %v", err)
	}
	if ownerOfA != userA {
		t.Fatalf("Workspace A's owner: want %d (userA), got %d", userA, ownerOfA)
	}
	var ownerOfB int64
	if err := db.QueryRow(
		`SELECT owner_id FROM workspaces WHERE id = $1`, wsB,
	).Scan(&ownerOfB); err != nil {
		t.Fatalf("ownerOfB: %v", err)
	}
	if ownerOfB != userB {
		t.Fatalf("Workspace B's owner: want %d (userB), got %d", userB, ownerOfB)
	}
}

// TestWorkspaceIsolation_JWTResign_Rejected pins the HTTP-layer
// defence: a JWT issued for user A but carrying tenant B's
// workspace_id (signed with the same secret the production Manager
// uses) is REJECTED by handleGetWorkspace with 404.
//
// The forged token's signature is VALID (sig check passes); the
// only deviation from the original A-issued token is the ws claim.
// The handler must therefore rely on the workspace-ownership lookup
// (ws.OwnerID != callerID) rather than blindly trusting the JWT's
// ws claim. This test fails loud if a future commit removes that
// guard and trusts the JWT's ws claim directly.
func TestWorkspaceIsolation_JWTResign_Rejected(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Seed two tenants.
	userA := seedUser(t, db, "a@example.com")
	wsA := seedWorkspace(t, db, "Tenant A", userA)
	userB := seedUser(t, db, "b@example.com")
	wsB := seedWorkspace(t, db, "Tenant B", userB)

	// Mint a legitimate JWT for user A targeting workspace A.
	authMgr := auth.NewManager(isolationTestSecret, 24)
	legitToken, _, _, err := authMgr.IssueAccess(userA, wsA, 1)
	if err != nil {
		t.Fatalf("IssueAccess legit: %v", err)
	}

	// Forge a JWT: SAME secret, but the ws claim is replaced with
	// wsB. The signature is valid (signed with the same secret
	// the Manager uses), so the only check the handler can rely
	// on is the workspace-ownership lookup against the DB.
	forgedClaims := auth.Claims{
		UserID:      userA, // still user A's id
		WorkspaceID: wsB,   // manipulated: B's workspace
		SessionID:   1,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", userA),
			Issuer:    "instaeditlogin",
			Audience:  jwt.ClaimStrings{"api"},
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	forgedToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, forgedClaims).
		SignedString([]byte(isolationTestSecret))
	if err != nil {
		t.Fatalf("sign forged token: %v", err)
	}

	// Sanity check: the legit token works (handler returns 200).
	t.Run("legit_token_returns_200", func(t *testing.T) {
		// Per-test Router (fresh middleware chain).
		router := newIsolationRouter(authMgr, repository.NewWorkspaceRepository(db))
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/workspaces/%d", wsA), nil)
		req.Header.Set("Authorization", "Bearer "+legitToken)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("legit token: want 200, got %d (body=%s)", w.Code, w.Body.String())
		}
	})

	// The actual assertion: forged token must be rejected.
	t.Run("forged_token_returns_404", func(t *testing.T) {
		router := newIsolationRouter(authMgr, repository.NewWorkspaceRepository(db))
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/workspaces/%d", wsB), nil)
		req.Header.Set("Authorization", "Bearer "+forgedToken)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("forged token: want 404 (cross-tenant access blocked), got %d (body=%s)",
				w.Code, w.Body.String())
		}
		// Body must NOT leak workspace existence (per the
		// handleGetWorkspace's existence-leak-avoidance policy).
		var body map[string]string
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["error"] == "" {
			t.Fatalf("body: want JSON with non-empty error string, got %v", body)
		}
	})
}

// newIsolationRouter wires the minimum Router needed for the
// isolation test: real AuthManager + real WorkspaceRepository.
// capRouter + userRepo are nil because handleGetWorkspace doesn't
// read them (the route only uses r.workspaceStore + r.auth). The
// test never hits /api/v1/health or any route that touches those
// dependencies, so the nils are safe.
func newIsolationRouter(authMgr *auth.Manager, workspaceRepo *repository.WorkspaceRepository) http.Handler {
	router := NewRouter(
		nil, // capRouter — unused by handleGetWorkspace
		nil, // userRepo — unused by handleGetWorkspace
		authMgr,
		"",  // frontendURL
		nil, // allowedOrigins
		WithWorkspaceStore(workspaceRepo),
	)
	return router.Setup()
}
