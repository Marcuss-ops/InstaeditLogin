package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func livestreamFixture() *models.Livestream {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	return &models.Livestream{
		ID:                "ls_123",
		WorkspaceID:       7,
		PlatformAccountID: 42,
		CreatedBy:         1,
		Title:             "WWE News 24/7",
		Description:       "Loop broadcast",
		PrivacyStatus:     models.LivestreamPrivacyUnlisted,
		PlaybackMode:      models.LivestreamPlaybackLoopContinuous,
		ScheduleType:      models.LivestreamScheduleManual,
		DesiredState:      models.LivestreamStateDraft,
		ActualState:       models.LivestreamStateDraft,
		Resolution:        models.LivestreamResolution1080p,
		FrameRate:         models.LivestreamFrameRate,
		AutoRestart:       true,
		Category:          "24",
		MadeForKids:       false,
		Language:          "it",
		ThumbnailMediaID:  strPtr("thumb-123"),
		DVREnabled:        true,
		AutoStart:         false,
		AutoStop:          true,
		LatencyPreference: models.LivestreamLatencyLow,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func strPtr(s string) *string {
	return &s
}

func TestLivestreamRepository_Create(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	ls := livestreamFixture()

	mock.ExpectExec(`INSERT INTO livestreams`).
		WithArgs(ls.ID, ls.WorkspaceID, ls.PlatformAccountID, ls.CreatedBy, ls.Title, ls.Description,
			ls.PrivacyStatus, ls.PlaybackMode, ls.ScheduleType, sqlmock.AnyArg(),
			ls.DesiredState, ls.ActualState, ls.YouTubeBroadcastID, ls.YouTubeStreamID,
			ls.Resolution, ls.FrameRate, ls.AutoRestart,
			ls.Category, ls.MadeForKids, ls.Language, ls.ThumbnailMediaID,
			ls.DVREnabled, ls.AutoStart, ls.AutoStop, ls.LatencyPreference).
		WillReturnResult(sqlmock.NewResult(1, 1))

	repo := NewLivestreamRepository(db)
	if err := repo.Create(context.Background(), ls); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestLivestreamRepository_Create_RejectsInvalidState(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	ls := livestreamFixture()
	ls.DesiredState = "not-a-state"

	repo := NewLivestreamRepository(db)
	if err := repo.Create(context.Background(), ls); err == nil {
		t.Fatal("expected error for invalid state")
	}
}

func TestLivestreamRepository_FindByID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	ls := livestreamFixture()

	rows := sqlmock.NewRows([]string{
		"id", "workspace_id", "platform_account_id", "created_by", "title", "description",
		"privacy_status", "playback_mode", "schedule_type", "scheduled_start_at",
		"desired_state", "actual_state", "youtube_broadcast_id", "youtube_stream_id",
		"resolution", "frame_rate", "auto_restart",
		"category", "made_for_kids", "language", "thumbnail_media_id",
		"dvr_enabled", "auto_start", "auto_stop", "latency_preference",
		"created_at", "updated_at",
	}).AddRow(ls.ID, ls.WorkspaceID, ls.PlatformAccountID, ls.CreatedBy, ls.Title, ls.Description,
		ls.PrivacyStatus, ls.PlaybackMode, ls.ScheduleType, nil,
		ls.DesiredState, ls.ActualState, "", "",
		ls.Resolution, ls.FrameRate, ls.AutoRestart,
		ls.Category, ls.MadeForKids, ls.Language, ls.ThumbnailMediaID,
		ls.DVREnabled, ls.AutoStart, ls.AutoStop, ls.LatencyPreference,
		ls.CreatedAt, ls.UpdatedAt)
	mock.ExpectQuery(`SELECT id, workspace_id`).WithArgs(ls.ID).WillReturnRows(rows)

	repo := NewLivestreamRepository(db)
	got, err := repo.FindByID(context.Background(), ls.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got == nil {
		t.Fatal("expected a row, got nil")
	}
	if got.ID != ls.ID || got.Title != ls.Title || got.ActualState != ls.ActualState {
		t.Errorf("row mismatch: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestLivestreamRepository_FindByID_NoRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT id, workspace_id`).WithArgs("missing").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	repo := NewLivestreamRepository(db)
	got, err := repo.FindByID(context.Background(), "missing")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing row, got %+v", got)
	}
}

func TestLivestreamRepository_ListByWorkspace(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	ls := livestreamFixture()

	rows := sqlmock.NewRows([]string{
		"id", "workspace_id", "platform_account_id", "created_by", "title", "description",
		"privacy_status", "playback_mode", "schedule_type", "scheduled_start_at",
		"desired_state", "actual_state", "youtube_broadcast_id", "youtube_stream_id",
		"resolution", "frame_rate", "auto_restart",
		"category", "made_for_kids", "language", "thumbnail_media_id",
		"dvr_enabled", "auto_start", "auto_stop", "latency_preference",
		"created_at", "updated_at",
	}).AddRow(ls.ID, ls.WorkspaceID, ls.PlatformAccountID, ls.CreatedBy, ls.Title, ls.Description,
		ls.PrivacyStatus, ls.PlaybackMode, ls.ScheduleType, nil,
		ls.DesiredState, ls.ActualState, "", "",
		ls.Resolution, ls.FrameRate, ls.AutoRestart,
		ls.Category, ls.MadeForKids, ls.Language, ls.ThumbnailMediaID,
		ls.DVREnabled, ls.AutoStart, ls.AutoStop, ls.LatencyPreference,
		ls.CreatedAt, ls.UpdatedAt)
	mock.ExpectQuery(`SELECT id, workspace_id`).WithArgs(int64(7)).WillReturnRows(rows)

	repo := NewLivestreamRepository(db)
	got, err := repo.ListByWorkspace(context.Background(), 7)
	if err != nil {
		t.Fatalf("ListByWorkspace: %v", err)
	}
	if len(got) != 1 || got[0].ID != ls.ID {
		t.Fatalf("unexpected list result: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestLivestreamRepository_Update(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	ls := livestreamFixture()
	ls.Title = "Updated title"

	mock.ExpectExec(`UPDATE livestreams SET`).
		WithArgs(ls.ID, ls.Title, ls.Description, ls.PrivacyStatus, ls.PlaybackMode,
			ls.ScheduleType, sqlmock.AnyArg(), ls.Resolution, ls.FrameRate, ls.AutoRestart,
			ls.Category, ls.MadeForKids, ls.Language, ls.ThumbnailMediaID,
			ls.DVREnabled, ls.AutoStart, ls.AutoStop, ls.LatencyPreference).
		WillReturnResult(sqlmock.NewResult(0, 1))

	repo := NewLivestreamRepository(db)
	if err := repo.Update(context.Background(), ls); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestLivestreamRepository_Delete(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(`DELETE FROM livestreams`).WithArgs("ls_123").WillReturnResult(sqlmock.NewResult(0, 1))

	repo := NewLivestreamRepository(db)
	if err := repo.Delete(context.Background(), "ls_123"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestLivestreamRepository_Delete_NoRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(`DELETE FROM livestreams`).WithArgs("missing").WillReturnResult(sqlmock.NewResult(0, 0))

	repo := NewLivestreamRepository(db)
	if err := repo.Delete(context.Background(), "missing"); err == nil {
		t.Fatal("expected ErrLivestreamNotFound for missing row")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
