package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func livestreamFixture() *models.Livestream {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	return &models.Livestream{
		ID:                   "ls_123",
		WorkspaceID:          7,
		PlatformAccountID:    42,
		CreatedBy:            1,
		Title:                "WWE News 24/7",
		Description:          "Loop broadcast",
		PrivacyStatus:        models.LivestreamPrivacyUnlisted,
		PlaybackMode:         models.LivestreamPlaybackLoopContinuous,
		ScheduleType:         models.LivestreamScheduleManual,
		DesiredState:         models.LivestreamStateDraft,
		ActualState:          models.LivestreamStateDraft,
		DesiredGeneration:    1,
		ConfigurationVersion: 1,
		Resolution:           models.LivestreamResolution1080p,
		FrameRate:            models.LivestreamFrameRate,
		AutoRestart:          true,
		Category:             "24",
		MadeForKids:          false,
		Language:             "it",
		ThumbnailMediaID:     strPtr("thumb-123"),
		DVREnabled:           true,
		AutoStart:            false,
		AutoStop:             true,
		LatencyPreference:    models.LivestreamLatencyLow,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

func strPtr(s string) *string { return &s }

func livestreamRowColumns() []string {
	return []string{
		"id", "workspace_id", "platform_account_id", "created_by", "title", "description",
		"privacy_status", "playback_mode", "schedule_type", "scheduled_start_at",
		"desired_state", "actual_state", "desired_generation", "configuration_version",
		"youtube_broadcast_id", "youtube_stream_id", "resolution", "frame_rate", "auto_restart",
		"category", "made_for_kids", "language", "thumbnail_media_id", "dvr_enabled",
		"auto_start", "auto_stop", "latency_preference", "created_at", "updated_at",
	}
}

func livestreamRow(ls *models.Livestream) *sqlmock.Rows {
	return sqlmock.NewRows(livestreamRowColumns()).AddRow(
		ls.ID, ls.WorkspaceID, ls.PlatformAccountID, ls.CreatedBy, ls.Title, ls.Description,
		ls.PrivacyStatus, ls.PlaybackMode, ls.ScheduleType, nil,
		ls.DesiredState, ls.ActualState, ls.DesiredGeneration, ls.ConfigurationVersion,
		"", "", ls.Resolution, ls.FrameRate, ls.AutoRestart,
		ls.Category, ls.MadeForKids, ls.Language, ls.ThumbnailMediaID,
		ls.DVREnabled, ls.AutoStart, ls.AutoStop, ls.LatencyPreference,
		ls.CreatedAt, ls.UpdatedAt)
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
			ls.DesiredState, ls.ActualState, ls.DesiredGeneration, ls.ConfigurationVersion,
			ls.YouTubeBroadcastID, ls.YouTubeStreamID, ls.Resolution, ls.FrameRate, ls.AutoRestart,
			ls.Category, ls.MadeForKids, ls.Language, ls.ThumbnailMediaID,
			ls.DVREnabled, ls.AutoStart, ls.AutoStop, ls.LatencyPreference).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := NewLivestreamRepository(db).Create(context.Background(), ls); err != nil {
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

	if err := NewLivestreamRepository(db).Create(context.Background(), ls); err == nil {
		t.Fatal("expected error for invalid state")
	}
}

func TestLivestreamRepository_FindByID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ls := livestreamFixture()
	mock.ExpectQuery(`SELECT id, workspace_id`).WithArgs(ls.ID).WillReturnRows(livestreamRow(ls))

	got, err := NewLivestreamRepository(db).FindByID(context.Background(), ls.ID)
	if err != nil || got == nil || got.ID != ls.ID || got.ConfigurationVersion != 1 {
		t.Fatalf("FindByID = %+v, %v", got, err)
	}
}

func TestLivestreamRepository_FindByID_NoRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(`SELECT id, workspace_id`).WithArgs("missing").WillReturnError(sql.ErrNoRows)

	got, err := NewLivestreamRepository(db).FindByID(context.Background(), "missing")
	if err != nil || got != nil {
		t.Fatalf("FindByID missing = %+v, %v", got, err)
	}
}

func TestLivestreamRepository_ListByWorkspace(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ls := livestreamFixture()
	mock.ExpectQuery(`SELECT id, workspace_id`).WithArgs(int64(7)).WillReturnRows(livestreamRow(ls))

	got, err := NewLivestreamRepository(db).ListByWorkspace(context.Background(), 7)
	if err != nil || len(got) != 1 || got[0].ID != ls.ID {
		t.Fatalf("ListByWorkspace = %+v, %v", got, err)
	}
}

func TestLivestreamRepository_Update(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ls := livestreamFixture()
	mock.ExpectExec(`UPDATE livestreams SET`).
		WithArgs(ls.ID, ls.Title, ls.Description, ls.PrivacyStatus, ls.PlaybackMode,
			ls.ScheduleType, sqlmock.AnyArg(), ls.Resolution, ls.FrameRate, ls.AutoRestart,
			ls.Category, ls.MadeForKids, ls.Language, ls.ThumbnailMediaID,
			ls.DVREnabled, ls.AutoStart, ls.AutoStop, ls.LatencyPreference, int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := NewLivestreamRepository(db).Update(context.Background(), ls); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if ls.ConfigurationVersion != 2 {
		t.Fatalf("configuration version = %d, want 2", ls.ConfigurationVersion)
	}
}

func TestLivestreamRepository_Delete(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec(`DELETE FROM livestreams`).WithArgs("ls_123").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := NewLivestreamRepository(db).Delete(context.Background(), "ls_123"); err != nil {
		t.Fatal(err)
	}
}

func TestLivestreamRepository_Delete_NoRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec(`DELETE FROM livestreams`).WithArgs("missing").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := NewLivestreamRepository(db).Delete(context.Background(), "missing"); err == nil {
		t.Fatal("expected ErrLivestreamNotFound")
	}
}
