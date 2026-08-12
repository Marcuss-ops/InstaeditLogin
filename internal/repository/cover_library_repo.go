package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// CoverLibraryStore is the persistence boundary for the cover/template
// catalog. It deliberately does not expose editor canvas internals.
type CoverLibraryStore interface {
	ListCoverLibrary(context.Context, int64, string, int) ([]models.CoverLibraryItem, error)
	ListCoverTemplates(context.Context, int64, string, string) ([]models.CoverTemplate, error)
	ListCoverTemplateVersions(context.Context, int64, int64) ([]models.CoverTemplateVersion, error)
	CreateCoverTemplate(context.Context, *models.CoverTemplate, *models.CoverTemplateVersion) error
	CreateCoverTemplateVersion(context.Context, int64, *models.CoverTemplateVersion) error
	ArchiveCoverTemplate(context.Context, int64, int64) error
}

// CoverLibraryRepository shares the database connection but remains a
// separate bounded-context type so the API cannot accidentally depend on
// content-package mutation methods.
type CoverLibraryRepository struct{ db *sql.DB }

func NewCoverLibraryRepository(db *sql.DB) *CoverLibraryRepository {
	return &CoverLibraryRepository{db: db}
}

var _ CoverLibraryStore = (*CoverLibraryRepository)(nil)

func (r *CoverLibraryRepository) ListCoverLibrary(ctx context.Context, workspaceID int64, status string, limit int) ([]models.CoverLibraryItem, error) {
	if workspaceID <= 0 {
		return nil, errors.New("workspace id is required")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	status = strings.TrimSpace(status)
	query := `SELECT e.id, p.workspace_id, p.id, p.name, e.revision_id, e.media_id::text,
	                 e.content_type, e.width, e.height, e.file_size,
	                 encode(e.sha256, 'hex'), e.renderer_version, e.render_profile,
	                 e.status, e.created_at, e.updated_at
	          FROM thumbnail_exports e
	          JOIN thumbnail_projects p ON p.id=e.project_id
	          WHERE p.workspace_id=$1 AND e.status='ready'`
	args := []any{workspaceID}
	switch status {
	case "", models.CoverLibraryStatusReady:
		query += ` AND p.status <> 'deleted'`
	case models.CoverLibraryStatusArchived:
		query += ` AND p.status='archived'`
	default:
		return []models.CoverLibraryItem{}, nil
	}
	query += ` ORDER BY e.created_at DESC, e.id DESC LIMIT $2`
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list cover library: %w", err)
	}
	defer rows.Close()
	items := make([]models.CoverLibraryItem, 0)
	for rows.Next() {
		var item models.CoverLibraryItem
		if err := rows.Scan(&item.ExportID, &item.WorkspaceID, &item.ProjectID, &item.ProjectName,
			&item.RevisionID, &item.MediaID, &item.ContentType, &item.Width, &item.Height,
			&item.FileSize, &item.SHA256, &item.RendererVersion, &item.RenderProfile,
			&item.Status, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan cover library item: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const coverTemplateColumns = `id, workspace_id, created_by, name, description, category, language, status, current_version_number, created_at, updated_at`

func scanCoverTemplate(row interface{ Scan(...any) error }) (*models.CoverTemplate, error) {
	item := &models.CoverTemplate{}
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.CreatedBy, &item.Name, &item.Description,
		&item.Category, &item.Language, &item.Status, &item.CurrentVersionNumber, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return nil, err
	}
	return item, nil
}

func scanCoverTemplateVersion(row interface{ Scan(...any) error }) (*models.CoverTemplateVersion, error) {
	item := &models.CoverTemplateVersion{}
	if err := row.Scan(&item.ID, &item.TemplateID, &item.VersionNumber, &item.EditorProjectID,
		&item.PreviewMediaID, &item.Slots, &item.CreatedBy, &item.CreatedAt); err != nil {
		return nil, err
	}
	if len(item.Slots) == 0 {
		item.Slots = json.RawMessage(`{}`)
	}
	return item, nil
}

func (r *CoverLibraryRepository) ListCoverTemplates(ctx context.Context, workspaceID int64, language, status string) ([]models.CoverTemplate, error) {
	if workspaceID <= 0 {
		return nil, errors.New("workspace id is required")
	}
	query := `SELECT ` + coverTemplateColumns + ` FROM cover_templates WHERE workspace_id=$1`
	args := []any{workspaceID}
	if language = strings.TrimSpace(language); language != "" {
		query += ` AND (language='' OR language=$2)`
		args = append(args, language)
	}
	if status = strings.TrimSpace(status); status != "" {
		query += fmt.Sprintf(` AND status=$%d`, len(args)+1)
		args = append(args, status)
	}
	query += ` ORDER BY updated_at DESC, id DESC`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list cover templates: %w", err)
	}
	defer rows.Close()
	items := make([]models.CoverTemplate, 0)
	for rows.Next() {
		item, scanErr := scanCoverTemplate(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan cover template: %w", scanErr)
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (r *CoverLibraryRepository) ListCoverTemplateVersions(ctx context.Context, workspaceID, templateID int64) ([]models.CoverTemplateVersion, error) {
	if workspaceID <= 0 || templateID <= 0 {
		return nil, errors.New("workspace and template ids are required")
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT v.id, v.template_id, v.version_number, v.editor_project_id, v.preview_media_id::text,
		        v.slots, v.created_by, v.created_at
		 FROM cover_template_versions v
		 JOIN cover_templates t ON t.id=v.template_id
		 WHERE t.workspace_id=$1 AND t.id=$2
		 ORDER BY v.version_number DESC`, workspaceID, templateID)
	if err != nil {
		return nil, fmt.Errorf("list cover template versions: %w", err)
	}
	defer rows.Close()
	items := make([]models.CoverTemplateVersion, 0)
	for rows.Next() {
		item, scanErr := scanCoverTemplateVersion(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan cover template version: %w", scanErr)
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func validateTemplateVersion(version *models.CoverTemplateVersion) error {
	if version == nil || strings.TrimSpace(version.EditorProjectID) == "" || version.CreatedBy <= 0 {
		return errors.New("editor_project_id and created_by are required")
	}
	if version.Slots == nil {
		version.Slots = json.RawMessage(`{}`)
	}
	var slots map[string]any
	if err := json.Unmarshal(version.Slots, &slots); err != nil || slots == nil {
		return errors.New("slots must be a JSON object")
	}
	return nil
}

func (r *CoverLibraryRepository) CreateCoverTemplate(ctx context.Context, template *models.CoverTemplate, version *models.CoverTemplateVersion) error {
	if template == nil || template.WorkspaceID <= 0 || template.CreatedBy <= 0 || strings.TrimSpace(template.Name) == "" {
		return errors.New("workspace, creator and template name are required")
	}
	if err := validateTemplateVersion(version); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin cover template: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO cover_templates (workspace_id, created_by, name, description, category, language, status, current_version_number)
		 SELECT $1,$2,$3,$4,$5,$6,'active',1 FROM workspaces w
		 WHERE w.id=$1 AND (w.owner_id=$2 OR EXISTS (SELECT 1 FROM workspace_members wm WHERE wm.workspace_id=w.id AND wm.user_id=$2))
		 RETURNING `+coverTemplateColumns,
		template.WorkspaceID, template.CreatedBy, strings.TrimSpace(template.Name), template.Description,
		template.Category, strings.TrimSpace(template.Language)).Scan(&template.ID, &template.WorkspaceID,
		&template.CreatedBy, &template.Name, &template.Description, &template.Category, &template.Language,
		&template.Status, &template.CurrentVersionNumber, &template.CreatedAt, &template.UpdatedAt); err != nil {
		return fmt.Errorf("insert cover template: %w", err)
	}
	version.TemplateID = template.ID
	version.VersionNumber = 1
	if err := insertCoverTemplateVersion(ctx, tx, template.WorkspaceID, version); err != nil {
		return err
	}
	return tx.Commit()
}

func insertCoverTemplateVersion(ctx context.Context, tx *sql.Tx, workspaceID int64, version *models.CoverTemplateVersion) error {
	var preview any
	if version.PreviewMediaID != nil && strings.TrimSpace(*version.PreviewMediaID) != "" {
		preview = strings.TrimSpace(*version.PreviewMediaID)
	}
	var inserted models.CoverTemplateVersion
	err := tx.QueryRowContext(ctx,
		`INSERT INTO cover_template_versions (template_id, version_number, editor_project_id, preview_media_id, slots, created_by)
		 SELECT $1,$2,$3,$4::uuid,$5,$6
		 WHERE $4 IS NULL OR EXISTS (
		   SELECT 1 FROM media_assets ma
		   JOIN workspaces w ON w.id=$7
		   WHERE ma.id=$4::uuid AND ma.status='ready' AND ma.expires_at>NOW()
		     AND (ma.user_id=w.owner_id OR EXISTS (SELECT 1 FROM workspace_members wm WHERE wm.workspace_id=w.id AND wm.user_id=ma.user_id))
		 )
		 RETURNING id, template_id, version_number, editor_project_id, preview_media_id::text, slots, created_by, created_at`,
		version.TemplateID, version.VersionNumber, strings.TrimSpace(version.EditorProjectID), preview,
		version.Slots, version.CreatedBy, workspaceID).Scan(&inserted.ID, &inserted.TemplateID,
		&inserted.VersionNumber, &inserted.EditorProjectID, &inserted.PreviewMediaID, &inserted.Slots,
		&inserted.CreatedBy, &inserted.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("template preview is missing or not visible in workspace")
	}
	if err != nil {
		return fmt.Errorf("insert cover template version: %w", err)
	}
	*version = inserted
	return nil
}

func (r *CoverLibraryRepository) CreateCoverTemplateVersion(ctx context.Context, workspaceID int64, version *models.CoverTemplateVersion) error {
	if workspaceID <= 0 {
		return errors.New("workspace id is required")
	}
	if err := validateTemplateVersion(version); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var templateStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM cover_templates WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, version.TemplateID, workspaceID).Scan(&templateStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrThumbnailProjectNotFound
		}
		return err
	}
	if templateStatus == models.CoverTemplateStatusArchived {
		return errors.New("archived templates cannot receive new versions")
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_number),0)+1 FROM cover_template_versions WHERE template_id=$1`, version.TemplateID).Scan(&version.VersionNumber); err != nil {
		return err
	}
	if err := insertCoverTemplateVersion(ctx, tx, workspaceID, version); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cover_templates SET current_version_number=$1, updated_at=NOW() WHERE id=$2 AND workspace_id=$3`, version.VersionNumber, version.TemplateID, workspaceID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *CoverLibraryRepository) ArchiveCoverTemplate(ctx context.Context, workspaceID, templateID int64) error {
	if workspaceID <= 0 || templateID <= 0 {
		return errors.New("workspace and template ids are required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE cover_templates SET status='archived', updated_at=NOW() WHERE id=$1 AND workspace_id=$2`, templateID, workspaceID)
	if err != nil {
		return fmt.Errorf("archive cover template: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrThumbnailProjectNotFound
	}
	return nil
}
