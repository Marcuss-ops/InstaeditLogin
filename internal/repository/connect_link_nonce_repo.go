package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ConnectLinkNonce mirrors a connect_link_nonces row.
// Nonces are issued inside the signed connect-link state JWT
// and must be consumed atomically on first callback.
type ConnectLinkNonce struct {
	Nonce             string
	ExpectedChannelID string
	ExpiresAt         time.Time
	ConsumedAt        *time.Time
	CreatedAt         time.Time
}

// ConnectLinkNonceRepository handles persistence for connect-link nonces.
type ConnectLinkNonceRepository struct {
	db *sql.DB
}

// NewConnectLinkNonceRepository constructs the repo.
func NewConnectLinkNonceRepository(db *sql.DB) *ConnectLinkNonceRepository {
	return &ConnectLinkNonceRepository{db: db}
}

// Create persists a fresh nonce with its expected channel id and expiry.
func (r *ConnectLinkNonceRepository) Create(nonce, expectedChannelID string, expiresAt time.Time) error {
	if nonce == "" {
		return errors.New("connect-link nonce: nonce is required")
	}
	if expectedChannelID == "" {
		return errors.New("connect-link nonce: expected_channel_id is required")
	}
	_, err := r.db.Exec(
		`INSERT INTO connect_link_nonces (nonce, expected_channel_id, expires_at, created_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (nonce) DO NOTHING`,
		nonce, expectedChannelID, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("create connect-link nonce: %w", err)
	}
	return nil
}

// Consume atomically marks a nonce as consumed. It returns true when
// the nonce exists, has not expired, and was not already consumed.
// It returns false for already-consumed, missing, or expired nonces.
func (r *ConnectLinkNonceRepository) Consume(nonce string) (bool, error) {
	if nonce == "" {
		return false, errors.New("connect-link nonce: nonce is required")
	}

	tx, err := r.db.Begin()
	if err != nil {
		return false, fmt.Errorf("consume connect-link nonce: begin tx: %w", err)
	}
	// Always rollback unless we explicitly commit. This keeps the
	// transaction short and avoids leaving dangling tx state for
	// the no-op (already-consumed / expired / missing) paths.
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var expiresAt time.Time
	var consumedAt *time.Time
	err = tx.QueryRow(
		`SELECT expires_at, consumed_at
		 FROM connect_link_nonces
		 WHERE nonce = $1
		 FOR UPDATE`,
		nonce,
	).Scan(&expiresAt, &consumedAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("consume connect-link nonce: select: %w", err)
	}
	if consumedAt != nil {
		return false, nil
	}
	if time.Now().After(expiresAt) {
		return false, nil
	}

	res, err := tx.Exec(
		`UPDATE connect_link_nonces
		 SET consumed_at = NOW()
		 WHERE nonce = $1 AND consumed_at IS NULL`,
		nonce,
	)
	if err != nil {
		return false, fmt.Errorf("consume connect-link nonce: update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("consume connect-link nonce: rows affected: %w", err)
	}
	if n == 0 {
		return false, nil
	}
	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("consume connect-link nonce: commit: %w", err)
	}
	committed = true
	return true, nil
}
