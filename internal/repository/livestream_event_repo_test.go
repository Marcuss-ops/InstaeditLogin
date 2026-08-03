package repository

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestLivestreamEventRepository_Record(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	event := &models.LivestreamEvent{
		LivestreamID: "live-1",
		EventType:    models.LivestreamEventBroadcastLive,
		Severity:     "info",
		Payload:      json.RawMessage(`{"actual_state":"live"}`),
	}
	mock.ExpectQuery(regexp.QuoteMeta(SQLRecordLivestreamEvent)).
		WithArgs(event.LivestreamID, event.RunID, event.EventType, event.Severity, event.Payload).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(1), time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)))

	if err := NewLivestreamEventRepository(db).Record(context.Background(), event); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLivestreamEventRepository_Record_RejectsUnknownType(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := NewLivestreamEventRepository(db).Record(context.Background(), &models.LivestreamEvent{
		LivestreamID: "live-1", EventType: "unknown", Severity: "info", Payload: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("unknown event type accepted")
	}
}

func TestLivestreamEventRepository_ListByRun(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	created := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	rows := sqlmock.NewRows([]string{"id", "livestream_id", "run_id", "event_type", "severity", "payload", "created_at"}).
		AddRow(int64(1), "live-1", "run-1", models.LivestreamEventRunCreated, "info", []byte(`{"generation":1}`), created)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, livestream_id, run_id, event_type, severity, payload, created_at")).
		WithArgs("run-1", 10).WillReturnRows(rows)

	got, err := NewLivestreamEventRepository(db).ListByRun(context.Background(), "run-1", 10)
	if err != nil || len(got) != 1 || got[0].EventType != models.LivestreamEventRunCreated {
		t.Fatalf("ListByRun = %+v, %v", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
