package repository_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestTokenRepository_MarkInvalidGrantTx_UpdatesGrantAndNonDisconnectedAccounts(t *testing.T) {
	db, mock := newMockTokenDB(t)
	repo := repository.NewTokenRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE oauth_connections SET status = $2::text, last_refresh_error = NULLIF($3::text, ''), last_refresh_at = CASE WHEN $2::text = 'active' THEN NOW() ELSE last_refresh_at END, updated_at = NOW() WHERE id = $1").
		WithArgs(int64(700), "reauth_required", "invalid_grant").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE platform_accounts
		    SET status = 'reauth_required',
		        reauth_required_at = NOW(),
		        last_error_code = $1,
		        last_error_message = $2,
		        updated_at = NOW()
		  WHERE oauth_connection_id = $3
		    AND status <> 'disconnected'`).
		WithArgs("SHARED_GRANT_REAUTH_REQUIRED", "Shared OAuth grant requires reauthorization", int64(700)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := repo.MarkInvalidGrantTx(context.Background(), tx, 700, "SHARED_GRANT_REAUTH_REQUIRED", "Shared OAuth grant requires reauthorization"); err != nil {
		t.Fatalf("MarkInvalidGrantTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestTokenRepository_MarkInvalidGrantTx_RollsBackWhenAccountPropagationFails(t *testing.T) {
	db, mock := newMockTokenDB(t)
	repo := repository.NewTokenRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE oauth_connections SET status = $2::text, last_refresh_error = NULLIF($3::text, ''), last_refresh_at = CASE WHEN $2::text = 'active' THEN NOW() ELSE last_refresh_at END, updated_at = NOW() WHERE id = $1").
		WithArgs(int64(701), "reauth_required", "invalid_grant").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE platform_accounts
		    SET status = 'reauth_required',
		        reauth_required_at = NOW(),
		        last_error_code = $1,
		        last_error_message = $2,
		        updated_at = NOW()
		  WHERE oauth_connection_id = $3
		    AND status <> 'disconnected'`).
		WithArgs("SHARED_GRANT_REAUTH_REQUIRED", "Shared OAuth grant requires reauthorization", int64(701)).
		WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := repo.MarkInvalidGrantTx(context.Background(), tx, 701, "SHARED_GRANT_REAUTH_REQUIRED", "Shared OAuth grant requires reauthorization"); err == nil {
		t.Fatal("expected propagation failure")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

var _ interface {
	MarkInvalidGrantTx(context.Context, *sql.Tx, int64, string, string) error
} = (*repository.TokenRepository)(nil)
