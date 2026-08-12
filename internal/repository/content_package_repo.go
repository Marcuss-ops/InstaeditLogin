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
	ListPublicationEvents(ctx context.Context, packageID int64) ([]*models.PublicationEvent, error)
	AppendPublicationEvent(ctx context.Context, event *models.PublicationEvent) error
}

const contentPackageColumns = `id, workspace_id, created_by, source_type,
drive_account_id, drive_file_id, source_filename, source_fingerprint,
velox_project_id, source_language, current_metadata_revision_id,
current_cover_media_id, state, version, created_at, updated_at`

func scanContentPackage(row interface{ Scan(...any) error }) (*models.ContentPackage, error) {
	pkg := &models.ContentPackage{}
	if err := row.Scan(&pkg.ID, &pkg.WorkspaceID, &pkg.CreatedBy, &pkg.SourceType,
		&pkg.DriveAccountID, &pkg.DriveFileID, &pkg.SourceFilename,
		&pkg.SourceFingerprint, &pkg.VeloxProjectID, &pkg.SourceLanguage,
		&pkg.CurrentMetadataRevisionID, &pkg.CurrentCoverMediaID, &pkg.State,
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
		&t.Language, &t.PrivacyStatus, &t.PlaylistID, &t.Enabled, &t.CreatedAt,
		&t.UpdatedAt); err != nil {
		return nil, err
	}
	return t, nil
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
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO content_packages
			 (workspace_id, created_by, source_type, drive_account_id, drive_file_id,
		  source_filename, source_fingerprint, velox_project_id, source_language,
		  current_cover_media_id, state, version)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		 RETURNING `+contentPackageColumns,
		pkg.WorkspaceID, pkg.CreatedBy, pkg.SourceType, pkg.DriveAccountID,
		pkg.DriveFileID, pkg.SourceFilename, pkg.SourceFingerprint,
		pkg.VeloxProjectID, pkg.SourceLanguage, pkg.CurrentCoverMediaID, pkg.State, pkg.Version,
	).Scan(&pkg.ID, &pkg.WorkspaceID, &pkg.CreatedBy, &pkg.SourceType,
		&pkg.DriveAccountID, &pkg.DriveFileID, &pkg.SourceFilename,
		&pkg.SourceFingerprint, &pkg.VeloxProjectID, &pkg.SourceLanguage,
		&pkg.CurrentMetadataRevisionID, &pkg.CurrentCoverMediaID, &pkg.State,
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
	result, err := r.db.ExecContext(ctx,
		`UPDATE content_packages
		 SET source_filename=$1, source_fingerprint=$2, source_language=$3,
		     current_cover_media_id=$4, state=$5, version=version+1, updated_at=NOW()
		 WHERE id=$6 AND workspace_id=$7 AND version=$8`,
		pkg.SourceFilename, pkg.SourceFingerprint, pkg.SourceLanguage,
		pkg.CurrentCoverMediaID, pkg.State, pkg.ID, pkg.WorkspaceID, expectedVersion)
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
		        playlist_id, enabled, created_at, updated_at
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM content_package_targets WHERE content_package_id=$1`, packageID); err != nil {
		return nil, err
	}
	for _, target := range targets {
		if target == nil || target.PlatformAccountID <= 0 || strings.TrimSpace(target.Language) == "" {
			return nil, errors.New("target account and language are required")
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
			 (content_package_id, platform_account_id, language, privacy_status, playlist_id, enabled)
			 VALUES ($1,$2,$3,$4,$5,$6)`, packageID, target.PlatformAccountID,
			target.Language, target.PrivacyStatus, target.PlaylistID, target.Enabled); err != nil {
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

func (r *ContentPackageRepository) UpsertSchedule(ctx context.Context, schedule *models.ContentSchedule, expectedVersion int64) error {
	if schedule == nil || schedule.ContentPackageID <= 0 || expectedVersion <= 0 || schedule.Timezone == "" || !schedule.PrepareAt.Before(schedule.ScheduledAt) {
		return errors.New("valid schedule and expected package version are required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO content_schedules (content_package_id, scheduled_at, prepare_at, timezone, status, package_version)
		 SELECT $1,$2,$3,$4,'scheduled',version+1 FROM content_packages WHERE id=$1 AND version=$5
		 ON CONFLICT (content_package_id) DO UPDATE SET scheduled_at=EXCLUDED.scheduled_at, prepare_at=EXCLUDED.prepare_at, timezone=EXCLUDED.timezone, status='scheduled', package_version=EXCLUDED.package_version, updated_at=NOW()
		 RETURNING id, content_package_id, scheduled_at, prepare_at, timezone, status, package_version, created_at, updated_at`,
		schedule.ContentPackageID, schedule.ScheduledAt, schedule.PrepareAt, schedule.Timezone, expectedVersion,
	).Scan(&schedule.ID, &schedule.ContentPackageID, &schedule.ScheduledAt, &schedule.PrepareAt,
		&schedule.Timezone, &schedule.Status, &schedule.PackageVersion, &schedule.CreatedAt, &schedule.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrContentPackageVersionConflict
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE content_packages SET state='scheduled', version=version+1, updated_at=NOW() WHERE id=$1 AND version=$2`, schedule.ContentPackageID, expectedVersion); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ContentPackageRepository) FindSchedule(ctx context.Context, packageID int64) (*models.ContentSchedule, error) {
	s := &models.ContentSchedule{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, content_package_id, scheduled_at, prepare_at, timezone, status, package_version, created_at, updated_at, lease_owner, lease_expires_at, heartbeat_at, attempt_count, next_attempt_at FROM content_schedules WHERE content_package_id=$1`, packageID).Scan(&s.ID, &s.ContentPackageID, &s.ScheduledAt, &s.PrepareAt, &s.Timezone, &s.Status, &s.PackageVersion, &s.CreatedAt, &s.UpdatedAt, &s.LeaseOwner, &s.LeaseExpiresAt, &s.HeartbeatAt, &s.AttemptCount, &s.NextAttemptAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return s, err
}

func (r *ContentPackageRepository) ClaimDueSchedules(ctx context.Context, workerID string, lease time.Duration, limit int) ([]*models.ContentSchedule, error) {
	if workerID == "" {
		return nil, errors.New("worker id is required")
	}
	if lease <= 0 {
		lease = 5 * time.Minute
	}
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows, err := r.db.QueryContext(ctx,
		`WITH candidates AS (
		 SELECT id FROM content_schedules
		 WHERE status IN ('scheduled','preparing') AND prepare_at <= NOW()
		   AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())
		   AND (lease_expires_at IS NULL OR lease_expires_at <= NOW())
		 ORDER BY prepare_at, id LIMIT $1 FOR UPDATE SKIP LOCKED
		)
		UPDATE content_schedules s
		 SET status='preparing', lease_owner=$2, lease_expires_at=NOW()+$3::interval,
		     heartbeat_at=NOW(), updated_at=NOW()
		 FROM candidates c WHERE s.id=c.id
		 RETURNING s.id, s.content_package_id, s.scheduled_at, s.prepare_at, s.timezone,
		           s.status, s.package_version, s.created_at, s.updated_at,
		           s.lease_owner, s.lease_expires_at, s.heartbeat_at, s.attempt_count, s.next_attempt_at`,
		limit, workerID, fmt.Sprintf("%f seconds", lease.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ContentSchedule
	for rows.Next() {
		s := &models.ContentSchedule{}
		if err := rows.Scan(&s.ID, &s.ContentPackageID, &s.ScheduledAt, &s.PrepareAt, &s.Timezone, &s.Status, &s.PackageVersion, &s.CreatedAt, &s.UpdatedAt, &s.LeaseOwner, &s.LeaseExpiresAt, &s.HeartbeatAt, &s.AttemptCount, &s.NextAttemptAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *ContentPackageRepository) MarkSchedulePrepared(ctx context.Context, scheduleID int64, workerID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var packageID int64
	if err := tx.QueryRowContext(ctx,
		`UPDATE content_schedules
		 SET status='ready_to_publish', lease_owner=NULL, lease_expires_at=NULL,
		     heartbeat_at=NULL, updated_at=NOW()
		 WHERE id=$1 AND status='preparing' AND lease_owner=$2
		 RETURNING content_package_id`, scheduleID, workerID).Scan(&packageID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrContentPackageVersionConflict
		}
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE content_packages SET state='ready_to_publish', updated_at=NOW() WHERE id=$1`, packageID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ContentPackageRepository) MarkScheduleBlocked(ctx context.Context, scheduleID int64, workerID, reason string) error {
	_ = reason // The publication event stores the human-readable blocker.
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var packageID int64
	if err := tx.QueryRowContext(ctx,
		`UPDATE content_schedules
		 SET status='blocked', lease_owner=NULL, lease_expires_at=NULL,
		     heartbeat_at=NULL, updated_at=NOW()
		 WHERE id=$1 AND status='preparing' AND lease_owner=$2
		 RETURNING content_package_id`, scheduleID, workerID).Scan(&packageID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrContentPackageVersionConflict
		}
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE content_packages SET state='blocked', updated_at=NOW() WHERE id=$1`, packageID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ContentPackageRepository) MarkScheduleRetry(ctx context.Context, scheduleID int64, workerID string, nextAttempt time.Time, reason string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE content_schedules SET status='scheduled', attempt_count=attempt_count+1, next_attempt_at=$1, lease_owner=NULL, lease_expires_at=NULL, heartbeat_at=NULL, updated_at=NOW() WHERE id=$2 AND status='preparing' AND lease_owner=$3`, nextAttempt, scheduleID, workerID)
	return err
}

func (r *ContentPackageRepository) CancelSchedule(ctx context.Context, packageID, expectedVersion int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx,
		`UPDATE content_schedules SET status='cancelled', updated_at=NOW()
		 WHERE content_package_id=$1 AND status NOT IN ('published','cancelled')`, packageID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrContentPackageNotFound
	}
	result, err = tx.ExecContext(ctx,
		`UPDATE content_packages SET state='draft', version=version+1, updated_at=NOW() WHERE id=$1 AND version=$2`, packageID, expectedVersion)
	if err != nil {
		return err
	}
	n, _ = result.RowsAffected()
	if n == 0 {
		return ErrContentPackageVersionConflict
	}
	return tx.Commit()
}

func scanPublishSnapshot(row interface{ Scan(...any) error }) (*models.PublishSnapshot, error) {
	s := &models.PublishSnapshot{}
	err := row.Scan(&s.ID, &s.ContentScheduleID, &s.ContentPackageID, &s.PackageVersion,
		&s.TargetAccountID, &s.Language, &s.MetadataRevisionID, &s.TranslationBundleID,
		&s.CoverMediaID, &s.SourceMediaAssetID, &s.Title, &s.Description, &s.Tags,
		&s.PrivacyStatus, &s.PublishAt, &s.CreatedAt)
	if len(s.Tags) == 0 {
		s.Tags = json.RawMessage("[]")
	}
	return s, err
}

func (r *ContentPackageRepository) CreatePublishSnapshot(ctx context.Context, snapshot *models.PublishSnapshot) error {
	if snapshot == nil || snapshot.ContentScheduleID <= 0 || snapshot.ContentPackageID <= 0 || snapshot.TargetAccountID <= 0 || snapshot.MetadataRevisionID <= 0 {
		return errors.New("publish snapshot fields are required")
	}
	if snapshot.Tags == nil {
		snapshot.Tags = json.RawMessage("[]")
	}
	if snapshot.PrivacyStatus == "" {
		snapshot.PrivacyStatus = "private"
	}
	row := r.db.QueryRowContext(ctx,
		`INSERT INTO publish_snapshots
		 (content_schedule_id, content_package_id, package_version, target_account_id, language,
		  metadata_revision_id, translation_bundle_id, cover_media_id, source_media_asset_id,
		  title, description, tags, privacy_status, publish_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		 ON CONFLICT (content_schedule_id,target_account_id) DO NOTHING
		 RETURNING id, content_schedule_id, content_package_id, package_version, target_account_id,
		           language, metadata_revision_id, translation_bundle_id, cover_media_id,
		           source_media_asset_id, title, description, tags, privacy_status, publish_at, created_at`,
		snapshot.ContentScheduleID, snapshot.ContentPackageID, snapshot.PackageVersion,
		snapshot.TargetAccountID, snapshot.Language, snapshot.MetadataRevisionID,
		snapshot.TranslationBundleID, snapshot.CoverMediaID, snapshot.SourceMediaAssetID,
		snapshot.Title, snapshot.Description, snapshot.Tags, snapshot.PrivacyStatus,
		snapshot.PublishAt)
	created, err := scanPublishSnapshot(row)
	if err == nil {
		*snapshot = *created
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	existing, err := scanPublishSnapshot(r.db.QueryRowContext(ctx,
		`SELECT id, content_schedule_id, content_package_id, package_version, target_account_id,
		        language, metadata_revision_id, translation_bundle_id, cover_media_id,
		        source_media_asset_id, title, description, tags, privacy_status, publish_at, created_at
		 FROM publish_snapshots WHERE content_schedule_id=$1 AND target_account_id=$2`, snapshot.ContentScheduleID, snapshot.TargetAccountID))
	if err != nil {
		return err
	}
	*snapshot = *existing
	return nil
}

func (r *ContentPackageRepository) ListPublishSnapshots(ctx context.Context, scheduleID int64) ([]*models.PublishSnapshot, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, content_schedule_id, content_package_id, package_version, target_account_id,
		        language, metadata_revision_id, translation_bundle_id, cover_media_id,
		        source_media_asset_id, title, description, tags, privacy_status, publish_at, created_at
		 FROM publish_snapshots WHERE content_schedule_id=$1 ORDER BY target_account_id, id`, scheduleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.PublishSnapshot
	for rows.Next() {
		s, scanErr := scanPublishSnapshot(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *ContentPackageRepository) AppendPublicationEvent(ctx context.Context, event *models.PublicationEvent) error {
	if event == nil || event.ContentPackageID <= 0 || event.Stage == "" || event.EventType == "" {
		return errors.New("publication event fields are required")
	}
	return r.db.QueryRowContext(ctx,
		`INSERT INTO publication_events (content_package_id, content_schedule_id, target_publication_id, stage, event_type, attempt_no, error_code, message)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, occurred_at`, event.ContentPackageID,
		event.ContentScheduleID, event.TargetPublicationID, event.Stage, event.EventType,
		event.AttemptNo, event.ErrorCode, event.Message).Scan(&event.ID, &event.OccurredAt)
}

func (r *ContentPackageRepository) ListPublicationEvents(ctx context.Context, packageID int64) ([]*models.PublicationEvent, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, content_package_id, content_schedule_id, target_publication_id, stage, event_type, attempt_no, error_code, message, occurred_at
		 FROM publication_events WHERE content_package_id=$1 ORDER BY occurred_at DESC, id DESC`, packageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []*models.PublicationEvent
	for rows.Next() {
		e := &models.PublicationEvent{}
		if err := rows.Scan(&e.ID, &e.ContentPackageID, &e.ContentScheduleID, &e.TargetPublicationID, &e.Stage, &e.EventType, &e.AttemptNo, &e.ErrorCode, &e.Message, &e.OccurredAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
