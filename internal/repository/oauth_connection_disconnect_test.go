package repository_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func expectGrantDisconnectBegin(mock sqlmock.Sqlmock, grantID, userID int64) {
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT user_id
	   FROM oauth_connections
	  WHERE id = $1
	  FOR UPDATE`).
		WithArgs(grantID).
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(userID))
	mock.ExpectQuery(`SELECT id
	   FROM platform_accounts
	  WHERE oauth_connection_id = $1
	  ORDER BY id
	  FOR UPDATE`).
		WithArgs(grantID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(21).AddRow(22))
	mock.ExpectExec(`UPDATE platform_accounts
	    SET status = 'disconnected',
	        connected_at = NULL,
	        reauth_required_at = NULL,
	        last_error_code = 'DISCONNECTED',
	        last_error_message = 'OAuth grant disconnected by user',
	        updated_at = NOW()
	  WHERE oauth_connection_id = $1`).
		WithArgs(grantID).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`UPDATE oauth_connections
	    SET status = 'disconnected',
	        reauth_required_at = NULL,
	        last_refresh_error = NULL,
	        updated_at = NOW()
	  WHERE id = $1`).
		WithArgs(grantID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM tokens WHERE oauth_connection_id = $1`).
		WithArgs(grantID).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectGrantDisconnectOutboxAndAudit(mock sqlmock.Sqlmock, grantID, userID int64) {
	mock.ExpectExec(`INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
	 VALUES ($1, $2, $3, $4::jsonb)`).
		WithArgs("oauth_connection", grantID, "oauth_connection.disconnected", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO audit_logs (user_id, action, resource_type, resource_id, result, metadata)
	 VALUES ($1, $2, $3, $4, $5, $6::jsonb)`).
		WithArgs(userID, "oauth_grant_disconnected", "oauth_connection", grantID, "success", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestUserRepository_DisconnectOAuthGrantTx_CommitsAllStateTogether(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)

	expectGrantDisconnectBegin(mock, 55, 9)
	expectGrantDisconnectOutboxAndAudit(mock, 55, 9)
	mock.ExpectCommit()

	if err := repo.DisconnectOAuthGrantTx(context.Background(), 55); err != nil {
		t.Fatalf("DisconnectOAuthGrantTx: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUserRepository_DisconnectOAuthGrantTx_RollsBackWhenAuditInsertFails(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)

	expectGrantDisconnectBegin(mock, 55, 9)
	mock.ExpectExec(`INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
	 VALUES ($1, $2, $3, $4::jsonb)`).
		WithArgs("oauth_connection", int64(55), "oauth_connection.disconnected", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO audit_logs (user_id, action, resource_type, resource_id, result, metadata)
	 VALUES ($1, $2, $3, $4, $5, $6::jsonb)`).
		WithArgs(int64(9), "oauth_grant_disconnected", "oauth_connection", int64(55), "success", sqlmock.AnyArg()).
		WillReturnError(errors.New("audit database unavailable"))
	mock.ExpectRollback()

	if err := repo.DisconnectOAuthGrantTx(context.Background(), 55); err == nil {
		t.Fatal("DisconnectOAuthGrantTx should fail when audit insertion fails")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUserRepository_DisconnectOAuthGrantTx_RejectsInvalidIDWithoutDBWork(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)

	if err := repo.DisconnectOAuthGrantTx(context.Background(), 0); err == nil {
		t.Fatal("expected invalid id error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected database work: %v", err)
	}
}
