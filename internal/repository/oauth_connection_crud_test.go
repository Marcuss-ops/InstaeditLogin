package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestFindOAuthConnectionByID_FallsBackToLegacyScopes(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := repository.NewUserRepository(db)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	query := `SELECT id, user_id, provider, provider_subject_id, provider_resource_id,
	        status, COALESCE(NULLIF(granted_scopes, '{}'::TEXT[]), scopes), last_refresh_at,
	        last_refresh_error, created_at, updated_at
         FROM oauth_connections
         WHERE id = $1`
	mock.ExpectQuery(query).WithArgs(int64(7)).WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "provider", "provider_subject_id", "provider_resource_id", "status", "scopes", "last_refresh_at", "last_refresh_error", "created_at", "updated_at"}).
			AddRow(7, 9, "youtube", "google-sub", "UC-channel", "active", pqArray("https://www.googleapis.com/auth/youtube.force-ssl"), nil, nil, now, now),
	)
	grant, err := repo.FindOAuthConnectionByID(context.Background(), 7)
	if err != nil {
		t.Fatalf("FindOAuthConnectionByID: %v", err)
	}
	if grant == nil || len(grant.GrantedScopes) != 1 || grant.GrantedScopes[0] != "https://www.googleapis.com/auth/youtube.force-ssl" {
		t.Fatalf("unexpected grant: %#v", grant)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFindOAuthConnectionByID_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := repository.NewUserRepository(db)
	query := `SELECT id, user_id, provider, provider_subject_id, provider_resource_id,
	        status, COALESCE(NULLIF(granted_scopes, '{}'::TEXT[]), scopes), last_refresh_at,
        last_refresh_error, created_at, updated_at
         FROM oauth_connections
         WHERE id = $1`
	mock.ExpectQuery(query).WithArgs(int64(99)).WillReturnError(sql.ErrNoRows)
	grant, err := repo.FindOAuthConnectionByID(context.Background(), 99)
	if err != nil || grant != nil {
		t.Fatalf("not found: grant=%#v err=%v", grant, err)
	}
}

func TestFindOAuthConnectionByID_DBError(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := repository.NewUserRepository(db)
	query := `SELECT id, user_id, provider, provider_subject_id, provider_resource_id,
	        status, COALESCE(NULLIF(granted_scopes, '{}'::TEXT[]), scopes), last_refresh_at,
        last_refresh_error, created_at, updated_at
         FROM oauth_connections
         WHERE id = $1`
	mock.ExpectQuery(query).WithArgs(int64(1)).WillReturnError(errors.New("db unavailable"))
	_, err = repo.FindOAuthConnectionByID(context.Background(), 1)
	if err == nil || !errors.Is(err, errors.Unwrap(err)) {
		t.Fatalf("expected wrapped DB error, got %v", err)
	}
}

// pqArray is a tiny sqlmock argument value for PostgreSQL TEXT[] output.
// lib/pq's array scanner accepts the wire representation returned by a
// PostgreSQL driver, which sqlmock models as a string.
func pqArray(value string) string { return "{" + value + "}" }
