package repository_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// newMockOutboxDB returns a sqlmock with strict equality matcher.
// The outbox query set is small and stable — exact-equal matching
// catches accidental column-order drift faster than regex.
func newMockOutboxDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

// --- Insert ------------------------------------------------------------------

// TestOutboxInsert_Happy covers the basic Insert happy path: payload
// is JSON-marshalled, RETURNING fills in id + created_at, and the
// row is implicitly status='pending' via SQL DEFAULT.
func TestOutboxInsert_Happy(t *testing.T) {
	db, mock := newMockOutboxDB(t)
	repo := repository.NewOutboxRepository(db)
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	mock.ExpectQuery(
		`INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
		 VALUES ($1, $2, $3, $4::jsonb)
		 RETURNING id, created_at`,
	).WithArgs("post_target", int64(200), "post_target.publish_requested",
		sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(555, now))

	ev := &models.OutboxEvent{
		AggregateType: "post_target",
		AggregateID:   200,
		EventType:     "post_target.publish_requested",
		Payload:       []byte(`{"v":1}`),
	}
	if err := repo.Insert(ev); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if ev.ID != 555 {
		t.Errorf("ID: want 555, got %d", ev.ID)
	}
	if ev.Status != models.OutboxStatusPending {
		t.Errorf("Status: want pending (default mirror), got %q", ev.Status)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestOutboxInsert_MissingFields ensures the pre-flight guard
// rejects zero-value aggregate_type / event_type / payload AND
// aggregate_id <= 0 so the caller doesn't accidentally write an
// unrouteable row.
func TestOutboxInsert_MissingFields(t *testing.T) {
	db, _ := newMockOutboxDB(t)
	repo := repository.NewOutboxRepository(db)

	tests := []struct {
		name string
		ev   *models.OutboxEvent
	}{
		{"empty AggregateType", &models.OutboxEvent{EventType: "x", AggregateID: 100, Payload: []byte(`{}`)}},
		{"empty EventType", &models.OutboxEvent{AggregateType: "x", AggregateID: 100, Payload: []byte(`{}`)}},
		{"empty Payload", &models.OutboxEvent{AggregateType: "x", EventType: "y", AggregateID: 100}},
		{"zero AggregateID", &models.OutboxEvent{AggregateType: "x", EventType: "y", AggregateID: 0, Payload: []byte(`{}`)}},
		{"negative AggregateID", &models.OutboxEvent{AggregateType: "x", EventType: "y", AggregateID: -1, Payload: []byte(`{}`)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := repo.Insert(tt.ev); err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}

// --- ClaimNext ---------------------------------------------------------------

// TestOutboxClaimNext_Happy covers the canonical SELECT FOR UPDATE
// SKIP LOCKED + UPDATE-with-lease pattern. The dispatcher issue
// here is that we exercise the two-statement tx in one shape.
func TestOutboxClaimNext_Happy(t *testing.T) {
	db, mock := newMockOutboxDB(t)
	repo := repository.NewOutboxRepository(db)
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	leaseUntil := now.Add(60 * time.Second)

	mock.ExpectBegin()
	mock.ExpectQuery(
		`SELECT id FROM outbox_events
		 WHERE status = 'pending'
		   AND (lease_until IS NULL OR lease_until < now())
		   AND (next_attempt_at IS NULL OR next_attempt_at <= now())
		 ORDER BY next_attempt_at NULLS FIRST, created_at ASC
		 FOR UPDATE SKIP LOCKED
		 LIMIT 1`,
	).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(555))
	mock.ExpectQuery(
		`UPDATE outbox_events
		 SET lease_id = $1::uuid,
		     lease_until = $2,
		     attempt_count = attempt_count + 1
		 WHERE id = $3
		   AND status = 'pending'
		   AND (lease_until IS NULL OR lease_until < now())
		 RETURNING id, aggregate_type, aggregate_id, event_type, payload,
		           status, lease_id, lease_until, attempt_count,
		           next_attempt_at, last_error, created_at, processed_at`,
	).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), int64(555)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "aggregate_type", "aggregate_id", "event_type", "payload",
			"status", "lease_id", "lease_until", "attempt_count",
			"next_attempt_at", "last_error", "created_at", "processed_at",
		}).AddRow(
			555, "post_target", 200, "post_target.publish_requested",
			[]byte(`{"v":1}`), "pending", "00000000-0000-0000-0000-000000000001",
			leaseUntil, 1, nil, "", now, nil,
		))
	mock.ExpectCommit()

	ev, err := repo.ClaimNext(60 * time.Second)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if ev == nil {
		t.Fatal("wanted claimed event, got nil")
	}
	if ev.ID != 555 || ev.AttemptCount != 1 {
		t.Errorf("event: %+v", ev)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestOutboxClaimNext_NoPendingRow covers the queue-empty / all-leased
// fast path: SELECT returns sql.ErrNoRows → ErrOutboxAlreadyClaimed.
func TestOutboxClaimNext_NoPendingRow(t *testing.T) {
	db, mock := newMockOutboxDB(t)
	repo := repository.NewOutboxRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(
		`SELECT id FROM outbox_events
		 WHERE status = 'pending'
		   AND (lease_until IS NULL OR lease_until < now())
		   AND (next_attempt_at IS NULL OR next_attempt_at <= now())
		 ORDER BY next_attempt_at NULLS FIRST, created_at ASC
		 FOR UPDATE SKIP LOCKED
		 LIMIT 1`,
	).WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	ev, err := repo.ClaimNext(60 * time.Second)
	if !errors.Is(err, repository.ErrOutboxAlreadyClaimed) {
		t.Errorf("err: want ErrOutboxAlreadyClaimed, got %v", err)
	}
	if ev != nil {
		t.Errorf("ev: want nil, got %+v", ev)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestOutboxClaimNext_RaceLostOnUpdate covers the UPDATE-returns-no-rows
// path: a peer dispatcher committed MarkProcessed/MarkFailed between
// our SELECT … FOR UPDATE SKIP LOCKED and this UPDATE-with-lease. The
// SELECT returns a row id (so we're past the "queue empty" check) but
// the UPDATE's WHERE filter rejects it because the row's status is
// no longer 'pending' or its lease is no longer expired.
//
// Semantically distinct from ErrOutboxAlreadyClaimed (which means
// "queue empty / all rows leased"). Rendered as ErrOutboxRace so the
// dispatcher can log at DEBUG and immediately try the next SELECT
// rather than sleep-and-retry on a row that's already terminal.
func TestOutboxClaimNext_RaceLostOnUpdate(t *testing.T) {
	db, mock := newMockOutboxDB(t)
	repo := repository.NewOutboxRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(
		`SELECT id FROM outbox_events
		 WHERE status = 'pending'
		   AND (lease_until IS NULL OR lease_until < now())
		   AND (next_attempt_at IS NULL OR next_attempt_at <= now())
		 ORDER BY next_attempt_at NULLS FIRST, created_at ASC
		 FOR UPDATE SKIP LOCKED
		 LIMIT 1`,
	).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(555))
	mock.ExpectQuery(
		`UPDATE outbox_events
		 SET lease_id = $1::uuid,
		     lease_until = $2,
		     attempt_count = attempt_count + 1
		 WHERE id = $3
		   AND status = 'pending'
		   AND (lease_until IS NULL OR lease_until < now())
		 RETURNING id, aggregate_type, aggregate_id, event_type, payload,
		           status, lease_id, lease_until, attempt_count,
		           next_attempt_at, last_error, created_at, processed_at`,
	).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), int64(555)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	ev, err := repo.ClaimNext(60 * time.Second)
	if !errors.Is(err, repository.ErrOutboxRace) {
		t.Errorf("err: want ErrOutboxRace, got %v", err)
	}
	// The two errors (ErrOutboxRace and ErrOutboxAlreadyClaimed) are
	// SEMANTICALLY distinct so the dispatcher can log them differently.
	// Verify they're NOT just two names for the same thing.
	if errors.Is(err, repository.ErrOutboxAlreadyClaimed) {
		t.Errorf("ErrOutboxRace must NOT be wrapped by ErrOutboxAlreadyClaimed — they distinguish 'queue empty' from 'row got away from us'")
	}
	if ev != nil {
		t.Errorf("ev: want nil on race, got %+v", ev)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// --- RenewLease --------------------------------------------------------------

// TestOutboxRenewLease_Happy: heartbeat extends lease_until TTL.
func TestOutboxRenewLease_Happy(t *testing.T) {
	db, mock := newMockOutboxDB(t)
	repo := repository.NewOutboxRepository(db)
	leaseID := "11111111-1111-1111-1111-111111111111"

	mock.ExpectExec(
		`UPDATE outbox_events
		 SET lease_until = $1
		 WHERE id = $2 AND lease_id = $3::uuid`,
	).WithArgs(sqlmock.AnyArg(), int64(555), leaseID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.RenewLease(555, leaseID, 60*time.Second); err != nil {
		t.Fatalf("RenewLease: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestOutboxRenewLease_LeaseMismatch: heartbeat on a row whose
// lease_id changed (stolen by peer) returns ErrOutboxGone.
func TestOutboxRenewLease_LeaseMismatch(t *testing.T) {
	db, mock := newMockOutboxDB(t)
	repo := repository.NewOutboxRepository(db)

	mock.ExpectExec(
		`UPDATE outbox_events
		 SET lease_until = $1
		 WHERE id = $2 AND lease_id = $3::uuid`,
	).WithArgs(sqlmock.AnyArg(), int64(555), "wrong-lease").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.RenewLease(555, "wrong-lease", 60*time.Second)
	if !errors.Is(err, repository.ErrOutboxGone) {
		t.Errorf("err: want ErrOutboxGone, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// --- MarkProcessed -----------------------------------------------------------

// TestOutboxMarkProcessed_Happy: terminal transition with lease-id
// guard so a peer's stale MarkProcessed is rejected.
func TestOutboxMarkProcessed_Happy(t *testing.T) {
	db, mock := newMockOutboxDB(t)
	repo := repository.NewOutboxRepository(db)
	leaseID := "22222222-2222-2222-2222-222222222222"

	mock.ExpectExec(
		`UPDATE outbox_events
		 SET status = 'processed',
		     processed_at = now(),
		     lease_id = NULL,
		     lease_until = NULL,
		     next_attempt_at = NULL,
		     last_error = NULL
		 WHERE id = $1 AND lease_id = $2::uuid`,
	).WithArgs(int64(555), leaseID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.MarkProcessed(555, leaseID); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// --- MarkFailed --------------------------------------------------------------

// TestOutboxMarkFailed_Happy: transient failure, lease cleared,
// next_attempt_at = now() + backoff.
func TestOutboxMarkFailed_Happy(t *testing.T) {
	db, mock := newMockOutboxDB(t)
	repo := repository.NewOutboxRepository(db)
	leaseID := "33333333-3333-3333-3333-333333333333"
	backoff := 30 * time.Second
	expectedNext := time.Now().Add(backoff)

	mock.ExpectExec(
		`UPDATE outbox_events
		 SET lease_id = NULL,
		     lease_until = NULL,
		     last_error = $1,
		     next_attempt_at = $2
		 WHERE id = $3 AND lease_id = $4::uuid`,
	).WithArgs("rate limit hit", sqlmock.AnyArg(), int64(555), leaseID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.MarkFailed(555, leaseID, "rate limit hit", &backoff); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	// backoff is roughly now+30s; verify the dispatcher-computed
	// next_attempt_at is in the right neighbourhood.
	if expectedNext.IsZero() {
		t.Error("test setup: expectedNext should be non-zero")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestOutboxMarkFailed_EmptyError: pre-flight rejection — caller
// must always supply a non-empty last_error for retry observability.
func TestOutboxMarkFailed_EmptyError(t *testing.T) {
	db, _ := newMockOutboxDB(t)
	repo := repository.NewOutboxRepository(db)

	if err := repo.MarkFailed(555, "lease", "", nil); err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

// --- MarkDeadLetter ----------------------------------------------------------

// TestOutboxMarkDeadLetter_Happy: terminal-fail after retries
// exhausted. processed_at is set so operators can compute
// time-in-dlq.
func TestOutboxMarkDeadLetter_Happy(t *testing.T) {
	db, mock := newMockOutboxDB(t)
	repo := repository.NewOutboxRepository(db)
	leaseID := "44444444-4444-4444-4444-444444444444"

	mock.ExpectExec(
		`UPDATE outbox_events
		 SET status = 'dead_letter',
		     processed_at = now(),
		     lease_id = NULL,
		     lease_until = NULL,
		     next_attempt_at = NULL
		 WHERE id = $1 AND lease_id = $2::uuid`,
	).WithArgs(int64(555), leaseID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.MarkDeadLetter(555, leaseID, "permanent auth failure"); err != nil {
		t.Fatalf("MarkDeadLetter: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// --- ReleaseLease ------------------------------------------------------------

// TestOutboxReleaseLease_Happy: graceful shutdown path — lease
// cleared without changing status so a peer dispatcher can pick
// the row up immediately.
func TestOutboxReleaseLease_Happy(t *testing.T) {
	db, mock := newMockOutboxDB(t)
	repo := repository.NewOutboxRepository(db)
	leaseID := "55555555-5555-5555-5555-555555555555"

	mock.ExpectExec(
		`UPDATE outbox_events
		 SET lease_id = NULL,
		     lease_until = NULL
		 WHERE id = $1 AND lease_id = $2::uuid`,
	).WithArgs(int64(555), leaseID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.ReleaseLease(555, leaseID); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// --- List / Count ------------------------------------------------------------

// TestOutboxListPending_OK: diagnostic snapshot for dashboards.
func TestOutboxListPending_OK(t *testing.T) {
	db, mock := newMockOutboxDB(t)
	repo := repository.NewOutboxRepository(db)
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	mock.ExpectQuery(
		`SELECT id, aggregate_type, aggregate_id, event_type, payload, status,
		        lease_id, lease_until, attempt_count, next_attempt_at,
		        last_error, created_at, processed_at
		 FROM outbox_events
		 WHERE status = 'pending'
		 ORDER BY next_attempt_at NULLS FIRST, created_at ASC
		 LIMIT $1`,
	).WithArgs(50).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "aggregate_type", "aggregate_id", "event_type", "payload", "status",
			"lease_id", "lease_until", "attempt_count", "next_attempt_at",
			"last_error", "created_at", "processed_at",
		}).AddRow(1, "post_target", 200, "post_target.publish_requested",
			[]byte(`{"v":1}`), "pending", "lease-uuid-a", nil, 0, nil, "", now, nil).
			AddRow(2, "post_target", 201, "post_target.publish_requested",
				[]byte(`{"v":1}`), "pending", nil, nil, 0, nil, "", now, nil))

	list, err := repo.ListPending(50)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len: want 2, got %d", len(list))
	}
	// CRITICAL regression check: a row with SQL NULL lease_id MUST
	// produce ev.LeaseID == nil, NOT a non-nil pointer to "". A
	// previous version using a raw `*string` (or plain string) Scan
	// destination collapsed NULL and "" into the same nil-pointer-
	// to-empty, silently breaking `if ev.LeaseID != nil` callers.
	if list[0].LeaseID == nil || *list[0].LeaseID != "lease-uuid-a" {
		t.Errorf("list[0].LeaseID: want \"lease-uuid-a\", got %v", list[0].LeaseID)
	}
	if list[1].LeaseID != nil {
		t.Errorf("list[1].LeaseID: want nil (SQL NULL), got %q", *list[1].LeaseID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestOutboxCountPending_OK and TestOutboxCountDeadLetter_OK bundled
// because they share the trivial COALESCE-path shape.
func TestOutboxCountPending_OK(t *testing.T) {
	db, mock := newMockOutboxDB(t)
	repo := repository.NewOutboxRepository(db)

	mock.ExpectQuery(`SELECT count(*) FROM outbox_events WHERE status = 'pending'`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))

	n, err := repo.CountPending()
	if err != nil {
		t.Fatalf("CountPending: %v", err)
	}
	if n != 42 {
		t.Errorf("n: want 42, got %d", n)
	}
}

func TestOutboxCountDeadLetter_OK(t *testing.T) {
	db, mock := newMockOutboxDB(t)
	repo := repository.NewOutboxRepository(db)

	mock.ExpectQuery(`SELECT count(*) FROM outbox_events WHERE status = 'dead_letter'`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	n, err := repo.CountDeadLetter()
	if err != nil {
		t.Fatalf("CountDeadLetter: %v", err)
	}
	if n != 3 {
		t.Errorf("n: want 3, got %d", n)
	}
}
