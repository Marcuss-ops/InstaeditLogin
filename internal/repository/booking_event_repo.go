package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// BookingEventRepository persists the BookingEvent rows produced
// by the strategy-call modal on the marketing site. The
// Insert method is INSERT-OR-UPDATE on dedupe_hash so a
// refresh-spam from the same browser with the same answers
// lands idempotently as the same id; the sales team can
// grep the dedupe_hash directly when they need to de-duplicate
// the CSV export.
type BookingEventRepository struct {
	db *sql.DB
}

// NewBookingEventRepository constructs a BookingEventRepository
// bound to the supplied *sql.DB. Production wiring in
// internal/bootstrap/app.go passes the application DB; tests
// pass an in-memory fake.
func NewBookingEventRepository(db *sql.DB) *BookingEventRepository {
	return &BookingEventRepository{db: db}
}

// Insert persists a booking event row. The SQL uses
// `ON CONFLICT (dedupe_hash) DO UPDATE SET dedupe_hash = booking_events.dedupe_hash`
// to satisfy Postgres' DO UPDATE non-empty-set requirement while
// being a true no-op (the value being assigned is the same as the
// existing one). The RETURNING clause always yields a row, so the
// caller never has to handle sql.ErrNoRows: the same id is
// returned whether we just inserted or hit a dedupe collision.
//
// The caller is responsible for computing `event.DedupeHash` and
// `event.IPHash` (the SHA-256 hashes). Doing the hashing in the
// handler keeps the repository side pure-SQL so the SQL stays
// reviewable in isolation.
//
// Metadata is JSON-marshaled here rather than in the handler so the
// handler stays JSON-decode-only and the repo owns the JSONB encoding
// policy. An empty/nil map yields `[]byte("{}")`; the SQL COALESCE
// downgrades an empty string to `'{}'::jsonb` so legacy rows keep
// the constraint `metadata NOT NULL`. The optional payload extensions
// (utm_source / utm_campaign / etc.) ride this column when the
// upstream scheduler's redirect chain strips query strings — see
// `pkg/api/booking_events.go::handleCreateBookingEvent` and the
// `BookingProvider` modal in `web/src/components/booking/`.
func (r *BookingEventRepository) Insert(event *models.BookingEvent) error {
	// Marshal-once + bound to []byte so we don't take on a JSON
	// dependency in the handler; the array form also round-trips
	// through pgx correctly (passing a map[string]any as the SQL
	// driver value would require a pgx type-specific hook).
	var metadataJSON []byte
	if len(event.Metadata) > 0 {
		var err error
		metadataJSON, err = json.Marshal(event.Metadata)
		if err != nil {
			return fmt.Errorf("booking_events insert: marshal metadata: %w", err)
		}
	}

	err := r.db.QueryRow(
		`INSERT INTO booking_events (
			intent, goal, budget, ready,
			ip_hash, user_agent, referer,
			dedupe_hash, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9::jsonb, '{}'::jsonb))
		ON CONFLICT (dedupe_hash) DO UPDATE
			SET dedupe_hash = booking_events.dedupe_hash
		RETURNING id, created_at`,
		event.Intent, event.Goal, event.Budget, event.Ready,
		event.IPHash, event.UserAgent, event.Referer,
		event.DedupeHash,
		// Empty []byte when Metadata is nil/empty -> COALESCE in
		// the SQL drops it to '{}'::jsonb so the NOT NULL column
		// constraint always has a valid JSON object.
		string(metadataJSON),
	).Scan(&event.ID, &event.CreatedAt)
	if err != nil {
		return fmt.Errorf("booking_events insert: %w", err)
	}
	return nil
}
