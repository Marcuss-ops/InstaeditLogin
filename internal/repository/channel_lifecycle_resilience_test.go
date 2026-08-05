package repository_test

import (
	"context"
	"errors"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestChannelLifecycle_ContextCancellationBeforeBegin(t *testing.T) {
	tests := []struct {
		name string
		call func(context.Context, *repository.UserRepository) error
	}{
		{"disconnect channel", func(ctx context.Context, repo *repository.UserRepository) error {
			_, _, err := repo.DisconnectPlatformAccountTx(ctx, 21, nil)
			return err
		}},
		{"permanent delete", func(ctx context.Context, repo *repository.UserRepository) error {
			_, err := repo.PermanentlyDeleteAccountTx(ctx, 21, nil)
			return err
		}},
		{"revoke grant", func(ctx context.Context, repo *repository.UserRepository) error {
			return repo.DisconnectOAuthGrantTx(ctx, 55)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, mock := newMockUserDB(t)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := tc.call(ctx, repository.NewUserRepository(db))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("want context.Canceled, got %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("canceled operation performed database work: %v", err)
			}
		})
	}

	t.Run("remove from group", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err = repository.NewGroupRepository(db).RemoveAccountFromGroupTx(ctx, 7, 9, 101)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("canceled operation performed database work: %v", err)
		}
	})
}

func TestUserRepository_DisconnectPlatformAccountTx_CleanupFailureRollsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	repo := repository.NewUserRepository(db)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id, status`).WithArgs(int64(21)).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id", "status"}).AddRow(55, "active"))
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock($1)")).
		WithArgs(int64(55)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE platform_accounts`).WithArgs(int64(21)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT COUNT`).WithArgs(int64(55)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM group_accounts WHERE account_id = $1")).
		WithArgs(int64(21)).WillReturnError(errors.New("group cleanup unavailable"))
	mock.ExpectRollback()

	_, _, err = repo.DisconnectPlatformAccountTx(context.Background(), 21, nil)
	if err == nil {
		t.Fatal("cleanup failure must be returned")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("rollback expectations: %v", err)
	}
}

func TestUserRepository_PermanentlyDeleteAccount_CleanupFailureRollsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	repo := repository.NewUserRepository(db)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT user_id, platform, status, COALESCE`).WithArgs(int64(21)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "platform", "status", "oauth_connection_id"}).AddRow(1, "instagram", "active", 0))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM tokens WHERE platform_account_id = $1")).
		WithArgs(int64(21)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM group_accounts WHERE account_id = $1")).
		WithArgs(int64(21)).WillReturnError(errors.New("group cleanup unavailable"))
	mock.ExpectRollback()

	_, err = repo.PermanentlyDeleteAccountTx(context.Background(), 21, nil)
	if err == nil {
		t.Fatal("cleanup failure must be returned")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("rollback expectations: %v", err)
	}
}
