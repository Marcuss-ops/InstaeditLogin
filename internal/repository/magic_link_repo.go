// SPRINT 1.2 — magic_link_tokens repository.
//
// Persists login magic-link tokens issued by POST /api/v1/auth/magic-link/start.
// Pattern borrowed from the existing idempotency_records repo: Issue inserts
// a row with token_hash (SHA-256 of the URL-safe plaintext we send in the
// email) and an expires_at = now + ttl, then Consume atomically marks
// consumed_at and returns the row payload. NEVER store the plaintext token.
//
// Single-use is enforced at consume time via UPDATE consumed_at = NOW() and
// the resulting rows-affected count; a replayed token reads no rows.
//
// user_id is nullable because the token may be issued BEFORE the user
// exists (first-time signup). VerifyMagicLink then sets user_id on success.

package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// MagicLinkToken mirrors a magic_link_tokens row.
type MagicLinkToken struct {
	ID         uuid.UUID
	UserID     *int64
	Email      string
	TokenHash  []byte
	Purpose    string
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	CreatedAt  time.Time
}

// ErrMagicLinkTokenNotFound is the sentinel for a missing or expired token.
var ErrMagicLinkTokenNotFound = errors.New("magic link token not found or expired")

// MagicLinkRepository handles persistence for magic_link_tokens.
type MagicLinkRepository struct {
	db *sql.DB
}

// NewMagicLinkRepository constructs the repo.
func NewMagicLinkRepository(db *sql.DB) *MagicLinkRepository {
	return &MagicLinkRepository{db: db}
}

// Issue inserts a fresh token row. tokenHash MUST be the SHA-256 of the
// plaintext we sent (the plaintext itself is not stored). Returns the
// token id as a canonical UUID string so callers (handler layer) can
// satisfy the AuthMagicLinkStore interface contract without depending
// on google/uuid directly.
func (r *MagicLinkRepository) Issue(email string, tokenHash []byte, ttl time.Duration) (string, error) {
	id := uuid.New()
	_, err := r.db.Exec(
		`INSERT INTO magic_link_tokens (id, email, token_hash, expires_at)
		 VALUES ($1, $2, $3, NOW() + $4::interval)`,
		id, email, tokenHash, fmt.Sprintf("%d seconds", int(ttl.Seconds())),
	)
	if err != nil {
		return "", fmt.Errorf("issue magic link: %w", err)
	}
	return id.String(), nil
}

// Consume atomically reads the row by token_hash, marks consumed_at = NOW()
// and binds the user_id (passed in by the caller — may be nil if the user
// was just created in the same transaction). Returns
// ErrMagicLinkTokenNotFound when the row is missing, expired, or already
// consumed.
//
// The rows-affected count is the single-use guarantee: a second Consume
// call sees 0 and returns the not-found sentinel.
func (r *MagicLinkRepository) Consume(tokenHash []byte, userID *int64) (*MagicLinkToken, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("consume magic link: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	row := &MagicLinkToken{}
	var consumedAt sql.NullTime
	var userIDCol sql.NullInt64
	err = tx.QueryRow(
		`SELECT id, user_id, email, token_hash, purpose, expires_at, consumed_at, created_at
		 FROM magic_link_tokens
		 WHERE token_hash = $1
		 FOR UPDATE`,
		tokenHash,
	).Scan(&row.ID, &userIDCol, &row.Email, &row.TokenHash, &row.Purpose,
		&row.ExpiresAt, &consumedAt, &row.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrMagicLinkTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("consume magic link: select: %w", err)
	}
	if userIDCol.Valid {
		v := userIDCol.Int64
		row.UserID = &v
	}
	if consumedAt.Valid {
		t := consumedAt.Time
		row.ConsumedAt = &t
	}
	if row.ConsumedAt != nil {
		return nil, ErrMagicLinkTokenNotFound
	}
	if time.Now().After(row.ExpiresAt) {
		return nil, ErrMagicLinkTokenNotFound
	}

	res, err := tx.Exec(
		`UPDATE magic_link_tokens
		 SET consumed_at = NOW(), user_id = COALESCE(user_id, $1)
		 WHERE id = $2 AND consumed_at IS NULL`,
		userID, row.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("consume magic link: update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("consume magic link: rows affected: %w", err)
	}
	if n == 0 {
		// Race: another in-flight Consume won the claim between our
		// SELECT FOR UPDATE and UPDATE. From the caller's perspective
		// this is the same as "already consumed".
		return nil, ErrMagicLinkTokenNotFound
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("consume magic link: commit: %w", err)
	}
	return row, nil
}
