package repository_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// The queries must match the repository byte-for-byte (the test
// harness uses QueryMatcherEqual). Kept in sync with
// internal/repository/oauth_capacity.go.
const countActiveRefreshTokensSQL = `SELECT COUNT(DISTINCT oc.id)
  FROM oauth_connections oc
  JOIN tokens t ON t.oauth_connection_id = oc.id
 WHERE oc.provider = 'youtube'
   AND oc.provider_subject_id = $1
   AND oc.oauth_client_key = $2
   AND oc.status = 'active'
   AND t.token_type = 'bearer'
   AND t.encrypted_refresh_token IS NOT NULL
   AND octet_length(t.encrypted_refresh_token) > 0`

const listPoolUsageSQL = `SELECT oc.provider_subject_id,
       oc.oauth_client_key,
       COUNT(DISTINCT oc.id) AS active_refresh_grants
  FROM oauth_connections oc
  JOIN tokens t ON t.oauth_connection_id = oc.id
 WHERE oc.provider = 'youtube'
   AND oc.provider_subject_id = $1
   AND oc.status = 'active'
   AND t.token_type = 'bearer'
   AND t.encrypted_refresh_token IS NOT NULL
   AND octet_length(t.encrypted_refresh_token) > 0
 GROUP BY oc.provider_subject_id, oc.oauth_client_key`

func TestCountActiveRefreshTokens_ReturnsCount(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := repository.NewOAuthTokenCapacityRepository(db)

	mock.ExpectQuery(countActiveRefreshTokensSQL).
		WithArgs("google-subject-1", "youtube_pool_a").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(48)))

	count, err := repo.CountActiveRefreshTokens(context.Background(), "google-subject-1", "youtube_pool_a")
	if err != nil {
		t.Fatalf("CountActiveRefreshTokens: %v", err)
	}
	if count != 48 {
		t.Errorf("count: want 48, got %d", count)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCountActiveRefreshTokens_DBError(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := repository.NewOAuthTokenCapacityRepository(db)

	mock.ExpectQuery(countActiveRefreshTokensSQL).
		WithArgs("google-subject-1", "youtube_pool_a").
		WillReturnError(errors.New("db unavailable"))

	if _, err := repo.CountActiveRefreshTokens(context.Background(), "google-subject-1", "youtube_pool_a"); err == nil {
		t.Fatal("CountActiveRefreshTokens: want wrapped DB error, got nil")
	}
}

func TestCountActiveRefreshTokens_EmptySubjectRejected(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := repository.NewOAuthTokenCapacityRepository(db)

	if _, err := repo.CountActiveRefreshTokens(context.Background(), "", "youtube_pool_a"); err == nil {
		t.Fatal("empty subject: want error, got nil")
	}
	if _, err := repo.CountActiveRefreshTokens(context.Background(), "google-subject-1", ""); err == nil {
		t.Fatal("empty client key: want error, got nil")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListPoolUsage_ReturnsRows(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := repository.NewOAuthTokenCapacityRepository(db)

	mock.ExpectQuery(listPoolUsageSQL).
		WithArgs("google-subject-1").
		WillReturnRows(sqlmock.NewRows([]string{"provider_subject_id", "oauth_client_key", "active_refresh_grants"}).
			AddRow("google-subject-1", "youtube_pool_a", int64(48)).
			AddRow("google-subject-1", "youtube_pool_b", int64(43)))

	rows, err := repo.ListPoolUsage(context.Background(), "google-subject-1")
	if err != nil {
		t.Fatalf("ListPoolUsage: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[0].OAuthClientKey != "youtube_pool_a" || rows[0].ActiveRefreshTokens != 48 {
		t.Errorf("row[0]: want (youtube_pool_a, 48), got %+v", rows[0])
	}
	if rows[1].OAuthClientKey != "youtube_pool_b" || rows[1].ActiveRefreshTokens != 43 {
		t.Errorf("row[1]: want (youtube_pool_b, 43), got %+v", rows[1])
	}
	if rows[0].ProviderSubjectID != "google-subject-1" {
		t.Errorf("row[0] subject: want google-subject-1, got %q", rows[0].ProviderSubjectID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListPoolUsage_EmptyClientKeyDefaultsToPoolA(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := repository.NewOAuthTokenCapacityRepository(db)

	mock.ExpectQuery(listPoolUsageSQL).
		WithArgs("google-subject-1").
		WillReturnRows(sqlmock.NewRows([]string{"provider_subject_id", "oauth_client_key", "active_refresh_grants"}).
			AddRow("google-subject-1", "", int64(5)))

	rows, err := repo.ListPoolUsage(context.Background(), "google-subject-1")
	if err != nil {
		t.Fatalf("ListPoolUsage: %v", err)
	}
	if len(rows) != 1 || rows[0].OAuthClientKey != "youtube_pool_a" {
		t.Errorf("legacy empty client key must default to youtube_pool_a, got %+v", rows)
	}
}

func TestListPoolUsage_NoRows_ReturnsEmpty(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := repository.NewOAuthTokenCapacityRepository(db)

	mock.ExpectQuery(listPoolUsageSQL).
		WithArgs("google-subject-1").
		WillReturnRows(sqlmock.NewRows([]string{"provider_subject_id", "oauth_client_key", "active_refresh_grants"}))

	rows, err := repo.ListPoolUsage(context.Background(), "google-subject-1")
	if err != nil {
		t.Fatalf("ListPoolUsage: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("want 0 rows, got %d", len(rows))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListPoolUsage_DBError(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := repository.NewOAuthTokenCapacityRepository(db)

	mock.ExpectQuery(listPoolUsageSQL).
		WithArgs("google-subject-1").
		WillReturnError(errors.New("db unavailable"))

	if _, err := repo.ListPoolUsage(context.Background(), "google-subject-1"); err == nil {
		t.Fatal("ListPoolUsage: want wrapped DB error, got nil")
	}
}

func TestListPoolUsage_EmptySubjectRejected(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := repository.NewOAuthTokenCapacityRepository(db)

	if _, err := repo.ListPoolUsage(context.Background(), ""); err == nil {
		t.Fatal("empty subject: want error, got nil")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
