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

// ThumbnailProjectRepository persists autonomous, workspace-scoped thumbnail
// projects. Every read/write takes workspaceID so tenant isolation is
// enforced in SQL as well as by the HTTP authorization gate.
type ThumbnailProjectRepository struct {
	db *sql.DB
}

func NewThumbnailProjectRepository(db *sql.DB) *ThumbnailProjectRepository {
	return &ThumbnailProjectRepository{db: db}
}

const thumbnailProjectColumns = `id, workspace_id, created_by, name, description,
	canvas_width, canvas_height, status, current_revision_id, preview_media_id,
	latest_export_id, version, created_at, updated_at`

func scanThumbnailProject(row interface{ Scan(...any) error }) (*models.ThumbnailProject, error) {
	project := &models.ThumbnailProject{}
	if err := row.Scan(
		&project.ID, &project.WorkspaceID, &project.CreatedBy, &project.Name,
		&project.Description, &project.CanvasWidth, &project.CanvasHeight,
		&project.Status, &project.CurrentRevisionID, &project.PreviewMediaID,
		&project.LatestExportID, &project.Version, &project.CreatedAt,
		&project.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return project, nil
}

func validateThumbnailProject(project *models.ThumbnailProject) error {
	if project == nil {
		return fmt.Errorf("%w: project is required", ErrThumbnailProjectInvalid)
	}
	if project.WorkspaceID <= 0 || project.CreatedBy <= 0 {
		return fmt.Errorf("%w: workspace and creator are required", ErrThumbnailProjectInvalid)
	}
	if strings.TrimSpace(project.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrThumbnailProjectInvalid)
	}
	if project.CanvasWidth <= 0 || project.CanvasWidth > 16384 ||
		project.CanvasHeight <= 0 || project.CanvasHeight > 16384 {
		return fmt.Errorf("%w: canvas dimensions must be between 1 and 16384", ErrThumbnailProjectInvalid)
	}
	if project.Status == "" {
		project.Status = models.ThumbnailProjectStatusDraft
	}
	if !project.Status.IsValid() {
		return fmt.Errorf("invalid thumbnail project status %q", project.Status)
	}
	return nil
}

// Create inserts a project without requiring any channel, video, OAuth
// connection, platform account, or provider capability.
func (r *ThumbnailProjectRepository) Create(ctx context.Context, project *models.ThumbnailProject) error {
	if err := validateThumbnailProject(project); err != nil {
		return err
	}
	if project.ID == "" {
		project.ID = "thumbproj_" + uuid.NewString()
	}
	if project.Version <= 0 {
		project.Version = 1
	}
	if project.CreatedAt.IsZero() {
		project.CreatedAt = time.Now().UTC()
	}
	if project.UpdatedAt.IsZero() {
		project.UpdatedAt = project.CreatedAt
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO thumbnail_projects
			(id, workspace_id, created_by, name, description, canvas_width,
			 canvas_height, status, version, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		project.ID, project.WorkspaceID, project.CreatedBy, strings.TrimSpace(project.Name),
		project.Description, project.CanvasWidth, project.CanvasHeight, project.Status,
		project.Version, project.CreatedAt, project.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create thumbnail project: %w", err)
	}
	return nil
}

// FindByID returns a project only within the supplied workspace. Deleted
// projects remain addressable for internal lifecycle operations but the API
// treats them as unavailable to normal reads.
func (r *ThumbnailProjectRepository) FindByID(ctx context.Context, workspaceID int64, id string) (*models.ThumbnailProject, error) {
	if workspaceID <= 0 || strings.TrimSpace(id) == "" {
		return nil, errors.New("workspace id and thumbnail project id are required")
	}
	project, err := scanThumbnailProject(r.db.QueryRowContext(ctx,
		`SELECT `+thumbnailProjectColumns+`
		 FROM thumbnail_projects
		 WHERE workspace_id = $1 AND id = $2`, workspaceID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find thumbnail project: %w", err)
	}
	return project, nil
}

// ListByWorkspace returns non-deleted projects newest-first. Archived rows
// remain visible so the library can expose an Archived filter later.
func (r *ThumbnailProjectRepository) ListByWorkspace(ctx context.Context, workspaceID int64) ([]models.ThumbnailProject, error) {
	if workspaceID <= 0 {
		return nil, errors.New("workspace id is required")
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+thumbnailProjectColumns+`
		 FROM thumbnail_projects
		 WHERE workspace_id = $1 AND status <> $2
		 ORDER BY updated_at DESC, id`, workspaceID, models.ThumbnailProjectStatusDeleted)
	if err != nil {
		return nil, fmt.Errorf("list thumbnail projects: %w", err)
	}
	defer rows.Close()
	projects := make([]models.ThumbnailProject, 0)
	for rows.Next() {
		project, scanErr := scanThumbnailProject(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan thumbnail project: %w", scanErr)
		}
		projects = append(projects, *project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate thumbnail projects: %w", err)
	}
	return projects, nil
}

// UpdateCAS updates project metadata while requiring the caller's version.
// The version increment and all editable fields happen in one atomic UPDATE.
func (r *ThumbnailProjectRepository) UpdateCAS(ctx context.Context, project *models.ThumbnailProject, expectedVersion int64) error {
	if err := validateThumbnailProject(project); err != nil {
		return err
	}
	if expectedVersion <= 0 {
		return fmt.Errorf("%w: version must be positive", ErrThumbnailProjectInvalid)
	}
	if project.Status == models.ThumbnailProjectStatusDeleted {
		return errors.New("deleted thumbnail projects cannot be updated")
	}
	result, err := r.db.ExecContext(ctx,
		`UPDATE thumbnail_projects
		 SET name = $1, description = $2, canvas_width = $3, canvas_height = $4,
		     status = $5, version = version + 1, updated_at = NOW()
		 WHERE workspace_id = $6 AND id = $7 AND version = $8
		   AND status <> $9`,
		strings.TrimSpace(project.Name), project.Description, project.CanvasWidth,
		project.CanvasHeight, project.Status, project.WorkspaceID, project.ID,
		expectedVersion, models.ThumbnailProjectStatusDeleted)
	if err != nil {
		return fmt.Errorf("update thumbnail project: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update thumbnail project rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: project_id=%s expected_version=%d", ErrThumbnailProjectConflict, project.ID, expectedVersion)
	}
	project.Version = expectedVersion + 1
	project.UpdatedAt = time.Now().UTC()
	return nil
}

// UpdateStatusCAS archives or soft-deletes a project without destroying its
// revisions, exports, or assignments. It is the lifecycle-safe DELETE path.
func (r *ThumbnailProjectRepository) UpdateStatusCAS(ctx context.Context, workspaceID int64, id string, status models.ThumbnailProjectStatus, expectedVersion int64) error {
	if workspaceID <= 0 || strings.TrimSpace(id) == "" || expectedVersion <= 0 {
		return fmt.Errorf("%w: workspace, project id, and version are required", ErrThumbnailProjectInvalid)
	}
	if status != models.ThumbnailProjectStatusArchived && status != models.ThumbnailProjectStatusDeleted {
		return fmt.Errorf("invalid thumbnail project lifecycle status %q", status)
	}
	result, err := r.db.ExecContext(ctx,
		`UPDATE thumbnail_projects
		 SET status = $1, version = version + 1, updated_at = NOW()
		 WHERE workspace_id = $2 AND id = $3 AND version = $4
		   AND status <> $5`,
		status, workspaceID, id, expectedVersion, models.ThumbnailProjectStatusDeleted)
	if err != nil {
		return fmt.Errorf("update thumbnail project status: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update thumbnail project status rows affected: %w", err)
	}
	if n == 0 {
		var exists bool
		if err := r.db.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM thumbnail_projects WHERE workspace_id = $1 AND id = $2)`,
			workspaceID, id).Scan(&exists); err != nil {
			return fmt.Errorf("check thumbnail project status target: %w", err)
		}
		if !exists {
			return fmt.Errorf("%w: project_id=%s", ErrThumbnailProjectNotFound, id)
		}
		return fmt.Errorf("%w: project_id=%s expected_version=%d", ErrThumbnailProjectConflict, id, expectedVersion)
	}
	return nil
}
