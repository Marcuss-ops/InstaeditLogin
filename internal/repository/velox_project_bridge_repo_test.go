package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func newVeloxBridgeMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func TestVeloxProjectBridgeRepository_CreatePersistsOnlyMinimalMapping(t *testing.T) {
	db, mock := newVeloxBridgeMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	mock.ExpectExec(`INSERT INTO velox_project_bridges`).
		WithArgs(int64(7), "thumbproj_1", "vx_1", "velox", nil, nil, models.ThumbnailProjectStatusDeleted).
		WillReturnResult(sqlmock.NewResult(0, 1))

	bridge := &models.VeloxProjectBridge{
		ProjectID: " thumbproj_1 ", WorkspaceID: 7, ExternalProjectID: " vx_1 ",
	}
	if err := repo.CreateVeloxProjectBridge(context.Background(), bridge); err != nil {
		t.Fatalf("CreateVeloxProjectBridge: %v", err)
	}
	if bridge.ProjectID != "thumbproj_1" || bridge.ExternalProjectID != "vx_1" {
		t.Fatalf("bridge was not normalized: %+v", bridge)
	}
	if bridge.EditorProvider != "velox" {
		t.Fatalf("bridge editor_provider was not defaulted: %q", bridge.EditorProvider)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestVeloxProjectBridgeRepository_CreateQueryRejectsLegacyContextColumns(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(_ string, actual string) error {
		lower := strings.ToLower(actual)
		for _, legacy := range []string{"group_id", "channel_id", "channel_ids", "member_ids", "platform_account_id", "video_id", "language"} {
			if strings.Contains(lower, legacy) {
				return fmt.Errorf("legacy bridge field %q found in SQL", legacy)
			}
		}
		return nil
	})))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	repo := repository.NewThumbnailProjectRepository(db)
	mock.ExpectExec("INSERT INTO velox_project_bridges").
		WithArgs(int64(7), "thumbproj_1", "vx_1", "velox", nil, nil, models.ThumbnailProjectStatusDeleted).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.CreateVeloxProjectBridge(context.Background(), &models.VeloxProjectBridge{
		ProjectID: "thumbproj_1", WorkspaceID: 7, ExternalProjectID: "vx_1",
	}); err != nil {
		t.Fatalf("CreateVeloxProjectBridge: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestVeloxProjectBridgeRepository_RepeatedCreateRemainsSingleOwner(t *testing.T) {
	db, mock := newVeloxBridgeMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	mock.ExpectExec(`INSERT INTO velox_project_bridges`).
		WithArgs(int64(7), "thumbproj_1", "vx_1", "velox", nil, nil, models.ThumbnailProjectStatusDeleted).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO velox_project_bridges`).
		WithArgs(int64(7), "thumbproj_1", "vx_1", "velox", nil, nil, models.ThumbnailProjectStatusDeleted).
		WillReturnError(&pq.Error{Code: "23505", Constraint: "velox_project_bridges_pkey"})

	bridge := &models.VeloxProjectBridge{ProjectID: "thumbproj_1", WorkspaceID: 7, ExternalProjectID: "vx_1"}
	if err := repo.CreateVeloxProjectBridge(context.Background(), bridge); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := repo.CreateVeloxProjectBridge(context.Background(), bridge); !errors.Is(err, repository.ErrVeloxProjectBridgeConflict) {
		t.Fatalf("repeated create: want conflict, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestVeloxProjectBridgeRepository_MapsUniqueConflict(t *testing.T) {
	db, mock := newVeloxBridgeMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	mock.ExpectExec(`INSERT INTO velox_project_bridges`).
		WithArgs(int64(7), "thumbproj_1", "vx_1", "velox", nil, nil, models.ThumbnailProjectStatusDeleted).
		WillReturnError(&pq.Error{Code: "23505", Constraint: "velox_project_bridges_pkey"})
	err := repo.CreateVeloxProjectBridge(context.Background(), &models.VeloxProjectBridge{ProjectID: "thumbproj_1", WorkspaceID: 7, ExternalProjectID: "vx_1"})
	if !errors.Is(err, repository.ErrVeloxProjectBridgeConflict) {
		t.Fatalf("want bridge conflict, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestVeloxProjectBridgeRepository_FindScopesWorkspace(t *testing.T) {
	db, mock := newVeloxBridgeMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	mock.ExpectQuery(`SELECT project_id, workspace_id, external_project_id,`).
		WithArgs(int64(7), "thumbproj_1").
		WillReturnRows(sqlmock.NewRows([]string{"project_id", "workspace_id", "external_project_id", "editor_provider", "editor_status", "last_editor_sync_at", "created_at", "updated_at"}).
			AddRow("thumbproj_1", 7, "vx_1", "velox", "linked", nil, time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)))

	bridge, err := repo.FindVeloxProjectBridge(context.Background(), 7, "thumbproj_1")
	if err != nil || bridge == nil {
		t.Fatalf("FindVeloxProjectBridge: bridge=%+v err=%v", bridge, err)
	}
	if bridge.WorkspaceID != 7 || bridge.ExternalProjectID != "vx_1" {
		t.Fatalf("unexpected bridge: %+v", bridge)
	}
	if bridge.EditorProvider != "velox" || bridge.EditorStatus != "linked" {
		t.Fatalf("editor metadata not scanned: provider=%q status=%q", bridge.EditorProvider, bridge.EditorStatus)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestVeloxProjectBridgeRepository_FindAllowsNullOptionalEditorStatus(t *testing.T) {
	db, mock := newVeloxBridgeMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	mock.ExpectQuery(`SELECT project_id, workspace_id, external_project_id,`).
		WithArgs(int64(7), "thumbproj_1").
		WillReturnRows(sqlmock.NewRows([]string{"project_id", "workspace_id", "external_project_id", "editor_provider", "editor_status", "last_editor_sync_at", "created_at", "updated_at"}).
			AddRow("thumbproj_1", 7, "vx_1", "velox", nil, nil, time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)))

	bridge, err := repo.FindVeloxProjectBridge(context.Background(), 7, "thumbproj_1")
	if err != nil || bridge == nil {
		t.Fatalf("FindVeloxProjectBridge with null status: bridge=%+v err=%v", bridge, err)
	}
	if bridge.EditorStatus != "" {
		t.Fatalf("null editor_status should normalize to empty string, got %q", bridge.EditorStatus)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestVeloxProjectBridgeRepository_FindForeignWorkspaceIsHidden(t *testing.T) {
	db, mock := newVeloxBridgeMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	mock.ExpectQuery(`SELECT project_id, workspace_id, external_project_id,`).
		WithArgs(int64(8), "thumbproj_1").
		WillReturnError(sql.ErrNoRows)

	bridge, err := repo.FindVeloxProjectBridge(context.Background(), 8, "thumbproj_1")
	if err != nil {
		t.Fatalf("FindVeloxProjectBridge: %v", err)
	}
	if bridge != nil {
		t.Fatalf("foreign workspace bridge leaked: %+v", bridge)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestVeloxProjectBridgeRepository_DeleteForeignWorkspaceIsNotFound(t *testing.T) {
	db, mock := newVeloxBridgeMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	mock.ExpectExec(`DELETE FROM velox_project_bridges`).
		WithArgs(int64(8), "thumbproj_1").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.DeleteVeloxProjectBridge(context.Background(), 8, "thumbproj_1")
	if !errors.Is(err, repository.ErrVeloxProjectBridgeNotFound) {
		t.Fatalf("foreign workspace delete: want not found, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestVeloxProjectBridgeRepository_MapsExternalUniqueConflict(t *testing.T) {
	db, mock := newVeloxBridgeMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	mock.ExpectExec(`INSERT INTO velox_project_bridges`).
		WithArgs(int64(7), "thumbproj_2", "vx_1", "velox", nil, nil, models.ThumbnailProjectStatusDeleted).
		WillReturnError(&pq.Error{Code: "23505", Constraint: "velox_project_bridges_external_project_uq"})

	err := repo.CreateVeloxProjectBridge(context.Background(), &models.VeloxProjectBridge{ProjectID: "thumbproj_2", WorkspaceID: 7, ExternalProjectID: "vx_1"})
	if !errors.Is(err, repository.ErrVeloxProjectBridgeConflict) {
		t.Fatalf("external ownership conflict: want conflict, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestVeloxProjectBridgeRepository_EnsureEditorSessionProjectMintsRow(t *testing.T) {
	db, mock := newVeloxBridgeMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	mock.ExpectExec(`INSERT INTO thumbnail_projects`).
		WithArgs("ytsess_ensure_pg", int64(7), int64(9), "YouTube cover", models.ThumbnailProjectStatusDraft).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.EnsureThumbnailProjectForEditorSession(context.Background(), 7, "ytsess_ensure_pg", 9); err != nil {
		t.Fatalf("EnsureThumbnailProjectForEditorSession: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestVeloxProjectBridgeRepository_EnsureEditorSessionProjectValidates(t *testing.T) {
	db, _ := newVeloxBridgeMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	for _, tc := range []struct {
		name        string
		workspaceID int64
		projectID   string
		createdBy   int64
	}{
		{"zero workspace", 0, "sess", 9},
		{"blank project", 7, "  ", 9},
		{"zero creator", 7, "sess", 0},
	} {
		if err := repo.EnsureThumbnailProjectForEditorSession(context.Background(), tc.workspaceID, tc.projectID, tc.createdBy); !errors.Is(err, repository.ErrThumbnailProjectInvalid) {
			t.Fatalf("%s: want ErrThumbnailProjectInvalid, got %v", tc.name, err)
		}
	}
}

func TestVeloxProjectBridgeRepository_DeleteScopesWorkspace(t *testing.T) {
	db, mock := newVeloxBridgeMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	mock.ExpectExec(`DELETE FROM velox_project_bridges`).
		WithArgs(int64(7), "thumbproj_1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.DeleteVeloxProjectBridge(context.Background(), 7, "thumbproj_1"); err != nil {
		t.Fatalf("DeleteVeloxProjectBridge: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
