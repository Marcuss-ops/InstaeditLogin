// Package processors tests for the publish_jobs materialiser.
// Mirrors the sqlmock QueryMatcherEqual convention used in
// internal/outbox/dispatcher_test.go and internal/repository/
// outbox_repo_test.go so a contributor familiar with either can
// read this without re-orienting.
package processors_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox/processors"
)

// validPayload is the canonical happy-path payload shape written
// by internal/repository/post_repo.go::Create. Kept in one place
// so all happy-path tests share the same contract.
func validPayload() []byte {
	return []byte(`{
		"event_version":"v1",
		"post_id":200,
		"target_id":555,
		"workspace_id":10,
		"platform_account_id":1000,
		"scheduled_at":"2025-07-12T15:00:00Z",
		"title":"My post",
		"caption":"Hello world",
		"media_url":"https://example.com/x.jpg"
	}`)
}

// validEvent builds a minimal OutboxEvent that mirrors the rows
// ClaimNext returns (post_target + post_target.publish_requested).
func validEvent(id int64) *models.OutboxEvent {
	return &models.OutboxEvent{
		ID:            id,
		AggregateType: "post_target",
		AggregateID:   555,
		EventType:     "post_target.publish_requested",
		Payload:       validPayload(),
		Status:        models.OutboxStatusPending,
		AttemptCount:  1,
	}
}

// --- Happy path -------------------------------------------------------------

// TestPublishJobsMaterialiser_Happy covers the canonical success:
// payload decodes, INSERT fires with expected args, materialiser
// returns nil so the dispatcher calls MarkProcessed.
func TestPublishJobsMaterialiser_Happy(t *testing.T) {
	db, mock := newDBf(t)
	process := processors.NewPublishJobsMaterialiser(db)

	mock.ExpectExec(
		`INSERT INTO publish_jobs
		    (post_target_id, outbox_event_id, status, attempt_number, version)
		 VALUES ($1, $2, 'pending', 0, 1)`,
	).WithArgs(int64(555), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := process(context.Background(), validEvent(42)); err != nil {
		t.Fatalf("Process: want nil, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestPublishJobsMaterialiser_AlreadyMaterialised covers the
// retry-after-MarkFailed path: the dispatcher re-claims an event
// and we re-attempt INSERT, but the partial UNIQUE index trips
// 23505. The materialiser treats this as idempotent success so
// MarkProcessed fires (instead of re-trying forever).
func TestPublishJobsMaterialiser_AlreadyMaterialised(t *testing.T) {
	db, mock := newDBf(t)
	process := processors.NewPublishJobsMaterialiser(db)

	mock.ExpectExec(
		`INSERT INTO publish_jobs
		    (post_target_id, outbox_event_id, status, attempt_number, version)
		 VALUES ($1, $2, 'pending', 0, 1)`,
	).WithArgs(int64(555), int64(77)).
		WillReturnError(&pq.Error{
			Code:       "23505",
			Message:    "duplicate key value violates unique constraint \"uniq_publish_jobs_outbox_event\"",
			Constraint: "uniq_publish_jobs_outbox_event",
		})

	if err := process(context.Background(), validEvent(77)); err != nil {
		// Idempotency path: must return nil even though the INSERT
		// failed — the dispatcher sees success → MarkProcessed.
		t.Fatalf("Process: want nil on idempotency re-run, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestPublishJobsMaterialiser_NullTargetOK covers the null-target
// edge case (a future event_type that emits an outbox row without
// a post_target). TargetID is allowed to be 0; we pass
// sql.NullInt64{Valid:false}.
func TestPublishJobsMaterialiser_NullTargetOK(t *testing.T) {
	db, mock := newDBf(t)
	process := processors.NewPublishJobsMaterialiser(db)

	ev := validEvent(101)
	ev.AggregateID = 700 // simulate post_target id passthrough
	ev.Payload = []byte(`{
		"event_version":"v1",
		"post_id":200,
		"target_id":0,
		"workspace_id":10,
		"platform_account_id":1000
	}`)

	mock.ExpectExec(
		`INSERT INTO publish_jobs
		    (post_target_id, outbox_event_id, status, attempt_number, version)
		 VALUES ($1, $2, 'pending', 0, 1)`,
	).WithArgs(sql.NullInt64{Int64: 0, Valid: false}, int64(101)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := process(context.Background(), ev); err != nil {
		t.Fatalf("Process: want nil on null target, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// --- Terminal errors (should DLQ, not retry) -------------------------------

// TestPublishJobsMaterialiser_BadAggregateType_Terminal covers the
// "this row will never decode correctly" path. A wrong
// aggregate_type means the writer violated the contract —
// re-running the materialiser would re-fail. Wrap ErrTerminal.
func TestPublishJobsMaterialiser_BadAggregateType_Terminal(t *testing.T) {
	db, mock := newDBf(t)
	process := processors.NewPublishJobsMaterialiser(db)

	ev := validEvent(7)
	ev.AggregateType = "workspace"

	err := process(context.Background(), ev)
	if err == nil {
		t.Fatal("Process: want error on wrong aggregate_type, got nil")
	}
	if !errors.Is(err, outbox.ErrTerminal) {
		t.Errorf("err: want ErrTerminal (DLQ), got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("INSERT should not have fired, but: %v", err)
	}
}

// TestPublishJobsMaterialiser_BadEventType_Terminal: same as above
// but event_type is off. Routine — defined separately because the
// error string carries the specific mismatch (helpful when the
// row lands in DLQ for the operator to read).
func TestPublishJobsMaterialiser_BadEventType_Terminal(t *testing.T) {
	db, mock := newDBf(t)
	process := processors.NewPublishJobsMaterialiser(db)

	ev := validEvent(8)
	ev.EventType = "post_target.publish_completed"

	err := process(context.Background(), ev)
	if !errors.Is(err, outbox.ErrTerminal) {
		t.Errorf("err: want ErrTerminal, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("INSERT should not have fired, but: %v", err)
	}
}

// TestPublishJobsMaterialiser_BadJSON_Terminal: payload is not
// valid JSON. Cannot ever decode → terminal.
func TestPublishJobsMaterialiser_BadJSON_Terminal(t *testing.T) {
	db, mock := newDBf(t)
	process := processors.NewPublishJobsMaterialiser(db)

	ev := validEvent(9)
	ev.Payload = []byte(`{"event_version":`) // truncated — invalid JSON

	err := process(context.Background(), ev)
	if !errors.Is(err, outbox.ErrTerminal) {
		t.Errorf("err: want ErrTerminal, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("INSERT should not have fired, but: %v", err)
	}
}

// TestPublishJobsMaterialiser_UnknownVersion_Terminal: a forward-
// compat check. event_version=v1 is the only supported value
// today; anything else goes to DLQ so the operator can see
// "v2 landed but the dispatcher doesn't know about it yet".
func TestPublishJobsMaterialiser_UnknownVersion_Terminal(t *testing.T) {
	db, _ := newDBf(t)
	process := processors.NewPublishJobsMaterialiser(db)

	ev := validEvent(10)
	ev.Payload = []byte(`{
		"event_version":"v99",
		"post_id":200,
		"target_id":555,
		"workspace_id":10,
		"platform_account_id":1000
	}`)

	err := process(context.Background(), ev)
	if !errors.Is(err, outbox.ErrTerminal) {
		t.Errorf("err: want ErrTerminal, got %v", err)
	}
	if !strings.Contains(err.Error(), "v99") {
		t.Errorf("err message: want to mention v99, got %q", err.Error())
	}
}

// TestPublishJobsMaterialiser_MissingFields_Terminal: each
// required field in the payload absent → terminal. We iterate
// missing-field cases by emitting a stripped payload.
func TestPublishJobsMaterialiser_MissingFields_Terminal(t *testing.T) {
	db, _ := newDBf(t)
	process := processors.NewPublishJobsMaterialiser(db)

	cases := []struct {
		name    string
		payload []byte
	}{
		{"missing_post_id", []byte(`{"event_version":"v1","target_id":555,"workspace_id":10}`)},
		{"missing_workspace_id", []byte(`{"event_version":"v1","post_id":200,"target_id":555}`)},
		{"missing_event_version", []byte(`{"post_id":200,"target_id":555,"workspace_id":10}`)},
		{"zero_post_id", []byte(`{"event_version":"v1","post_id":0,"target_id":555,"workspace_id":10}`)},
		{"zero_workspace_id", []byte(`{"event_version":"v1","post_id":200,"target_id":555,"workspace_id":0}`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := validEvent(11)
			ev.Payload = c.payload
			err := process(context.Background(), ev)
			if !errors.Is(err, outbox.ErrTerminal) {
				t.Errorf("err: want ErrTerminal, got %v", err)
			}
		})
	}
}

// --- Transient errors (should retry, NOT DLQ) ------------------------------

// TestPublishJobsMaterialiser_TransientDBError_NonTerminal covers
// the retry path: a connection blip surfaces as a generic error
// that DOES NOT wrap ErrTerminal → the dispatcher backs off and
// retries up to MaxAttempts, then DLQs.
func TestPublishJobsMaterialiser_TransientDBError_NonTerminal(t *testing.T) {
	db, mock := newDBf(t)
	process := processors.NewPublishJobsMaterialiser(db)

	mock.ExpectExec(
		`INSERT INTO publish_jobs
		    (post_target_id, outbox_event_id, status, attempt_number, version)
		 VALUES ($1, $2, 'pending', 0, 1)`,
	).WithArgs(int64(555), int64(33)).
		WillReturnError(errors.New("connection reset by peer"))

	err := process(context.Background(), validEvent(33))
	if err == nil {
		t.Fatal("Process: want error on conn reset, got nil")
	}
	if errors.Is(err, outbox.ErrTerminal) {
		t.Errorf("err: must NOT be ErrTerminal (transient should retry), got %v", err)
	}
}

// TestPublishJobsMaterialiser_ContextCanceled_Transient covers the
// graceful-shutdown-during-INSERT path. Cancelled context surfaces
// as transient so the row is re-claimed by the next dispatcher,
// not DLQ'd. (Future refactors may want this to be terminal — but
// for now an in-flight shutdown is benign and a re-claim is safer.)
func TestPublishJobsMaterialiser_ContextCanceled_Transient(t *testing.T) {
	db, mock := newDBf(t)
	process := processors.NewPublishJobsMaterialiser(db)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mock.ExpectExec(
		`INSERT INTO publish_jobs
		    (post_target_id, outbox_event_id, status, attempt_number, version)
		 VALUES ($1, $2, 'pending', 0, 1)`,
	).WithArgs(int64(555), int64(45)).
		WillReturnError(context.Canceled)

	err := process(ctx, validEvent(45))
	if err == nil {
		t.Fatal("Process: want error on cancelled ctx, got nil")
	}
	if errors.Is(err, outbox.ErrTerminal) {
		t.Errorf("err: must NOT be ErrTerminal, got %v", err)
	}
}

// --- Fixture ----------------------------------------------------------------

// newDBf is the standard fixture: returns a sqlmock *sql.DB plus
// its expectations driver. Each test seeds expectations on the
// returned mock and verifies via mock.ExpectationsWereMet(). The
// *sql.DB returned by sqlmock.New satisfies the sql.DB interface
// the materialiser expects (it's the literal *sql.DB return).
func newDBf(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}
