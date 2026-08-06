package repository

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

const findPlatformAccountAliasQuery = `SELECT id, user_id, platform, platform_user_id, username, status, connected_at,
        last_validated_at, last_refresh_at, reauth_required_at,
        COALESCE(last_error_code, '') AS last_error_code,
        COALESCE(last_error_message, '') AS last_error_message,
        metadata, created_at, updated_at
 FROM platform_accounts
 WHERE (platform = $1 OR (platform = 'x' AND $1 = 'twitter'))
   AND platform_user_id = $2
 ORDER BY CASE WHEN platform = $1 THEN 0 ELSE 1 END, id ASC
 LIMIT 1`

func TestFindPlatformAccount_LegacyXIsReturnedAsTwitter(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectQuery(findPlatformAccountAliasQuery).
		WithArgs(models.PlatformTwitter, "x-user-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "platform", "platform_user_id", "username", "status", "connected_at",
			"last_validated_at", "last_refresh_at", "reauth_required_at", "last_error_code",
			"last_error_message", "metadata", "created_at", "updated_at",
		}).AddRow(7, 42, models.PlatformX, "x-user-1", "@creator", models.AccountStatusActive, now,
			nil, nil, nil, "", "", `{}`, now, now))

	account, err := NewUserRepository(db).FindPlatformAccount("x", "x-user-1")
	if err != nil {
		t.Fatalf("FindPlatformAccount: %v", err)
	}
	if account == nil || account.Platform != models.PlatformTwitter {
		t.Fatalf("legacy account platform: want %q, got %#v", models.PlatformTwitter, account)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreatePlatformAccount_NormalizesXBeforeInsert(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(`INSERT INTO platform_accounts (user_id, platform, platform_user_id, username, status, connected_at)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at, updated_at`).
		WithArgs(int64(42), models.PlatformTwitter, "x-user-2", "@creator", models.AccountStatusActive, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(8, time.Now(), time.Now()))

	account := &models.PlatformAccount{
		UserID:         42,
		Platform:       models.PlatformX,
		PlatformUserID: "x-user-2",
		Username:       "@creator",
	}
	if err := NewUserRepository(db).CreatePlatformAccount(account); err != nil {
		t.Fatalf("CreatePlatformAccount: %v", err)
	}
	if account.Platform != models.PlatformTwitter {
		t.Fatalf("inserted account platform: want %q, got %q", models.PlatformTwitter, account.Platform)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
