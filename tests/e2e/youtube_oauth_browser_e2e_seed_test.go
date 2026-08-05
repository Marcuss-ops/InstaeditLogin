//go:build e2e

package e2e

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type browserSmokeUserStore struct{}

func (*browserSmokeUserStore) AttachPlatformAccount(int64, *models.PlatformProfile, string) (*models.PlatformAccount, error) {
	return nil, fmt.Errorf("browserSmokeUserStore.AttachPlatformAccount not used (production path delegates to ChannelAuthorizationService)")
}
func (*browserSmokeUserStore) ListPlatformAccountsByUser(int64, string) ([]*models.PlatformAccount, error) {
	return nil, nil
}
func (*browserSmokeUserStore) FindOAuthConnectionByID(context.Context, int64) (*models.OAuthConnection, error) {
	return nil, nil // not exercised by the browser smoke suite
}
func (*browserSmokeUserStore) ListFilteredYouTubeAccounts(userID int64, workspaceID *int64, group, language, manager string) ([]*models.PlatformAccount, error) {
	return nil, nil
}
func (*browserSmokeUserStore) FindPlatformAccountByID(int64) (*models.PlatformAccount, error) {
	return nil, nil
}
func (*browserSmokeUserStore) FindPlatformAccount(string, string) (*models.PlatformAccount, error) {
	return nil, nil
}
func (*browserSmokeUserStore) UpdatePlatformAccount(*models.PlatformAccount) error {
	return nil
}
func (*browserSmokeUserStore) DeletePlatformAccount(int64) error {
	return nil
}
func (*browserSmokeUserStore) FindUserIDByEmail(context.Context, string) (int64, error) {
	return 0, nil
}
func (*browserSmokeUserStore) FinalizeAttach(context.Context, int64, []string) (int64, error) {
	return 0, fmt.Errorf("browserSmokeUserStore.FinalizeAttach not used (production path delegates to ChannelAuthorizationService)")
}
func (*browserSmokeUserStore) MarkReauthRequired(context.Context, int64, string, string) error {
	return nil
}
func (*browserSmokeUserStore) CountActiveAccountsOnConnection(context.Context, int64, int64) (int64, error) {
	return 0, nil // not exercised by the browser smoke suite
}

// -----------------------------------------------------------------------------
// schemaCopyFromProductionSessionsRow — bootstrap the e2e container's
// `sessions` table if it's absent (the harness's applyE2ESchema may
// not run migration 031 on all container variants). Idempotent.
// -----------------------------------------------------------------------------

const ensureSessionsTableForBrowserSmoke = `
CREATE TABLE IF NOT EXISTS sessions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
`

func ensureSessionsTable(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, stmt := range strings.Split(ensureSessionsTableForBrowserSmoke, ";") {
		s := strings.TrimSpace(stmt)
		if s == "" {
			continue
		}
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("ensureSessionsTable stmt=%q: %v", s, err)
		}
	}
}

// -----------------------------------------------------------------------------
// seedBrowserSmokeUser writes the (user, workspace, sessions,
// platform_account) quartet to the test container's Postgres so
// the production callback handler's bind path has FK targets
// to attach.
//
// Returns (userID, workspaceID, accountID, sessionID).
// -----------------------------------------------------------------------------

func seedBrowserSmokeUser(t *testing.T, db *sql.DB) (int64, int64, int64, int64) {
	t.Helper()
	var userID int64
	if err := db.QueryRow(
		`INSERT INTO users (email) VALUES ($1) RETURNING id`,
		browserSmokeE2EEmail,
	).Scan(&userID); err != nil {
		t.Fatalf("seedBrowserSmokeUser users: %v", err)
	}
	var workspaceID int64
	if err := db.QueryRow(
		`INSERT INTO workspaces (name, owner_id) VALUES ($1, $2) RETURNING id`,
		browserSmokeE2EWorkspace, userID,
	).Scan(&workspaceID); err != nil {
		t.Fatalf("seedBrowserSmokeUser workspaces: %v", err)
	}
	var sessionID int64
	if err := db.QueryRow(
		`INSERT INTO sessions (user_id, workspace_id, token_hash, expires_at)
		 VALUES ($1, $2, $3, NOW() + INTERVAL '24 hours')
		 RETURNING id`,
		userID, workspaceID, "browser-smoke-token-hash-"+randomHex16(),
	).Scan(&sessionID); err != nil {
		t.Fatalf("seedBrowserSmokeUser sessions: %v", err)
	}
	var accountID int64
	if err := db.QueryRow(
		`INSERT INTO platform_accounts
		   (user_id, workspace_id, platform, platform_user_id, status, username, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		 RETURNING id`,
		userID, workspaceID,
		models.PlatformYouTube,
		browserSmokeE2EChannel,
		models.AccountStatusPendingAuthorization,
		"browser-smoke-pending",
	).Scan(&accountID); err != nil {
		t.Fatalf("seedBrowserSmokeUser platform_accounts: %v", err)
	}
	return userID, workspaceID, accountID, sessionID
}

func randomHex16() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	out := ""
	for _, x := range b {
		out += fmt.Sprintf("%02x", x)
	}
	return out
}

// -----------------------------------------------------------------------------
// The test.
// -----------------------------------------------------------------------------
