// SPRINT 1.2 — connection_states repository.
//
// Persists per-platform OAuth connection state for the
// POST /api/v1/connections/{platform}/start + GET /api/v1/connections/
// {platform}/callback split. Workspace_id is stamped at start-time and
// checked at callback-time: if the active JWT's workspace_id differs from
// the stamped workspace_id, the callback rejects with 403 (BOLA guard).
//
// Each connection_state carries a 32-byte nonce; the HttpOnly cookie
// `connection_state_<id>` carries `<id>.<nonce>` and the OAuth `state`
// query param carries base64(id:nonce); callback compares both before
// claiming the row.

package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ConnectionState mirrors a connection_states row.
type ConnectionState struct {
	ID          uuid.UUID
	UserID      int64
	WorkspaceID int64
	Platform    string
	Nonce       string
	Scopes      []string
	RedirectURI string
	ExpiresAt   time.Time
	ConsumedAt  *time.Time
	CreatedAt   time.Time
}

// ErrConnectionStateNotFound is the sentinel for missing/expired/consumed state.
var ErrConnectionStateNotFound = errors.New("connection state not found, expired, or already consumed")

// ConnectionStateRepository handles persistence for connection_states.
type ConnectionStateRepository struct {
	db *sql.DB
}

// NewConnectionStateRepository constructs the repo.
func NewConnectionStateRepository(db *sql.DB) *ConnectionStateRepository {
	return &ConnectionStateRepository{db: db}
}

// Create inserts a fresh connection_state row and returns its id.
func (r *ConnectionStateRepository) Create(state *ConnectionState) error {
	if state.ID == uuid.Nil {
		state.ID = uuid.New()
	}
	if state.Nonce == "" {
		return fmt.Errorf("create connection state: nonce is required")
	}
	_, err := r.db.Exec(
		`INSERT INTO connection_states
		   (id, user_id, workspace_id, platform, nonce, scopes, redirect_uri, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, NOW() + INTERVAL '15 minutes')`,
		state.ID, state.UserID, state.WorkspaceID, state.Platform, state.Nonce,
		state.Scopes, state.RedirectURI,
	)
	if err != nil {
		return fmt.Errorf("create connection state: %w", err)
	}
	return nil
}

// Consume atomically claims the state row by id, verifies the nonce
// matches the value supplied by the caller (defense against cookie-
// tampering) and against an active JWT's workspace_id, then marks
// consumed_at = NOW(). Returns ErrConnectionStateNotFound otherwise.
//
// The atomic UPDATE is the single-use guarantee.
func (r *ConnectionStateRepository) Consume(id uuid.UUID, expectedNonce string, jwtWorkspaceID int64) (*ConnectionState, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("consume connection state: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	row := &ConnectionState{ID: id}
	var scopes sql.NullString
	var redirectURI sql.NullString
	var consumedAt sql.NullTime
	err = tx.QueryRow(
		`SELECT user_id, workspace_id, platform, nonce, scopes, redirect_uri, expires_at, consumed_at, created_at
		 FROM connection_states
		 WHERE id = $1
		 FOR UPDATE`,
		id,
	).Scan(&row.UserID, &row.WorkspaceID, &row.Platform, &row.Nonce,
		&scopes, &redirectURI, &row.ExpiresAt, &consumedAt, &row.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrConnectionStateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("consume connection state: select: %w", err)
	}
	if scopes.Valid {
		row.Scopes = splitCSV(scopes.String)
	}
	if redirectURI.Valid {
		row.RedirectURI = redirectURI.String
	}
	if consumedAt.Valid {
		t := consumedAt.Time
		row.ConsumedAt = &t
		return nil, ErrConnectionStateNotFound
	}
	if time.Now().After(row.ExpiresAt) {
		return nil, ErrConnectionStateNotFound
	}
	if row.Nonce != expectedNonce {
		return nil, ErrConnectionStateNotFound
	}
	// BOLA guard: the JWT's active workspace_id must match the
	// workspace stamped by /start. A leaked cookie can't be replayed
	// against a different active workspace.
	if row.WorkspaceID != jwtWorkspaceID {
		return nil, ErrConnectionStateNotFound
	}

	res, err := tx.Exec(
		`UPDATE connection_states SET consumed_at = NOW() WHERE id = $1 AND consumed_at IS NULL`,
		id,
	)
	if err != nil {
		return nil, fmt.Errorf("consume connection state: update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("consume connection state: rows affected: %w", err)
	}
	if n == 0 {
		return nil, ErrConnectionStateNotFound
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("consume connection state: commit: %w", err)
	}
	now := time.Now()
	row.ConsumedAt = &now
	return row, nil
}

// splitCSV is a minimal helper to keep tests focused; production code
// can swap to pq.Array if/when scope lists become common.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
