package repository

import (
	"database/sql"
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
func (r *BookingEventRepository) Insert(event *models.BookingEvent) error {
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
		// Pass empty string when metadata is nil; the COALESCE
		// above interprets NULL → '{}'::jsonb. We avoid using
		// `[]byte`/json.Marshal here so the repo doesn't take
		// on a JSON dependency; the handler/json coupling is a
		// future refactor if marketing ever wants to attach
		// per-row payload extensions.
		"",
	).Scan(&event.ID, &event.CreatedAt)
	if err != nil {
		return fmt.Errorf("booking_events insert: %w", err)
	}
	return nil
}
