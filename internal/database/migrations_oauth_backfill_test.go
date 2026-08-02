//go:build integration

package database

import (
	"bytes"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
	"github.com/lib/pq"
)

func TestMigration083_BackfillsCanonicalOAuthFieldsAndIsIdempotent(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 80); err != nil {
		t.Fatalf("RunMigrationsUpTo(80): %v", err)
	}
	for _, name := range []string{"082_normalize_youtube_long_lived_token.sql"} {
		body, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}

	var userID, connectionID, accountID int64
	if err := db.QueryRow(`
		INSERT INTO users (email, name)
		VALUES ('migration-083@example.invalid', 'Migration 083')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO oauth_connections (user_id, provider, provider_subject_id, provider_resource_id, scopes)
		VALUES ($1, 'youtube', 'subject-083', 'channel-083', $2)
		RETURNING id`, userID, "{youtube.upload,youtube.readonly}").Scan(&connectionID); err != nil {
		t.Fatalf("insert oauth connection: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO platform_accounts (user_id, platform, platform_user_id, username, oauth_connection_id)
		VALUES ($1, 'youtube', 'channel-083', 'Migration 083', $2)
		RETURNING id`, userID, connectionID).Scan(&accountID); err != nil {
		t.Fatalf("insert platform account: %v", err)
	}

	legacyCiphertext := []byte("legacy-ciphertext-083")
	legacyExpiry := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`
		INSERT INTO tokens (
			platform_account_id, oauth_connection_id, token_type,
			encrypted_token, encrypted_refresh_token, expires_at, scopes
		) VALUES ($1, $2, 'bearer', $3, $4, $5, $6)`,
		accountID, connectionID, legacyCiphertext, []byte("legacy-refresh-083"), legacyExpiry,
		"{youtube.upload,youtube.readonly}"); err != nil {
		t.Fatalf("insert legacy token: %v", err)
	}

	for _, name := range []string{"083_oauth_token_field_normalization.sql"} {
		body, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}

	var (
		canonicalCiphertext []byte
		canonicalExpiry     time.Time
		refreshExpiry       sql.NullTime
		grantedScopes       []string
		status              string
		lastRefreshError    sql.NullString
	)
	if err := db.QueryRow(`
		SELECT t.encrypted_access_token, t.access_token_expires_at,
		       t.refresh_token_expires_at, oc.granted_scopes,
		       oc.status, oc.last_refresh_error
		  FROM tokens t
		  JOIN oauth_connections oc ON oc.id = t.oauth_connection_id
		 WHERE t.oauth_connection_id = $1 AND t.token_type = 'bearer'`, connectionID).
		Scan(&canonicalCiphertext, &canonicalExpiry, &refreshExpiry, pq.Array(&grantedScopes), &status, &lastRefreshError); err != nil {
		t.Fatalf("read canonical fields: %v", err)
	}
	if !bytes.Equal(canonicalCiphertext, legacyCiphertext) {
		t.Fatalf("encrypted_access_token changed during backfill: got %q want %q", canonicalCiphertext, legacyCiphertext)
	}
	if !canonicalExpiry.Equal(legacyExpiry) {
		t.Fatalf("access_token_expires_at: got %s want %s", canonicalExpiry, legacyExpiry)
	}
	if refreshExpiry.Valid {
		t.Fatalf("refresh_token_expires_at should remain NULL for legacy rows without provider TTL: got %s", refreshExpiry.Time)
	}
	if status != models.AccountStatusActive {
		t.Fatalf("oauth connection status: got %q want %q", status, models.AccountStatusActive)
	}
	if lastRefreshError.Valid {
		t.Fatalf("last_refresh_error should default NULL, got %q", lastRefreshError.String)
	}
	if !reflect.DeepEqual(grantedScopes, []string{"youtube.upload", "youtube.readonly"}) {
		t.Fatalf("granted_scopes: got %#v", grantedScopes)
	}

	body, err := migrationFiles.ReadFile("migrations/083_oauth_token_field_normalization.sql")
	if err != nil {
		t.Fatalf("read migration 083: %v", err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("direct idempotent execution of migration 083: %v", err)
	}
}

// TestMigration084_AllowsMultipleYouTubeChannelsPerOAuthSubject verifies the
// grant/resource split. Two distinct YouTube platform accounts may reference
// one subject-keyed oauth_connection, while a second subject-keyed connection
// for the same user/provider/subject is rejected by the unique partial index.
func TestMigration084_AllowsMultipleYouTubeChannelsPerOAuthSubject(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 80); err != nil {
		t.Fatalf("RunMigrationsUpTo(80): %v", err)
	}
	for _, name := range []string{"082_normalize_youtube_long_lived_token.sql", "083_oauth_token_field_normalization.sql", "084_oauth_subject_shared_connections.sql", "085_grant_scoped_tokens.sql"} {
		body, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}

	var userID, connectionID, channelAID, channelBID int64
	if err := db.QueryRow(`
		INSERT INTO users (email, name)
		VALUES ('migration-084@example.invalid', 'Migration 084')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO oauth_connections (
			user_id, provider, provider_subject_id, provider_resource_id, scopes
		) VALUES ($1, 'youtube', 'google-subject-084', 'channel-a-084', $2)
		RETURNING id`, userID, "{youtube.upload}").Scan(&connectionID); err != nil {
		t.Fatalf("insert subject-keyed connection: %v", err)
	}
	for _, channel := range []string{"channel-a-084", "channel-b-084"} {
		var id int64
		if err := db.QueryRow(`
			INSERT INTO platform_accounts (
				user_id, platform, platform_user_id, username, oauth_connection_id
			) VALUES ($1, 'youtube', $2, $2, $3)
			RETURNING id`, userID, channel, connectionID).Scan(&id); err != nil {
			t.Fatalf("insert shared channel %s: %v", channel, err)
		}
		if channel == "channel-a-084" {
			channelAID = id
		} else {
			channelBID = id
		}
	}
	if channelAID == 0 || channelBID == 0 || channelAID == channelBID {
		t.Fatalf("expected two distinct platform accounts, got A=%d B=%d", channelAID, channelBID)
	}

	var sharedCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM platform_accounts
		 WHERE oauth_connection_id = $1`, connectionID).Scan(&sharedCount); err != nil {
		t.Fatalf("count shared channels: %v", err)
	}
	if sharedCount != 2 {
		t.Fatalf("shared channel count: got %d want 2", sharedCount)
	}

	// The resource hint is not the grant identity: a second modern
	// subject may legitimately use the same resource id.
	if _, err := db.Exec(`
		INSERT INTO oauth_connections (
			user_id, provider, provider_subject_id, provider_resource_id
		) VALUES ($1, 'youtube', 'google-subject-other-084', 'channel-a-084')`, userID); err != nil {
		t.Fatalf("different subject with same resource hint must be allowed: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO oauth_connections (
			user_id, provider, provider_subject_id, provider_resource_id
		) VALUES ($1, 'youtube', 'google-subject-084', 'channel-c-084')`, userID); err == nil {
		t.Fatal("duplicate subject-keyed oauth_connection insert unexpectedly succeeded")
	}

	// Revoking/deleting the shared grant must remove its encrypted token
	// but preserve both resource rows; the FK actions are CASCADE on tokens
	// and SET NULL on platform_accounts.
	if _, err := db.Exec(`
		INSERT INTO tokens (
			platform_account_id, oauth_connection_id, token_type,
			encrypted_token, encrypted_refresh_token
		) VALUES (NULL, $1, 'bearer', $2, $3)`,
		connectionID, []byte("access-084"), []byte("refresh-084")); err != nil {
		t.Fatalf("insert shared-grant token: %v", err)
	}
	var nullablePlatformAccount sql.NullInt64
	if err := db.QueryRow(`SELECT platform_account_id FROM tokens WHERE oauth_connection_id = $1`, connectionID).Scan(&nullablePlatformAccount); err != nil {
		t.Fatalf("read grant-scoped token channel reference: %v", err)
	}
	if nullablePlatformAccount.Valid {
		t.Fatalf("grant-scoped token platform_account_id: got %d want NULL", nullablePlatformAccount.Int64)
	}

	if _, err := db.Exec(`DELETE FROM oauth_connections WHERE id = $1`, connectionID); err != nil {
		t.Fatalf("delete shared OAuth connection: %v", err)
	}
	var remainingTokens, unlinkedChannels int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tokens WHERE oauth_connection_id = $1`, connectionID).Scan(&remainingTokens); err != nil {
		t.Fatalf("count deleted-grant tokens: %v", err)
	}
	if remainingTokens != 0 {
		t.Fatalf("shared-grant tokens remaining after revoke: got %d want 0", remainingTokens)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_accounts WHERE id IN ($1, $2) AND oauth_connection_id IS NULL`, channelAID, channelBID).Scan(&unlinkedChannels); err != nil {
		t.Fatalf("count unlinked channels: %v", err)
	}
	if unlinkedChannels != 2 {
		t.Fatalf("channels after shared-grant revoke: got %d unlinked want 2", unlinkedChannels)
	}

	for _, name := range []string{"084_oauth_subject_shared_connections.sql", "085_grant_scoped_tokens.sql"} {
		body, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("direct idempotent execution of migration %s: %v", name, err)
		}
	}
}

// ────────────────────────────────────────────────────────────────────
//  helpers
// ────────────────────────────────────────────────────────────────────

// readMigrationBodies reads each migration's SQL body via the
// same `embed.FS` package the runner uses. Internal-package access
// (`package database`, not `database_test`) is what makes this work.
