package repository_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestUserRepository_DisconnectOAuthGrantWithAccountRevocationTx_RejectsUnlinkedAccount(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT user_id
	   FROM oauth_connections
	  WHERE id = $1
	  FOR UPDATE`).
		WithArgs(int64(55)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(9))
	mock.ExpectQuery(`SELECT id
	   FROM platform_accounts
	  WHERE oauth_connection_id = $1
	  ORDER BY id
	  FOR UPDATE`).
		WithArgs(int64(55)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(21))
	mock.ExpectQuery(`SELECT provider FROM oauth_connections WHERE id = $1`).
		WithArgs(int64(55)).
		WillReturnRows(sqlmock.NewRows([]string{"provider"}).AddRow("youtube"))
	mock.ExpectRollback()

	remoteCalled := false
	err := repo.DisconnectOAuthGrantWithAccountRevocationTx(context.Background(), 55, 99, "youtube", func(context.Context, *sql.Tx) error {
		remoteCalled = true
		return nil
	})
	if err == nil || remoteCalled {
		t.Fatalf("unlinked account: err=%v remoteCalled=%v", err, remoteCalled)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
