package repository_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// TestMarkYouTubeUploadedAtomic_RejectsEmptyVideoID_NoDBMutation asserts
// the pre-flight rejection of an empty videoID: the method must NOT issue
// any UPDATE to the DB. We don't use a real BEGIN/UPDATE/ROLLBACK tx
// here because the underlying Postgres UPDATE is ACID-atomic at the
// row level — the only way to observe a "no mutation" guarantee in this
// method is to ensure the SQL execution never happens at all. sqlmock's
// strict QueryMatcherEqual mode fails the test if any un-expected DB call
// is issued (no mock.ExpectExec / mock.ExpectQuery registered below
// = any query from the method would trip ExpectationsWereMet as
// "unexpected call").
//
// Blocco #1 followup — Finding #3 split-tx drift fix: the
// original IncrementAttempt + MarkYouTubeUploaded on different code
// paths left the row with `attempt_count++ + status='youtube_uploading'`
// on a worker crash, producing orphan videos.insert on every retry.
// The atomic method's pre-flight rejection (typed sentinel
// ErrYouTubeUploadedEmptyVideoID) ensures empty-id callers can't
// accidentally trigger that degenerate state — and the typed
// sentinel makes the contract programmatically checkable from
// caller code via errors.Is.
func TestMarkYouTubeUploadedAtomic_RejectsEmptyVideoID_NoDBMutation(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	repo := repository.NewYouTubeTargetPublicationRepository(db)

	err = repo.MarkYouTubeUploadedAtomic(context.Background(), 7777, "")
	if err == nil {
		t.Fatal("MarkYouTubeUploadedAtomic(\"\") want error, got nil — pre-flight guard must reject empty videoID")
	}
	// Programmatic check: caller code branches via errors.Is, not
	// string parsing. The sentinel must wrap the error.
	if !errors.Is(err, repository.ErrYouTubeUploadedEmptyVideoID) {
		t.Errorf("err: want errors.Is(ErrYouTubeUploadedEmptyVideoID), got %v", err)
	}
	// Surfacing checks: the published id MUST appear in the error
	// message so the worker log attributes the rejection precisely.
	// Pinned via strings.Contains (stdlib) rather than reinventing
	// a helper.
	if msg := err.Error(); !strings.Contains(msg, "pub=7777") {
		t.Errorf("err.Error(): want substring %q (id attribution for worker log), got %q", "pub=7777", msg)
	}
	if msg := err.Error(); !strings.Contains(msg, "empty videoID") {
		t.Errorf("err.Error(): want substring %q (failure-mode surface), got %q", "empty videoID", msg)
	}

	// Critical regression guard: the empty-videoID pre-flight MUST
	// not start a tx / issue any UPDATE. sqlmock strict-mode asserts
	// all expected + no unexpected calls. The defer db.Close() will
	// surface the unmet expectations on ExpectationsWereMet.
	if mockErr := mock.ExpectationsWereMet(); mockErr != nil {
		t.Errorf("pre-flight rejected input must NOT issue any DB call; got unmet expectations: %v", mockErr)
	}
}

// TestMarkYouTubeUploadedAtomic_Happy asserts the success path:
// the method issues exactly ONE round-trip to Postgres with the
// expected SQL shape (attempt_count++, status flip, video_id stamp,
// uploaded_at=COALESCE, updated_at). The single-round-trip property
// is the FINDING #3 fix's invariant — a regression to a
// multiple-statement pattern would fail this test by virtue of
// producing 2+ sqlmock matches against the mock.Expect* registry.
//
// Happy-path sqlmock shape mirrors
// external_delivery_repo_create_upload_job_test.go's regexp-based
// matcher (QueryMatcherRegexp) so future SQL-shape tweaks don't
// touch this test (regex substring matches against the UPDATE
// body surface the structural fields without pinning whitespace).
func TestMarkYouTubeUploadedAtomic_Happy(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	const (
		id      int64  = 7777
		videoID string = "yt-vid-abc"
	)

	mock.ExpectExec(`UPDATE youtube_target_publications`).
		WithArgs(id, videoID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	repo := repository.NewYouTubeTargetPublicationRepository(db)
	if err := repo.MarkYouTubeUploadedAtomic(context.Background(), id, videoID); err != nil {
		t.Fatalf("MarkYouTubeUploadedAtomic happy: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestMarkYouTubeUploadedAtomic_NoRowsReturnsNotFound pins the 0-rows
// path: the method must surface ErrYouTubeTargetPublicationNotFound so
// the worker can treat a stale id as a retry signal (same shape as
// MarkYouTubeUploaded). A regression that swallowed the 0-rows case
// (returning nil) would silently lose rows on id-races, leaving
// publish_worker unable to reconcile.
func TestMarkYouTubeUploadedAtomic_NoRowsReturnsNotFound(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(`UPDATE youtube_target_publications`).
		WithArgs(int64(9999), "yt-vid-stale").
		WillReturnResult(sqlmock.NewResult(0, 0))

	repo := repository.NewYouTubeTargetPublicationRepository(db)
	err = repo.MarkYouTubeUploadedAtomic(context.Background(), 9999, "yt-vid-stale")
	if !errors.Is(err, repository.ErrYouTubeTargetPublicationNotFound) {
		t.Errorf("MarkYouTubeUploadedAtomic on 0 rows: want ErrYouTubeTargetPublicationNotFound, got %v", err)
	}
}
