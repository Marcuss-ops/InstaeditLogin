package credentials

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestNewFixtureEncryptorRoundTrip(t *testing.T) {
	encryptor, err := NewFixtureEncryptor()
	if err != nil {
		t.Fatalf("NewFixtureEncryptor: %v", err)
	}

	ciphertext, err := encryptor.Encrypt("fixture-secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if string(ciphertext) == "fixture-secret" {
		t.Fatal("fixture ciphertext must not contain plaintext")
	}

	plaintext, err := encryptor.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if plaintext != "fixture-secret" {
		t.Fatalf("round-trip plaintext: got %q, want fixture-secret", plaintext)
	}
}

func TestResolveOAuthConnectionID_ExistingLineage(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT user_id, platform, platform_user_id, oauth_connection_id`).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "platform", "platform_user_id", "oauth_connection_id"}).
			AddRow(int64(7), "youtube", "UCfixture", int64(99)))
	mock.ExpectCommit()

	if got := ResolveOAuthConnectionID(t, db, 42); got != 99 {
		t.Fatalf("ResolveOAuthConnectionID: got %d, want 99", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL expectations: %v", err)
	}
}

func TestSeedRefreshableBearerToken_UsesProductionTokensLineage(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	createdAt := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT user_id, platform, platform_user_id, oauth_connection_id`).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "platform", "platform_user_id", "oauth_connection_id"}).
			AddRow(int64(7), "youtube", "UCfixture", nil))
	mock.ExpectQuery(`INSERT INTO oauth_connections`).
		WithArgs(int64(7), "youtube", "UCfixture").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(99)))
	mock.ExpectExec(`UPDATE platform_accounts`).
		WithArgs(int64(99), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`INSERT INTO tokens`).
		WithArgs(int64(42), int64(99), models.TokenTypeBearer, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(123), createdAt))
	mock.ExpectCommit()

	fixture := SeedRefreshableBearerToken(t, db, 42)
	if fixture.OAuthConnectionID != 99 || fixture.TokenID != 123 {
		t.Fatalf("fixture identity: got %+v, want connection=99 token=123", fixture)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL expectations: %v", err)
	}
}
