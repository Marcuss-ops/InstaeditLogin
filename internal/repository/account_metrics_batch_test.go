package repository

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
)

func TestAccountMetricsRepositoryGetHistoryBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	from := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	dayOne := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	dayTwo := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`SELECT platform_account_id, metric_date, updated_at, subscribers, views, videos`).
		WithArgs(pq.Array([]int64{11, 22}), utcDay(from), utcDay(to)).
		WillReturnRows(sqlmock.NewRows([]string{
			"platform_account_id", "metric_date", "updated_at", "subscribers", "views", "videos",
			"watch_time_minutes", "impressions", "ctr", "revenue_cents", "rpm_cents", "cpm_cents",
		}).
			AddRow(int64(11), dayTwo, dayTwo.Add(12*time.Hour), int64(110), int64(1200), int64(11), nil, nil, nil, nil, nil, nil).
			AddRow(int64(11), dayOne, dayOne.Add(12*time.Hour), int64(100), int64(1000), int64(10), nil, nil, nil, nil, nil, nil).
			AddRow(int64(22), dayOne, dayOne.Add(12*time.Hour), int64(200), int64(2000), int64(20), nil, nil, nil, nil, nil, nil))

	got, err := NewAccountMetricsRepository(db).GetHistoryBatch([]int64{11, 22}, from, to)
	if err != nil {
		t.Fatalf("GetHistoryBatch: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
	if len(got) != 2 || len(got[11]) != 2 || len(got[22]) != 1 {
		t.Fatalf("unexpected grouped result: %+v", got)
	}
	if got[11][0].Subscribers != 100 || got[11][1].Subscribers != 110 {
		t.Fatalf("account 11 history order/values incorrect: %+v", got[11])
	}
}

func TestAccountMetricsRepositoryGetHistoryBatchEmptyIDs(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	got, err := NewAccountMetricsRepository(db).GetHistoryBatch(nil, time.Now(), time.Now())
	if err != nil {
		t.Fatalf("GetHistoryBatch empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d accounts, want 0", len(got))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("empty batch must not query: %v", err)
	}
}
