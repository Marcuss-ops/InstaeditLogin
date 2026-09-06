package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

var (
	ErrContentPackageNotFound        = errors.New("content package not found")
	ErrContentPackageVersionConflict = errors.New("content package version conflict")
	ErrContentPackageBlocked         = errors.New("content package is blocked")
)

// ContentPackageRepository owns product-level content intent. It deliberately
// does not create Post or UploadJob rows; those belong to preparation.
type ContentPackageRepository struct{ db *sql.DB }

func NewContentPackageRepository(db *sql.DB) *ContentPackageRepository {
	return &ContentPackageRepository{db: db}
}

type ContentPackageStore interface {
	CreatePackage(ctx context.Context, pkg *models.ContentPackage, revision *models.ContentMetadataRevision) error
	FindPackage(ctx context.Context, workspaceID, packageID int64) (*models.ContentPackage, error)
	FindPackageByID(ctx context.Context, packageID int64) (*models.ContentPackage, error)
	UpdatePackage(ctx context.Context, pkg *models.ContentPackage, expectedVersion int64) error
	ListTargets(ctx context.Context, packageID int64) ([]*models.ContentPackageTarget, error)
	ReplaceTargets(ctx context.Context, packageID int64, expectedVersion int64, targets []*models.ContentPackageTarget) ([]*models.ContentPackageTarget, error)
	CreateMetadataRevision(ctx context.Context, revision *models.ContentMetadataRevision, expectedVersion int64) error
	FindCurrentMetadata(ctx context.Context, packageID int64) (*models.ContentMetadataRevision, error)
	CreateTranslationBundle(ctx context.Context, bundle *models.TranslationBundle, entries []*models.TranslationEntry) error
	FindValidTranslationBundle(ctx context.Context, packageID, revisionID int64, languages []string) (*models.TranslationBundle, map[string]*models.TranslationEntry, error)
	ResolveTranslationEntries(ctx context.Context, packageID, revisionID int64, languages []string) (*models.TranslationBundle, map[string]*models.TranslationEntry, error)
	UpsertManualTranslation(ctx context.Context, packageID, revisionID, expectedVersion int64, entry *models.TranslationEntry) error
	UpsertSchedule(ctx context.Context, schedule *models.ContentSchedule, expectedVersion int64) error
	FindSchedule(ctx context.Context, packageID int64) (*models.ContentSchedule, error)
	ClaimDueSchedules(ctx context.Context, workerID string, lease time.Duration, limit int) ([]*models.ContentSchedule, error)
	MarkSchedulePrepared(ctx context.Context, scheduleID int64, workerID string) error
	MarkScheduleBlocked(ctx context.Context, scheduleID int64, workerID string, reason string) error
	MarkScheduleRetry(ctx context.Context, scheduleID int64, workerID string, nextAttempt time.Time, reason string) error
	CancelSchedule(ctx context.Context, packageID, expectedVersion int64) error
	CreatePublishSnapshot(ctx context.Context, snapshot *models.PublishSnapshot) error
	ListPublishSnapshots(ctx context.Context, scheduleID int64) ([]*models.PublishSnapshot, error)
	ListPublicationStatuses(ctx context.Context, packageID int64) ([]*models.ContentPackagePublicationStatus, error)
	ListPublicationEvents(ctx context.Context, packageID int64) ([]*models.PublicationEvent, error)
	AppendPublicationEvent(ctx context.Context, event *models.PublicationEvent) error
}

// Schedule lifecycle methods (UpsertSchedule, FindSchedule, ClaimDueSchedules,
// MarkSchedulePrepared/Blocked/Retry, CancelSchedule) live in
// content_package_schedule.go. Publication projection methods (snapshots,
// publication statuses/events, SyncPublicationState) live in
// content_package_publication.go. This file keeps the package/target/metadata
// core CRUD plus the shared ContentPackageStore contract.

const contentPackageColumns = `id, workspace_id, created_by, source_type,
drive_account_id, drive_file_id, source_filename, source_fingerprint,
velox_project_id, source_language, current_metadata_revision_id,
current_cover_media_id, current_cover_template_version_id, state, version, created_at, updated_at`

func scanContentPackage(row interface{ Scan(...any) error }) (*models.ContentPackage, error) {
	pkg := &models.ContentPackage{}
	if err := row.Scan(&pkg.ID, &pkg.WorkspaceID, &pkg.CreatedBy, &pkg.SourceType,
		&pkg.DriveAccountID, &pkg.DriveFileID, &pkg.SourceFilename,
		&pkg.SourceFingerprint, &pkg.VeloxProjectID, &pkg.SourceLanguage,
		&pkg.CurrentMetadataRevisionID, &pkg.CurrentCoverMediaID, &pkg.CurrentCoverTemplateVersionID, &pkg.State,
		&pkg.Version, &pkg.CreatedAt, &pkg.UpdatedAt); err != nil {
		return nil, err
	}
	return pkg, nil
}

func scanMetadataRevision(row interface{ Scan(...any) error }) (*models.ContentMetadataRevision, error) {
	r := &models.ContentMetadataRevision{}
	if err := row.Scan(&r.ID, &r.ContentPackageID, &r.RevisionNumber,
		&r.SourceLanguage, &r.Title, &r.Description, &r.Tags, &r.CreatedBy,
		&r.CreatedAt); err != nil {
		return nil, err
	}
	if len(r.Tags) == 0 {
		r.Tags = json.RawMessage("[]")
	}
	return r, nil
}

func scanTarget(row interface{ Scan(...any) error }) (*models.ContentPackageTarget, error) {
	t := &models.ContentPackageTarget{}
	if err := row.Scan(&t.ID, &t.ContentPackageID, &t.PlatformAccountID,
		&t.Language, &t.PrivacyStatus, &t.PlaylistID, &t.CoverMediaID, &t.CoverTemplateVersionID,
		&t.Enabled, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	return t, nil
}

type contentPackageQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func validateCoverTemplateVersionWorkspace(ctx context.Context, q contentPackageQueryer, workspaceID int64, versionID *int64) error {
	if versionID == nil || *versionID <= 0 {
		return nil
	}
	var visible bool
	if err := q.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM cover_template_versions v
			JOIN cover_templates t ON t.id=v.template_id
			WHERE v.id=$1 AND t.workspace_id=$2
		)`, *versionID, workspaceID).Scan(&visible); err != nil {
		return fmt.Errorf("validate cover template workspace: %w", err)
	}
	if !visible {
		return errors.New("cover template version does not belong to the content package workspace")
	}
	return nil
}

func (r *ContentPackageRepository) CreatePackage(ctx context.Context, pkg *models.ContentPackage, revision *models.ContentMetadataRevision) error {
	if pkg == nil || revision == nil || pkg.WorkspaceID <= 0 || pkg.CreatedBy <= 0 || strings.TrimSpace(pkg.DriveFileID) == "" {
		return errors.New("content package, workspace, creator, drive_file_id and initial metadata revision are required")
	}
	if strings.TrimSpace(pkg.SourceType) == "" {
		pkg.SourceType = "google_drive"
	}
	if strings.TrimSpace(pkg.SourceLanguage) == "" {
		pkg.SourceLanguage = revision.SourceLanguage
	}
	if strings.TrimSpace(pkg.SourceLanguage) == "" {
		pkg.SourceLanguage = "it"
	}
	if pkg.State == "" {
		pkg.State = models.ContentPackageStateDraft
	}
	if !pkg.State.IsValid() {
		return fmt.Errorf("invalid content package state %q", pkg.State)
	}
	if pkg.Version <= 0 {
		pkg.Version = 1
	}
	if revision.SourceLanguage == "" {
		revision.SourceLanguage = pkg.SourceLanguage
	}
	if revision.Tags == nil {
		revision.Tags = json.RawMessage("[]")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin content package: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateCoverTemplateVersionWorkspace(ctx, tx, pkg.WorkspaceID, pkg.CurrentCoverTemplateVersionID); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO content_packages
			 (workspace_id, created_by, source_type, drive_account_id, drive_file_id,
		  source_filename, source_fingerprint, velox_project_id, source_language,			 current_cover_media_id, current_cover_template_version_id, state, version)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		 RETURNING `+contentPackageColumns,
		pkg.WorkspaceID, pkg.CreatedBy, pkg.SourceType, pkg.DriveAccountID,
		pkg.DriveFileID, pkg.SourceFilename, pkg.SourceFingerprint,
		pkg.VeloxProjectID, pkg.SourceLanguage, pkg.CurrentCoverMediaID, pkg.CurrentCoverTemplateVersionID, pkg.State, pkg.Version,
	).Scan(&pkg.ID, &pkg.WorkspaceID, &pkg.CreatedBy, &pkg.SourceType,
		&pkg.DriveAccountID, &pkg.DriveFileID, &pkg.SourceFilename,
		&pkg.SourceFingerprint, &pkg.VeloxProjectID, &pkg.SourceLanguage,
		&pkg.CurrentMetadataRevisionID, &pkg.CurrentCoverMediaID, &pkg.CurrentCoverTemplateVersionID, &pkg.State,
		&pkg.Version, &pkg.CreatedAt, &pkg.UpdatedAt); err != nil {
		return fmt.Errorf("insert content package: %w", err)
	}
	revision.ContentPackageID = pkg.ID
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO content_metadata_revisions
		 (content_package_id, revision_number, source_language, title, description, tags, created_by)
		 VALUES ($1,1,$2,$3,$4,$5,$6)
		 RETURNING id, content_package_id, revision_number, source_language, title, description, tags, created_by, created_at`,
		pkg.ID, revision.SourceLanguage, revision.Title, revision.Description,
		revision.Tags, revision.CreatedBy,
	).Scan(&revision.ID, &revision.ContentPackageID, &revision.RevisionNumber,
		&revision.SourceLanguage, &revision.Title, &revision.Description,
		&revision.Tags, &revision.CreatedBy, &revision.CreatedAt); err != nil {
		return fmt.Errorf("insert initial metadata revision: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE content_packages SET current_metadata_revision_id=$1, updated_at=NOW() WHERE id=$2`,
		revision.ID, pkg.ID); err != nil {
		return fmt.Errorf("link initial metadata revision: %w", err)
	}
	pkg.CurrentMetadataRevisionID = &revision.ID
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit content package: %w", err)
	}
	return nil
}

func (r *ContentPackageRepository) FindPackage(ctx context.Context, workspaceID, packageID int64) (*models.ContentPackage, error) {
	pkg, err := scanContentPackage(r.db.QueryRowContext(ctx,
		`SELECT `+contentPackageColumns+` FROM content_packages WHERE workspace_id=$1 AND id=$2`, workspaceID, packageID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrContentPackageNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find content package: %w", err)
	}
	return pkg, nil
}

func (r *ContentPackageRepository) FindPackageByID(ctx context.Context, packageID int64) (*models.ContentPackage, error) {
	pkg, err := scanContentPackage(r.db.QueryRowContext(ctx,
		`SELECT `+contentPackageColumns+` FROM content_packages WHERE id=$1`, packageID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrContentPackageNotFound
	}
	if err != nil {
		return nil, err
	}
	return pkg, nil
}

func (r *ContentPackageRepository) UpdatePackage(ctx context.Context, pkg *models.ContentPackage, expectedVersion int64) error {
	if pkg == nil || pkg.ID <= 0 || expectedVersion <= 0 {
		return errors.New("content package and expected version are required")
	}
	if err := validateCoverTemplateVersionWorkspace(ctx, r.db, pkg.WorkspaceID, pkg.CurrentCoverTemplateVersionID); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx,
		`UPDATE content_packages
		 SET source_filename=$1, source_fingerprint=$2, source_language=$3,
		     current_cover_media_id=$4, current_cover_template_version_id=$5, state=$6, version=version+1, updated_at=NOW()
		 WHERE id=$7 AND workspace_id=$8 AND version=$9`,
		pkg.SourceFilename, pkg.SourceFingerprint, pkg.SourceLanguage,
		pkg.CurrentCoverMediaID, pkg.CurrentCoverTemplateVersionID, pkg.State, pkg.ID, pkg.WorkspaceID, expectedVersion)
	if err != nil {
		return fmt.Errorf("update content package: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrContentPackageVersionConflict
	}
	pkg.Version = expectedVersion + 1
	pkg.UpdatedAt = time.Now().UTC()
	return nil
}

func (r *ContentPackageRepository) ListTargets(ctx context.Context, packageID int64) ([]*models.ContentPackageTarget, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, content_package_id, platform_account_id, language, privacy_status,
		        playlist_id, cover_media_id, cover_template_version_id, enabled, created_at, updated_at
		 FROM content_package_targets WHERE content_package_id=$1 ORDER BY id`, packageID)
	if err != nil {
		return nil, fmt.Errorf("list content package targets: %w", err)
	}
	defer rows.Close()
	var out []*models.ContentPackageTarget
	for rows.Next() {
		t, scanErr := scanTarget(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *ContentPackageRepository) ReplaceTargets(ctx context.Context, packageID int64, expectedVersion int64, targets []*models.ContentPackageTarget) ([]*models.ContentPackageTarget, error) {
	if packageID <= 0 || expectedVersion <= 0 {
		return nil, errors.New("package and expected version are required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var packageWorkspaceID int64
	if err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM content_packages WHERE id=$1 FOR UPDATE`, packageID).Scan(&packageWorkspaceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrContentPackageNotFound
		}
		return nil, fmt.Errorf("find content package workspace: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM content_package_targets WHERE content_package_id=$1`, packageID); err != nil {
		return nil, err
	}
	for _, target := range targets {
		if target == nil || target.PlatformAccountID <= 0 || strings.TrimSpace(target.Language) == "" {
			return nil, errors.New("target account and language are required")
		}
		if err := validateCoverTemplateVersionWorkspace(ctx, tx, packageWorkspaceID, target.CoverTemplateVersionID); err != nil {
			return nil, err
		}
		if target.PrivacyStatus == "" {
			target.PrivacyStatus = "private"
		}
		var belongsToWorkspace bool
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS (
				SELECT 1
				  FROM content_packages p
				  JOIN workspace_channels wc ON wc.workspace_id = p.workspace_id
				 WHERE p.id = $1 AND wc.platform_account_id = $2
			)`, packageID, target.PlatformAccountID).Scan(&belongsToWorkspace); err != nil {
			return nil, fmt.Errorf("validate content package target ownership: %w", err)
		}
		if !belongsToWorkspace {
			return nil, errors.New("target account does not belong to the content package workspace")
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO content_package_targets
			 (content_package_id, platform_account_id, language, privacy_status, playlist_id, cover_media_id, cover_template_version_id, enabled)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, packageID, target.PlatformAccountID,
			target.Language, target.PrivacyStatus, target.PlaylistID, target.CoverMediaID, target.CoverTemplateVersionID, target.Enabled); err != nil {
			return nil, err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE content_packages SET version=version+1, updated_at=NOW() WHERE id=$1 AND version=$2`, packageID, expectedVersion)
	if err != nil {
		return nil, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil, ErrContentPackageVersionConflict
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.ListTargets(ctx, packageID)
}

func (r *ContentPackageRepository) CreateMetadataRevision(ctx context.Context, revision *models.ContentMetadataRevision, expectedVersion int64) error {
	if revision == nil || revision.ContentPackageID <= 0 || revision.CreatedBy <= 0 || expectedVersion <= 0 {
		return errors.New("metadata revision fields are required")
	}
	if revision.Tags == nil {
		revision.Tags = json.RawMessage("[]")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var next int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision_number),0)+1 FROM content_metadata_revisions WHERE content_package_id=$1`, revision.ContentPackageID).Scan(&next); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO content_metadata_revisions
		 (content_package_id, revision_number, source_language, title, description, tags, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 RETURNING id, content_package_id, revision_number, source_language, title, description, tags, created_by, created_at`,
		revision.ContentPackageID, next, revision.SourceLanguage, revision.Title,
		revision.Description, revision.Tags, revision.CreatedBy,
	).Scan(&revision.ID, &revision.ContentPackageID, &revision.RevisionNumber,
		&revision.SourceLanguage, &revision.Title, &revision.Description,
		&revision.Tags, &revision.CreatedBy, &revision.CreatedAt); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx,
		`UPDATE content_packages SET current_metadata_revision_id=$1, version=version+1, updated_at=NOW()
		 WHERE id=$2 AND version=$3`, revision.ID, revision.ContentPackageID, expectedVersion)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrContentPackageVersionConflict
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE translation_bundles SET status='stale' WHERE content_package_id=$1 AND source_metadata_revision_id<>$2 AND status IN ('completed','processing','pending')`,
		revision.ContentPackageID, revision.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ContentPackageRepository) FindCurrentMetadata(ctx context.Context, packageID int64) (*models.ContentMetadataRevision, error) {
	revision, err := scanMetadataRevision(r.db.QueryRowContext(ctx,
		`SELECT mr.id, mr.content_package_id, mr.revision_number, mr.source_language,
		        mr.title, mr.description, mr.tags, mr.created_by, mr.created_at
		 FROM content_metadata_revisions mr
		 JOIN content_packages p ON p.current_metadata_revision_id=mr.id
		 WHERE p.id=$1`, packageID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrContentPackageNotFound
	}
	return revision, err
}

func (r *ContentPackageRepository) CreateTranslationBundle(ctx context.Context, bundle *models.TranslationBundle, entries []*models.TranslationEntry) error {
	if bundle == nil || bundle.ContentPackageID <= 0 || bundle.SourceMetadataRevisionID <= 0 {
		return errors.New("translation bundle fields are required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if bundle.Provider == "" {
		bundle.Provider = "nvidia"
	}
	if bundle.Status == "" {
		bundle.Status = "completed"
	}
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO translation_bundles
		 (content_package_id, source_metadata_revision_id, provider, status, requested_languages, completed_at)
		 VALUES ($1,$2,$3,$4,$5,CASE WHEN $4='completed' THEN NOW() ELSE NULL END)
		 RETURNING id, created_at, completed_at`, bundle.ContentPackageID,
		bundle.SourceMetadataRevisionID, bundle.Provider, bundle.Status,
		pq.Array(bundle.RequestedLanguages)).Scan(&bundle.ID, &bundle.CreatedAt, &bundle.CompletedAt); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry == nil || strings.TrimSpace(entry.Language) == "" {
			return errors.New("translation language is required")
		}
		if entry.Origin == "" {
			entry.Origin = "nvidia"
		}
		if entry.Tags == nil {
			entry.Tags = json.RawMessage("[]")
		}
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO translation_entries (bundle_id, language, title, description, tags, origin)
			 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, created_at, updated_at`,
			bundle.ID, entry.Language, entry.Title, entry.Description, entry.Tags, entry.Origin,
		).Scan(&entry.ID, &entry.CreatedAt, &entry.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *ContentPackageRepository) FindValidTranslationBundle(ctx context.Context, packageID, revisionID int64, languages []string) (*models.TranslationBundle, map[string]*models.TranslationEntry, error) {
	if len(languages) == 0 {
		return nil, map[string]*models.TranslationEntry{}, nil
	}
	b := &models.TranslationBundle{}
	var raw []string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, content_package_id, source_metadata_revision_id, provider, status,
		        requested_languages, created_at, completed_at
		 FROM translation_bundles
		 WHERE content_package_id=$1 AND source_metadata_revision_id=$2 AND status='completed'
		 ORDER BY id DESC LIMIT 1`, packageID, revisionID).Scan(&b.ID, &b.ContentPackageID,
		&b.SourceMetadataRevisionID, &b.Provider, &b.Status, pq.Array(&raw), &b.CreatedAt, &b.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	b.RequestedLanguages = raw
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, bundle_id, language, title, description, tags, origin, created_at, updated_at
		 FROM translation_entries WHERE bundle_id=$1`, b.ID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	entries := make(map[string]*models.TranslationEntry)
	for rows.Next() {
		e := &models.TranslationEntry{}
		if err := rows.Scan(&e.ID, &e.BundleID, &e.Language, &e.Title, &e.Description, &e.Tags, &e.Origin, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, nil, err
		}
		entries[e.Language] = e
	}
	for _, lang := range languages {
		if entries[lang] == nil {
			return nil, nil, nil
		}
	}
	return b, entries, rows.Err()
}

// ResolveTranslationEntries combines completed bundles for one exact source
// revision. Manual entries win over generated entries, and newer bundles win
// over older bundles. A single bundle id is returned when all selected entries
// came from the same bundle; otherwise the newest contributing bundle is used
// for audit display and callers can inspect the event stream for the detail.
func (r *ContentPackageRepository) ResolveTranslationEntries(ctx context.Context, packageID, revisionID int64, languages []string) (*models.TranslationBundle, map[string]*models.TranslationEntry, error) {
	if len(languages) == 0 {
		return nil, map[string]*models.TranslationEntry{}, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT b.id, b.content_package_id, b.source_metadata_revision_id, b.provider,
		        b.status, b.requested_languages, b.created_at, b.completed_at,
		        e.id, e.bundle_id, e.language, e.title, e.description, e.tags,
		        e.origin, e.created_at, e.updated_at
		 FROM translation_bundles b
		 JOIN translation_entries e ON e.bundle_id=b.id
		 WHERE b.content_package_id=$1 AND b.source_metadata_revision_id=$2
		   AND b.status='completed' AND e.language = ANY($3::text[])
		 ORDER BY CASE WHEN e.origin='manual' THEN 0 ELSE 1 END, b.id DESC`, packageID, revisionID, pq.Array(languages))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	entries := make(map[string]*models.TranslationEntry)
	bundles := make(map[int64]*models.TranslationBundle)
	for rows.Next() {
		b := &models.TranslationBundle{}
		e := &models.TranslationEntry{}
		var requested []string
		if err := rows.Scan(&b.ID, &b.ContentPackageID, &b.SourceMetadataRevisionID,
			&b.Provider, &b.Status, pq.Array(&requested), &b.CreatedAt, &b.CompletedAt,
			&e.ID, &e.BundleID, &e.Language, &e.Title, &e.Description, &e.Tags,
			&e.Origin, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, nil, err
		}
		b.RequestedLanguages = requested
		bundles[b.ID] = b
		if _, exists := entries[e.Language]; !exists {
			entries[e.Language] = e
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var selected *models.TranslationBundle
	for _, lang := range languages {
		entry := entries[lang]
		if entry == nil {
			return nil, entries, nil
		}
		if candidate := bundles[entry.BundleID]; selected == nil || (candidate != nil && candidate.ID > selected.ID) {
			selected = candidate
		}
	}
	return selected, entries, nil
}

func (r *ContentPackageRepository) UpsertManualTranslation(ctx context.Context, packageID, revisionID, expectedVersion int64, entry *models.TranslationEntry) error {
	if entry == nil || strings.TrimSpace(entry.Language) == "" {
		return errors.New("translation entry and language are required")
	}
	if entry.Tags == nil {
		entry.Tags = json.RawMessage("[]")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var bundleID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO translation_bundles (content_package_id, source_metadata_revision_id, provider, status, requested_languages, completed_at)
		 VALUES ($1,$2,'manual','completed',ARRAY[$3]::text[],NOW())
		 RETURNING id`, packageID, revisionID, entry.Language).Scan(&bundleID); err != nil {
		return err
	}
	// A manual override is stored in a completed bundle tied to the exact
	// source revision. The newest matching manual bundle wins in the resolver.
	if err := tx.QueryRowContext(ctx,
		`WITH b AS (
		 SELECT $1::bigint AS id
		)
		 INSERT INTO translation_entries (bundle_id, language, title, description, tags, origin)
		 SELECT id,$3,$4,$5,$6,'manual' FROM b
		 ON CONFLICT (bundle_id,language) DO UPDATE SET title=EXCLUDED.title, description=EXCLUDED.description, tags=EXCLUDED.tags, origin='manual', updated_at=NOW()
		 RETURNING id, bundle_id, language, title, description, tags, origin, created_at, updated_at`,
		bundleID, entry.Language, entry.Title, entry.Description, entry.Tags,
	).Scan(&entry.ID, &entry.BundleID, &entry.Language, &entry.Title, &entry.Description,
		&entry.Tags, &entry.Origin, &entry.CreatedAt, &entry.UpdatedAt); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE content_packages SET version=version+1, updated_at=NOW() WHERE id=$1 AND version=$2`, packageID, expectedVersion)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrContentPackageVersionConflict
	}
	return tx.Commit()
}
