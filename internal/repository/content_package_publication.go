package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// Publication projection for Content Packages: publish snapshots, per-target
// publication status joins, the package-state projection (SyncPublicationState),
// and the publication event log. Split from content_package_repo.go (see the
// pointer comment there). These methods are read projections or derived state:
// upload_jobs, post_targets and youtube_target_publications remain the source
// of truth for provider execution.

func scanPublishSnapshot(row interface{ Scan(...any) error }) (*models.PublishSnapshot, error) {
	s := &models.PublishSnapshot{}
	err := row.Scan(&s.ID, &s.ContentScheduleID, &s.ContentPackageID, &s.PackageVersion,
		&s.TargetAccountID, &s.Language, &s.MetadataRevisionID, &s.TranslationBundleID,
		&s.CoverMediaID, &s.CoverTemplateVersionID, &s.SourceMediaAssetID, &s.Title, &s.Description, &s.Tags,
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
		 (content_schedule_id, content_package_id, package_version, target_account_id, language,	 metadata_revision_id, translation_bundle_id, cover_media_id, cover_template_version_id, source_media_asset_id,
		  title, description, tags, privacy_status, publish_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		 ON CONFLICT (content_schedule_id,target_account_id) DO NOTHING
		 RETURNING id, content_schedule_id, content_package_id, package_version, target_account_id,
		           language, metadata_revision_id, translation_bundle_id, cover_media_id, cover_template_version_id,
		           source_media_asset_id, title, description, tags, privacy_status, publish_at, created_at`,
		snapshot.ContentScheduleID, snapshot.ContentPackageID, snapshot.PackageVersion,
		snapshot.TargetAccountID, snapshot.Language, snapshot.MetadataRevisionID,
		snapshot.TranslationBundleID, snapshot.CoverMediaID, snapshot.CoverTemplateVersionID, snapshot.SourceMediaAssetID,
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
		        cover_template_version_id, source_media_asset_id, title, description, tags,
		        privacy_status, publish_at, created_at
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
		        cover_template_version_id, source_media_asset_id, title, description, tags,
		        privacy_status, publish_at, created_at
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

// ListPublicationStatuses exposes the execution state for every frozen
// target. The query deliberately joins the existing upload_jobs/posts/
// post_targets/YouTube publication tables; Content Packages remain the
// product aggregate and do not grow a parallel execution state machine.
func (r *ContentPackageRepository) ListPublicationStatuses(ctx context.Context, packageID int64) ([]*models.ContentPackagePublicationStatus, error) {
	packageIDText := fmt.Sprint(packageID)
	rows, err := r.db.QueryContext(ctx, `
		WITH package_jobs AS (
			SELECT DISTINCT ON (metadata->>'content_schedule_id')
				id, status, post_id, metadata->>'content_schedule_id' AS schedule_id
			FROM upload_jobs
			WHERE metadata->>'content_package_id'=$1
			  AND workspace_id=(SELECT workspace_id FROM content_packages WHERE id=$1::bigint)
			ORDER BY metadata->>'content_schedule_id', id DESC
		)
		SELECT s.content_package_id, s.id, s.target_account_id, s.language, s.title,
		       j.id, j.status, p.id, pt.id, pt.status,
		       y.youtube_video_id, y.thumbnail_status,
		       COALESCE(y.published_at, pt.published_at)
		FROM publish_snapshots s
		LEFT JOIN package_jobs j ON j.schedule_id=s.content_schedule_id::text
		LEFT JOIN posts p ON p.upload_job_id=j.id
		LEFT JOIN post_targets pt ON pt.post_id=p.id AND pt.platform_account_id=s.target_account_id
		LEFT JOIN youtube_target_publications y ON y.post_target_id=pt.id
		WHERE s.content_package_id=$1::bigint
		ORDER BY s.content_schedule_id, s.target_account_id, s.id`, packageIDText)
	if err != nil {
		return nil, fmt.Errorf("list content package publication statuses: %w", err)
	}
	defer rows.Close()
	var out []*models.ContentPackagePublicationStatus
	for rows.Next() {
		status := &models.ContentPackagePublicationStatus{}
		var uploadJobID, postID, postTargetID sql.NullInt64
		var uploadJobStatus, targetStatus sql.NullString
		var videoID, thumbnailStatus sql.NullString
		var publishedAt sql.NullTime
		if err := rows.Scan(&status.ContentPackageID, &status.ContentScheduleID, &status.TargetAccountID, &status.Language, &status.Title,
			&uploadJobID, &uploadJobStatus, &postID, &postTargetID, &targetStatus,
			&videoID, &thumbnailStatus, &publishedAt); err != nil {
			return nil, fmt.Errorf("scan content package publication status: %w", err)
		}
		if uploadJobID.Valid {
			status.UploadJobID = &uploadJobID.Int64
		}
		if uploadJobStatus.Valid {
			status.UploadJobStatus = uploadJobStatus.String
		}
		if postID.Valid {
			status.PostID = &postID.Int64
		}
		if postTargetID.Valid {
			status.PostTargetID = &postTargetID.Int64
		}
		if targetStatus.Valid {
			status.TargetStatus = targetStatus.String
		}
		if videoID.Valid {
			status.YouTubeVideoID = &videoID.String
		}
		if thumbnailStatus.Valid {
			status.ThumbnailStatus = &thumbnailStatus.String
		}
		if publishedAt.Valid {
			status.PublishedAt = &publishedAt.Time
		}
		out = append(out, status)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list content package publication statuses rows: %w", err)
	}
	return out, nil
}

// SyncPublicationState projects the existing per-target execution state onto
// the product-level package state. It is intentionally a projection only:
// upload_jobs, post_targets and youtube_target_publications remain the source
// of truth for provider execution.
func (r *ContentPackageRepository) SyncPublicationState(ctx context.Context, packageID int64) error {
	if packageID <= 0 {
		return errors.New("content package id must be positive")
	}
	statuses, err := r.ListPublicationStatuses(ctx, packageID)
	if err != nil {
		return err
	}
	if len(statuses) == 0 {
		return nil
	}

	published := 0
	failed := 0
	active := 0
	for _, status := range statuses {
		if status == nil {
			continue
		}
		if status.PublishedAt != nil || status.TargetStatus == string(models.PostStatusPublished) {
			published++
			continue
		}
		switch status.TargetStatus {
		case string(models.PostStatusFailed), string(models.PostStatusBlockedAuth), string(models.PostStatusDLQ):
			failed++
		default:
			active++
		}
	}

	var next models.ContentPackageState
	switch {
	case published == len(statuses):
		next = models.ContentPackageStatePublished
	case published > 0 && published+failed == len(statuses):
		next = models.ContentPackageStatePartiallyPublished
	case published > 0 || active > 0:
		next = models.ContentPackageStatePublishing
	case failed == len(statuses):
		next = models.ContentPackageStateBlocked
	default:
		return nil
	}
	_, err = r.db.ExecContext(ctx,
		`UPDATE content_packages SET state=$1, updated_at=NOW()
		 WHERE id=$2 AND state NOT IN ('draft','cancelled')`, next, packageID)
	return err
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
