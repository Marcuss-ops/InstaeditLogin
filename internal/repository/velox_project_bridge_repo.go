package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

const veloxProjectBridgeColumns = `project_id, workspace_id, velox_project_id,
	platform, platform_account_id, channel_id, video_id, language, created_at, updated_at`

func scanVeloxProjectBridge(row interface{ Scan(...any) error }) (*models.VeloxProjectBridge, error) {
	bridge := &models.VeloxProjectBridge{}
	if err := row.Scan(
		&bridge.ProjectID, &bridge.WorkspaceID, &bridge.VeloxProjectID,
		&bridge.Platform, &bridge.PlatformAccountID, &bridge.ChannelID,
		&bridge.VideoID, &bridge.Language, &bridge.CreatedAt, &bridge.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return bridge, nil
}

func mapVeloxProjectBridgeConstraint(err error) error {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return err
	}
	switch pqErr.Code {
	case "23505":
		if pqErr.Constraint == "velox_project_bridges_pkey" ||
			strings.Contains(pqErr.Constraint, "velox_project_bridges_project") ||
			strings.Contains(pqErr.Constraint, "velox_project_bridges_velox") {
			return fmt.Errorf("%w: constraint=%s", ErrVeloxProjectBridgeConflict, pqErr.Constraint)
		}
	case "23503":
		return fmt.Errorf("%w: channel or project is not visible in workspace", ErrVeloxProjectBridgeNotFound)
	case "23514":
		return fmt.Errorf("%w: bridge context is invalid", ErrVeloxProjectBridgeInvalid)
	}
	return err
}

// CreateVeloxProjectBridge inserts one bridge. Composite foreign keys make
// both the project and optional channel context workspace-local at SQL level.
func (r *ThumbnailProjectRepository) CreateVeloxProjectBridge(ctx context.Context, bridge *models.VeloxProjectBridge) error {
	if err := bridge.NormalizeAndValidate(); err != nil {
		return fmt.Errorf("%w: %v", ErrVeloxProjectBridgeInvalid, err)
	}
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO velox_project_bridges
			(project_id, workspace_id, velox_project_id, platform,
			 platform_account_id, channel_id, video_id, language)
		SELECT p.id, p.workspace_id, $3, $4, $5, $6, $7, $8
		  FROM thumbnail_projects p
		 WHERE p.workspace_id = $1 AND p.id = $2
		   AND p.status <> $9`,
		bridge.WorkspaceID, bridge.ProjectID, bridge.VeloxProjectID, nullableBridgeString(bridge.Platform),
		bridge.PlatformAccountID, bridge.ChannelID, bridge.VideoID, bridge.Language,
		models.ThumbnailProjectStatusDeleted)
	if err != nil {
		return fmt.Errorf("create velox project bridge: %w", mapVeloxProjectBridgeConstraint(err))
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("create velox project bridge rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: project_id=%s", ErrVeloxProjectBridgeNotFound, bridge.ProjectID)
	}
	return nil
}

func nullableBridgeString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

// FindVeloxProjectBridge returns a bridge only inside the requested workspace.
// A missing row and a foreign workspace are intentionally indistinguishable.
func (r *ThumbnailProjectRepository) FindVeloxProjectBridge(ctx context.Context, workspaceID int64, projectID string) (*models.VeloxProjectBridge, error) {
	projectID = strings.TrimSpace(projectID)
	if workspaceID <= 0 || projectID == "" {
		return nil, fmt.Errorf("%w: workspace and project are required", ErrVeloxProjectBridgeInvalid)
	}
	bridge, err := scanVeloxProjectBridge(r.db.QueryRowContext(ctx,
		`SELECT `+veloxProjectBridgeColumns+`
		   FROM velox_project_bridges
		  WHERE workspace_id = $1 AND project_id = $2`, workspaceID, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find velox project bridge: %w", err)
	}
	return bridge, nil
}

// DeleteVeloxProjectBridge removes InstaEdit's relationship only. It never
// deletes editor data in Velox.
func (r *ThumbnailProjectRepository) DeleteVeloxProjectBridge(ctx context.Context, workspaceID int64, projectID string) error {
	projectID = strings.TrimSpace(projectID)
	if workspaceID <= 0 || projectID == "" {
		return fmt.Errorf("%w: workspace and project are required", ErrVeloxProjectBridgeInvalid)
	}
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM velox_project_bridges WHERE workspace_id = $1 AND project_id = $2`, workspaceID, projectID)
	if err != nil {
		return fmt.Errorf("delete velox project bridge: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete velox project bridge rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: project_id=%s", ErrVeloxProjectBridgeNotFound, projectID)
	}
	return nil
}
