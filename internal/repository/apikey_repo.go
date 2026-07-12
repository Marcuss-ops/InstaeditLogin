// API key repository — CRUD + workspace-scoped queries for api_keys.
//
// Style intentionally mirrors WorkspaceRepository: no context.Context parameter,
// not-found returns (nil, nil), errors wrapped with fmt.Errorf("%w", err), and a
// dedicated ErrApiKeyNotFound sentinel for callers that need explicit
// zero-row semantics (e.g. revoke of a stale id).
//
// Tenant scoping policy: every method that takes a workspace_id parameter
// enforces it as a hard SQL filter. FindByHash is the lone exception — the
// middleware uses it as a single-lookup path and then enforces
// "row.WorkspaceID == requestWorkspaceID" at the HTTP layer.
//
// NO plaintext key ever touches the repository. The Create method
// takes a precomputed SHA-256 hash; the caller bridges it via auth.Hash.
//
// Taglio 5c: tenant anchor is WorkspaceID (was OrganizationID). ProjectID removed.

package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ApiKeyRepository handles persistence for workspace-scoped API keys.
type ApiKeyRepository struct {
	db *sql.DB
}

// NewApiKeyRepository constructs a new repository bound to db.
func NewApiKeyRepository(db *sql.DB) *ApiKeyRepository {
	return &ApiKeyRepository{db: db}
}

// ErrApiKeyNotFound is the sentinel for zero-row Update/Delete/Revoke operations.
var ErrApiKeyNotFound = errors.New("api key not found")

// ErrApiKeyHashCollided is returned by Create when the SHA-256 hash
// collides with an existing row (astronomically rare).
var ErrApiKeyHashCollided = errors.New("api key hash collision")

// Create persists a new api_keys row from the supplied ApiKey plus
// the SHA-256 hash of the plaintext.
//
// Field expectations:
//   - Key.WorkspaceID and Key.CreatedBy must be > 0 (DB FKs).
//   - Key.Name must be non-empty.
//   - Key.Environment must be "test" or "live".
//   - Key.KeyPrefix is the visible prefix for dashboard display.
//   - hash must be exactly 32 bytes (SHA-256 output).
func (r *ApiKeyRepository) Create(key *models.ApiKey, hash []byte) error {
	if len(hash) != 32 {
		return fmt.Errorf("api key hash must be 32 bytes (sha256); got %d", len(hash))
	}
	if key.WorkspaceID <= 0 {
		return errors.New("api key workspace_id is required")
	}
	if key.CreatedBy <= 0 {
		return errors.New("api key created_by is required")
	}
	perms := key.Permissions
	if perms == nil {
		perms = []string{}
	}
	err := r.db.QueryRow(
		`INSERT INTO api_keys
		    (workspace_id, created_by, name, environment,
		     key_prefix, key_hash, permissions, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id, created_at, updated_at`,
		key.WorkspaceID, key.CreatedBy,
		key.Name, key.Environment, key.KeyPrefix, hash, perms, key.ExpiresAt,
	).Scan(&key.ID, &key.CreatedAt, &key.UpdatedAt)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" &&
			pqErr.Constraint == "api_keys_key_hash_key" {
			return fmt.Errorf("%w", ErrApiKeyHashCollided)
		}
		return fmt.Errorf("failed to create api key: %w", err)
	}
	return nil
}

// FindByIDForWorkspace returns the api_keys row with the given id, scoped
// to wsID. (nil, nil) when no row matches.
func (r *ApiKeyRepository) FindByIDForWorkspace(wsID, id int64) (*models.ApiKey, error) {
	row := r.db.QueryRow(
		`SELECT id, workspace_id, created_by, name,
		        environment, key_prefix, permissions, expires_at,
		        revoked_at, last_used_at, created_at, updated_at
		 FROM api_keys
		 WHERE id = $1 AND workspace_id = $2`,
		id, wsID,
	)
	key, err := scanApiKeyRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find api key by id: %w", err)
	}
	return key, nil
}

// FindByHash is the middleware hot-path lookup. Returns (nil, nil)
// when no row matches the hash. Does NOT check revoked_at/expires_at —
// the middleware applies IsActive at the Go layer.
func (r *ApiKeyRepository) FindByHash(hash []byte) (*models.ApiKey, error) {
	if len(hash) != 32 {
		return nil, fmt.Errorf("api key hash must be 32 bytes; got %d", len(hash))
	}
	row := r.db.QueryRow(
		`SELECT id, workspace_id, created_by, name,
		        environment, key_prefix, permissions, expires_at,
		        revoked_at, last_used_at, created_at, updated_at
		 FROM api_keys
		 WHERE key_hash = $1`,
		hash,
	)
	key, err := scanApiKeyRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find api key by hash: %w", err)
	}
	return key, nil
}

// ListByWorkspace returns every API key whose workspace_id matches.
// Order: created_at DESC.
func (r *ApiKeyRepository) ListByWorkspace(wsID int64) ([]models.ApiKey, error) {
	rows, err := r.db.Query(
		`SELECT id, workspace_id, created_by, name,
		        environment, key_prefix, permissions, expires_at,
		        revoked_at, last_used_at, created_at, updated_at
		 FROM api_keys
		 WHERE workspace_id = $1
		 ORDER BY created_at DESC`,
		wsID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list api keys by workspace: %w", err)
	}
	defer rows.Close()
	var keys []models.ApiKey
	for rows.Next() {
		key, err := scanApiKeyRow(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, *key)
	}
	return keys, nil
}

// CountByWorkspace returns the number of API keys (active + revoked) for
// a workspace.
func (r *ApiKeyRepository) CountByWorkspace(wsID int64) (int, error) {
	var n int
	err := r.db.QueryRow(
		`SELECT COUNT(*) FROM api_keys WHERE workspace_id = $1`,
		wsID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("failed to count api keys by workspace: %w", err)
	}
	return n, nil
}

// Revoke marks a key as revoked (revoked_at = NOW()). Idempotent —
// re-revoking an already-revoked key does NOT bump revoked_at again.
func (r *ApiKeyRepository) Revoke(wsID, id int64) error {
	result, err := r.db.Exec(
		`UPDATE api_keys
		 SET revoked_at = COALESCE(revoked_at, $1),
		     updated_at = $1
		 WHERE id = $2 AND workspace_id = $3`,
		time.Now(), id, wsID,
	)
	if err != nil {
		return fmt.Errorf("failed to revoke api key: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrApiKeyNotFound, id)
	}
	return nil
}

// MarkUsed bumps last_used_at to NOW(). Called from the Bearer middleware
// after a successful hash lookup, on the hot path.
func (r *ApiKeyRepository) MarkUsed(wsID, id int64) error {
	now := time.Now()
	_, err := r.db.Exec(
		`UPDATE api_keys
		 SET last_used_at = $1
		 WHERE id = $2 AND workspace_id = $3`,
		now, id, wsID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark api key used: %w", err)
	}
	return nil
}

// UpdateName renames an active API key. Tenant filter is mandatory.
func (r *ApiKeyRepository) UpdateName(wsID, id int64, name string) error {
	if name == "" {
		return errors.New("api key name cannot be empty")
	}
	result, err := r.db.Exec(
		`UPDATE api_keys
		 SET name = $1, updated_at = $2
		 WHERE id = $3 AND workspace_id = $4`,
		name, time.Now(), id, wsID,
	)
	if err != nil {
		return fmt.Errorf("failed to rename api key: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrApiKeyNotFound, id)
	}
	return nil
}

// Rotate atomically revokes the old key and inserts a new key in
// a single transaction. The new key carries the same metadata as
// the old one (workspace_id, name, environment, permissions, expires_at)
// and is owned by the same created_by.
func (r *ApiKeyRepository) Rotate(wsID, oldID int64, newKey *models.ApiKey, newHash []byte) error {
	if len(newHash) != 32 {
		return fmt.Errorf("api key hash must be 32 bytes; got %d", len(newHash))
	}
	if newKey == nil {
		return errors.New("new api key cannot be nil")
	}
	if newKey.WorkspaceID <= 0 || newKey.WorkspaceID != wsID {
		return errors.New("new key workspace_id must match wsID param")
	}
	if newKey.CreatedBy <= 0 {
		return errors.New("new key created_by is required")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin rotate tx: %w", err)
	}
	defer tx.Rollback()

	// 1) Revoke the old key.
	result, err := tx.Exec(
		`UPDATE api_keys
		 SET revoked_at = COALESCE(revoked_at, NOW()),
		     updated_at = NOW()
		 WHERE id = $1 AND workspace_id = $2`,
		oldID, wsID,
	)
	if err != nil {
		return fmt.Errorf("failed to revoke old key in rotate tx: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected in rotate: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrApiKeyNotFound, oldID)
	}

	// 2) Insert the new key with the freshly supplied hash.
	perms := newKey.Permissions
	if perms == nil {
		perms = []string{}
	}
	if err := tx.QueryRow(
		`INSERT INTO api_keys
		    (workspace_id, created_by, name, environment,
		     key_prefix, key_hash, permissions, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id, created_at, updated_at`,
		newKey.WorkspaceID, newKey.CreatedBy, newKey.Name, newKey.Environment,
		newKey.KeyPrefix, newHash, perms, newKey.ExpiresAt,
	).Scan(&newKey.ID, &newKey.CreatedAt, &newKey.UpdatedAt); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" &&
			pqErr.Constraint == "api_keys_key_hash_key" {
			return fmt.Errorf("%w", ErrApiKeyHashCollided)
		}
		return fmt.Errorf("failed to insert new key in rotate tx: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit rotate tx: %w", err)
	}
	return nil
}

// --- internal helpers --------------------------------------------------------

type rowScanner interface {
	Scan(dest ...any) error
}

func scanApiKeyRow(r rowScanner) (*models.ApiKey, error) {
	key := &models.ApiKey{}
	if err := r.Scan(&key.ID, &key.WorkspaceID, &key.CreatedBy,
		&key.Name, &key.Environment, &key.KeyPrefix, &key.Permissions,
		&key.ExpiresAt, &key.RevokedAt, &key.LastUsedAt,
		&key.CreatedAt, &key.UpdatedAt); err != nil {
		return nil, fmt.Errorf("failed to scan api key: %w", err)
	}
	return key, nil
}
