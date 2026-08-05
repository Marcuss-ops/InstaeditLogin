package repository

import (
	"database/sql"
	"errors"
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

	if err := repo.Consume(nonce); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestConnectLinkNonceRepository_Create_EmptyExpectedChannel pins the
// YouTube OAuth Client Pool contract (migration 101): a generic "add
// channel" login flow has no channel id yet, so Create must accept an
// empty expectedChannelID and store NULL — instead of failing with 500
// before the operator reaches Google's consent screen. The jti
// single-use requirement is unchanged.
func TestConnectLinkNonceRepository_Create_EmptyExpectedChannel(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	repo := NewConnectLinkNonceRepository(db)
	nonce := "abcdef0123456789abcdef0123456789"
	expiresAt := time.Now().Add(30 * time.Minute)

	// Empty channel id → NULL is inserted, not an empty string.
	mock.ExpectExec(`INSERT INTO connect_link_nonces`).
		WithArgs(nonce, nil, expiresAt).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.Create(nonce, "", expiresAt); err != nil {
		t.Fatalf("Create with empty expected_channel_id must succeed: %v", err)
	}

	// The jti requirement is still enforced.
	if err := repo.Create("", "UC1234567890abcdefghij", expiresAt); err == nil {
		t.Fatal("Create with empty jti must fail")
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

	if err := repo.Consume(nonce); err == nil {
		t.Fatal("Consume: expected error for already-consumed nonce")
	} else if !errors.Is(err, ErrNonceConsumed) {
		t.Fatalf("Consume: expected ErrNonceConsumed, got %v", err)
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

	if err := repo.Consume(nonce); err == nil {
		t.Fatal("Consume: expected error for expired nonce")
	} else if !errors.Is(err, ErrNonceExpired) {
		t.Fatalf("Consume: expected ErrNonceExpired, got %v", err)
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

	if err := repo.Consume(nonce); err == nil {
		t.Fatal("Consume: expected error for missing nonce")
	} else if !errors.Is(err, ErrNonceMissing) {
		t.Fatalf("Consume: expected ErrNonceMissing, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}
