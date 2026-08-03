package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

const thumbnailAssetColumns = `project_id, media_id, role, object_id, created_at`

func scanThumbnailProjectAsset(row interface{ Scan(...any) error }) (*models.ThumbnailProjectAsset, error) {
	asset := &models.ThumbnailProjectAsset{}
	if err := row.Scan(&asset.ProjectID, &asset.MediaID, &asset.Role, &asset.ObjectID, &asset.CreatedAt); err != nil {
		return nil, err
	}
	return asset, nil
}

// CreateAsset links an existing media_assets row to a project. Ownership is
// checked against the project's workspace membership before insertion.
func (r *ThumbnailProjectRepository) CreateAsset(ctx context.Context, workspaceID int64, asset *models.ThumbnailProjectAsset) error {
	if err := asset.NormalizeAndValidate(); err != nil {
		return fmt.Errorf("%w: %v", ErrThumbnailProjectInvalid, err)
	}
	if workspaceID <= 0 {
		return fmt.Errorf("%w: workspace id must be positive", ErrThumbnailProjectInvalid)
	}
	if _, err := uuid.Parse(asset.MediaID); err != nil {
		return fmt.Errorf("%w: media_id must be a UUID", ErrThumbnailProjectInvalid)
	}
	createdAt := asset.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO thumbnail_project_assets (project_id, media_id, role, object_id, created_at)
		SELECT p.id, ma.id, $3, $4, $5
		  FROM thumbnail_projects p
		  JOIN media_assets ma ON ma.id = $2::uuid AND ma.status = 'ready' AND ma.expires_at > NOW()
		 WHERE p.workspace_id = $1 AND p.id = $6 AND p.status <> $7
		   AND EXISTS (
			   SELECT 1 FROM workspaces w
			    WHERE w.id = p.workspace_id
			      AND (w.owner_id = ma.user_id OR EXISTS (
					  SELECT 1 FROM workspace_members wm
					   WHERE wm.workspace_id = w.id AND wm.user_id = ma.user_id
				  ))
		   )
	`, workspaceID, asset.MediaID, asset.Role, asset.ObjectID, createdAt,
		asset.ProjectID, models.ThumbnailProjectStatusDeleted)
	if err != nil {
		if strings.Contains(err.Error(), "thumbnail_project_assets_project_media_role_pk") {
			return fmt.Errorf("%w: asset already linked", ErrThumbnailDomainConflict)
		}
		return fmt.Errorf("create thumbnail project asset: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("create thumbnail project asset rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: project or media asset is not owned by workspace", ErrThumbnailProjectNotFound)
	}
	asset.CreatedAt = createdAt
	return nil
}

func (r *ThumbnailProjectRepository) ListAssets(ctx context.Context, workspaceID int64, projectID string) ([]models.ThumbnailProjectAsset, error) {
	if workspaceID <= 0 || strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("%w: workspace and project are required", ErrThumbnailProjectInvalid)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+thumbnailAssetColumns+`
		  FROM thumbnail_project_assets a
		 WHERE a.project_id = $2
		   AND EXISTS (SELECT 1 FROM thumbnail_projects p WHERE p.id = a.project_id AND p.workspace_id = $1 AND p.status <> $3)
		 ORDER BY a.created_at, a.media_id, a.role`, workspaceID, projectID, models.ThumbnailProjectStatusDeleted)
	if err != nil {
		return nil, fmt.Errorf("list thumbnail project assets: %w", err)
	}
	defer rows.Close()
	assets := make([]models.ThumbnailProjectAsset, 0)
	for rows.Next() {
		asset, scanErr := scanThumbnailProjectAsset(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan thumbnail project asset: %w", scanErr)
		}
		assets = append(assets, *asset)
	}
	return assets, rows.Err()
}

func (r *ThumbnailProjectRepository) DeleteAsset(ctx context.Context, workspaceID int64, projectID, mediaID, role string) error {
	asset := &models.ThumbnailProjectAsset{ProjectID: projectID, MediaID: mediaID, Role: role}
	if err := asset.NormalizeAndValidate(); err != nil {
		return fmt.Errorf("%w: %v", ErrThumbnailProjectInvalid, err)
	}
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM thumbnail_project_assets a
		 USING thumbnail_projects p
		 WHERE a.project_id = p.id AND p.workspace_id = $1 AND p.id = $2
	   AND p.status <> $5
	   AND a.media_id = $3::uuid AND a.role = $4	`, workspaceID, projectID, mediaID, role, models.ThumbnailProjectStatusDeleted)
	if err != nil {
		return fmt.Errorf("delete thumbnail project asset: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: project asset not found", ErrThumbnailProjectAssetNotFound)
	}
	return nil
}

const thumbnailExportColumns = `id, project_id, revision_id, media_id, content_type, width, height, file_size, sha256, renderer_version, status, last_error, created_at`

func scanThumbnailExport(row interface{ Scan(...any) error }) (*models.ThumbnailExport, error) {
	export := &models.ThumbnailExport{}
	if err := row.Scan(&export.ID, &export.ProjectID, &export.RevisionID, &export.MediaID, &export.ContentType, &export.Width, &export.Height, &export.FileSize, &export.SHA256, &export.RendererVersion, &export.Status, &export.LastError, &export.CreatedAt); err != nil {
		return nil, err
	}
	return export, nil
}

// CreateExport persists an export only when its revision belongs to the same
// project and the media row belongs to the project creator.
func (r *ThumbnailProjectRepository) CreateExport(ctx context.Context, workspaceID int64, export *models.ThumbnailExport) error {
	if err := export.NormalizeAndValidate(); err != nil {
		return fmt.Errorf("%w: %v", ErrThumbnailProjectInvalid, err)
	}
	if export.ID == "" {
		export.ID = "thumbexp_" + uuid.NewString()
	}
	if workspaceID <= 0 {
		return fmt.Errorf("%w: workspace id must be positive", ErrThumbnailProjectInvalid)
	}
	createdAt := export.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("create thumbnail export begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO thumbnail_exports
			(id, project_id, revision_id, media_id, content_type, width, height, file_size, sha256, renderer_version, status, last_error, created_at)
		SELECT $2, p.id, r.id, ma.id, $5, $6, $7, $8, $9, $10, $11, $12, $13
		  FROM thumbnail_projects p
		  JOIN thumbnail_project_revisions r ON r.project_id = p.id AND r.id = $3
		  JOIN media_assets ma ON ma.id = $4::uuid AND ma.status = 'ready' AND ma.expires_at > NOW()
		 WHERE p.workspace_id = $1 AND p.id = $14 AND p.status <> $15
		   AND EXISTS (
			   SELECT 1 FROM workspaces w
			    WHERE w.id = p.workspace_id
			      AND (w.owner_id = ma.user_id OR EXISTS (
					  SELECT 1 FROM workspace_members wm
					   WHERE wm.workspace_id = w.id AND wm.user_id = ma.user_id
				  ))
		   )
	`, workspaceID, export.ID, export.RevisionID, export.MediaID, export.ContentType, export.Width, export.Height,
		export.FileSize, export.SHA256, export.RendererVersion, export.Status, export.LastError, createdAt,
		export.ProjectID, models.ThumbnailProjectStatusDeleted)
	if err != nil {
		return fmt.Errorf("create thumbnail export: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: project, revision, or ready media asset is not visible in workspace", ErrThumbnailProjectNotFound)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE thumbnail_projects
		   SET latest_export_id = $1,
		       preview_media_id = CASE WHEN $2 = 'ready' THEN $3::uuid ELSE preview_media_id END,
		       updated_at = $4
		 WHERE id = $5 AND workspace_id = $6`,
		export.ID, export.Status, export.MediaID, createdAt, export.ProjectID, workspaceID); err != nil {
		return fmt.Errorf("update thumbnail project export pointers: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("create thumbnail export commit: %w", err)
	}
	committed = true
	export.CreatedAt = createdAt
	return nil
}

// UpdateExportStatus completes a render without allowing a project or export
// to escape its workspace. Ready exports require a verified SHA-256 and size;
// failed exports retain the diagnostic error and cannot be assigned. The
// export transition and project preview pointer update are one transaction.
func (r *ThumbnailProjectRepository) UpdateExportStatus(ctx context.Context, workspaceID int64, exportID, status, lastError string, sha256 []byte, fileSize int64, rendererVersion string) error {
	normalizedError := strings.TrimSpace(lastError)
	normalizedRenderer := strings.TrimSpace(rendererVersion)
	normalizedStatus, err := models.NormalizeThumbnailProjectExportStatus(status, normalizedError, sha256, fileSize, normalizedRenderer)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrThumbnailProjectInvalid, err)
	}
	exportID = strings.TrimSpace(exportID)
	if workspaceID <= 0 || exportID == "" {
		return fmt.Errorf("%w: workspace_id and export_id are required", ErrThumbnailProjectInvalid)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("update thumbnail export status begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var projectID, mediaID string
	err = tx.QueryRowContext(ctx, `
		UPDATE thumbnail_exports e
		   SET status = $1,
		       last_error = $2,
		       sha256 = CASE WHEN $1 = 'ready' THEN $3 ELSE e.sha256 END,
		       file_size = CASE WHEN $1 = 'ready' THEN $4 ELSE e.file_size END,
		       renderer_version = CASE WHEN $5 <> '' THEN $5 ELSE e.renderer_version END
		 WHERE e.id = $6
		   AND e.status = 'rendering'
		   AND EXISTS (
			   SELECT 1 FROM thumbnail_projects p
			    WHERE p.id = e.project_id AND p.workspace_id = $7 AND p.status <> $8
		   )
		 RETURNING e.project_id, e.media_id`,
		normalizedStatus, normalizedError, sha256, fileSize, normalizedRenderer, exportID, workspaceID,
		models.ThumbnailProjectStatusDeleted).Scan(&projectID, &mediaID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: export is missing, outside workspace, or has an invalid lifecycle transition", ErrThumbnailExportNotFound)
	}
	if err != nil {
		return fmt.Errorf("update thumbnail export status: %w", err)
	}
	if normalizedStatus == models.ThumbnailProjectExportStatusReady {
		if _, err := tx.ExecContext(ctx, `
			UPDATE thumbnail_projects
			   SET latest_export_id = $1, preview_media_id = $2::uuid, updated_at = NOW()
			 WHERE id = $3 AND workspace_id = $4`, exportID, mediaID, projectID, workspaceID); err != nil {
			return fmt.Errorf("update thumbnail preview pointer: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("update thumbnail export status commit: %w", err)
	}
	committed = true
	return nil
}

func (r *ThumbnailProjectRepository) FindExport(ctx context.Context, workspaceID int64, exportID string) (*models.ThumbnailExport, error) {
	exportID = strings.TrimSpace(exportID)
	if workspaceID <= 0 || exportID == "" {
		return nil, fmt.Errorf("%w: workspace and export are required", ErrThumbnailProjectInvalid)
	}
	export, err := scanThumbnailExport(r.db.QueryRowContext(ctx, `
		SELECT `+thumbnailExportColumns+`
		  FROM thumbnail_exports e
		 WHERE e.id = $2
		   AND EXISTS (SELECT 1 FROM thumbnail_projects p WHERE p.id = e.project_id AND p.workspace_id = $1 AND p.status <> $3)
	`, workspaceID, exportID, models.ThumbnailProjectStatusDeleted))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: export_id=%s", ErrThumbnailExportNotFound, exportID)
	}
	if err != nil {
		return nil, fmt.Errorf("find thumbnail export: %w", err)
	}
	return export, nil
}

func (r *ThumbnailProjectRepository) ListExports(ctx context.Context, workspaceID int64, projectID string) ([]models.ThumbnailExport, error) {
	if workspaceID <= 0 || strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("%w: workspace and project are required", ErrThumbnailProjectInvalid)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+thumbnailExportColumns+`
		  FROM thumbnail_exports e
		 WHERE e.project_id = $2
		   AND EXISTS (SELECT 1 FROM thumbnail_projects p WHERE p.id = e.project_id AND p.workspace_id = $1 AND p.status <> $3)
		 ORDER BY e.created_at DESC, e.id`, workspaceID, projectID, models.ThumbnailProjectStatusDeleted)
	if err != nil {
		return nil, fmt.Errorf("list thumbnail exports: %w", err)
	}
	defer rows.Close()
	exports := make([]models.ThumbnailExport, 0)
	for rows.Next() {
		export, scanErr := scanThumbnailExport(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan thumbnail export: %w", scanErr)
		}
		exports = append(exports, *export)
	}
	return exports, rows.Err()
}

const thumbnailAssignmentColumns = `id, workspace_id, project_id, export_id, platform_account_id, platform, youtube_video_id, target_language, status, created_at, updated_at`

func scanThumbnailAssignment(row interface{ Scan(...any) error }) (*models.ThumbnailAssignment, error) {
	assignment := &models.ThumbnailAssignment{}
	if err := row.Scan(&assignment.ID, &assignment.WorkspaceID, &assignment.ProjectID, &assignment.ExportID, &assignment.PlatformAccountID, &assignment.Platform, &assignment.YouTubeVideoID, &assignment.TargetLanguage, &assignment.Status, &assignment.CreatedAt, &assignment.UpdatedAt); err != nil {
		return nil, err
	}
	return assignment, nil
}

func (r *ThumbnailProjectRepository) CreateAssignment(ctx context.Context, assignment *models.ThumbnailAssignment) error {
	if err := assignment.NormalizeAndValidate(); err != nil {
		return fmt.Errorf("%w: %v", ErrThumbnailProjectInvalid, err)
	}
	if assignment.WorkspaceID <= 0 || assignment.PlatformAccountID <= 0 {
		return fmt.Errorf("%w: workspace and platform account are required", ErrThumbnailProjectInvalid)
	}
	if assignment.ID == "" {
		assignment.ID = "thumbassign_" + uuid.NewString()
	}
	createdAt := assignment.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := assignment.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO thumbnail_assignments
			(id, workspace_id, project_id, export_id, platform_account_id, platform, youtube_video_id, target_language, status, created_at, updated_at)
		SELECT $2, p.workspace_id, p.id, e.id, $5, $6, $7, $8, $9, $10, $11
		  FROM thumbnail_projects p
		  JOIN thumbnail_exports e ON e.project_id = p.id AND e.id = $4 AND e.status = 'ready'
		 WHERE p.workspace_id = $1 AND p.id = $3 AND p.status <> $12
	`, assignment.WorkspaceID, assignment.ID, assignment.ProjectID, assignment.ExportID, assignment.PlatformAccountID,
		assignment.Platform, assignment.YouTubeVideoID, assignment.TargetLanguage, assignment.Status, createdAt, updatedAt,
		models.ThumbnailProjectStatusDeleted)
	if err != nil {
		return fmt.Errorf("create thumbnail assignment: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: project or ready export not found", ErrThumbnailExportNotFound)
	}
	assignment.CreatedAt = createdAt
	assignment.UpdatedAt = updatedAt
	return nil
}

func (r *ThumbnailProjectRepository) ListAssignments(ctx context.Context, workspaceID int64, projectID string) ([]models.ThumbnailAssignment, error) {
	if workspaceID <= 0 || strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("%w: workspace and project are required", ErrThumbnailProjectInvalid)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+thumbnailAssignmentColumns+`
		  FROM thumbnail_assignments a
		 WHERE a.project_id = $2
		   AND a.workspace_id = $1
		   AND EXISTS (SELECT 1 FROM thumbnail_projects p WHERE p.id = a.project_id AND p.status <> $3)
		 ORDER BY a.created_at DESC, a.id`, workspaceID, projectID, models.ThumbnailProjectStatusDeleted)
	if err != nil {
		return nil, fmt.Errorf("list thumbnail assignments: %w", err)
	}
	defer rows.Close()
	assignments := make([]models.ThumbnailAssignment, 0)
	for rows.Next() {
		assignment, scanErr := scanThumbnailAssignment(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan thumbnail assignment: %w", scanErr)
		}
		assignments = append(assignments, *assignment)
	}
	return assignments, rows.Err()
}

func normalizeThumbnailAssignmentStatus(status string) (string, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case models.ThumbnailProjectAssignmentStatusDraft,
		models.ThumbnailProjectAssignmentStatusPending,
		models.ThumbnailProjectAssignmentStatusApplied,
		models.ThumbnailProjectAssignmentStatusFailed,
		models.ThumbnailProjectAssignmentStatusCancelled:
		return status, nil
	default:
		return "", fmt.Errorf("unsupported thumbnail assignment status %q", status)
	}
}

func (r *ThumbnailProjectRepository) UpdateAssignmentStatus(ctx context.Context, workspaceID int64, assignmentID, status string) error {
	if workspaceID <= 0 || strings.TrimSpace(assignmentID) == "" {
		return fmt.Errorf("%w: workspace_id and assignment_id are required", ErrThumbnailProjectInvalid)
	}
	normalizedStatus, err := normalizeThumbnailAssignmentStatus(status)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrThumbnailProjectInvalid, err)
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE thumbnail_assignments SET status = $1, updated_at = NOW()
		 WHERE id = $2 AND workspace_id = $3
		   AND EXISTS (SELECT 1 FROM thumbnail_projects p WHERE p.id = thumbnail_assignments.project_id AND p.status <> $4)`, normalizedStatus, assignmentID, workspaceID, models.ThumbnailProjectStatusDeleted)
	if err != nil {
		return fmt.Errorf("update thumbnail assignment: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: assignment_id=%s", ErrThumbnailAssignmentNotFound, assignmentID)
	}
	return nil
}
