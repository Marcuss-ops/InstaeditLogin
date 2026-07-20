package repository

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// newAssetRepoMockDB wires a sqlmock-backed *sql.DB into the
// MediaAssetRepository so the new tests can assert the SQL byte-for-byte
// shape (the runtime guard + happy path UPDATE both must match the
// post-Task-6/10 production SQL exactly so a regression in the
// repo's query shape gets caught at the unit-test layer instead of
// only at the integration test layer).
func newAssetRepoMockDB(t *testing.T) (*MediaAssetRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	repo := NewMediaAssetRepository(db)
	return repo, mock, func() { _ = db.Close() }
}

// TestMediaAssetRepository_MarkReady_RejectsEmptySHA — Task 6/10
// runtime guard. The empty-SHA path MUST skip the UPDATE entirely
// (returns ErrMediaAssetSHARequired before any DB roundtrip) so
// the per-MarkReady SQL doesn't accidentally accept the empty
// sentinel — the migration 056 NOT NULL constraint accepts ''
// as a non-NULL value, so a repo regression that drops the
// guard would silently preserve the empty sentinel at the SQL
// layer. Locking the guard via sqlmock at the repo unit-test
// boundary catches such a regression here rather than in
// pkg/api/media_test (where the mockMediaStore doesn't replicate
// the repo's runtime logic).
func TestMediaAssetRepository_MarkReady_RejectsEmptySHA(t *testing.T) {
	repo, mock, cleanup := newAssetRepoMockDB(t)
	defer cleanup()

	// No sqlmock.ExpectExec — the runtime guard rejects BEFORE
	// hitting the DB. If a future change moves the guard past the
	// UPDATE call, this assertion breaks at compile time of the
	// squel ExpectationsComplete called below.
	err := repo.MarkReady("00000000-0000-4000-8000-000000000001", "", 1024, "video/mp4")
	if !errors.Is(err, ErrMediaAssetSHARequired) {
		t.Fatalf("MarkReady(id, \"\", ...) with empty SHA: expected wrapped ErrMediaAssetSHARequired, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expected no DB calls (guard rejects before UPDATE); got unmet expectations: %v", err)
	}
}

// TestMediaAssetRepository_MarkReady_HappyPath_SHAStamp — Task 6/10
// happy path. The UPDATE must assign sha256=$2 directly (no COALESCE;
// the runtime guard guarantees $2 is non-empty so the COALESCE was
// removed as dead code). This regex matcher locks both the column
// projection AND the parameter binding — a regression that re-added
// `COALESCE(NULLIF($2, ''), sha256)` or that read sha256 from a
// different variable would trip the matcher.
func TestMediaAssetRepository_MarkReady_HappyPath_SHAStamp(t *testing.T) {
	repo, mock, cleanup := newAssetRepoMockDB(t)
	defer cleanup()

	const (
		assetID    = "00000000-0000-4000-8000-000000000001"
		goodSHA    = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		mediaCT    = "video/mp4"
		sizeBytesI = int64(1024)
	)

	// Regex matcher so the test isn't fragile to whitespace or
	// parameter ordering. Anchored to the COALESCE-free UPDATE form
	// post-Task-6/10.
	mock.ExpectExec(`UPDATE media_assets
		    SET status = \$1, sha256 = \$2,
		        size_bytes = \$3, content_type = \$4, error_message = '', updated_at = \$5
		  WHERE id = \$6`).
		WithArgs(
			"ready",   // MediaAssetStatusReady
			goodSHA,   // $2 — direct sha256, no COALESCE
			sizeBytesI,
			mediaCT,
			sqlmock.AnyArg(), // updated_at — time.Now() can't be predicted
			assetID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.MarkReady(assetID, goodSHA, sizeBytesI, mediaCT); err != nil {
		t.Fatalf("MarkReady happy path: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expected UPDATE with sha256=$2 (no COALESCE); got unmet: %v", err)
	}
}

// TestMediaAssetRepository_MarkReady_MissingRow_NotFound — locks the
// ErrMediaAssetNotFound wrap on the rows-affected=0 path so a future
// refactor that drops the check would surface "asset transitioned
// to ready but the row doesn't exist" as a real bug instead of a
// silent no-op.
func TestMediaAssetRepository_MarkReady_MissingRow_NotFound(t *testing.T) {
	repo, mock, cleanup := newAssetRepoMockDB(t)
	defer cleanup()

	const (
		assetID = "00000000-0000-4000-8000-000000000099"
		goodSHA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	)

	mock.ExpectExec(`UPDATE media_assets.*WHERE id = \$6`).
		WithArgs(
			"ready",
			goodSHA,
			int64(512),
			"video/mp4",
			sqlmock.AnyArg(),
			assetID,
		).
		WillReturnResult(sqlmock.NewResult(0, 0)) // 0 rows affected

	err := repo.MarkReady(assetID, goodSHA, 512, "video/mp4")
	if !errors.Is(err, ErrMediaAssetNotFound) {
		t.Fatalf("expected wrapped ErrMediaAssetNotFound when row absent; got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expected UPDATE returning 0 rows; got unmet: %v", err)
	}
}
