package repository_test

import (
	"context"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestChannelLifecycle_InFlightDatabaseTimeoutRollsBack(t *testing.T) {
	t.Run("remove from group", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT id FROM groups`).WillDelayFor(50 * time.Millisecond).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		err = repository.NewGroupRepository(db).RemoveAccountFromGroupTx(ctx, 7, 9, 101)
		if err == nil {
			t.Fatal("want timeout/cancellation error, got nil")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("disconnect channel", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT oauth_connection_id, status`).WillDelayFor(50 * time.Millisecond).
			WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id", "status"}).AddRow(55, "active"))
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		_, _, err = repository.NewUserRepository(db).DisconnectPlatformAccountTx(ctx, 21, nil)
		if err == nil {
			t.Fatal("want timeout/cancellation error, got nil")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("permanent delete", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT user_id, platform, status`).WillDelayFor(50 * time.Millisecond).
			WillReturnRows(sqlmock.NewRows([]string{"user_id", "platform", "status", "oauth_connection_id"}).AddRow(1, "instagram", "active", 0))
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		_, err = repository.NewUserRepository(db).PermanentlyDeleteAccountTx(ctx, 21, nil)
		if err == nil {
			t.Fatal("want timeout/cancellation error, got nil")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("revoke grant", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT user_id`).WillDelayFor(50 * time.Millisecond).
			WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(1))
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		err = repository.NewUserRepository(db).DisconnectOAuthGrantTx(ctx, 55)
		if err == nil {
			t.Fatal("want timeout/cancellation error, got nil")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}
