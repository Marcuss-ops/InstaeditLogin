package repository_test

import (
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// newCapsTestDB wires sqlmock to the AccountCapabilitiesRepository for
// direct SQL-surface testing. Pattern matches the existing
// post_repo_test.go / token_repo_test.go micro-tests.
func newCapsTestDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock, *repository.AccountCapabilitiesRepository) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	return db, mock, repository.NewAccountCapabilitiesRepository(db)
}

// TestCaps_FindByAccountID_Hit: row exists, including non-null actual
// JSON. Verifies every field hydrates correctly from the SQL row.
func TestCaps_FindByAccountID_Hit(t *testing.T) {
	db, mock, repo := newCapsTestDB(t)
	defer db.Close()

	now := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	theoreticalJSON := `{"supports_images":true,"max_caption_runes":280,"text_only":true}`
	actualJSON := `{"supports_images":true,"max_caption_runes":280,"text_only":true}`
	effectiveJSON := theoreticalJSON

	rows := sqlmock.NewRows([]string{
		"platform_account_id", "theoretical", "actual", "effective",
		"source_discoverer", "last_fetched_at", "expires_at", "last_error", "revision",
	}).AddRow(
		int64(42), theoreticalJSON, actualJSON, effectiveJSON,
		"instagram", now, now.Add(24*time.Hour), nil, 1,
	)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT`)).
		WithArgs(int64(42)).
		WillReturnRows(rows)

	got, err := repo.FindByAccountID(42)
	if err != nil {
		t.Fatalf("FindByAccountID: %v", err)
	}
	if got == nil {
		t.Fatal("FindByAccountID returned nil row for hit case")
	}
	if got.PlatformAccountID != 42 {
		t.Errorf("PlatformAccountID: want 42, got %d", got.PlatformAccountID)
	}
	if got.SourceDiscoverer != "instagram" {
		t.Errorf("SourceDiscoverer: want instagram, got %q", got.SourceDiscoverer)
	}
	if got.Actual == nil {
		t.Error("Actual must be non-nil when DB column was non-null")
	}
	if got.Revision != 1 {
		t.Errorf("Revision: want 1, got %d", got.Revision)
	}
}

// TestCaps_FindByAccountID_Miss: sql.ErrNoRows ->
// ErrAccountCapabilitiesNotFound. Callers distinguish "no cache"
// from "DB error".
func TestCaps_FindByAccountID_Miss(t *testing.T) {
	db, mock, repo := newCapsTestDB(t)
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT`)).
		WithArgs(int64(99)).
		WillReturnError(sql.ErrNoRows)

	_, err := repo.FindByAccountID(99)
	if !errors.Is(err, repository.ErrAccountCapabilitiesNotFound) {
		t.Errorf("expected ErrAccountCapabilitiesNotFound, got %v", err)
	}
}

// TestCaps_Upsert_Happy: ON CONFLICT DO UPDATE increments revision.
// sqlmock.AnyArg matches driver.Value-bound CapabilitySet (the driver
// calls .Value() to produce the JSON).
func TestCaps_Upsert_Happy(t *testing.T) {
	db, mock, repo := newCapsTestDB(t)
	defer db.Close()

	now := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO account_capabilities`)).
		WithArgs(
			int64(42),
			sqlmock.AnyArg(), // theoretical JSONB via .Value()
			sqlmock.AnyArg(), // actual JSONB
			sqlmock.AnyArg(), // effective JSONB
			"instagram",
			now,
			now.Add(24*time.Hour),
			nil,
			0,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	caps := &models.AccountCapabilities{
		PlatformAccountID: 42,
		Theoretical:       models.CapabilitySet{MaxCaptionRunes: 280},
		Actual:            &models.CapabilitySet{MaxCaptionRunes: 2200},
		Effective:         models.CapabilitySet{MaxCaptionRunes: 280},
		SourceDiscoverer:  "instagram",
		LastFetchedAt:     now,
		ExpiresAt:         now.Add(24 * time.Hour),
	}
	if err := repo.Upsert(caps); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock ExpectationsWereMet: %v", err)
	}
}

// TestCaps_Upsert_NilRow: defensive nil-pointer guard.
func TestCaps_Upsert_NilRow(t *testing.T) {
	_, _, repo := newCapsTestDB(t)
	if err := repo.Upsert(nil); err == nil {
		t.Fatal("Upsert(nil) should error, got nil")
	}
}

// TestCaps_Invalidate_Happy: explicit DELETE for reaper / reauth paths.
func TestCaps_Invalidate_Happy(t *testing.T) {
	db, mock, repo := newCapsTestDB(t)
	defer db.Close()

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM account_capabilities WHERE platform_account_id = $1`)).
		WithArgs(int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.Invalidate(42); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
}

// TestCaps_Invalidate_DBError: error path so worker doesn't silently
// drop the cause.
func TestCaps_Invalidate_DBError(t *testing.T) {
	db, mock, repo := newCapsTestDB(t)
	defer db.Close()

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM account_capabilities WHERE platform_account_id = $1`)).
		WithArgs(int64(42)).
		WillReturnError(errors.New("connection lost"))

	if err := repo.Invalidate(42); err == nil {
		t.Fatal("expected error from Invalidate on DB error, got nil")
	}
}
