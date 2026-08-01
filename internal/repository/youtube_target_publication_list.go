package repository

import (
	"context"
	"fmt"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ListByPostTargetIDs (Blocco Carosello content-pipeline endpoint) returns
// every youtube_target_publications row whose post_target_id appears
// in the supplied slice. (nil, nil) when postTargetIDs is empty or
// no rows match. One round-trip using WHERE post_target_id =
// ANY($1::bigint[]) so the response scales with the post's target
// fan-out (typically 1..30 rows) rather than the full table. The
// post_target_id = ANY predicate hits the UNIQUE index introduced by
// migration 066's UNIQUE(post_target_id) constraint, so the planner
// uses an index-only scan.
func (r *YouTubeTargetPublicationRepository) ListByPostTargetIDs(ctx context.Context, postTargetIDs []int64) ([]*models.YouTubeTargetPublication, error) {
	if len(postTargetIDs) == 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+ytTargetPubsSelectColumns+`
		 FROM youtube_target_publications
		 WHERE post_target_id = ANY($1::bigint[])
		 ORDER BY id ASC`, pq.Array(postTargetIDs))
	if err != nil {
		return nil, fmt.Errorf("youtube target publication ListByPostTargetIDs: %w", err)
	}
	defer rows.Close()

	var out []*models.YouTubeTargetPublication
	for rows.Next() {
		pub := &models.YouTubeTargetPublication{}
		if err := scanYouTubeTargetPublication(rows, pub); err != nil {
			return nil, fmt.Errorf("youtube target publication ListByPostTargetIDs scan: %w", err)
		}
		out = append(out, pub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("youtube target publication ListByPostTargetIDs rows: %w", err)
	}
	return out, nil
}

// ListByUploadJobID returns all publication rows tied to an upload job,
// ordered by id ASC. The pipeline view endpoint reads this and joins onto
// drive/storage accounts; empty result is a valid "no fan-out yet" state.
func (r *YouTubeTargetPublicationRepository) ListByUploadJobID(ctx context.Context, uploadJobID int64) ([]*models.YouTubeTargetPublication, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+ytTargetPubsSelectColumns+`
		 FROM youtube_target_publications
		 WHERE upload_job_id = $1
		 ORDER BY id ASC`, uploadJobID)
	if err != nil {
		return nil, fmt.Errorf("youtube target publication ListByUploadJobID: %w", err)
	}
	defer rows.Close()

	var out []*models.YouTubeTargetPublication
	for rows.Next() {
		pub := &models.YouTubeTargetPublication{}
		if err := scanYouTubeTargetPublication(rows, pub); err != nil {
			return nil, fmt.Errorf("youtube target publication ListByUploadJobID scan: %w", err)
		}
		out = append(out, pub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("youtube target publication ListByUploadJobID rows: %w", err)
	}
	return out, nil
}
