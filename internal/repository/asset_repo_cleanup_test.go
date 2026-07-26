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

// TestDeleteEligibleAssets_DeletesSafe pins the canonical
// happy-path: media_assets row's companion
// youtube_target_publications is in 'youtube_uploaded' + has
// published_at set + published_at + buffer < NOW(), with no
// peer post_target in retrying/dlq and no peer yt_pub with a
// future publish_at. The single-row DELETEs and reports 1.
//
// Narrows TestDeleteEligibleAssets_HappyBranch (3 rows) to 1 row
// so a future refactor that accidentally broadens or narrows
// the predicate lands a regression catch here rather than
// masking it under "we deleted more/less, must be right"
// reasoning.
func TestDeleteEligibleAssets_DeletesSafe(t *testing.T) {
	// Regression-class coverage: literalize ALL THREE positive WHERE
	// predicates with their ytp. alias prefix for column-precision
	// (the regex cannot accidentally match a column literalised on a
	// different subquery alias like pt2).
	//
	//   1. ytp.youtube_upload_status = 'youtube_uploaded'
	//        — canonical enum-literal lock-step (upload_worker
	//        stamps, cleanup WHERE matches). A rename breaks this.
	//   2. ytp.published_at IS NOT NULL
	//        — precondition for the age arithmetic to be
	//        well-defined (NULL + interval = NULL, never < NOW()).
	//   3. ytp.published_at + make_interval(days => $1::int) < NOW()
	//        — the buffer-arithmetic age predicate; the user
	//        spec explicitly named this. The `\$1` literalises
	//        the $1 positional placeholder (NOT regex back-ref).
	//
	// (?s)DOTALL + lazy `.*?` chains span the multi-line SQL.
	deleteSafeShape := regexp.MustCompile(`(?s)DELETE FROM media_assets ma.*?ytp\.youtube_upload_status = 'youtube_uploaded'.*?ytp\.published_at IS NOT NULL.*?ytp\.published_at \+ make_interval\(days => \$1::int\) < NOW\(\)`)

	repo, sqlMock, cleanup := newAssetCleanupMockDB(t)
	defer cleanup()

	sqlMock.ExpectExec(deleteSafeShape.String()).
		WithArgs(7).
		WillReturnResult(sqlmock.NewResult(0, 1))

	deleted, err := repo.DeleteEligibleAssets(context.Background(), 7)
	if err != nil {
		t.Fatalf("DeleteEligibleAssets(7) delete_safe: %v", err)
	}
	if deleted != 1 {
		t.Errorf("delete_safe: want 1 row deleted, got %d", deleted)
	}
	if err := sqlMock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
}

// TestDeleteEligibleAssets_RetainsFuturePublish pins the
// retain_future branch: even when the canonical happy-path
// conditions are met (yt uploaded + published + aged), if ANY
// peer row has a future-scheduled publish_at, the DELETE must
// report 0 rows deleted (the asset stays alive until that
// publish lands).
//
// The DELETE has TWO future-publish retain gates:
//   - gate #2: `posts p WHERE p.publish_at > NOW()` (parent
//     cursor future-scheduled publish)
//   - gate #3: `youtube_target_publications ytp2 WHERE
//     ytp2.publish_at > NOW()` (peer yt_pub future-scheduled
//     publish)
//
// A regression that drops EITHER subquery will fail this test's
// regex match — the (?s)DOTALL flag makes '.' match newlines so
// the multi-line SQL can be verified inline.
//
// As with TestDeleteEligibleAssets_DeletesSafe, we also assert
// the result propagation: WillReturnResult(0) → (0, nil).
func TestDeleteEligibleAssets_RetainsFuturePublish(t *testing.T) {
	// Asserts both future-publish gates are present in the emitted
	// SQL: `p.publish_at > NOW()` (posts subquery) AND
	// `ytp2.publish_at > NOW()` (youtube_target_publications subquery).
	// The `.*?` (lazy) lets the regex engine skip forward through
	// the intermediate WHERE clauses; (?s) enables DOTALL so newlines
	// don't break the pattern.
	retainFutureShape := regexp.MustCompile(`(?s)DELETE FROM media_assets ma.*?p\.publish_at > NOW\(\).*?ytp2\.publish_at > NOW\(\)`)

	repo, sqlMock, cleanup := newAssetCleanupMockDB(t)
	defer cleanup()

	sqlMock.ExpectExec(retainFutureShape.String()).
		WithArgs(7).
		WillReturnResult(sqlmock.NewResult(0, 0))

	deleted, err := repo.DeleteEligibleAssets(context.Background(), 7)
	if err != nil {
		t.Fatalf("DeleteEligibleAssets(7) retain_future: %v", err)
	}
	if deleted != 0 {
		t.Errorf("retain_future: want 0 rows deleted, got %d", deleted)
	}
	if err := sqlMock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
}

// TestDeleteEligibleAssets_RetainsRetryPeer pins the
// retain_retry branch: when ANY post_targets row on the same
// post_id is in {'retrying','dlq'}, the media_asset must stay
// alive. Operators triage the stuck target; deleting the asset
// under their feet would lose the audit trail.
//
// The DELETE has gate #1: `post_targets pt2 WHERE pt2.status IN
// ('retrying','dlq')`. A regression that drops this subquery
// fails this test's regex match loud.
//
// Real-postgres coverage lives in the integration test which
// seeds post_targets rows with status='retrying' and verifies
// the DELETE skips the parent media_asset.
func TestDeleteEligibleAssets_RetainsRetryPeer(t *testing.T) {
	retainRetryShape := regexp.MustCompile(`(?s)DELETE FROM media_assets ma.*?pt2\.post_id = uj\.post_id.*?pt2\.status IN \('retrying','dlq'\)`)

	repo, sqlMock, cleanup := newAssetCleanupMockDB(t)
	defer cleanup()

	sqlMock.ExpectExec(retainRetryShape.String()).
		WithArgs(7).
		WillReturnResult(sqlmock.NewResult(0, 0))

	deleted, err := repo.DeleteEligibleAssets(context.Background(), 7)
	if err != nil {
		t.Fatalf("DeleteEligibleAssets(7) retain_retry: %v", err)
	}
	if deleted != 0 {
		t.Errorf("retain_retry: want 0 rows deleted, got %d", deleted)
	}
	if err := sqlMock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
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
