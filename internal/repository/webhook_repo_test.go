package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestWebhookRepository_MarkSuccessUsesLeaseCAS(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := NewWebhookRepository(db)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE webhook_deliveries")).
		WithArgs(int64(7), "lease-a", "ok").
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := r.MarkSuccess(context.Background(), 7, "lease-a", "ok"); !errors.Is(err, ErrWebhookLeaseLost) {
		t.Fatalf("MarkSuccess error = %v, want ErrWebhookLeaseLost", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookRepository_ClaimDueDeliveriesReturnsLeaseAndAttempt(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := NewWebhookRepository(db)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, event_id, endpoint_id, attempt, status")).
		WithArgs(2).
		WillReturnRows(sqlmock.NewRows([]string{"id", "event_id", "endpoint_id", "attempt", "status", "request_log", "response_log", "scheduled_at", "completed_at", "last_error"}).
			AddRow(7, 11, 13, 0, "pending", "", "", time.Now(), nil, ""))
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE webhook_deliveries")).
		WithArgs(int64(7), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"attempt", "lease_until", "heartbeat_at"}).AddRow(1, time.Now().Add(time.Minute), time.Now()))
	mock.ExpectCommit()
	got, err := r.ClaimDueDeliveries(context.Background(), 2, time.Minute)
	if err != nil {
		t.Fatalf("ClaimDueDeliveries: %v", err)
	}
	if len(got) != 1 || got[0].Attempt != 1 || got[0].LeaseID == "" {
		t.Fatalf("claimed = %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
