package repository_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func expectDisconnectTransaction(mock sqlmock.Sqlmock, accountID, oauthConnectionID int64, activeSiblings int64) {
	expectDisconnectTransactionWithJobs(mock, accountID, oauthConnectionID, activeSiblings, nil)
}

// expectDisconnectTransactionWithJobs extends the disconnect expectations
// with the P1 channel cleanup (group memberships, workspace channels, future
// jobs). canceledPostIDs, when non-nil, makes the cancel-future-jobs step
// return those parent post ids and triggers the aggregate recompute sequence
// (lock targets → lock post → resolve → update) for each of them.
func expectDisconnectTransactionWithJobs(mock sqlmock.Sqlmock, accountID, oauthConnectionID int64, activeSiblings int64, canceledPostIDs []int64) {
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id
	   FROM platform_accounts
	  WHERE id = $1
	  FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(oauthConnectionID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(oauthConnectionID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE platform_accounts
	    SET status = 'disconnected',
	        connected_at = NULL,
	        last_error_code = 'DISCONNECTED',
	        last_error_message = 'account disconnected by user',
	        updated_at = NOW()
	  WHERE id = $1`).
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT COUNT(*)
		   FROM platform_accounts
		  WHERE oauth_connection_id = $1
		    AND status = 'active'`).
		WithArgs(oauthConnectionID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(activeSiblings))
	expectCleanupAfterDisconnect(mock, accountID, canceledPostIDs)
	mock.ExpectCommit()
}

// expectCleanupAfterDisconnect covers the P1 disconnect cleanup performed in
// the same transaction as the status flip.
func expectCleanupAfterDisconnect(mock sqlmock.Sqlmock, accountID int64, canceledPostIDs []int64) {
	mock.ExpectExec(`DELETE FROM group_accounts WHERE account_id = $1`).
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`DELETE FROM workspace_channels WHERE platform_account_id = $1`).
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 1))
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
}

// expectAggregateRecomputeForPost pins the lock order used by
// cancelFutureJobsTx: target locks first, then the parent post, then the
// status resolve and aggregate UPDATE — the same sequence as CancelPost.
func expectAggregateRecomputeForPost(mock sqlmock.Sqlmock, postID int64) {
	mock.ExpectQuery(`SELECT id FROM post_targets WHERE post_id = $1 ORDER BY id ASC FOR UPDATE`).
		WithArgs(postID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(200))
	mock.ExpectQuery(`SELECT id FROM posts WHERE id = $1 FOR UPDATE`).
		WithArgs(postID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(postID))
	mock.ExpectQuery(`SELECT status FROM post_targets WHERE post_id = $1 ORDER BY id ASC`).
		WithArgs(postID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(models.PostStatusDraft))
	mock.ExpectExec(`UPDATE posts SET status = $1 WHERE id = $2`).
		WithArgs(models.PostStatusDraft, postID).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestUserRepository_DisconnectPlatformAccountTx_PreservesGrantForActiveSibling(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id, status
	   FROM platform_accounts
	  WHERE id = $1
	  FOR UPDATE`).
		WithArgs(int64(21)).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id", "status"}).AddRow(55, models.AccountStatusActive))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").WithArgs(int64(55)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE platform_accounts
	    SET status = 'disconnected',
	        connected_at = NULL,
	        last_error_code = 'DISCONNECTED',
	        last_error_message = 'account disconnected by user',
	        updated_at = NOW()
	  WHERE id = $1`).WithArgs(int64(21)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT COUNT(*)
		   FROM platform_accounts
		  WHERE oauth_connection_id = $1
		    AND status = 'active'`).WithArgs(int64(55)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	expectCleanupAfterDisconnect(mock, 21, nil)
	mock.ExpectCommit()

	called := false
	lastOnGrant, handled, err := repo.DisconnectPlatformAccountTx(context.Background(), 21, func(context.Context, *sql.Tx) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("DisconnectPlatformAccountTx: %v", err)
	}
	if !handled || lastOnGrant || called {
		t.Fatalf("shared grant result: handled=%v lastOnGrant=%v callbackCalled=%v, want true/false/false", handled, lastOnGrant, called)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUserRepository_DisconnectPlatformAccountTx_LastChannelRevokesInsideTransaction(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id, status
	   FROM platform_accounts
	  WHERE id = $1
	  FOR UPDATE`).
		WithArgs(int64(21)).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id", "status"}).AddRow(55, models.AccountStatusActive))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").WithArgs(int64(55)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE platform_accounts
	    SET status = 'disconnected',
	        connected_at = NULL,
	        last_error_code = 'DISCONNECTED',
	        last_error_message = 'account disconnected by user',
	        updated_at = NOW()
	  WHERE id = $1`).WithArgs(int64(21)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT COUNT(*)
		   FROM platform_accounts
		  WHERE oauth_connection_id = $1
		    AND status = 'active'`).WithArgs(int64(55)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(`UPDATE oauth_connections
		    SET status = 'disconnected',
		        reauth_required_at = NULL,
		        last_refresh_error = NULL,
		        updated_at = NOW()
		  WHERE id = $1`).WithArgs(int64(55)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM tokens WHERE oauth_connection_id = $1`).WithArgs(int64(55)).WillReturnResult(sqlmock.NewResult(0, 1))
	expectCleanupAfterDisconnect(mock, 21, nil)
	mock.ExpectCommit()

	called := false
	lastOnGrant, handled, err := repo.DisconnectPlatformAccountTx(context.Background(), 21, func(_ context.Context, tx *sql.Tx) error {
		called = true
		if tx == nil {
			t.Fatal("transaction-aware revoke callback received nil transaction")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("DisconnectPlatformAccountTx: %v", err)
	}
	if !handled || !lastOnGrant || !called {
		t.Fatalf("last grant result: handled=%v lastOnGrant=%v callbackCalled=%v, want true/true/true", handled, lastOnGrant, called)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUserRepository_DisconnectPlatformAccountTx_RemoteRevokeFailureRollsBack(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id, status
	   FROM platform_accounts
	  WHERE id = $1
	  FOR UPDATE`).WithArgs(int64(21)).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id", "status"}).AddRow(55, models.AccountStatusActive))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").WithArgs(int64(55)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE platform_accounts
	    SET status = 'disconnected',
	        connected_at = NULL,
	        last_error_code = 'DISCONNECTED',
	        last_error_message = 'account disconnected by user',
	        updated_at = NOW()
	  WHERE id = $1`).WithArgs(int64(21)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT COUNT(*)
		   FROM platform_accounts
		  WHERE oauth_connection_id = $1
		    AND status = 'active'`).WithArgs(int64(55)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectRollback()

	_, _, err := repo.DisconnectPlatformAccountTx(context.Background(), 21, func(context.Context, *sql.Tx) error {
		return context.Canceled
	})
	if err == nil {
		t.Fatal("remote revoke failure must be returned")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUserRepository_DisconnectPlatformAccountTx_AlreadyDisconnectedIsIdempotent(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id, status
	   FROM platform_accounts
	  WHERE id = $1
	  FOR UPDATE`).WithArgs(int64(21)).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id", "status"}).AddRow(55, models.AccountStatusDisconnected))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").WithArgs(int64(55)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE platform_accounts
	    SET status = 'disconnected',
	        connected_at = NULL,
	        last_error_code = 'DISCONNECTED',
	        last_error_message = 'account disconnected by user',
	        updated_at = NOW()
	  WHERE id = $1`).WithArgs(int64(21)).WillReturnResult(sqlmock.NewResult(0, 1))
	expectCleanupAfterDisconnect(mock, 21, nil)
	mock.ExpectCommit()

	callbackCalled := false
	lastOnGrant, handled, err := repo.DisconnectPlatformAccountTx(context.Background(), 21, func(context.Context, *sql.Tx) error {
		callbackCalled = true
		return nil
	})
	if err != nil {
		t.Fatalf("idempotent disconnect: %v", err)
	}
	if !handled || lastOnGrant || callbackCalled {
		t.Fatalf("idempotent result: handled=%v lastOnGrant=%v callbackCalled=%v, want true/false/false", handled, lastOnGrant, callbackCalled)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUserRepository_DisconnectPlatformAccount_PreservesGrantForActiveSibling(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	expectDisconnectTransaction(mock, 21, 55, 1)

	lastOnGrant, handled, err := repo.DisconnectPlatformAccount(context.Background(), 21)
	if err != nil {
		t.Fatalf("DisconnectPlatformAccount: %v", err)
	}
	if !handled {
		t.Fatal("DisconnectPlatformAccount must report the operation as handled")
	}
	if lastOnGrant {
		t.Fatal("grant must be preserved while an active sibling remains")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUserRepository_DisconnectPlatformAccount_AllowsRevokeForLastChannel(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	expectDisconnectTransaction(mock, 21, 55, 0)

	lastOnGrant, handled, err := repo.DisconnectPlatformAccount(context.Background(), 21)
	if err != nil {
		t.Fatalf("DisconnectPlatformAccount: %v", err)
	}
	if !handled || !lastOnGrant {
		t.Fatalf("last channel result: handled=%v lastOnGrant=%v, want true/true", handled, lastOnGrant)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUserRepository_DisconnectPlatformAccount_LegacyAccountSkipsGrantLock(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id
	   FROM platform_accounts
	  WHERE id = $1
	  FOR UPDATE`).
		WithArgs(int64(21)).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(sql.NullInt64{}))
	mock.ExpectExec(`UPDATE platform_accounts
	    SET status = 'disconnected',
	        connected_at = NULL,
	        last_error_code = 'DISCONNECTED',
	        last_error_message = 'account disconnected by user',
	        updated_at = NOW()
	  WHERE id = $1`).
		WithArgs(int64(21)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectCleanupAfterDisconnect(mock, 21, nil)
	mock.ExpectCommit()

	lastOnGrant, handled, err := repo.DisconnectPlatformAccount(context.Background(), 21)
	if err != nil {
		t.Fatalf("DisconnectPlatformAccount: %v", err)
	}
	if !handled || lastOnGrant {
		t.Fatalf("legacy result: handled=%v lastOnGrant=%v, want true/false", handled, lastOnGrant)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestUserRepository_DisconnectPlatformAccount_CancelsFutureJobs pins the P1
// job cleanup: non-terminal post targets of the account are reset to draft
// and the parent post aggregates are recomputed with the same lock order and
// resolver as CancelPost — in the SAME transaction as the status flip, so a
// failure rolls everything back.
func TestUserRepository_DisconnectPlatformAccount_CancelsFutureJobs(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	expectDisconnectTransactionWithJobs(mock, 21, 55, 1, []int64{100})

	lastOnGrant, handled, err := repo.DisconnectPlatformAccount(context.Background(), 21)
	if err != nil {
		t.Fatalf("DisconnectPlatformAccount: %v", err)
	}
	if !handled || lastOnGrant {
		t.Fatalf("result: handled=%v lastOnGrant=%v, want true/false", handled, lastOnGrant)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
