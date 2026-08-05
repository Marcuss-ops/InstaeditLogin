package repository_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// expectPermanentDeleteTransaction pins the full PermanentlyDeleteAccountTx
// query sequence. When oauthConnectionID is 0 the account is treated as a
// legacy pre-043 attach and only per-account token rows are purged. When
// activeSiblings is 0 the account is the last channel on the grant: the
// grant tokens and the oauth_connections row are removed. canceledPostIDs
// feeds the cancel-future-jobs aggregate recompute.
func expectPermanentDeleteTransaction(mock sqlmock.Sqlmock, accountID, oauthConnectionID, activeSiblings int64, canceledPostIDs []int64) {
	mock.ExpectBegin()
	lockRows := sqlmock.NewRows([]string{"user_id", "platform", "platform_user_id", "status", "oauth_connection_id"})
	if oauthConnectionID > 0 {
		lockRows.AddRow(int64(1), "youtube", "UC-xyz", "active", oauthConnectionID)
	} else {
		lockRows.AddRow(int64(1), "youtube", "UC-xyz", "active", int64(0))
	}
	mock.ExpectQuery(`SELECT user_id, platform, platform_user_id, status, COALESCE(oauth_connection_id, 0)
   FROM platform_accounts
  WHERE id = $1
  FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(lockRows)
	if oauthConnectionID > 0 {
		mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
			WithArgs(oauthConnectionID).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery(`SELECT COUNT(*)
   FROM platform_accounts
  WHERE oauth_connection_id = $1
    AND status = 'active'
    AND id <> $2`).
			WithArgs(oauthConnectionID, accountID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(activeSiblings))
		if activeSiblings == 0 {
			mock.ExpectExec(`DELETE FROM tokens WHERE oauth_connection_id = $1`).
				WithArgs(oauthConnectionID).
				WillReturnResult(sqlmock.NewResult(0, 2))
			mock.ExpectExec(`DELETE FROM oauth_connections WHERE id = $1`).
				WithArgs(oauthConnectionID).
				WillReturnResult(sqlmock.NewResult(0, 1))
		}
	} else {
		mock.ExpectExec(`DELETE FROM tokens WHERE platform_account_id = $1`).
			WithArgs(accountID).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec(`DELETE FROM group_accounts WHERE account_id = $1`).
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`DELETE FROM workspace_channels WHERE platform_account_id = $1`).
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM account_resource_snapshots WHERE platform_account_id = $1`).
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	for _, query := range []string{
		`DELETE FROM account_capabilities WHERE platform_account_id = $1`,
		`DELETE FROM account_metric_history WHERE platform_account_id = $1`,
		`DELETE FROM youtube_video_edits WHERE platform_account_id = $1`,
		`DELETE FROM youtube_thumbnail_batch_items WHERE platform_account_id = $1`,
		`UPDATE external_destinations
    SET enabled = FALSE,
        default_metadata = '{}'::jsonb,
        updated_at = NOW()
  WHERE platform_account_id = $1`,
		`DELETE FROM livestreams WHERE platform_account_id = $1`,
	} {
		mock.ExpectExec(query).WithArgs(accountID).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	rows := sqlmock.NewRows([]string{"post_id"})
	for _, postID := range canceledPostIDs {
		rows.AddRow(postID)
	}
	mock.ExpectQuery(`UPDATE post_targets
    SET status = 'draft', error_message = ''
  WHERE platform_account_id = $1
    AND status NOT IN ('published', 'partially_published', 'failed', 'dlq')
  RETURNING post_id`).
		WithArgs(accountID).
		WillReturnRows(rows)
	for _, postID := range canceledPostIDs {
		expectAggregateRecomputeForPost(mock, postID)
	}
	mock.ExpectExec(`UPDATE platform_accounts
    SET status = 'deleted',
        username = '[deleted]',
        platform_user_id = '[deleted:' || id::text || ']',
        metadata = '{}'::jsonb,
        connected_at = NULL,
        last_validated_at = NULL,
        last_refresh_at = NULL,
        reauth_required_at = NULL,
        last_error_code = 'DELETED',
        last_error_message = 'account permanently deleted by user',
        updated_at = NOW()
  WHERE id = $1`).
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
 VALUES ($1, $2, $3, $4::jsonb)`).
		WithArgs("platform_account", accountID, "platform_account.deleted", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO audit_logs (user_id, action, resource_type, resource_id, result, metadata)
 VALUES ($1, $2, $3, $4, $5, $6::jsonb)`).
		WithArgs(int64(1), "account_deleted", "platform_account", accountID, "success", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

// TestUserRepository_PermanentlyDeleteAccount_LastChannel_RemovesGrant pins
// the last-channel path: the remote revoke callback runs, the grant token
// rows are purged, the oauth_connections row is removed, and the account row
// is tombstoned — all in one transaction.
func TestUserRepository_PermanentlyDeleteAccount_LastChannel_RemovesGrant(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	expectPermanentDeleteTransaction(mock, 21, 55, 0, nil)

	revokeCalled := false
	handled, err := repo.PermanentlyDeleteAccountTx(context.Background(), 21, func(context.Context, *sql.Tx) error {
		revokeCalled = true
		return nil
	})
	if err != nil {
		t.Fatalf("PermanentlyDeleteAccountTx: %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if !revokeCalled {
		t.Fatal("revoke callback must be invoked for the last channel on the grant")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestUserRepository_PermanentlyDeleteAccount_SiblingActive_PreservesGrant
// pins the shared-grant guarantee: deleting one channel of a grant still
// used by an active sibling must NOT touch the grant, its tokens, or the
// oauth_connections row — the tombstone alone removes the channel from every
// publishable surface.
func TestUserRepository_PermanentlyDeleteAccount_SiblingActive_PreservesGrant(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	expectPermanentDeleteTransaction(mock, 21, 55, 1, nil)

	handled, err := repo.PermanentlyDeleteAccountTx(context.Background(), 21, func(context.Context, *sql.Tx) error {
		t.Fatal("revoke must NOT be called while an active sibling still uses the grant")
		return nil
	})
	if err != nil {
		t.Fatalf("PermanentlyDeleteAccountTx: %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestUserRepository_PermanentlyDeleteAccount_LegacyAccount_PurgesTokens pins
// the pre-043 path: no oauth_connection, no grant lock/count — the per-account
// token rows are purged directly and the row is tombstoned.
func TestUserRepository_PermanentlyDeleteAccount_LegacyAccount_PurgesTokens(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	expectPermanentDeleteTransaction(mock, 21, 0, 0, nil)

	handled, err := repo.PermanentlyDeleteAccountTx(context.Background(), 21, nil)
	if err != nil {
		t.Fatalf("PermanentlyDeleteAccountTx: %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestUserRepository_PermanentlyDeleteAccount_CancelsFutureJobs pins the job
// cleanup: a cancelled future job triggers the parent aggregate recompute
// sequence inside the delete transaction.
func TestUserRepository_PermanentlyDeleteAccount_CancelsFutureJobs(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	expectPermanentDeleteTransaction(mock, 21, 55, 0, []int64{100})

	handled, err := repo.PermanentlyDeleteAccountTx(context.Background(), 21, func(context.Context, *sql.Tx) error {
		return nil
	})
	if err != nil {
		t.Fatalf("PermanentlyDeleteAccountTx: %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUserRepository_PermanentlyDeleteAccount_LastChannel_RequiresRevoke(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT user_id, platform, platform_user_id, status, COALESCE(oauth_connection_id, 0)
   FROM platform_accounts
  WHERE id = $1
  FOR UPDATE`).
		WithArgs(int64(21)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "platform", "platform_user_id", "status", "oauth_connection_id"}).AddRow(1, "youtube", "UC-xyz", "active", 55))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").WithArgs(int64(55)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT COUNT(*)
   FROM platform_accounts
  WHERE oauth_connection_id = $1
    AND status = 'active'
    AND id <> $2`).WithArgs(int64(55), int64(21)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectRollback()

	handled, err := repo.PermanentlyDeleteAccountTx(context.Background(), 21, nil)
	if err == nil || !strings.Contains(err.Error(), "remote revoke is not configured") {
		t.Fatalf("want missing-revoke error, got handled=%v err=%v", handled, err)
	}
	if handled {
		t.Fatal("handled must be false when last-grant revocation is unavailable")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUserRepository_PermanentlyDeleteAccount_InvalidID(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)

	handled, err := repo.PermanentlyDeleteAccountTx(context.Background(), 0, nil)
	if err == nil {
		t.Fatal("expected invalid-id error, got nil")
	}
	if handled {
		t.Fatal("handled must be false for an invalid id")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
