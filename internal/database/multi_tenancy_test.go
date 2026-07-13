//go:build integration

// Package database contains integration tests for the multi-tenancy
// migration (028_multi_tenancy.sql).
//
// Run with: go test -tags=integration -run 'TestMultiTenancy' ./internal/database/...
package database

import (
	"database/sql"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

// -----------------------------------------------------------------------
// Multi-tenancy migration tests — FASE 2.1
// -----------------------------------------------------------------------

// insertMTUser creates a user row (SaaS or OAuth-style) and returns the id.
func insertMTUser(t *testing.T, db *sql.DB, email, name string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id`,
		email, name,
	).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

// insertMTWorkspace creates a workspace and returns the id.
func insertMTWorkspace(t *testing.T, db *sql.DB, name string, ownerID int64) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`INSERT INTO workspaces (name, owner_id) VALUES ($1, $2) RETURNING id`,
		name, ownerID,
	).Scan(&id); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	return id
}

// countTable returns the number of rows in a table.
func countTable(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

// tableExists checks if a table exists.
func tableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var exists bool
	if err := db.QueryRow(
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1
		)`, table,
	).Scan(&exists); err != nil {
		t.Fatalf("tableExists %s: %v", table, err)
	}
	return exists
}
func colExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var exists bool
	if err := db.QueryRow(
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = $1 AND column_name = $2
		)`, table, column,
	).Scan(&exists); err != nil {
		t.Fatalf("colExists %s.%s: %v", table, column, err)
	}
	return exists
}

// ────────────────────────────────────────────────────────────────────
//  Tests
// ────────────────────────────────────────────────────────────────────

// TestMultiTenancy_AddsSaaSUsersColumns verifies that the migration adds
// password_hash and email_verified columns to the users table.
func TestMultiTenancy_AddsSaaSUsersColumns(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	if !colExists(t, db, "users", "password_hash") {
		t.Error("users.password_hash column missing after migration")
	}
	if !colExists(t, db, "users", "email_verified") {
		t.Error("users.email_verified column missing after migration")
	}

	// Insert a user — email_verified defaults to FALSE.
	uid := insertMTUser(t, db, "saas@example.com", "SaaS User")
	var verified bool
	db.QueryRow(`SELECT email_verified FROM users WHERE id = $1`, uid).Scan(&verified)
	if verified {
		t.Error("new user email_verified should default to FALSE")
	}
}

// TestMultiTenancy_CreatesWorkspaceMembers verifies that the
// workspace_members table and role enum exist, and members can be
// inserted with valid roles.
func TestMultiTenancy_CreatesWorkspaceMembers(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	uid := insertMTUser(t, db, "admin@test.com", "Admin")
	wsID := insertMTWorkspace(t, db, "Test Workspace", uid)

	// Insert member as editor.
	var memID int64
	if err := db.QueryRow(
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ($1, $2, 'editor') RETURNING id`,
		wsID, uid,
	).Scan(&memID); err != nil {
		t.Fatalf("insert workspace_member (editor): %v", err)
	}

	// Read back role.
	var role string
	db.QueryRow(`SELECT role FROM workspace_members WHERE id = $1`, memID).Scan(&role)
	if role != "editor" {
		t.Errorf("role: want editor, got %s", role)
	}

	// Verify UNIQUE constraint: same (workspace_id, user_id) fails.
	if _, err := db.Exec(
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ($1, $2, 'viewer')`, wsID, uid,
	); err == nil {
		t.Error("expected UNIQUE violation on duplicate (workspace_id, user_id)")
	}
}

// TestMultiTenancy_CreatesWorkspaceInvites verifies that the
// workspace_invites table exists with token uniqueness.
func TestMultiTenancy_CreatesWorkspaceInvites(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	uid := insertMTUser(t, db, "owner@test.com", "Owner")
	wsID := insertMTWorkspace(t, db, "Invite WS", uid)

	// Insert an invite.
	var invID int64
	if err := db.QueryRow(
		`INSERT INTO workspace_invites (workspace_id, email, role, token, invited_by, expires_at)
		 VALUES ($1, $2, 'admin', 'tok_abc123', $3, NOW() + INTERVAL '7 days')
		 RETURNING id`,
		wsID, "invitee@test.com", uid,
	).Scan(&invID); err != nil {
		t.Fatalf("insert workspace_invite: %v", err)
	}
	if invID <= 0 {
		t.Fatal("invite id should be positive")
	}

	// Token uniqueness: duplicate token fails.
	if _, err := db.Exec(
		`INSERT INTO workspace_invites (workspace_id, email, role, token, invited_by, expires_at)
		 VALUES ($1, $2, 'editor', 'tok_abc123', $3, NOW() + INTERVAL '7 days')`,
		wsID, "another@test.com", uid,
	); err == nil {
		t.Error("expected UNIQUE violation on duplicate token")
	}
}

// TestMultiTenancy_AddsWorkspaceIDToPlatformAccounts verifies that the
// workspace_id FK column was added to platform_accounts.
func TestMultiTenancy_AddsWorkspaceIDToPlatformAccounts(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	if !colExists(t, db, "platform_accounts", "workspace_id") {
		t.Error("platform_accounts.workspace_id column missing")
	}

	// Insert an account with workspace_id set.
	uid := insertMTUser(t, db, "pa_user@test.com", "PA User")
	wsID := insertMTWorkspace(t, db, "PA WS", uid)

	var accID int64
	if err := db.QueryRow(
		`INSERT INTO platform_accounts (user_id, platform, platform_user_id, username, workspace_id)
		 VALUES ($1, 'instagram', 'ig_ws_123', 'ig_ws_user', $2)
		 RETURNING id`,
		uid, wsID,
	).Scan(&accID); err != nil {
		t.Fatalf("insert platform_account with workspace_id: %v", err)
	}

	// Read back workspace_id.
	var gotWSID sql.NullInt64
	db.QueryRow(
		`SELECT workspace_id FROM platform_accounts WHERE id = $1`, accID,
	).Scan(&gotWSID)
	if !gotWSID.Valid || gotWSID.Int64 != wsID {
		t.Errorf("workspace_id: want %d, got %v", wsID, gotWSID)
	}
}

// TestMultiTenancy_BackfillsExistingOwnersAsAdmin verifies that the
// backfill INSERT adds workspace owners as admin members.
func TestMultiTenancy_BackfillsExistingOwnersAsAdmin(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	// Run all migrations EXCEPT 028, then insert data, then apply 028.
	if err := RunMigrationsUpTo(db, 27); err != nil {
		t.Fatalf("RunMigrationsUpTo(27): %v", err)
	}

	uid := insertMTUser(t, db, "owner_backfill@test.com", "Backfill Owner")
	wsID := insertMTWorkspace(t, db, "Backfill WS", uid)

	// Pre-028: workspace_members table does not exist yet.
	if tableExists(t, db, "workspace_members") {
		t.Fatal("workspace_members should not exist before migration 028")
	}

	// Apply 028 — should backfill the owner.
	if err := applyMigrationByName(t, db, "028_multi_tenancy.sql"); err != nil {
		t.Fatalf("apply 028: %v", err)
	}

	// Owner is now an admin member.
	if got := countTable(t, db, "workspace_members"); got != 1 {
		t.Fatalf("workspace_members after backfill: want 1, got %d", got)
	}

	var role string
	db.QueryRow(
		`SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
		wsID, uid,
	).Scan(&role)
	if role != "admin" {
		t.Errorf("backfilled role: want admin, got %s", role)
	}
}

// TestMultiTenancy_PreservesExistingData verifies that existing users,
// accounts, and tokens are untouched by the migration.
func TestMultiTenancy_PreservesExistingData(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 27); err != nil {
		t.Fatalf("RunMigrationsUpTo(27): %v", err)
	}

	uid := insertMTUser(t, db, "preserve@test.com", "Preserve User")
	wsID := insertMTWorkspace(t, db, "Preserve WS", uid)

	var accID int64
	db.QueryRow(
		`INSERT INTO platform_accounts (user_id, platform, platform_user_id, username)
		 VALUES ($1, 'tiktok', 'tt_preserve_123', 'tt_preserve_user')
		 RETURNING id`,
		uid,
	).Scan(&accID)

	// Apply 028.
	if err := applyMigrationByName(t, db, "028_multi_tenancy.sql"); err != nil {
		t.Fatalf("apply 028: %v", err)
	}

	// User preserved.
	var name, email string
	db.QueryRow(`SELECT name, email FROM users WHERE id = $1`, uid).Scan(&name, &email)
	if name != "Preserve User" || email != "preserve@test.com" {
		t.Errorf("user preserved: want (Preserve User, preserve@test.com), got (%s, %s)", name, email)
	}

	// Account preserved (workspace_id is NULL, not overwritten).
	var platform, puid string
	var gotWSID sql.NullInt64
	db.QueryRow(
		`SELECT platform, platform_user_id, workspace_id FROM platform_accounts WHERE id = $1`,
		accID,
	).Scan(&platform, &puid, &gotWSID)
	if platform != "tiktok" {
		t.Errorf("platform: want tiktok, got %s", platform)
	}
	if puid != "tt_preserve_123" {
		t.Errorf("platform_user_id: want tt_preserve_123, got %s", puid)
	}
	if gotWSID.Valid {
		t.Errorf("workspace_id: want NULL (not backfilled), got %d", gotWSID.Int64)
	}

	// Workspace preserved.
	var wsName string
	var wsOwnerID int64
	db.QueryRow(`SELECT name, owner_id FROM workspaces WHERE id = $1`, wsID).Scan(&wsName, &wsOwnerID)
	if wsName != "Preserve WS" || wsOwnerID != uid {
		t.Errorf("workspace preserved: want (Preserve WS, %d), got (%s, %d)", uid, wsName, wsOwnerID)
	}
}

// TestMultiTenancy_CanRunTwiceSafely verifies that applying the
// migration a second time is idempotent.
func TestMultiTenancy_CanRunTwiceSafely(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	uid := insertMTUser(t, db, "idem@test.com", "Idem User")
	_ = insertMTWorkspace(t, db, "Idem WS", uid)

	// Apply 028 again.
	if err := applyMigrationByName(t, db, "028_multi_tenancy.sql"); err != nil {
		t.Fatalf("second apply 028: %v", err)
	}

	// Members table: only 1 row (backfill didn't duplicate).
	if got := countTable(t, db, "workspace_members"); got != 1 {
		t.Errorf("workspace_members after 2nd run: want 1, got %d", got)
	}

	// Columns still exist (ADD COLUMN IF NOT EXISTS is idempotent).
	if !colExists(t, db, "users", "password_hash") {
		t.Error("password_hash still exists after 2nd run")
	}
}

// TestMultiTenancy_InviteTokenUniqueness verifies token uniqueness
// across the workspace_invites table.
func TestMultiTenancy_InviteTokenUniqueness(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	uid := insertMTUser(t, db, "inviter@test.com", "Inviter")
	wsID := insertMTWorkspace(t, db, "Token WS", uid)

	// First invite.
	if _, err := db.Exec(
		`INSERT INTO workspace_invites (workspace_id, email, role, token, invited_by, expires_at)
		 VALUES ($1, $2, 'editor', 'unique_token_1', $3, NOW() + INTERVAL '7 days')`,
		wsID, "a@test.com", uid,
	); err != nil {
		t.Fatalf("insert first invite: %v", err)
	}

	// Second invite with same token → must fail.
	if _, err := db.Exec(
		`INSERT INTO workspace_invites (workspace_id, email, role, token, invited_by, expires_at)
		 VALUES ($1, $2, 'viewer', 'unique_token_1', $3, NOW() + INTERVAL '7 days')`,
		wsID, "b@test.com", uid,
	); err == nil {
		t.Error("expected UNIQUE violation on duplicate token")
	}
}

// TestMultiTenancy_PendingInvitePartialUnique verifies that the partial
// unique index on workspace_invites rejects a second pending invite
// (accepted_at IS NULL) for the same (workspace_id, email).
func TestMultiTenancy_PendingInvitePartialUnique(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	uid := insertMTUser(t, db, "partial@test.com", "Partial User")
	wsID := insertMTWorkspace(t, db, "Partial WS", uid)

	// First pending invite.
	if _, err := db.Exec(
		`INSERT INTO workspace_invites (workspace_id, email, role, token, invited_by, expires_at)
		 VALUES ($1, $2, 'editor', 'partial_tok_1', $3, NOW() + INTERVAL '7 days')`,
		wsID, "same@test.com", uid,
	); err != nil {
		t.Fatalf("insert first pending invite: %v", err)
	}

	// Second pending invite for same (workspace, email) → must fail.
	if _, err := db.Exec(
		`INSERT INTO workspace_invites (workspace_id, email, role, token, invited_by, expires_at)
		 VALUES ($1, $2, 'viewer', 'partial_tok_2', $3, NOW() + INTERVAL '7 days')`,
		wsID, "same@test.com", uid,
	); err == nil {
		t.Error("expected partial UNIQUE violation: two pending invites for same (workspace_id, email)")
	}

	// Accept the first invite, then re-invite: should succeed.
	db.Exec(`UPDATE workspace_invites SET accepted_at = NOW() WHERE token = 'partial_tok_1'`)

	if _, err := db.Exec(
		`INSERT INTO workspace_invites (workspace_id, email, role, token, invited_by, expires_at)
		 VALUES ($1, $2, 'viewer', 'partial_tok_3', $3, NOW() + INTERVAL '7 days')`,
		wsID, "same@test.com", uid,
	); err != nil {
		t.Fatalf("re-invite after acceptance should succeed: %v", err)
	}
}

// user_oauth_profiles table exists, enforces UNIQUE(user_id, platform),
// and has been backfilled from platform_accounts.
func TestMultiTenancy_CreatesUserOAuthProfiles(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	// Run all migrations EXCEPT 028, seed data, then apply 028.
	if err := RunMigrationsUpTo(db, 27); err != nil {
		t.Fatalf("RunMigrationsUpTo(27): %v", err)
	}

	uid := insertMTUser(t, db, "oauth@test.com", "OAuth User")

	// Insert a platform_account (as happens during real OAuth flow).
	if _, err := db.Exec(
		`INSERT INTO platform_accounts (user_id, platform, platform_user_id, username)
		 VALUES ($1, 'instagram', 'ig_oauth_456', 'ig_oauth_user')`,
		uid,
	); err != nil {
		t.Fatalf("insert platform_account: %v", err)
	}

	// Pre-028: user_oauth_profiles table does not exist.
	if colExists(t, db, "user_oauth_profiles", "id") {
		t.Fatal("user_oauth_profiles should not exist before migration 028")
	}

	// Apply 028.
	if err := applyMigrationByName(t, db, "028_multi_tenancy.sql"); err != nil {
		t.Fatalf("apply 028: %v", err)
	}

	// Table exists with columns.
	if !colExists(t, db, "user_oauth_profiles", "user_id") {
		t.Error("user_oauth_profiles.user_id column missing")
	}
	if !colExists(t, db, "user_oauth_profiles", "platform") {
		t.Error("user_oauth_profiles.platform column missing")
	}
	if !colExists(t, db, "user_oauth_profiles", "platform_user_id") {
		t.Error("user_oauth_profiles.platform_user_id column missing")
	}

	// Backfill: existing platform_account row → user_oauth_profiles row.
	var username string
	db.QueryRow(
		`SELECT username FROM user_oauth_profiles
		 WHERE user_id = $1 AND platform = 'instagram'`,
		uid,
	).Scan(&username)
	if username != "ig_oauth_user" {
		t.Errorf("backfilled username: want ig_oauth_user, got %s", username)
	}

	// UNIQUE(user_id, platform, platform_user_id): same (user, platform, id) fails.
	if _, err := db.Exec(
		`INSERT INTO user_oauth_profiles (user_id, platform, platform_user_id)
		 VALUES ($1, 'instagram', 'ig_oauth_456')`,
		uid,
	); err == nil {
		t.Error("expected UNIQUE violation on duplicate (user_id, platform, platform_user_id)")
	}

	// Different platform for same user is allowed.
	if err := db.QueryRow(
		`INSERT INTO user_oauth_profiles (user_id, platform, platform_user_id, username)
		 VALUES ($1, 'tiktok', 'tt_profile_001', 'tt_user')
		 RETURNING id`,
		uid,
	).Err(); err != nil {
		t.Fatalf("different platform should be allowed: %v", err)
	}
}
