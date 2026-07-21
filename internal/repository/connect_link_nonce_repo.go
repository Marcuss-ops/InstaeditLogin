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

// Sentinel errors returned by Consume so callers can distinguish
// why a connect-link nonce was rejected. These are intentionally
// distinct from database/internal errors so the OAuth callback can
// map them to a 410 Gone with a clear reason for operators.
var (
	ErrNonceMissing  = errors.New("connect-link nonce missing")
	ErrNonceExpired  = errors.New("connect-link nonce expired")
	ErrNonceConsumed = errors.New("connect-link nonce already consumed")
)

// Create persists a fresh jti with its expected channel id and expiry.
// The jti is the JWT's RegisteredClaims.ID (formerly exposed as a
// custom "nonce" claim).
func (r *ConnectLinkNonceRepository) Create(jti, expectedChannelID string, expiresAt time.Time) error {
	if jti == "" {
		return errors.New("connect-link jti: jti is required")
	}
	if expectedChannelID == "" {
		return errors.New("connect-link jti: expected_channel_id is required")
	}
	_, err := r.db.Exec(
		`INSERT INTO connect_link_nonces (nonce, expected_channel_id, expires_at, created_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (nonce) DO NOTHING`,
		jti, expectedChannelID, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("create connect-link jti: %w", err)
	}
	return nil
}

// Consume atomically marks a jti as consumed. It returns nil on
// success. For known rejection cases it returns one of the sentinel
// errors ErrNonceMissing, ErrNonceExpired, or ErrNonceConsumed so the
// caller can log/metric the exact reason. Any other error indicates
// a database or transaction failure.
func (r *ConnectLinkNonceRepository) Consume(jti string) error {
	if jti == "" {
		return errors.New("connect-link jti: jti is required")
	}

	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("consume connect-link nonce: begin tx: %w", err)
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
		jti,
	).Scan(&expiresAt, &consumedAt)
	if err == sql.ErrNoRows {
		return ErrNonceMissing
	}
	if err != nil {
		return fmt.Errorf("consume connect-link nonce: select: %w", err)
	}
	if consumedAt != nil {
		return ErrNonceConsumed
	}
	if time.Now().After(expiresAt) {
		return ErrNonceExpired
	}

	res, err := tx.Exec(
		`UPDATE connect_link_nonces
		 SET consumed_at = NOW()
		 WHERE nonce = $1 AND consumed_at IS NULL`,
		jti,
	)
	if err != nil {
		return fmt.Errorf("consume connect-link nonce: update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("consume connect-link nonce: rows affected: %w", err)
	}
	if n == 0 {
		// Another concurrent consumer won the race; treat as consumed.
		return ErrNonceConsumed
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("consume connect-link nonce: commit: %w", err)
	}
	committed = true
	return nil
}
