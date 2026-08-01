package repository

import "database/sql"

// PostRepository handles persistence for posts and post targets. Its methods
// are split across focused files in this package while this type remains the
// single compatibility surface used by callers.
type PostRepository struct {
	db *sql.DB
}

// ns converts a nullable *string model field to sql.NullString.
func ns(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

// NewPostRepository creates a new PostRepository.
func NewPostRepository(db *sql.DB) *PostRepository {
	return &PostRepository{db: db}
}

const contentPipelineSelectColumns = `
	id, workspace_id, title, caption, media_url,
	ingest_after, publish_at, status,
	privacy_level, default_privacy_level,
	created_at, upload_job_id, media_asset_id, storage_object_key, bucket`
