//go:build integration

// Package database_test contains integration tests for the meta→instagram
// migration (020_rename_meta_to_instagram.sql).
//
// Run with: go test -tags=integration -run 'MigrationMetaToInstagram|MigrationInstagram' ./internal/database/...
package database

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

// insertPlatformAccount inserts a platform_accounts row and returns its id.
func insertPlatformAccount(t *testing.T, db *sql.DB, platform, platformUserID, username string) int64 {
	t.Helper()
	// Create a user first (platform_accounts FK).
	var userID int64
	if err := db.QueryRow(`INSERT INTO users (email, name) VALUES ($1, $1) RETURNING id`, username+"@test.com").Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	// Create workspace for the user.
	var wsID int64
	if err := db.QueryRow(`INSERT INTO workspaces (name, owner_id) VALUES ($1, $2) RETURNING id`, username+"-ws", userID).Scan(&wsID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	var accID int64
	if err := db.QueryRow(
		`INSERT INTO platform_accounts (user_id, platform, platform_user_id, username, workspace_id)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		userID, platform, platformUserID, username, wsID,
	).Scan(&accID); err != nil {
		t.Fatalf("insert platform_account (%s/%s): %v", platform, platformUserID, err)
	}
	return accID
}

// insertToken inserts an encrypted token for a platform_account.
func insertToken(t *testing.T, db *sql.DB, accountID int64, tokenType, tokenValue string) int64 {
	t.Helper()
	var tokID int64
	if err := db.QueryRow(
		`INSERT INTO tokens (platform_account_id, token_type, encrypted_token, scopes)
		 VALUES ($1, $2, decode($3, 'hex'), ARRAY['test'])
		 RETURNING id`, accountID, tokenType, tokenValue).Scan(&tokID); err != nil {
		t.Fatalf("insert token: %v", err)
	}
	return tokID
}

// insertPostTarget inserts a post (with a queued post) and returns the post_target id.
func insertPostTarget(t *testing.T, db *sql.DB, workspaceID, accountID int64) int64 {
	t.Helper()
	var postID int64
	if err := db.QueryRow(
		`INSERT INTO posts (workspace_id, status) VALUES ($1, 'queued') RETURNING id`,
		workspaceID,
	).Scan(&postID); err != nil {
		t.Fatalf("insert post: %v", err)
	}
	var targetID int64
	if err := db.QueryRow(
		`INSERT INTO post_targets (post_id, platform_account_id, status) VALUES ($1, $2, 'queued') RETURNING id`,
		postID, accountID,
	).Scan(&targetID); err != nil {
		t.Fatalf("insert post_target: %v", err)
	}
	return targetID
}

// countPlatform returns the row count for a given platform value.
func countPlatform(t *testing.T, db *sql.DB, platform string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM platform_accounts WHERE platform = $1`, platform,
	).Scan(&count); err != nil {
		t.Fatalf("count platform %s: %v", platform, err)
	}
	return count
}

// ────────────────────────────────────────────────────────────────────
//  Tests
// ────────────────────────────────────────────────────────────────────

// TestMigrationMetaToInstagram is the main scenario: before the migration,
// one account has platform='meta'; after the migration, it's 'instagram'.
func TestMigrationMetaToInstagram(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	insertPlatformAccount(t, db, "meta", "ig_business_123", "test_ig_user")

	// Apply the migration (RunMigrations already ran on empty DB;
	// 020 was a noop then. Now there's data, so 020 does the rename).
	if err := applyMigrationByName(t, db, "020_rename_meta_to_instagram.sql"); err != nil {
		t.Fatalf("apply 020: %v", err)
	}

	// Verify: meta accounts are now instagram.
	if got := countPlatform(t, db, "meta"); got != 0 {
		t.Errorf("meta accounts after migration: want 0, got %d", got)
	}
	if got := countPlatform(t, db, "instagram"); got != 1 {
		t.Errorf("instagram accounts after migration: want 1, got %d", got)
	}
}

// TestMigrationDoesNotChangeOtherPlatforms verifies that youtube, tiktok, and
// twitter accounts are untouched by the migration.
func TestMigrationDoesNotChangeOtherPlatforms(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	insertPlatformAccount(t, db, "meta", "ig_1", "ig_user")
	insertPlatformAccount(t, db, "youtube", "yt_channel_1", "yt_user")
	insertPlatformAccount(t, db, "tiktok", "tt_user_1", "tt_user")
	twitterID := insertPlatformAccount(t, db, "twitter", "tw_user_1", "tw_user")

	// Pre-migration: verify twitter user_id is preserved.
	var twitterUserID string
	db.QueryRow(`SELECT platform_user_id FROM platform_accounts WHERE id = $1`, twitterID).Scan(&twitterUserID)

	if err := applyMigrationByName(t, db, "020_rename_meta_to_instagram.sql"); err != nil {
		t.Fatalf("apply 020: %v", err)
	}

	// Other platforms untouched.
	if got := countPlatform(t, db, "youtube"); got != 1 {
		t.Errorf("youtube: want 1, got %d", got)
	}
	if got := countPlatform(t, db, "tiktok"); got != 1 {
		t.Errorf("tiktok: want 1, got %d", got)
	}
	if got := countPlatform(t, db, "twitter"); got != 1 {
		t.Errorf("twitter: want 1, got %d", got)
	}

	// Twitter platform_user_id preserved.
	var gotUserID string
	db.QueryRow(`SELECT platform_user_id FROM platform_accounts WHERE id = $1`, twitterID).Scan(&gotUserID)
	if gotUserID != twitterUserID {
		t.Errorf("twitter platform_user_id: want %q, got %q", twitterUserID, gotUserID)
	}
}

// TestMigrationPreservesAccountID verifies that the platform_accounts.id
// does not change after the migration.
func TestMigrationPreservesAccountID(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	metaID := insertPlatformAccount(t, db, "meta", "ig_account_456", "ig_user_456")

	if err := applyMigrationByName(t, db, "020_rename_meta_to_instagram.sql"); err != nil {
		t.Fatalf("apply 020: %v", err)
	}

	var id int64
	var platform, platformUserID string
	if err := db.QueryRow(
		`SELECT id, platform, platform_user_id FROM platform_accounts WHERE id = $1`, metaID,
	).Scan(&id, &platform, &platformUserID); err != nil {
		t.Fatalf("query account: %v", err)
	}
	if id != metaID {
		t.Errorf("id changed: want %d, got %d", metaID, id)
	}
	if platform != "instagram" {
		t.Errorf("platform: want instagram, got %s", platform)
	}
	if platformUserID != "ig_account_456" {
		t.Errorf("platform_user_id: want ig_account_456, got %s", platformUserID)
	}
}

// TestMigrationPreservesTokens verifies that tokens associated with a
// meta account are still reachable after the migration.
func TestMigrationPreservesTokens(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	metaID := insertPlatformAccount(t, db, "meta", "ig_tok_789", "tok_user")
	tokID := insertToken(t, db, metaID, "access", "deadbeef")

	if err := applyMigrationByName(t, db, "020_rename_meta_to_instagram.sql"); err != nil {
		t.Fatalf("apply 020: %v", err)
	}

	// Token is still reachable via the platform_account_id FK.
	var gotTokenValue string
	if err := db.QueryRow(
		`SELECT encode(t.encrypted_token, 'hex')
		   FROM tokens t
		   JOIN platform_accounts pa ON pa.id = t.platform_account_id
		  WHERE pa.id = $1 AND t.id = $2`,
		metaID, tokID,
	).Scan(&gotTokenValue); err != nil {
		t.Fatalf("query token: %v (token should still be reachable)", err)
	}
	if gotTokenValue != "deadbeef" {
		t.Errorf("token value: want deadbeef, got %s", gotTokenValue)
	}
}

// TestMigrationPreservesPostTargets verifies that post_targets linked to
// a meta account remain reachable and unchanged after the migration.
func TestMigrationPreservesPostTargets(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	metaID := insertPlatformAccount(t, db, "meta", "ig_post_user", "post_ig_user")
	// Get the workspace_id for this account.
	var wsID int64
	db.QueryRow(`SELECT workspace_id FROM platform_accounts WHERE id = $1`, metaID).Scan(&wsID)

	targetID := insertPostTarget(t, db, wsID, metaID)

	if err := applyMigrationByName(t, db, "020_rename_meta_to_instagram.sql"); err != nil {
		t.Fatalf("apply 020: %v", err)
	}

	// Post target still exists and its platform_account_id still matches.
	var gotTargetID, gotAccountID int64
	var gotStatus string
	if err := db.QueryRow(
		`SELECT id, platform_account_id, status FROM post_targets WHERE id = $1`, targetID,
	).Scan(&gotTargetID, &gotAccountID, &gotStatus); err != nil {
		t.Fatalf("post_target should still exist after migration: %v", err)
	}
	if gotTargetID != targetID {
		t.Errorf("target id changed: want %d, got %d", targetID, gotTargetID)
	}
	if gotAccountID != metaID {
		t.Errorf("platform_account_id changed: want %d, got %d", metaID, gotAccountID)
	}
	if gotStatus != "queued" {
		t.Errorf("status changed: want queued, got %s", gotStatus)
	}
}

// TestMigrationLeavesNoMetaAccounts verifies that after the migration,
// SELECT COUNT(*) WHERE platform='meta' returns 0.
func TestMigrationLeavesNoMetaAccounts(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Insert multiple meta accounts.
	insertPlatformAccount(t, db, "meta", "ig_a", "user_a")
	insertPlatformAccount(t, db, "meta", "ig_b", "user_b")
	insertPlatformAccount(t, db, "meta", "ig_c", "user_c")

	// Also insert non-meta accounts.
	insertPlatformAccount(t, db, "youtube", "yt_1", "yt_user")

	if err := applyMigrationByName(t, db, "020_rename_meta_to_instagram.sql"); err != nil {
		t.Fatalf("apply 020: %v", err)
	}

	// Zero meta accounts remain.
	if got := countPlatform(t, db, "meta"); got != 0 {
		t.Errorf("meta accounts remaining: want 0, got %d", got)
	}
	// All three are now instagram.
	if got := countPlatform(t, db, "instagram"); got != 3 {
		t.Errorf("instagram accounts: want 3, got %d", got)
	}
	// YouTube remains.
	if got := countPlatform(t, db, "youtube"); got != 1 {
		t.Errorf("youtube accounts: want 1, got %d", got)
	}
}

// TestMigrationCanRunTwiceSafely verifies that applying the migration a
// second time is idempotent (no errors, no data corruption).
func TestMigrationCanRunTwiceSafely(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	insertPlatformAccount(t, db, "meta", "ig_idem_1", "idem_user")

	// First run.
	if err := applyMigrationByName(t, db, "020_rename_meta_to_instagram.sql"); err != nil {
		t.Fatalf("1st apply 020: %v", err)
	}
	if got := countPlatform(t, db, "instagram"); got != 1 {
		t.Fatalf("after 1st run: want 1 instagram, got %d", got)
	}

	// Second run must not fail and must not duplicate rows.
	if err := applyMigrationByName(t, db, "020_rename_meta_to_instagram.sql"); err != nil {
		t.Fatalf("second run of migration failed: %v", err)
	}
	if got := countPlatform(t, db, "meta"); got != 0 {
		t.Errorf("after 2nd run: want 0 meta, got %d", got)
	}
	if got := countPlatform(t, db, "instagram"); got != 1 {
		t.Errorf("after 2nd run: want 1 instagram, got %d", got)
	}
}

// TestMigrationHandlesMetaInstagramCollision verifies that the migration
// detects a UNIQUE(platform, platform_user_id) collision and aborts
// cleanly without modifying any data.
func TestMigrationHandlesMetaInstagramCollision(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Create a meta account with platform_user_id = "collision_123".
	insertPlatformAccount(t, db, "meta", "collision_123", "meta_user")

	// Create an instagram account with the SAME platform_user_id.
	insertPlatformAccount(t, db, "instagram", "collision_123", "ig_user")

	// The migration should detect this collision and abort.
	err := applyMigrationByName(t, db, "020_rename_meta_to_instagram.sql")
	if err == nil {
		t.Fatal("expected collision error from migration, got nil")
	}

	// Verify no data was modified: meta still exists, instagram still exists.
	if got := countPlatform(t, db, "meta"); got != 1 {
		t.Errorf("meta accounts after collision: want 1 (unchanged), got %d", got)
	}
	if got := countPlatform(t, db, "instagram"); got != 1 {
		t.Errorf("instagram accounts after collision: want 1 (unchanged), got %d", got)
	}

	// Verify both accounts retain their original usernames.
	var metaUser, igUser string
	db.QueryRow(`SELECT username FROM platform_accounts WHERE platform = 'meta'`).Scan(&metaUser)
	db.QueryRow(`SELECT username FROM platform_accounts WHERE platform = 'instagram'`).Scan(&igUser)
	if metaUser != "meta_user" {
		t.Errorf("meta username: want meta_user, got %s", metaUser)
	}
	if igUser != "ig_user" {
		t.Errorf("instagram username: want ig_user, got %s", igUser)
	}
}

// ────────────────────────────────────────────────────────────────────
//  helpers
// ────────────────────────────────────────────────────────────────────

// applyMigrationByName reads and executes a single embedded migration SQL
// file by name. Used to test idempotency and collision paths.
func applyMigrationByName(t *testing.T, db *sql.DB, name string) error {
	t.Helper()
	sql, err := migrationFiles.ReadFile("migrations/" + name)
	if err != nil {
		return fmt.Errorf("read embedded %s: %w", name, err)
	}
	_, err = db.Exec(string(sql))
	return err
}
