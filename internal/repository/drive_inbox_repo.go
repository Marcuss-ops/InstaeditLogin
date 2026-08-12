package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

var ErrDriveInboxNotFound = errors.New("drive inbox not found")
var ErrDriveInboxItemNotFound = errors.New("drive inbox item not found")

type DriveInboxStore interface {
	CreateInbox(ctx context.Context, inbox *models.DriveInbox) error
	FindInbox(ctx context.Context, workspaceID, inboxID int64) (*models.DriveInbox, error)
	ListInboxes(ctx context.Context, workspaceID int64) ([]*models.DriveInbox, error)
	ListEnabledInboxes(ctx context.Context) ([]*models.DriveInbox, error)
	MarkInboxScanned(ctx context.Context, inboxID int64, cursor string) error
	ListInboxItems(ctx context.Context, inboxID int64, status string) ([]*models.DriveInboxItem, error)
	UpsertInboxItem(ctx context.Context, item *models.DriveInboxItem) error
	ClaimInboxItem(ctx context.Context, inboxID, itemID, userID int64, pkg *models.ContentPackage, revision *models.ContentMetadataRevision) (*models.ContentPackage, error)
}

type DriveInboxRepository struct{ db *sql.DB }

func NewDriveInboxRepository(db *sql.DB) *DriveInboxRepository { return &DriveInboxRepository{db: db} }

func scanDriveInbox(row interface{ Scan(...any) error }) (*models.DriveInbox, error) {
	i := &models.DriveInbox{}
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.DriveAccountID, &i.FolderID, &i.Enabled, &i.LastScanAt, &i.Cursor, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func scanDriveInboxItem(row interface{ Scan(...any) error }) (*models.DriveInboxItem, error) {
	i := &models.DriveInboxItem{}
	err := row.Scan(&i.ID, &i.InboxID, &i.DriveFileID, &i.Filename, &i.MimeType, &i.SizeBytes, &i.ModifiedTime, &i.Fingerprint, &i.Status, &i.ContentPackageID, &i.FirstSeenAt, &i.LastSeenAt)
	return i, err
}

func (r *DriveInboxRepository) CreateInbox(ctx context.Context, inbox *models.DriveInbox) error {
	if inbox == nil || inbox.WorkspaceID <= 0 || inbox.DriveAccountID <= 0 || strings.TrimSpace(inbox.FolderID) == "" {
		return errors.New("workspace, drive account and folder_id are required")
	}
	if err := r.db.QueryRowContext(ctx,
		`INSERT INTO drive_inboxes (workspace_id, drive_account_id, folder_id, enabled)
		 VALUES ($1,$2,$3,$4) RETURNING id, workspace_id, drive_account_id, folder_id, enabled, last_scan_at, cursor, created_at, updated_at`,
		inbox.WorkspaceID, inbox.DriveAccountID, inbox.FolderID, inbox.Enabled).Scan(&inbox.ID, &inbox.WorkspaceID, &inbox.DriveAccountID, &inbox.FolderID, &inbox.Enabled, &inbox.LastScanAt, &inbox.Cursor, &inbox.CreatedAt, &inbox.UpdatedAt); err != nil {
		return fmt.Errorf("create drive inbox: %w", err)
	}
	return nil
}

func (r *DriveInboxRepository) FindInbox(ctx context.Context, workspaceID, inboxID int64) (*models.DriveInbox, error) {
	inbox, err := scanDriveInbox(r.db.QueryRowContext(ctx,
		`SELECT id, workspace_id, drive_account_id, folder_id, enabled, last_scan_at, cursor, created_at, updated_at FROM drive_inboxes WHERE workspace_id=$1 AND id=$2`, workspaceID, inboxID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDriveInboxNotFound
	}
	return inbox, err
}

func (r *DriveInboxRepository) ListInboxes(ctx context.Context, workspaceID int64) ([]*models.DriveInbox, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, workspace_id, drive_account_id, folder_id, enabled, last_scan_at, cursor, created_at, updated_at
		 FROM drive_inboxes WHERE workspace_id=$1 ORDER BY id DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.DriveInbox
	for rows.Next() {
		i, scanErr := scanDriveInbox(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (r *DriveInboxRepository) ListEnabledInboxes(ctx context.Context) ([]*models.DriveInbox, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, workspace_id, drive_account_id, folder_id, enabled, last_scan_at, cursor, created_at, updated_at
		 FROM drive_inboxes WHERE enabled=true ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.DriveInbox
	for rows.Next() {
		i, scanErr := scanDriveInbox(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (r *DriveInboxRepository) MarkInboxScanned(ctx context.Context, inboxID int64, cursor string) error {
	var value any
	if strings.TrimSpace(cursor) != "" {
		value = cursor
	}
	_, err := r.db.ExecContext(ctx, `UPDATE drive_inboxes SET cursor=$1, last_scan_at=NOW(), updated_at=NOW() WHERE id=$2`, value, inboxID)
	return err
}

func (r *DriveInboxRepository) ListInboxItems(ctx context.Context, inboxID int64, status string) ([]*models.DriveInboxItem, error) {
	query := `SELECT id, inbox_id, drive_file_id, filename, mime_type, size_bytes, modified_time, fingerprint, status, content_package_id, first_seen_at, last_seen_at FROM drive_inbox_items WHERE inbox_id=$1`
	args := []any{inboxID}
	if strings.TrimSpace(status) != "" {
		query += ` AND status=$2`
		args = append(args, status)
	}
	query += ` ORDER BY last_seen_at DESC, id DESC`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.DriveInboxItem
	for rows.Next() {
		i, scanErr := scanDriveInboxItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (r *DriveInboxRepository) UpsertInboxItem(ctx context.Context, item *models.DriveInboxItem) error {
	if item == nil || item.InboxID <= 0 || strings.TrimSpace(item.DriveFileID) == "" {
		return errors.New("inbox_id and drive_file_id are required")
	}
	return r.db.QueryRowContext(ctx,
		`INSERT INTO drive_inbox_items (inbox_id, drive_file_id, filename, mime_type, size_bytes, modified_time, fingerprint, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,'ready_for_review')
		 ON CONFLICT (inbox_id, drive_file_id) DO UPDATE SET filename=EXCLUDED.filename, mime_type=EXCLUDED.mime_type, size_bytes=EXCLUDED.size_bytes, modified_time=EXCLUDED.modified_time, fingerprint=EXCLUDED.fingerprint, last_seen_at=NOW(), status=CASE WHEN drive_inbox_items.status IN ('claimed','ignored') THEN drive_inbox_items.status ELSE 'ready_for_review' END
		 RETURNING id, inbox_id, drive_file_id, filename, mime_type, size_bytes, modified_time, fingerprint, status, content_package_id, first_seen_at, last_seen_at`,
		item.InboxID, item.DriveFileID, item.Filename, item.MimeType, item.SizeBytes, item.ModifiedTime, item.Fingerprint).Scan(&item.ID, &item.InboxID, &item.DriveFileID, &item.Filename, &item.MimeType, &item.SizeBytes, &item.ModifiedTime, &item.Fingerprint, &item.Status, &item.ContentPackageID, &item.FirstSeenAt, &item.LastSeenAt)
}

// ClaimInboxItem atomically creates the editable package and links the item.
// It is intentionally not an upload operation: no media asset or UploadJob is
// created here.
func (r *DriveInboxRepository) ClaimInboxItem(ctx context.Context, inboxID, itemID, userID int64, pkg *models.ContentPackage, revision *models.ContentMetadataRevision) (*models.ContentPackage, error) {
	if pkg == nil || revision == nil || inboxID <= 0 || itemID <= 0 || userID <= 0 {
		return nil, errors.New("inbox claim fields are required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var workspaceID, driveAccountID int64
	var driveFileID, filename, fingerprint string
	err = tx.QueryRowContext(ctx,
		`SELECT i.inbox_id, b.workspace_id, b.drive_account_id, i.drive_file_id, i.filename, i.fingerprint
		 FROM drive_inbox_items i JOIN drive_inboxes b ON b.id=i.inbox_id
		 JOIN workspaces w ON w.id=b.workspace_id
		 WHERE i.id=$1 AND i.inbox_id=$2 AND i.status NOT IN ('ignored','claimed')
		   AND (w.owner_id=$3 OR EXISTS (SELECT 1 FROM workspace_members wm WHERE wm.workspace_id=w.id AND wm.user_id=$3))
		 FOR UPDATE`, itemID, inboxID, userID).Scan(&inboxID, &workspaceID, &driveAccountID, &driveFileID, &filename, &fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDriveInboxItemNotFound
	}
	if err != nil {
		return nil, err
	}
	pkg.WorkspaceID, pkg.CreatedBy, pkg.DriveAccountID, pkg.DriveFileID, pkg.SourceFilename, pkg.SourceFingerprint = workspaceID, userID, &driveAccountID, driveFileID, filename, fingerprint
	if pkg.SourceType == "" {
		pkg.SourceType = "google_drive"
	}
	pkg.State = models.ContentPackageStateDraft
	pkg.Version = 1
	if pkg.SourceLanguage == "" {
		pkg.SourceLanguage = revision.SourceLanguage
	}
	if pkg.SourceLanguage == "" {
		pkg.SourceLanguage = "it"
	}
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO content_packages (workspace_id, created_by, source_type, drive_account_id, drive_file_id, source_filename, source_fingerprint, source_language, current_cover_media_id, state, version)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'draft',1) RETURNING id, created_at, updated_at`, pkg.WorkspaceID, pkg.CreatedBy, pkg.SourceType, pkg.DriveAccountID, pkg.DriveFileID, pkg.SourceFilename, pkg.SourceFingerprint, pkg.SourceLanguage, pkg.CurrentCoverMediaID).Scan(&pkg.ID, &pkg.CreatedAt, &pkg.UpdatedAt); err != nil {
		return nil, err
	}
	revision.ContentPackageID = pkg.ID
	if revision.SourceLanguage == "" {
		revision.SourceLanguage = pkg.SourceLanguage
	}
	if revision.Tags == nil {
		revision.Tags = []byte("[]")
	}
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO content_metadata_revisions (content_package_id, revision_number, source_language, title, description, tags, created_by)
		 VALUES ($1,1,$2,$3,$4,$5,$6) RETURNING id, revision_number, created_at`, pkg.ID, revision.SourceLanguage, revision.Title, revision.Description, revision.Tags, userID).Scan(&revision.ID, &revision.RevisionNumber, &revision.CreatedAt); err != nil {
		return nil, err
	}
	pkg.CurrentMetadataRevisionID = &revision.ID
	revision.CreatedBy = userID
	if _, err := tx.ExecContext(ctx, `UPDATE content_packages SET current_metadata_revision_id=$1 WHERE id=$2`, revision.ID, pkg.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE drive_inbox_items SET status='claimed', content_package_id=$1, last_seen_at=NOW() WHERE id=$2 AND status NOT IN ('ignored','claimed')`, pkg.ID, itemID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return pkg, nil
}
