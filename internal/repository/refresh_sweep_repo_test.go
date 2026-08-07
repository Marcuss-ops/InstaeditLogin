package repository_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// newMockSweepDB returns a (*sql.DB, sqlmock.Sqlmock) trio wired to a
// single sqlmock controller with STRICT equality matching — the query
// must match repository.SQLListDormantRefreshGrants byte-for-byte, so
// a production-side SQL drift fails the test instead of silently
// matching a loose regexp.
func newMockSweepDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

// TestRefreshSweep_ListDormantGrants_SelectsAtRiskGrants pins the
// happy path: the horizon (120d) flows through as $1, the 7-day TTL
// window as $2, and every returned row is scanned into
// models.DormantRefreshGrant preserving provider + both ids.
func TestRefreshSweep_ListDormantGrants_SelectsAtRiskGrants(t *testing.T) {
	db, mock := newMockSweepDB(t)
	repo := repository.NewRefreshSweepRepository(db)

	rows := sqlmock.NewRows([]string{"oauth_connection_id", "platform_account_id", "provider"}).
		AddRow(int64(1), int64(10), "youtube").
		AddRow(int64(2), int64(20), "google-drive")
	mock.ExpectQuery(repository.SQLListDormantRefreshGrants).
		WithArgs(120, "7 days", "15 minutes").
		WillReturnRows(rows)

	grants, err := repo.ListDormantRefreshGrants(context.Background(), 120)
	if err != nil {
		t.Fatalf("ListDormantRefreshGrants: %v", err)
	}
	if len(grants) != 2 {
		t.Fatalf("want 2 grants, got %d: %+v", len(grants), grants)
	}
	want := []struct {
		ocID, paID int64
		provider   string
	}{
		{1, 10, "youtube"},
		{2, 20, "google-drive"},
	}
	for i, w := range want {
		if grants[i].OAuthConnectionID != w.ocID || grants[i].PlatformAccountID != w.paID || grants[i].Provider != w.provider {
			t.Errorf("grant[%d]: want (%d,%d,%q), got (%d,%d,%q)",
				i, w.ocID, w.paID, w.provider,
				grants[i].OAuthConnectionID, grants[i].PlatformAccountID, grants[i].Provider)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestRefreshSweep_ListDormantGrants_Empty pins the empty result
// contract: no rows → empty slice, nil error (the worker skips the
// pass when len==0 without logging noise).
func TestRefreshSweep_ListDormantGrants_Empty(t *testing.T) {
	db, mock := newMockSweepDB(t)
	repo := repository.NewRefreshSweepRepository(db)

	rows := sqlmock.NewRows([]string{"oauth_connection_id", "platform_account_id", "provider"})
	mock.ExpectQuery(repository.SQLListDormantRefreshGrants).
		WithArgs(150, "7 days", "15 minutes").
		WillReturnRows(rows)

	grants, err := repo.ListDormantRefreshGrants(context.Background(), 150)
	if err != nil {
		t.Fatalf("ListDormantRefreshGrants: %v", err)
	}
	if len(grants) != 0 {
		t.Errorf("want 0 grants, got %d", len(grants))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestRefreshSweep_ListDormantGrants_ZeroHorizonUsesDefault pins the
// defensive default: horizonDays <= 0 falls back to 120 (the same
// default the worker applies), so a zero-value config still selects
// the intended cohort.
func TestRefreshSweep_ListDormantGrants_ZeroHorizonUsesDefault(t *testing.T) {
	db, mock := newMockSweepDB(t)
	repo := repository.NewRefreshSweepRepository(db)

	rows := sqlmock.NewRows([]string{"oauth_connection_id", "platform_account_id", "provider"})
	mock.ExpectQuery(repository.SQLListDormantRefreshGrants).
		WithArgs(repository.DefaultRefreshSweepHorizonDays, "7 days", "15 minutes").
		WillReturnRows(rows)

	if _, err := repo.ListDormantRefreshGrants(context.Background(), 0); err != nil {
		t.Fatalf("ListDormantRefreshGrants: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestRefreshSweep_ListDormantGrants_QueryError pins the error
// propagation: a DB failure surfaces wrapped, never swallowed.
func TestRefreshSweep_ListDormantGrants_QueryError(t *testing.T) {
	db, mock := newMockSweepDB(t)
	repo := repository.NewRefreshSweepRepository(db)

	mock.ExpectQuery(repository.SQLListDormantRefreshGrants).
		WithArgs(120, "7 days", "15 minutes").
		WillReturnError(sql.ErrConnDone)

	if _, err := repo.ListDormantRefreshGrants(context.Background(), 120); err == nil {
		t.Fatal("want error, got nil")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// ---------------------------------------------------------------------
// Single-flighted selection (ListDormantRefreshGrantsSingleFlighted)
// ---------------------------------------------------------------------

// TestRefreshSweep_SingleFlighted_WinsLock_SelectsAndCommits pins the
// winner path: the tx acquires pg_try_advisory_xact_lock(RefreshSweepLockID),
// runs the selection INSIDE the lock tx, commits, and reports won=true
// with the selected grants.
func TestRefreshSweep_SingleFlighted_WinsLock_SelectsAndCommits(t *testing.T) {
	db, mock := newMockSweepDB(t)
	repo := repository.NewRefreshSweepRepository(db)

	rows := sqlmock.NewRows([]string{"oauth_connection_id", "platform_account_id", "provider"}).
		AddRow(int64(1), int64(10), "youtube")
	mock.ExpectBegin()
	mock.ExpectQuery(repository.SQLRefreshSweepSingleFlightLock).
		WithArgs(repository.RefreshSweepLockID).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_xact_lock"}).AddRow(true))
	mock.ExpectQuery(repository.SQLListDormantRefreshGrants).
		WithArgs(120, "7 days", "15 minutes").
		WillReturnRows(rows)
	mock.ExpectCommit()

	grants, won, err := repo.ListDormantRefreshGrantsSingleFlighted(context.Background(), 120)
	if err != nil {
		t.Fatalf("ListDormantRefreshGrantsSingleFlighted: %v", err)
	}
	if !won {
		t.Error("want won=true on the winning replica, got false")
	}
	if len(grants) != 1 || grants[0].PlatformAccountID != 10 {
		t.Errorf("want 1 grant (account 10), got %+v", grants)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestRefreshSweep_SingleFlighted_LockHeldByOtherReplica_Skips pins
// the fan-out guard: when pg_try_advisory_xact_lock returns false
// (another replica owns the tick), the method returns won=false with
// NO grants and NO selection query — the tx is rolled back (which
// auto-releases the lock). This is the whole point of the
// single-flight: losers must not run the SELECT.
func TestRefreshSweep_SingleFlighted_LockHeldByOtherReplica_Skips(t *testing.T) {
	db, mock := newMockSweepDB(t)
	repo := repository.NewRefreshSweepRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(repository.SQLRefreshSweepSingleFlightLock).
		WithArgs(repository.RefreshSweepLockID).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_xact_lock"}).AddRow(false))
	mock.ExpectRollback()

	grants, won, err := repo.ListDormantRefreshGrantsSingleFlighted(context.Background(), 120)
	if err != nil {
		t.Fatalf("ListDormantRefreshGrantsSingleFlighted: %v", err)
	}
	if won {
		t.Error("want won=false when the lock is held by another replica, got true")
	}
	if len(grants) != 0 {
		t.Errorf("want 0 grants on a lost tick (SELECT must not run), got %+v", grants)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestRefreshSweep_SingleFlighted_LockQueryError pins the lock-acquire
// failure path: a DB error on pg_try_advisory_xact_lock surfaces
// wrapped (never swallowed as a silent skip).
func TestRefreshSweep_SingleFlighted_LockQueryError(t *testing.T) {
	db, mock := newMockSweepDB(t)
	repo := repository.NewRefreshSweepRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(repository.SQLRefreshSweepSingleFlightLock).
		WithArgs(repository.RefreshSweepLockID).
		WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()

	if _, _, err := repo.ListDormantRefreshGrantsSingleFlighted(context.Background(), 120); err == nil {
		t.Fatal("want error, got nil")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
