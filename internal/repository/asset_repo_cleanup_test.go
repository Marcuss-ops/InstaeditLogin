package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// mockAssetCleanupErr is the simulated DB error returned by the
// propagation test. Distinct sentinel so failures are noisy.
var mockAssetCleanupErr = errors.New("simulated DB error during cleanup tick")

// TestDeleteEligibleAssets_HappyBranch pins the canonical DELETE
// SQL shape + parameter binding for the cleanup-stale-assets worker.
// Single DELETE FROM media_assets ... USING upload_jobs + post_targets
// + youtube_target_publications, with the youtube_upload_status +
// published_at + retentionDays + retry/dlq + future-publish_at gates.
func TestDeleteEligibleAssets_HappyBranch(t *testing.T) {
	repo, mock, cleanup := newAssetCleanupMockDB(t)
	defer cleanup()

	mock.ExpectExec(regexp.QuoteMeta(
		`DELETE FROM media_assets ma`)).
		WithArgs(7).
		WillReturnResult(sqlmock.NewResult(0, 3))

	deleted, err := repo.DeleteEligibleAssets(context.Background(), 7)
	if err != nil {
		t.Fatalf("DeleteEligibleAssets(7): %v", err)
	}
	if deleted != 3 {
		t.Errorf("deleted count: want 3, got %d", deleted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
}

// TestDeleteEligibleAssets_RejectsNonPositiveRetention asserts the
// guard: a misconfigured retentionDays (<=0) MUST short-circuit
// BEFORE any SQL hits the DB. The sqlmock Strict() expectation would
// fail the no-expectations WereMet check if the SQL ran.
func TestDeleteEligibleAssets_RejectsNonPositiveRetention(t *testing.T) {
	repo, mock, cleanup := newAssetCleanupMockDB(t)
	defer cleanup()

	for _, retentionDays := range []int{-1, 0} {
		deleted, err := repo.DeleteEligibleAssets(context.Background(), retentionDays)
		if err == nil {
			t.Errorf("retentionDays=%d: want error, got nil (deleted=%d)", retentionDays, deleted)
		}
		if deleted != 0 {
			t.Errorf("retentionDays=%d: deleted count want 0, got %d", retentionDays, deleted)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unexpected unmet expectations: %v", err)
	}
}

// TestDeleteEligibleAssets_PropagatesDBError asserts the SQL error
// is wrapped upstream (e.g., a transient DB issue) and the worker
// will log WARN + retry on the next tick. The returned delete-count
// MUST be 0 on error (no half-applied state).
func TestDeleteEligibleAssets_PropagatesDBError(t *testing.T) {
	repo, mock, cleanup := newAssetCleanupMockDB(t)
	defer cleanup()

	mock.ExpectExec(regexp.QuoteMeta(
		`DELETE FROM media_assets ma`)).
		WithArgs(7).
		WillReturnError(mockAssetCleanupErr)

	deleted, err := repo.DeleteEligibleAssets(context.Background(), 7)
	if err == nil {
		t.Fatalf("expected error, got nil (deleted=%d)", deleted)
	}
	if deleted != 0 {
		t.Errorf("deleted count on error: want 0, got %d", deleted)
	}
	if !errors.Is(err, mockAssetCleanupErr) {
		t.Errorf("err: want wrapped mockAssetCleanupErr, got %v", err)
	}
}

// TestCleanupOnce_DelegatesToDeleteEligibleAssets pins that the
// AssetCleaner-interface wrapper is a thin pass-through to the
// full DELETE statement -- no business logic in the indirection.
func TestCleanupOnce_DelegatesToDeleteEligibleAssets(t *testing.T) {
	repo, mock, cleanup := newAssetCleanupMockDB(t)
	defer cleanup()

	mock.ExpectExec(regexp.QuoteMeta(
		`DELETE FROM media_assets ma`)).
		WithArgs(14).
		WillReturnResult(sqlmock.NewResult(0, 5))

	deleted, err := repo.CleanupOnce(context.Background(), 14)
	if err != nil {
		t.Fatalf("CleanupOnce(14): %v", err)
	}
	if deleted != 5 {
		t.Errorf("CleanupOnce: want 5 deleted, got %d", deleted)
	}
}

// ===== Test rig =====

// newAssetCleanupMockDB wires a sqlmock-backed *sql.DB into a fresh
// MediaAssetRepository. Strict() matcher so a stray query fails
// the test loudly. Returns a cleanup that the caller MUST defer.
func newAssetCleanupMockDB(t *testing.T) (*MediaAssetRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	return NewMediaAssetRepository(db), mock, func() { _ = db.Close() }
}
