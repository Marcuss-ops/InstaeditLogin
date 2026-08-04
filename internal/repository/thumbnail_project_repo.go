package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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

func scanThumbnailProjectRevision(row interface{ Scan(...any) error }) (*models.ThumbnailProjectRevision, error) {
	revision := &models.ThumbnailProjectRevision{}
	if err := row.Scan(&revision.ID, &revision.ProjectID, &revision.RevisionNumber,
		&revision.SchemaVersion, &revision.SnapshotJSON, &revision.SnapshotSHA256,
		&revision.RendererVersion, &revision.CreatedBy, &revision.CreatedAt); err != nil {
		return nil, err
	}
	return revision, nil
}

func canonicalSnapshot(snapshot json.RawMessage) (json.RawMessage, []byte, error) {
	var value any
	if len(snapshot) == 0 {
		return nil, nil, fmt.Errorf("%w: snapshot is required", ErrThumbnailProjectInvalid)
	}
	if err := json.Unmarshal(snapshot, &value); err != nil {
		return nil, nil, fmt.Errorf("%w: invalid snapshot JSON", ErrThumbnailProjectInvalid)
	}
	object, ok := value.(map[string]any)
	if !ok || object == nil {
		return nil, nil, fmt.Errorf("%w: snapshot must be a JSON object", ErrThumbnailProjectInvalid)
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: canonicalize snapshot", ErrThumbnailProjectInvalid)
	}
	hash := sha256.Sum256(canonical)
	return canonical, hash[:], nil
}

func validateSnapshot(snapshot models.ThumbnailProjectSnapshot, createdBy int64) (json.RawMessage, []byte, error) {
	if snapshot.SchemaVersion < 1 || strings.TrimSpace(snapshot.RendererVersion) == "" || snapshot.BaseVersion <= 0 || createdBy <= 0 {
		return nil, nil, fmt.Errorf("%w: schema_version, renderer_version, base_version, and creator are required", ErrThumbnailProjectInvalid)
	}
	return canonicalSnapshot(snapshot.SnapshotJSON)
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
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO thumbnail_projects
			(id, workspace_id, created_by, name, description, canvas_width,
			 canvas_height, status, version, created_at, updated_at)
		 SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
		   FROM workspaces w
		  WHERE w.id = $2
		    AND (w.owner_id = $3 OR EXISTS (
				SELECT 1 FROM workspace_members wm
				 WHERE wm.workspace_id = w.id AND wm.user_id = $3
			))`,
		project.ID, project.WorkspaceID, project.CreatedBy, strings.TrimSpace(project.Name),
		project.Description, project.CanvasWidth, project.CanvasHeight, project.Status,
		project.Version, project.CreatedAt, project.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create thumbnail project: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("create thumbnail project rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: creator is not a member of workspace", ErrThumbnailProjectNotFound)
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

func revisionResult(projectID string, revision *models.ThumbnailProjectRevision, version int64, deduplicated bool) *models.ThumbnailProjectSnapshotResult {
	return &models.ThumbnailProjectSnapshotResult{ProjectID: projectID, RevisionID: revision.ID, RevisionNumber: revision.RevisionNumber, Version: version, SavedAt: revision.CreatedAt, SnapshotSHA256: hex.EncodeToString(revision.SnapshotSHA256), Revision: revision, Deduplicated: deduplicated}
}

func (r *ThumbnailProjectRepository) lockProject(ctx context.Context, tx *sql.Tx, workspaceID int64, projectID string) (int64, error) {
	var version int64
	var status models.ThumbnailProjectStatus
	err := tx.QueryRowContext(ctx, `SELECT version, status FROM thumbnail_projects WHERE workspace_id = $1 AND id = $2 FOR UPDATE`, workspaceID, projectID).Scan(&version, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: project_id=%s", ErrThumbnailProjectNotFound, projectID)
	}
	if err != nil {
		return 0, fmt.Errorf("lock thumbnail project: %w", err)
	}
	if status == models.ThumbnailProjectStatusDeleted {
		return 0, fmt.Errorf("%w: project_id=%s", ErrThumbnailProjectNotFound, projectID)
	}
	return version, nil
}

func (r *ThumbnailProjectRepository) nextRevisionNumber(ctx context.Context, tx *sql.Tx, projectID string) (int64, error) {
	var number int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision_number), 0) + 1 FROM thumbnail_project_revisions WHERE project_id = $1`, projectID).Scan(&number); err != nil {
		return 0, err
	}
	return number, nil
}

func insertThumbnailRevision(ctx context.Context, tx *sql.Tx, revision *models.ThumbnailProjectRevision) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO thumbnail_project_revisions (id, project_id, revision_number, schema_version, snapshot_json, snapshot_sha256, renderer_version, created_by, created_at) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9)`, revision.ID, revision.ProjectID, revision.RevisionNumber, revision.SchemaVersion, revision.SnapshotJSON, revision.SnapshotSHA256, revision.RendererVersion, revision.CreatedBy, revision.CreatedAt)
	return err
}

// snapshotAssetRefs extracts the media references of a canonical schema
// version 1 snapshot: every drawable object carrying a media_id becomes a
// thumbnail_project_assets link, with the role derived from the object
// type (image objects default to foreground; future font/background
// objects map to their roles). References are deduplicated by
// (media_id, role) — the table's primary key. The snapshot remains the
// single source of truth; this derivation exists so the library can
// answer "which media does this project use" without scanning JSON.
// A malformed payload degrades to no links (validateSnapshot already
// guarantees canonical snapshots parse as JSON objects).
func snapshotAssetRefs(snapshotJSON json.RawMessage) []models.ThumbnailProjectAsset {
	var snapshot struct {
		Objects []struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			MediaID string `json:"media_id"`
		} `json:"objects"`
	}
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		return nil
	}
	seen := make(map[string]bool, len(snapshot.Objects))
	assets := make([]models.ThumbnailProjectAsset, 0, len(snapshot.Objects))
	for _, obj := range snapshot.Objects {
		mediaID := strings.TrimSpace(obj.MediaID)
		if mediaID == "" {
			continue
		}
		// Non-UUID references are skipped, never rejected: the upsert
		// casts $2::uuid, and a malformed id must not fail the snapshot
		// save with an opaque SQL error (same contract as CreateAsset
		// and the media resolver, which reject non-UUIDs explicitly).
		if _, err := uuid.Parse(mediaID); err != nil {
			continue
		}
		role := models.ThumbnailProjectAssetRoleForeground
		switch strings.TrimSpace(obj.Type) {
		case "font":
			role = models.ThumbnailProjectAssetRoleFont
		case "background":
			role = models.ThumbnailProjectAssetRoleBackground
		}
		key := role + "\x00" + mediaID
		if seen[key] {
			continue
		}
		seen[key] = true
		asset := models.ThumbnailProjectAsset{MediaID: mediaID, Role: role}
		if objectID := strings.TrimSpace(obj.ID); objectID != "" {
			asset.ObjectID = &objectID
		}
		assets = append(assets, asset)
	}
	return assets
}

// upsertSnapshotAssets records every media reference of a persisted
// snapshot into thumbnail_project_assets inside the caller's
// transaction. Links are guarded exactly like CreateAsset: the media row
// must exist, be ready, non-expired, and owned by the workspace (or a
// member) or the reference is silently skipped — an image can never be
// linked to a project outside its workspace, and a snapshot save never
// fails because of a stale, missing, or malformed media row. The
// object_id is refreshed to the latest object using that media, so
// duplicated objects keep the link current (note: the PK is
// project_id+media_id+role, so object_id tracks the most recent object
// for that media/role, never a full mapping — the snapshot JSON remains
// the source of truth). Historical links are never deleted (old
// revisions may still reference them).
func (r *ThumbnailProjectRepository) upsertSnapshotAssets(ctx context.Context, tx *sql.Tx, workspaceID int64, projectID string, snapshotJSON json.RawMessage) error {
	assets := snapshotAssetRefs(snapshotJSON)
	if len(assets) == 0 {
		return nil
	}
	for i := range assets {
		asset := &assets[i]
		if _, err := tx.ExecContext(ctx, `
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
			ON CONFLICT ON CONSTRAINT thumbnail_project_assets_project_media_role_pk
			DO UPDATE SET object_id = EXCLUDED.object_id
		`, workspaceID, asset.MediaID, asset.Role, asset.ObjectID, time.Now().UTC(),
			projectID, models.ThumbnailProjectStatusDeleted); err != nil {
			return fmt.Errorf("upsert thumbnail project asset: %w", err)
		}
	}
	return nil
}

// SaveSnapshot appends an immutable revision and advances the project version atomically.
func (r *ThumbnailProjectRepository) SaveSnapshot(ctx context.Context, workspaceID int64, projectID string, snapshot models.ThumbnailProjectSnapshot, createdBy int64) (*models.ThumbnailProjectSnapshotResult, error) {
	canonical, hash, err := validateSnapshot(snapshot, createdBy)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("save snapshot begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	version, err := r.lockProject(ctx, tx, workspaceID, projectID)
	if err != nil {
		return nil, err
	}
	if version != snapshot.BaseVersion {
		return nil, fmt.Errorf("%w: expected=%d current=%d", ErrThumbnailProjectConflict, snapshot.BaseVersion, version)
	}
	// Register the media references of the snapshot (image objects etc.)
	// so thumbnail_project_assets always mirrors the canvas — server
	// side, in the same transaction, on every save (deduplicated or not).
	if err := r.upsertSnapshotAssets(ctx, tx, workspaceID, projectID, canonical); err != nil {
		return nil, err
	}
	existing := &models.ThumbnailProjectRevision{}
	err = tx.QueryRowContext(ctx, `SELECT r.id, r.project_id, r.revision_number, r.schema_version, r.snapshot_json, r.snapshot_sha256, r.renderer_version, r.created_by, r.created_at
		FROM thumbnail_project_revisions r
		JOIN thumbnail_projects p ON p.current_revision_id = r.id
		WHERE r.project_id = $1 AND r.snapshot_sha256 = $2
		ORDER BY r.revision_number DESC LIMIT 1`, projectID, hash).Scan(&existing.ID, &existing.ProjectID, &existing.RevisionNumber, &existing.SchemaVersion, &existing.SnapshotJSON, &existing.SnapshotSHA256, &existing.RendererVersion, &existing.CreatedBy, &existing.CreatedAt)
	if err == nil {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		committed = true
		return revisionResult(projectID, existing, version, true), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	number, err := r.nextRevisionNumber(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	revision := &models.ThumbnailProjectRevision{ID: "thumbrev_" + uuid.NewString(), ProjectID: projectID, RevisionNumber: number, SchemaVersion: snapshot.SchemaVersion, SnapshotJSON: canonical, SnapshotSHA256: hash, RendererVersion: strings.TrimSpace(snapshot.RendererVersion), CreatedBy: createdBy, CreatedAt: now}
	if err := insertThumbnailRevision(ctx, tx, revision); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE thumbnail_projects SET current_revision_id = $1, version = version + 1, updated_at = $2 WHERE workspace_id = $3 AND id = $4 AND version = $5`, revision.ID, now, workspaceID, projectID, version); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return revisionResult(projectID, revision, version+1, false), nil
}

// ListRevisions returns immutable revisions for a visible project.
func (r *ThumbnailProjectRepository) ListRevisions(ctx context.Context, workspaceID int64, projectID string) ([]models.ThumbnailProjectRevision, error) {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM thumbnail_projects WHERE workspace_id = $1 AND id = $2 AND status <> $3)`, workspaceID, projectID, models.ThumbnailProjectStatusDeleted).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("%w: project_id=%s", ErrThumbnailProjectNotFound, projectID)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, project_id, revision_number, schema_version, snapshot_json, snapshot_sha256, renderer_version, created_by, created_at FROM thumbnail_project_revisions WHERE project_id = $1 ORDER BY revision_number DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ThumbnailProjectRevision{}
	for rows.Next() {
		revision, scanErr := scanThumbnailProjectRevision(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, *revision)
	}
	return out, rows.Err()
}

// FindRevision returns one immutable revision within the workspace.
func (r *ThumbnailProjectRepository) FindRevision(ctx context.Context, workspaceID int64, projectID, revisionID string) (*models.ThumbnailProjectRevision, error) {
	revision, err := scanThumbnailProjectRevision(r.db.QueryRowContext(ctx, `SELECT r.id, r.project_id, r.revision_number, r.schema_version, r.snapshot_json, r.snapshot_sha256, r.renderer_version, r.created_by, r.created_at FROM thumbnail_project_revisions r JOIN thumbnail_projects p ON p.id = r.project_id WHERE p.workspace_id = $1 AND p.id = $2 AND r.id = $3 AND p.status <> $4`, workspaceID, projectID, revisionID, models.ThumbnailProjectStatusDeleted))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: revision_id=%s", ErrThumbnailProjectRevisionNotFound, revisionID)
	}
	return revision, err
}

// RestoreRevision always creates a new immutable revision, even for an identical hash.
func (r *ThumbnailProjectRepository) RestoreRevision(ctx context.Context, workspaceID int64, projectID, revisionID string, baseVersion, createdBy int64, rendererVersion string) (*models.ThumbnailProjectSnapshotResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	version, err := r.lockProject(ctx, tx, workspaceID, projectID)
	if err != nil {
		return nil, err
	}
	if version != baseVersion {
		return nil, fmt.Errorf("%w: expected=%d current=%d", ErrThumbnailProjectConflict, baseVersion, version)
	}
	source := &models.ThumbnailProjectRevision{}
	if err := tx.QueryRowContext(ctx, `SELECT id, project_id, revision_number, schema_version, snapshot_json, snapshot_sha256, renderer_version, created_by, created_at FROM thumbnail_project_revisions WHERE project_id = $1 AND id = $2`, projectID, revisionID).Scan(&source.ID, &source.ProjectID, &source.RevisionNumber, &source.SchemaVersion, &source.SnapshotJSON, &source.SnapshotSHA256, &source.RendererVersion, &source.CreatedBy, &source.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: revision_id=%s", ErrThumbnailProjectRevisionNotFound, revisionID)
		}
		return nil, err
	}
	// Restoring an old revision re-establishes its media links (upsert:
	// never deletes, old history keeps its references).
	if err := r.upsertSnapshotAssets(ctx, tx, workspaceID, projectID, source.SnapshotJSON); err != nil {
		return nil, err
	}
	number, err := r.nextRevisionNumber(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(rendererVersion) == "" {
		rendererVersion = source.RendererVersion
	}
	now := time.Now().UTC()
	revision := &models.ThumbnailProjectRevision{ID: "thumbrev_" + uuid.NewString(), ProjectID: projectID, RevisionNumber: number, SchemaVersion: source.SchemaVersion, SnapshotJSON: append(json.RawMessage(nil), source.SnapshotJSON...), SnapshotSHA256: append([]byte(nil), source.SnapshotSHA256...), RendererVersion: strings.TrimSpace(rendererVersion), CreatedBy: createdBy, CreatedAt: now}
	if err := insertThumbnailRevision(ctx, tx, revision); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE thumbnail_projects SET current_revision_id = $1, version = version + 1, updated_at = $2 WHERE workspace_id = $3 AND id = $4 AND version = $5`, revision.ID, now, workspaceID, projectID, version); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return revisionResult(projectID, revision, version+1, false), nil
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
