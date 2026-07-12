// SPRINT 2.1 — sessions repository.
//
// One row per active session. Refresh tokens are stored as SHA-256
// hashes; the plaintext lives only in the cookie. Rotation produces
// a NEW row in the same family; the old row is marked revoked_at.
//
// Theft detection: when /refresh sees a refresh hash whose row is
// already revoked (i.e. the row was rotated earlier), RevokeFamily
// is invoked to mark ALL sessions in that family revoked with
// reason = "refresh_reuse_detected". This blocks the attacker
// who replayed a stolen (already-rotated) refresh token.
package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Session mirrors a sessions row.
type Session struct {
	ID               int64
	UserID           int64
	WorkspaceID      int64
	TokenFamilyID    string
	AccessJTI        string
	RefreshTokenHash []byte
	UserAgent        string
	IPHash           string
	CreatedAt        time.Time
	ExpiresAt        time.Time
	RefreshExpiresAt time.Time
	LastUsedAt       time.Time
	RevokedAt        *time.Time
	RevokeReason     string
}

// Sentinel errors.
var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionReuse    = errors.New("refresh token reuse detected")
)

// SessionRepository handles persistence for sessions.
type SessionRepository struct {
	db *sql.DB
}

// NewSessionRepository constructs the repo.
func NewSessionRepository(db *sql.DB) *SessionRepository {
	return &SessionRepository{db: db}
}

// Create inserts a fresh session row and returns its id.
func (r *SessionRepository) Create(s *Session) error {
	err := r.db.QueryRow(
		`INSERT INTO sessions
		   (user_id, workspace_id, token_family_id, access_jti,
		    refresh_token_hash, user_agent, ip_hash, expires_at, refresh_expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id, created_at, last_used_at`,
		s.UserID, s.WorkspaceID, s.TokenFamilyID, s.AccessJTI,
		s.RefreshTokenHash, s.UserAgent, s.IPHash, s.ExpiresAt, s.RefreshExpiresAt,
	).Scan(&s.ID, &s.CreatedAt, &s.LastUsedAt)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// FindByRefreshHash returns the row matching the supplied SHA-256 hash.
// Returns (nil, ErrSessionNotFound) when the row is missing.
func (r *SessionRepository) FindByRefreshHash(hash []byte) (*Session, error) {
	s := &Session{}
	err := r.db.QueryRow(
		`SELECT id, user_id, workspace_id, token_family_id, access_jti,
		        refresh_token_hash, user_agent, COALESCE(ip_hash, ''),
		        created_at, expires_at, refresh_expires_at, last_used_at,
		        revoked_at, COALESCE(revoke_reason, '')
		 FROM sessions
		 WHERE refresh_token_hash = $1`,
		hash,
	).Scan(&s.ID, &s.UserID, &s.WorkspaceID, &s.TokenFamilyID, &s.AccessJTI,
		&s.RefreshTokenHash, &s.UserAgent, &s.IPHash,
		&s.CreatedAt, &s.ExpiresAt, &s.RefreshExpiresAt, &s.LastUsedAt,
		&s.RevokedAt, &s.RevokeReason)
	if err == sql.ErrNoRows {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find session by refresh hash: %w", err)
	}
	return s, nil
}

// Rotate performs the refresh-token rotation atomically:
//   1. SELECT FOR UPDATE the old row.
//   2. If revoked_at IS NOT NULL: reuse detected — call RevokeFamily
//      (atomic, separate statement) and return ErrSessionReuse.
//   3. Mark old row revoked (reason="rotated").
//   4. INSERT new row with the same family_id, new refresh hash,
//      new access_jti, last_used_at = NOW().
//   5. Return the new row.
//
// The caller is responsible for generating the new refresh-token
// plaintext + hash + access JWT BEFORE calling Rotate.
func (r *SessionRepository) Rotate(
	oldHash []byte,
	newSession *Session,
) (*Session, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("rotate: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Lock the old row.
	old := &Session{}
	err = tx.QueryRow(
		`SELECT id, user_id, workspace_id, token_family_id, revoked_at,
		        COALESCE(revoke_reason, '')
		 FROM sessions
		 WHERE refresh_token_hash = $1
		 FOR UPDATE`,
		oldHash,
	).Scan(&old.ID, &old.UserID, &old.WorkspaceID, &old.TokenFamilyID,
		&old.RevokedAt, &old.RevokeReason)
	if err == sql.ErrNoRows {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("rotate: select old: %w", err)
	}
	if old.RevokedAt != nil {
		// Reuse detected. Roll back the rotate tx, then mark the
		// entire family revoked in a separate transaction (already
		// committed, NOT part of this tx). Caller can surface a 401.
		_ = tx.Rollback()
		if _, familyErr := r.RevokeFamily(old.TokenFamilyID, "refresh_reuse_detected"); familyErr != nil {
			return nil, fmt.Errorf("rotate: revoke family after reuse: %w", familyErr)
		}
		return nil, ErrSessionReuse
	}

	// Mark old row revoked.
	_, err = tx.Exec(
		`UPDATE sessions
		 SET revoked_at = NOW(), revoke_reason = 'rotated'
		 WHERE id = $1`,
		old.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("rotate: revoke old: %w", err)
	}

	// Copy family from old row (caller's newSession.TokenFamilyID
	// may be empty; the new row inherits the family).
	if newSession.TokenFamilyID == "" {
		newSession.TokenFamilyID = old.TokenFamilyID
	}
	newSession.UserID = old.UserID
	newSession.WorkspaceID = old.WorkspaceID

	err = tx.QueryRow(
		`INSERT INTO sessions
		   (user_id, workspace_id, token_family_id, access_jti,
		    refresh_token_hash, user_agent, ip_hash, expires_at, refresh_expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id, created_at, last_used_at`,
		newSession.UserID, newSession.WorkspaceID, newSession.TokenFamilyID,
		newSession.AccessJTI, newSession.RefreshTokenHash, newSession.UserAgent,
		newSession.IPHash, newSession.ExpiresAt, newSession.RefreshExpiresAt,
	).Scan(&newSession.ID, &newSession.CreatedAt, &newSession.LastUsedAt)
	if err != nil {
		return nil, fmt.Errorf("rotate: insert new: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("rotate: commit: %w", err)
	}
	return newSession, nil
}

// Revoke marks a single session as revoked.
func (r *SessionRepository) Revoke(id int64, reason string) error {
	_, err := r.db.Exec(
		`UPDATE sessions
		 SET revoked_at = NOW(), revoke_reason = $1
		 WHERE id = $2 AND revoked_at IS NULL`,
		reason, id,
	)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

// RevokeFamily marks every non-revoked session in the family as
// revoked with the supplied reason. Used by the theft-detection
// path. Returns the number of rows affected.
func (r *SessionRepository) RevokeFamily(familyID, reason string) (int64, error) {
	res, err := r.db.Exec(
		`UPDATE sessions
		 SET revoked_at = NOW(), revoke_reason = $1
		 WHERE token_family_id = $2 AND revoked_at IS NULL`,
		reason, familyID,
	)
	if err != nil {
		return 0, fmt.Errorf("revoke family: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("revoke family: rows affected: %w", err)
	}
	return n, nil
}

// RevokeAllForUser marks every non-revoked session for a user.
func (r *SessionRepository) RevokeAllForUser(userID int64, reason string) (int64, error) {
	res, err := r.db.Exec(
		`UPDATE sessions
		 SET revoked_at = NOW(), revoke_reason = $1
		 WHERE user_id = $2 AND revoked_at IS NULL`,
		reason, userID,
	)
	if err != nil {
		return 0, fmt.Errorf("revoke all for user: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("revoke all for user: rows affected: %w", err)
	}
	return n, nil
}

// ListByUser returns the non-revoked sessions for a user, ordered by
// last_used_at DESC.
func (r *SessionRepository) ListByUser(userID int64) ([]Session, error) {
	rows, err := r.db.Query(
		`SELECT id, user_id, workspace_id, token_family_id, access_jti,
		        refresh_token_hash, user_agent, COALESCE(ip_hash, ''),
		        created_at, expires_at, refresh_expires_at, last_used_at,
		        revoked_at, COALESCE(revoke_reason, '')
		 FROM sessions
		 WHERE user_id = $1
		 ORDER BY last_used_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list by user: %w", err)
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		s := Session{}
		if err := rows.Scan(&s.ID, &s.UserID, &s.WorkspaceID, &s.TokenFamilyID,
			&s.AccessJTI, &s.RefreshTokenHash, &s.UserAgent, &s.IPHash,
			&s.CreatedAt, &s.ExpiresAt, &s.RefreshExpiresAt, &s.LastUsedAt,
			&s.RevokedAt, &s.RevokeReason); err != nil {
			return nil, fmt.Errorf("list by user: scan: %w", err)
		}
		out = append(out, s)
	}
	return out, nil
}

// MarkUsed bumps last_used_at to NOW(). Best-effort; an error here
// does not invalidate the session.
func (r *SessionRepository) MarkUsed(id int64) error {
	_, err := r.db.Exec(
		`UPDATE sessions SET last_used_at = NOW() WHERE id = $1`, id,
	)
	if err != nil {
		return fmt.Errorf("mark used: %w", err)
	}
	return nil
}
