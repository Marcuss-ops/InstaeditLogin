package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// Tests for DeliverySessionRepository.ResetForReinitiate — the in-place
// reset that replaced the delete + re-Create recovery cycle. Contract:
//
//  1. attempt_count = attempt_count + 1 (monotonic across recoveries —
//     the property that makes the destination's re-initiate budget
//     enforceable) and the new count is returned.
//  2. Upload-progress fields are cleared in place (state='initiated',
//     session_uri_encrypted=NULL, uploaded_bytes=0, remote ids NULL,
//     error fields NULL) — no DELETE, no INSERT.
//  3. CAS: expectedVersion mismatch (0 rows) surfaces
//     ErrDeliverySessionVersionMismatch.
//  4. Invalid input is rejected before touching the DB.

func TestDeliverySessionRepo_ResetForReinitiate_MonotonicAttemptCount(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	repo := NewDeliverySessionRepository(db)

	mock.ExpectQuery(`UPDATE delivery_sessions`).
		WithArgs(int64(7), "publish_worker_post_completion", int(3)).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_count"}).AddRow(4))

	got, err := repo.ResetForReinitiate(context.Background(), 7, 3, "publish_worker_post_completion")
	if err != nil {
		t.Fatalf("ResetForReinitiate: %v", err)
	}
	if got != 4 {
		t.Errorf("newAttemptCount = %d, want 4 (row had 3; monotonic +1)", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestDeliverySessionRepo_ResetForReinitiate_ResetsProgressInPlace(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	repo := NewDeliverySessionRepository(db)

	// Exact-SQL pin: the reset must clear the upload-progress fields and
	// bump attempt_count + version in a single in-place UPDATE. If a
	// future refactor reintroduces DELETE + INSERT (or drops the
	// attempt_count bump), this equality fails.
	const wantSQL = `UPDATE delivery_sessions
            SET state                 = 'initiated',
                session_uri_encrypted = NULL,
                uploaded_bytes        = 0,
                remote_file_id        = NULL,
                remote_url            = NULL,
                error_message         = NULL,
                error_code            = NULL,
                worker_id             = $2,
                lease_expires_at      = NULL,
                attempt_count         = attempt_count + 1,
                version               = version + 1,
                updated_at            = NOW()
          WHERE id      = $1
            AND version = $3
          RETURNING attempt_count`
	mock.ExpectQuery(wantSQL).
		WithArgs(int64(9), "w1", int(5)).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_count"}).AddRow(6))

	got, err := repo.ResetForReinitiate(context.Background(), 9, 5, "w1")
	if err != nil {
		t.Fatalf("ResetForReinitiate: %v", err)
	}
	if got != 6 {
		t.Errorf("newAttemptCount = %d, want 6", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestDeliverySessionRepo_ResetForReinitiate_CASMismatchIsTyped(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	repo := NewDeliverySessionRepository(db)

	// CAS loss: a peer reset/completed the row first → 0 rows → the
	// QueryRow scan returns sql.ErrNoRows, which the repo must map to
	// the typed ErrDeliverySessionVersionMismatch (the destination's
	// recovery branch treats it as "peer won" and re-Finds).
	mock.ExpectQuery(`UPDATE delivery_sessions`).
		WithArgs(int64(11), "w1", int(8)).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_count"})) // empty → ErrNoRows

	_, err = repo.ResetForReinitiate(context.Background(), 11, 8, "w1")
	if !errors.Is(err, ErrDeliverySessionVersionMismatch) {
		t.Errorf("err = %v, want ErrDeliverySessionVersionMismatch", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestDeliverySessionRepo_ResetForReinitiate_DBErrorWrapped(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	repo := NewDeliverySessionRepository(db)

	boom := errors.New("connection reset")
	mock.ExpectQuery(`UPDATE delivery_sessions`).
		WithArgs(int64(2), "w1", int(1)).
		WillReturnError(boom)

	if _, err := repo.ResetForReinitiate(context.Background(), 2, 1, "w1"); !errors.Is(err, boom) {
		t.Errorf("err = %v, want wrapped %v", err, boom)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestDeliverySessionRepo_ResetForReinitiate_InputGuards(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	repo := NewDeliverySessionRepository(db)

	if _, err := repo.ResetForReinitiate(context.Background(), 0, 1, "w1"); err == nil {
		t.Errorf("non-positive id must be rejected before SQL")
	}
	if _, err := repo.ResetForReinitiate(context.Background(), 1, 1, ""); err == nil {
		t.Errorf("empty workerID must be rejected before SQL")
	}
	// Guards fire before any DB round-trip; sqlmock must see zero calls.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("guards must not touch the DB; unmet expectations: %v", err)
	}
}

// Cross-check: the reset keeps the row identity (deliverable_type,
// idempotency_key) intact so FindByIdempotencyKey still resolves it after
// a reset — the property the fresh-initiate branch relies on. Modeled by
// scanning a post-reset row through the shared scanner shape.
func TestDeliverySessionRepo_ResetForReinitiate_RowIdentitySurvives(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	repo := NewDeliverySessionRepository(db)

	// Reset succeeds...
	mock.ExpectQuery(`UPDATE delivery_sessions`).
		WithArgs(int64(4), "w1", int(2)).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_count"}).AddRow(3))

	if _, err := repo.ResetForReinitiate(context.Background(), 4, 2, "w1"); err != nil {
		t.Fatalf("ResetForReinitiate: %v", err)
	}

	// ...and the subsequent Find resolves the SAME (type, key) identity
	// with the reset fields cleared.
	findCols := []string{
		"id", "deliverable_type", "idempotency_key", "state",
		"session_uri_encrypted", "uploaded_bytes", "total_bytes", "chunk_size",
		"mime_type", "folder_id", "filename", "app_properties",
		"remote_file_id", "remote_url",
		"worker_id", "lease_expires_at", "expires_at",
		"error_message", "error_code", "attempt_count", "version",
		"created_at", "updated_at",
	}
	expires := time.Now().Add(7 * 24 * time.Hour)
	mock.ExpectQuery(`SELECT id, deliverable_type, idempotency_key, state`).
		WithArgs("google-drive", "key-post-reset").
		WillReturnRows(sqlmock.NewRows(findCols).AddRow(
			int64(4), "google-drive", "key-post-reset", "initiated",
			nil, 0, int64(1024), int64(262144),
			"video/mp4", "folder-x", "file.mp4", []byte(`{"instaedit_delivery_id":"key-post-reset"}`),
			nil, nil,
			"w1", nil, expires,
			nil, nil, 3, 3,
			time.Now(), time.Now(),
		))

	ds, err := repo.FindByIdempotencyKey(context.Background(), "google-drive", "key-post-reset")
	if err != nil {
		t.Fatalf("FindByIdempotencyKey: %v", err)
	}
	if ds.State != models.DeliverySessionStateInitiated {
		t.Errorf("state = %q, want %q", ds.State, models.DeliverySessionStateInitiated)
	}
	if ds.SessionURIEncrypted != "" {
		t.Errorf("session_uri_encrypted = %q, want cleared", ds.SessionURIEncrypted)
	}
	if ds.AttemptCount != 3 {
		t.Errorf("attempt_count = %d, want 3 (monotonic across the reset)", ds.AttemptCount)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}
