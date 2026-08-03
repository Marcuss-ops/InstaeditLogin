package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func livestreamRunFixture() *models.LivestreamRun {
	return &models.LivestreamRun{
		ID:                   "run-1",
		LivestreamID:         "live-1",
		PlatformAccountID:    42,
		Generation:           1,
		Status:               models.LivestreamRunStatusPreparing,
		ConfigurationVersion: 1,
		ReconnectCount:       0,
		AttemptCount:         0,
		ErrorCode:            "",
		ErrorMessage:         "",
		LastErrorCode:        "",
		LastErrorMessage:     "",
	}
}

func livestreamRunColumnsForTest() []string {
	return []string{
		"id", "livestream_id", "platform_account_id", "generation", "status",
		"youtube_broadcast_id", "youtube_stream_id", "configuration_version",
		"worker_id", "lease_expires_at", "heartbeat_at", "last_frame_at", "encoder_pid",
		"reconnect_count", "attempt_count", "started_at", "live_at", "ended_at",
		"error_code", "error_message", "last_error_code", "last_error_message",
		"created_at", "updated_at",
	}
}

func addLivestreamRunRow(rows *sqlmock.Rows, run *models.LivestreamRun) *sqlmock.Rows {
	return rows.AddRow(
		run.ID, run.LivestreamID, run.PlatformAccountID, run.Generation, string(run.Status),
		run.YouTubeBroadcastID, run.YouTubeStreamID, run.ConfigurationVersion,
		run.WorkerID, run.LeaseExpiresAt, run.HeartbeatAt, run.LastFrameAt, run.EncoderPID,
		run.ReconnectCount, run.AttemptCount, run.StartedAt, run.LiveAt, run.EndedAt,
		run.ErrorCode, run.ErrorMessage, run.LastErrorCode, run.LastErrorMessage,
		time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
	)
}

func TestLivestreamRunRepository_Create(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	run := livestreamRunFixture()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO livestream_runs")).
		WithArgs(run.ID, run.LivestreamID, run.PlatformAccountID, run.Generation, run.Status,
			run.YouTubeBroadcastID, run.YouTubeStreamID, run.ConfigurationVersion,
			run.WorkerID, run.LeaseExpiresAt, run.HeartbeatAt, run.LastFrameAt, run.EncoderPID,
			run.ReconnectCount, run.AttemptCount, run.StartedAt, run.LiveAt, run.EndedAt,
			run.ErrorCode, run.ErrorMessage, run.LastErrorCode, run.LastErrorMessage).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := NewLivestreamRunRepository(db).Create(context.Background(), run); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLivestreamRunRepository_Create_MapsActiveChannelConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO livestream_runs")).
		WillReturnError(&pq.Error{Code: "23505", Constraint: "livestream_one_active_run_per_channel"})

	err = NewLivestreamRunRepository(db).Create(context.Background(), livestreamRunFixture())
	if !errors.Is(err, models.ErrLivestreamRunActiveConflict) {
		t.Fatalf("Create error = %v, want active conflict", err)
	}
}

func TestLivestreamRunRepository_Create_ValidatesCountersAndVersion(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	run := livestreamRunFixture()
	run.ConfigurationVersion = 0
	if err := NewLivestreamRunRepository(db).Create(context.Background(), run); err == nil {
		t.Fatal("expected invalid configuration version error")
	}
}

func TestLivestreamRunRepository_CreateNext_SerializesGeneration(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	run := livestreamRunFixture()
	run.Generation = 0
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtext($1))")).
		WithArgs(run.LivestreamID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COALESCE(MAX(generation), 0) + 1")).
		WithArgs(run.LivestreamID).
		WillReturnRows(sqlmock.NewRows([]string{"generation"}).AddRow(int64(7)))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO livestream_runs")).
		WithArgs(run.ID, run.LivestreamID, run.PlatformAccountID, int64(7), run.Status,
			run.YouTubeBroadcastID, run.YouTubeStreamID, run.ConfigurationVersion,
			run.WorkerID, run.LeaseExpiresAt, run.HeartbeatAt, run.LastFrameAt, run.EncoderPID,
			run.ReconnectCount, run.AttemptCount, run.StartedAt, run.LiveAt, run.EndedAt,
			run.ErrorCode, run.ErrorMessage, run.LastErrorCode, run.LastErrorMessage).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := NewLivestreamRunRepository(db).CreateNext(context.Background(), run); err != nil {
		t.Fatalf("CreateNext: %v", err)
	}
	if run.Generation != 7 {
		t.Fatalf("generation = %d, want 7", run.Generation)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLivestreamRunRepository_ClaimBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	run := livestreamRunFixture()
	worker := "live-worker-1"
	lease := 2 * time.Minute
	mock.ExpectQuery(regexp.QuoteMeta(SQLClaimLivestreamRuns)).
		WithArgs(2, worker, sqlmock.AnyArg()).
		WillReturnRows(addLivestreamRunRow(sqlmock.NewRows(livestreamRunColumnsForTest()), run))

	got, err := NewLivestreamRunRepository(db).ClaimBatch(context.Background(), worker, 2, lease)
	if err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if len(got) != 1 || got[0].ID != run.ID {
		t.Fatalf("claimed runs = %+v", got)
	}
	if got[0].Status != run.Status {
		t.Fatalf("claim changed status to %q", got[0].Status)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLivestreamRunRepository_ClaimBatch_ArgumentValidation(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewLivestreamRunRepository(db)
	if _, err := repo.ClaimBatch(context.Background(), "", 1, time.Minute); err == nil {
		t.Fatal("empty worker ID accepted")
	}
	if _, err := repo.ClaimBatch(context.Background(), "worker", 1, 0); err == nil {
		t.Fatal("non-positive lease accepted")
	}
}

func TestLivestreamRunRepository_Heartbeat(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	run := livestreamRunFixture()
	run.WorkerID = stringPtr("worker-1")
	run.HeartbeatAt = timePtr(time.Now().UTC())
	mock.ExpectQuery(regexp.QuoteMeta(SQLHeartbeatLivestreamRun)).
		WithArgs(sqlmock.AnyArg(), run.ID, "worker-1").
		WillReturnRows(addLivestreamRunRow(sqlmock.NewRows(livestreamRunColumnsForTest()), run))

	if err := NewLivestreamRunRepository(db).Heartbeat(context.Background(), run.ID, "worker-1", time.Minute); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLivestreamRunRepository_Heartbeat_LeaseLost(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(SQLHeartbeatLivestreamRun)).
		WithArgs(sqlmock.AnyArg(), "run-1", "stale-worker").
		WillReturnError(sql.ErrNoRows)
	if err := NewLivestreamRunRepository(db).Heartbeat(context.Background(), "run-1", "stale-worker", time.Minute); !errors.Is(err, models.ErrLivestreamRunLeaseLost) {
		t.Fatalf("Heartbeat error = %v, want lease lost", err)
	}
}

func TestLivestreamRunRepository_AdvanceConfigurationVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(SQLAdvanceLivestreamRunConfigurationVersion)).
		WithArgs("run-1", int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"configuration_version"}).AddRow(int64(4)))

	got, err := NewLivestreamRunRepository(db).AdvanceConfigurationVersion(context.Background(), "run-1", 3)
	if err != nil || got != 4 {
		t.Fatalf("AdvanceConfigurationVersion = %d, %v", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLivestreamRunRepository_AdvanceConfigurationVersion_Conflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta(SQLAdvanceLivestreamRunConfigurationVersion)).
		WithArgs("run-1", int64(3)).WillReturnError(sql.ErrNoRows)

	_, err = NewLivestreamRunRepository(db).AdvanceConfigurationVersion(context.Background(), "run-1", 3)
	if !errors.Is(err, models.ErrLivestreamRunVersionConflict) {
		t.Fatalf("error = %v, want version conflict", err)
	}
}

func TestLivestreamRunRepository_UpdateStatus_RequiresLeaseAndVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	run := livestreamRunFixture()
	run.Status = models.LivestreamRunStatusLive
	run.WorkerID = stringPtr("worker-1")
	mock.ExpectQuery(regexp.QuoteMeta(SQLUpdateLivestreamRunStatus)).
		WithArgs(models.LivestreamRunStatusLive, run.ID, "worker-1", int64(1)).
		WillReturnRows(addLivestreamRunRow(sqlmock.NewRows(livestreamRunColumnsForTest()), run))

	got, err := NewLivestreamRunRepository(db).UpdateStatus(context.Background(), run.ID, "worker-1", 1, models.LivestreamRunStatusLive)
	if err != nil || got.Status != models.LivestreamRunStatusLive {
		t.Fatalf("UpdateStatus = %+v, %v", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLivestreamRunRepository_FindAndList(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	run := livestreamRunFixture()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + livestreamRunColumns + " FROM livestream_runs WHERE id = $1")).
		WithArgs(run.ID).WillReturnRows(addLivestreamRunRow(sqlmock.NewRows(livestreamRunColumnsForTest()), run))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+livestreamRunColumns)).
		WithArgs(run.LivestreamID, 10).WillReturnRows(addLivestreamRunRow(sqlmock.NewRows(livestreamRunColumnsForTest()), run))

	repo := NewLivestreamRunRepository(db)
	got, err := repo.FindByID(context.Background(), run.ID)
	if err != nil || got == nil || got.ID != run.ID {
		t.Fatalf("FindByID = %+v, %v", got, err)
	}
	list, err := repo.ListByLivestream(context.Background(), run.LivestreamID, 10)
	if err != nil || len(list) != 1 || list[0].ID != run.ID {
		t.Fatalf("ListByLivestream = %+v, %v", list, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func stringPtr(s string) *string     { return &s }
func timePtr(t time.Time) *time.Time { return &t }
