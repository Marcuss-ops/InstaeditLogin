package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestLivestreamRepository_UpdateWithVersion_Stale(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ls := livestreamFixture()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE livestreams SET")).
		WithArgs(ls.ID, ls.Title, ls.Description, ls.PrivacyStatus, ls.PlaybackMode,
			ls.ScheduleType, sqlmock.AnyArg(), ls.Resolution, ls.FrameRate, ls.AutoRestart,
			ls.Category, ls.MadeForKids, ls.Language, ls.ThumbnailMediaID,
			ls.DVREnabled, ls.AutoStart, ls.AutoStop, ls.LatencyPreference, int64(4)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = NewLivestreamRepository(db).UpdateWithVersion(context.Background(), ls, 4)
	if !errors.Is(err, models.ErrLivestreamConfigurationStale) {
		t.Fatalf("UpdateWithVersion error = %v", err)
	}
}

func TestLivestreamRepository_SetDesiredState(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE livestreams")).
		WithArgs("live-1", models.LivestreamDesiredRunning, int64(3), models.LivestreamDesiredPrepared).
		WillReturnRows(sqlmock.NewRows([]string{"desired_generation"}).AddRow(int64(8)))

	got, err := NewLivestreamRepository(db).SetDesiredState(context.Background(), "live-1", 3, models.LivestreamDesiredPrepared, models.LivestreamDesiredRunning)
	if err != nil || got != 8 {
		t.Fatalf("SetDesiredState = %d, %v", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLivestreamRepository_SetDesiredState_RejectsInvalidEdge(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = NewLivestreamRepository(db).SetDesiredState(context.Background(), "live-1", 1, models.LivestreamDesiredRunning, models.LivestreamDesiredPrepared)
	if !errors.Is(err, models.ErrInvalidLivestreamDesiredTransition) {
		t.Fatalf("error = %v", err)
	}
}

func TestLivestreamRunRepository_TransitionStatus(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	run := livestreamRunFixture()
	run.Status = models.LivestreamRunStatusPreparing
	mock.ExpectQuery(regexp.QuoteMeta(SQLTransitionLivestreamRun)).
		WithArgs(models.LivestreamActualReady, run.ID, "worker-1", int64(1), models.LivestreamActualPreparing).
		WillReturnRows(addLivestreamRunRow(sqlmock.NewRows(livestreamRunColumnsForTest()), run))

	got, err := NewLivestreamRunRepository(db).TransitionStatus(context.Background(), run.ID, "worker-1", 1, models.LivestreamActualPreparing, models.LivestreamActualReady)
	if err != nil || got == nil {
		t.Fatalf("TransitionStatus = %+v, %v", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLivestreamRunRepository_TransitionStatus_RejectsInvalidEdge(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = NewLivestreamRunRepository(db).TransitionStatus(context.Background(), "run-1", "worker-1", 1, models.LivestreamActualLive, models.LivestreamActualPreparing)
	if !errors.Is(err, models.ErrInvalidLivestreamActualTransition) {
		t.Fatalf("error = %v", err)
	}
}

func TestLivestreamRunRepository_TransitionStatus_LeaseLost(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta(SQLTransitionLivestreamRun)).
		WithArgs(models.LivestreamActualReady, "run-1", "worker-1", int64(1), models.LivestreamActualPreparing).
		WillReturnError(sql.ErrNoRows)
	_, err = NewLivestreamRunRepository(db).TransitionStatus(context.Background(), "run-1", "worker-1", 1, models.LivestreamActualPreparing, models.LivestreamActualReady)
	if !errors.Is(err, models.ErrLivestreamRunLeaseLost) {
		t.Fatalf("error = %v", err)
	}
}
