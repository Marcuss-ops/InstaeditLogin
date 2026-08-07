package repository_test

import (
	"context"
	"database/sql"
	"errors"
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
