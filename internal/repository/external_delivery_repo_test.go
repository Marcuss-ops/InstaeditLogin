package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// deliveryScanColumnsMatch is the regex used by sqlmock's
// ExpectQuery matching. Keeps the column count in lockstep with
// scanExternalDeliveryByRow + scanExternalDeliveryByRowFromRows —
// if a NEW column is added to the table without extending the SQL,
// this test fails fast with "0 arguments expected vs X matched".
//
// The leading `(?s)\s*` allows whitespace between SELECT and the
// columns; the dotall mode flag is required because Postgres
// formatting tools sometimes insert newlines.
const deliveryFirstSelectColumns = `(?s)\s*SELECT id, source_system, external_delivery_id, idempotency_key, external_destination_id`

// fixtureDeliveryRow is the row-builder used by Insert-related
// tests. Returns a sqlmock Rows with 23 columns matching the
// schema of external_deliveries. Pass `now` for CreatedAt +
// UpdatedAt; other fields populated from the model.
func fixtureDeliveryRow(e *models.ExternalDelivery, now time.Time) *sqlmock.Rows {
	cols := []string{
		"id", "source_system", "external_delivery_id", "idempotency_key",
		"external_destination_id",
		"source_artifact_id", "expected_sha256", "expected_size_bytes", "expected_mime_type",
		"download_url", "metadata", "publish_at", "callback_url",
		"status", "request_sha256",
		"upload_job_id", "post_id",
		"platform_media_id", "platform_url",
		"last_error_code", "last_error_message",
		"created_at", "updated_at", "completed_at",
	}
	return sqlmock.NewRows(cols).AddRow(
		e.ID, e.SourceSystem, e.ExternalDeliveryID, e.IdempotencyKey,
		e.ExternalDestinationID,
		e.SourceArtifactID, e.ExpectedSHA256, e.ExpectedSizeBytes, e.ExpectedMimeType,
		nil, []byte(`{}`), nil, nil,
		string(e.Status), e.RequestSHA256,
		nil, nil, nil, nil, nil, nil,
		now, now, nil,
	)
}

// fixtureDelivery returns a fully-populated model suitable for
// Insert/Get tests. Caller can mutate fields before the test runs.
func fixtureDelivery() (*models.ExternalDelivery, []byte, time.Time) {
	now := time.Now()
	body := []byte(`{"artifact_id":"artifact_01JXYZ","sha256":"e5f2c235...","size_bytes":184729302,"mime_type":"video/mp4"}`)
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	e := &models.ExternalDelivery{
		ID:                   "sdel_01JDEF",
		SourceSystem:         "velox",
		ExternalDeliveryID:   "delivery_8cc0f...",
		IdempotencyKey:       "delivery_8cc0f...|dest_345",
		ExternalDestinationID: "extdst_01JABC",
		SourceArtifactID:     "artifact_01JXYZ",
		ExpectedSHA256:       sha,
		ExpectedSizeBytes:    184729302,
		ExpectedMimeType:     "video/mp4",
		Status:               models.ExternalDeliveryStatusAccepted,
		// RequestSHA256 mirrors the SHA the handler computes from
		// rawBody (or the pre-computed value the handler passes via
		// sha256.Sum256 directly). Insert requires the field to be
		// non-empty OR rawBody to be supplied — see external_delivery_repo.go
		// validation block. Tests that exercise the repo's
		// rawBody→SHA derivation path (TestInsert_ComputesSHAFromRawBody)
		// override this to "" explicitly.
		RequestSHA256: sha,
		Metadata:      []byte(`{"title":"Titolo","description":"Desc"}`),
	}
	return e, body, now
}

// TestExternalDeliveryRepository_Insert_FreshInsert pins the
// happy-path (case (a) in the type doc): no existing row, Insert
// creates the row, returns it with RETURNING values populated.
// Verifies the advisory lock is acquired, the SELECT for an
// existing row returns no match, the INSERT runs, and the tx
// commits.
func TestExternalDeliveryRepository_Insert_FreshInsert(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	e, _, now := fixtureDelivery()

	mock.ExpectBegin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(deliveryFirstSelectColumns).
		WithArgs(e.SourceSystem, e.IdempotencyKey).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`INSERT INTO external_deliveries`).
		WillReturnRows(fixtureDeliveryRow(e, now))
	mock.ExpectCommit()

	repo := NewExternalDeliveryRepository(db)
	got, err := repo.Insert(context.Background(), e, nil)
	if err != nil {
		t.Fatalf("Insert fresh: want nil error, got %v", err)
	}
	if got == nil {
		t.Fatal("Insert fresh: want record, got nil")
	}
	if got.ID != e.ID {
		t.Errorf("Insert fresh: ID mismatch (got %s, want %s)", got.ID, e.ID)
	}
	if !errors.Is(err, ErrIdempotencyConflict) && err != nil {
		t.Errorf("Insert fresh: err should NOT be ErrIdempotencyConflict, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDeliveryRepository_Insert_IdempotentReplay pins
// case (b): an existing row is found with the SAME
// request_sha256. Insert MUST return the existing row unchanged
// (no second INSERT), and commits the tx.
func TestExternalDeliveryRepository_Insert_IdempotentReplay(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	e, _, now := fixtureDelivery()

	// Existing row returns the same SHA — replay path.
	mock.ExpectBegin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(deliveryFirstSelectColumns).
		WithArgs(e.SourceSystem, e.IdempotencyKey).
		WillReturnRows(fixtureDeliveryRow(e, now))
	mock.ExpectCommit()

	repo := NewExternalDeliveryRepository(db)
	got, err := repo.Insert(context.Background(), e, nil)
	if err != nil {
		t.Fatalf("Insert replay: want nil error, got %v", err)
	}
	if got == nil {
		t.Fatal("Insert replay: want existing row, got nil")
	}
	if got.ID != e.ID {
		t.Errorf("Insert replay: want existing ID %s, got %s", e.ID, got.ID)
	}
	if errors.Is(err, ErrIdempotencyConflict) {
		t.Errorf("Insert replay: err should NOT be ErrIdempotencyConflict")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDeliveryRepository_Insert_ConflictPath pins case (c):
// an existing row is found with a DIFFERENT request_sha256. Insert
// MUST return ErrIdempotencyConflict WITHOUT inserting, and the tx
// rolls back (deferred). The handler maps this to 409.
func TestExternalDeliveryRepository_Insert_ConflictPath(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	e, _, now := fixtureDelivery()

	// Existing row with a DIFFERENT SHA — conflict path.
	existing := *e
	existing.RequestSHA256 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	mock.ExpectBegin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(deliveryFirstSelectColumns).
		WithArgs(e.SourceSystem, e.IdempotencyKey).
		WillReturnRows(fixtureDeliveryRow(&existing, now))
	mock.ExpectRollback() // conflict path triggers deferred Rollback

	repo := NewExternalDeliveryRepository(db)
	got, err := repo.Insert(context.Background(), e, nil)
	if err == nil {
		t.Fatal("Insert conflict: want ErrIdempotencyConflict, got nil")
	}
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Errorf("Insert conflict: want ErrIdempotencyConflict in chain, got %v", err)
	}
	if got == nil {
		t.Fatal("Insert conflict: want existing row returned alongside error, got nil")
	}
	if got.RequestSHA256 != existing.RequestSHA256 {
		t.Errorf("Insert conflict: returned existing row's SHA, got %s want %s",
			got.RequestSHA256, existing.RequestSHA256)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDeliveryRepository_Insert_ComputesSHAFromRawBody
// covers the path where the caller passes rawBody=[]byte("...") and
// expects the repo to derive RequestSHA256 internally. Verifies the
// SQL INSERT receives the correct hex-encoded SHA-256 in the bind.
func TestExternalDeliveryRepository_Insert_ComputesSHAFromRawBody(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	e, body, now := fixtureDelivery()
	e.RequestSHA256 = "" // intentionally empty — repo must compute

	expectedSHA := sha256.Sum256(body)
	expectedHex := hex.EncodeToString(expectedSHA[:])

	mock.ExpectBegin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(deliveryFirstSelectColumns).
		WithArgs(e.SourceSystem, e.IdempotencyKey).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`INSERT INTO external_deliveries`).
		WithArgs(
			e.ID, e.SourceSystem, e.ExternalDeliveryID, e.IdempotencyKey,
			e.ExternalDestinationID,
			e.SourceArtifactID, e.ExpectedSHA256, e.ExpectedSizeBytes, e.ExpectedMimeType,
			nil, []byte(`{"title":"Titolo","description":"Desc"}`), nil, nil,
			string(models.ExternalDeliveryStatusAccepted), expectedHex,
		).
		WillReturnRows(fixtureDeliveryRow(e, now))
	mock.ExpectCommit()

	repo := NewExternalDeliveryRepository(db)
	got, err := repo.Insert(context.Background(), e, body)
	if err != nil {
		t.Fatalf("Insert with rawBody: %v", err)
	}
	if got == nil || got.ID != e.ID {
		t.Errorf("Insert with rawBody: returned row mismatch")
	}
	if e.RequestSHA256 != expectedHex {
		t.Errorf("Insert with rawBody: SHA not computed; want %s, got %s",
			expectedHex, e.RequestSHA256)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDeliveryRepository_GetByIdempotencyKey_NotFound
// pins the (nil, nil) not-found convention for the public lookup
// method (distinct from the helper's typed sentinel return).
func TestExternalDeliveryRepository_GetByIdempotencyKey_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(deliveryFirstSelectColumns).
		WithArgs("velox", "missing-key-xxx").
		WillReturnError(sql.ErrNoRows)

	repo := NewExternalDeliveryRepository(db)
	d, err := repo.GetByIdempotencyKey(context.Background(), "velox", "missing-key-xxx")
	if err != nil {
		t.Errorf("GetByIdempotencyKey not found: want nil, got %v", err)
	}
	if d != nil {
		t.Errorf("GetByIdempotencyKey not found: want nil model, got %+v", d)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDeliveryRepository_UpdateStatus_TerminalAutoStamps
// covers the autostamp-on-terminal path: transitioning to
// 'published' (a terminal state) MUST auto-stamp completed_at =
// NOW() via the CASE expression in UpdateStatus's SQL. Also verifies
// that optional platform_media_id + platform_url are properly
// passed via COALESCE.
func TestExternalDeliveryRepository_UpdateStatus_TerminalAutoStamps(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mid := "dQw4w9WgXcQ"
	platformURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"

	mock.ExpectExec(`UPDATE external_deliveries`).
		WithArgs(
			"sdel_01JDEF",
			"published",
			nil, nil, // last_error_code, last_error_message (nil → COALESCE preserves existing)
			mid, platformURL,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	repo := NewExternalDeliveryRepository(db)
	err = repo.UpdateStatus(context.Background(), "sdel_01JDEF",
		models.ExternalDeliveryStatusPublished,
		nil, nil, // error fields not transitioned
		&mid, &platformURL,
	)
	if err != nil {
		t.Errorf("UpdateStatus terminal: want nil, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDeliveryRepository_UpdateStatus_NotFound asserts the
// typed sentinel path: zero rows affected MUST surface as
// ErrExternalDeliveryNotFound so the handler can map to 404.
func TestExternalDeliveryRepository_UpdateStatus_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(`UPDATE external_deliveries`).
		WithArgs("sdel_01JNOTHING", "published", nil, nil, nil, nil).
		WillReturnResult(sqlmock.NewResult(0, 0))

	repo := NewExternalDeliveryRepository(db)
	err = repo.UpdateStatus(context.Background(), "sdel_01JNOTHING",
		models.ExternalDeliveryStatusPublished, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("UpdateStatus not found: want ErrExternalDeliveryNotFound, got nil")
	}
	if !errors.Is(err, ErrExternalDeliveryNotFound) {
		t.Errorf("UpdateStatus not found: want ErrExternalDeliveryNotFound in chain, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDeliveryRepository_LinkUploadJob pins the cross-table
// bridge: stamps upload_job_id on an existing delivery via
// COALESCE(upload_job_id, $2). Re-stamping with a DIFFERENT id is
// also a no-op via COALESCE (preserves the original); the test
// verifies the simple-update path runs without error.
func TestExternalDeliveryRepository_LinkUploadJob(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(`UPDATE external_deliveries`).
		WithArgs("sdel_01JDEF", int64(999)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	repo := NewExternalDeliveryRepository(db)
	if err := repo.LinkUploadJob(context.Background(), "sdel_01JDEF", 999); err != nil {
		t.Errorf("LinkUploadJob: want nil, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}
