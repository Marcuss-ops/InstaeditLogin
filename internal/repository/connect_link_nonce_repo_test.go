package repository

import (
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestConnectLinkNonceRepository_CreateAndConsume(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	repo := NewConnectLinkNonceRepository(db)
	nonce := "abcdef0123456789abcdef0123456789"
	expectedChannelID := "UC1234567890abcdefghij"
	expiresAt := time.Now().Add(30 * time.Minute)

	mock.ExpectExec(`INSERT INTO connect_link_nonces`).
		WithArgs(nonce, expectedChannelID, expiresAt).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.Create(nonce, expectedChannelID, expiresAt); err != nil {
		t.Fatalf("Create: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT expires_at, consumed_at FROM connect_link_nonces WHERE nonce = \$1 FOR UPDATE`).
		WithArgs(nonce).
		WillReturnRows(sqlmock.NewRows([]string{"expires_at", "consumed_at"}).AddRow(expiresAt, nil))
	mock.ExpectExec(`UPDATE connect_link_nonces SET consumed_at = NOW\(\) WHERE nonce = \$1 AND consumed_at IS NULL`).
		WithArgs(nonce).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	consumed, err := repo.Consume(nonce)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if !consumed {
		t.Fatal("Consume: expected consumed=true")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

func TestConnectLinkNonceRepository_Consume_AlreadyConsumed(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	repo := NewConnectLinkNonceRepository(db)
	nonce := "deadbeef0123456789abcdef01234567"
	now := time.Now()
	consumedAt := now.Add(-5 * time.Minute)
	expiresAt := now.Add(30 * time.Minute)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT expires_at, consumed_at FROM connect_link_nonces WHERE nonce = \$1 FOR UPDATE`).
		WithArgs(nonce).
		WillReturnRows(sqlmock.NewRows([]string{"expires_at", "consumed_at"}).AddRow(expiresAt, consumedAt))
	mock.ExpectRollback()

	consumed, err := repo.Consume(nonce)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if consumed {
		t.Fatal("Consume: expected consumed=false for already-consumed nonce")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

func TestConnectLinkNonceRepository_Consume_Expired(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	repo := NewConnectLinkNonceRepository(db)
	nonce := "cafebabe0123456789abcdef01234567"
	expiresAt := time.Now().Add(-5 * time.Minute)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT expires_at, consumed_at FROM connect_link_nonces WHERE nonce = \$1 FOR UPDATE`).
		WithArgs(nonce).
		WillReturnRows(sqlmock.NewRows([]string{"expires_at", "consumed_at"}).AddRow(expiresAt, nil))
	mock.ExpectRollback()

	consumed, err := repo.Consume(nonce)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if consumed {
		t.Fatal("Consume: expected consumed=false for expired nonce")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

func TestConnectLinkNonceRepository_Consume_Missing(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	repo := NewConnectLinkNonceRepository(db)
	nonce := "00000000000000000000000000000000"

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT expires_at, consumed_at FROM connect_link_nonces WHERE nonce = \$1 FOR UPDATE`).
		WithArgs(nonce).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	consumed, err := repo.Consume(nonce)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if consumed {
		t.Fatal("Consume: expected consumed=false for missing nonce")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}
