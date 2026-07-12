// API key repository — CRUD + tenant-scoped queries for api_keys.
//
// Style intentionally mirrors OrganizationRepository / UserRepository /
// WorkspaceRepository: no context.Context parameter, not-found returns
// (nil, nil), errors wrapped with fmt.Errorf("%w", err), and a
// dedicated ErrApiKeyNotFound sentinel for callers that need explicit
// zero-row semantics (e.g. revoke of a stale id).
//
// Tenant scoping policy (Taglio 4.5+): every method that takes an
// organization_id parameter enforces it as a hard SQL filter. The
// caller cannot reach across tenants even by mistake. FindByHash is
// the lone exception — the middleware uses it as a single-lookup
// path and then enforces "row.OrganizationID == requestOrgID" at
// the HTTP layer (pkg/api/apikeys.go, commit 3) using the tenant
// already on the request. The ability to call FindByHash without
// an explicit org filter is intentional: an API key is itself a
// tenant-scoped credential, and the row that comes back carries
// the tenant.
//
// NO plaintext key ever touches the repository. The Create method
// takes a precomputed SHA-256 hash (the auth package's Hash helper);
// the caller is the auth/apikey.go helpers + the API layer that
// bridges them. Storing the plaintext here would defeat the entire
// threat model.

package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ApiKeyRepository handles persistence for tenant API keys. Bind to
// a single *sql.DB at construction time; the DB is the tenant root
// (the FK on organization_id enforces the cascade).
type ApiKeyRepository struct {
	db *sql.DB
}

// NewApiKeyRepository constructs a new repository bound to db.
func NewApiKeyRepository(db *sql.DB) *ApiKeyRepository {
	return &ApiKeyRepository{db: db}
}

// ErrApiKeyNotFound is the sentinel for zero-row Update/Delete/Revoke
// operations. Use errors.Is for explicit "api key does not exist"
// paths (e.g. revoke of a stale id, soft-delete checks).
var ErrApiKeyNotFound = errors.New("api key not found")

// ErrApiKeyHashCollided is returned by Create when the SHA-256 hash
// collides with an existing row. Astronomically rare (256-bit hash
// space over 256-bit input), but the UNIQUE constraint makes the
// invariant explicit and forces a deterministic error path instead
// of silent corruption. The caller retries Generate + Create; the
// new random secret very likely won't collide again.
var ErrApiKeyHashCollided = errors.New("api key hash collision")

// Create persists a new api_keys row from the supplied ApiKey plus
// the SHA-256 hash of the plaintext. The plaintext is NOT passed
// in — only its hash is, since the repository is the boundary
// between "request layer (sees plaintext once)" and "storage layer
// (only ever sees hash)".
//
// Field expectations:
//   - Key.ID is assigned on return via RETURNING id.
//   - Key.OrganizationID and Key.CreatedBy must be > 0 (DB FKs).
//   - Key.ProjectID may be nil for an org-wide key.
//   - Key.Name must be non-empty.
//   - Key.Environment must be "test" or "live" (caller-side check).
//   - Key.KeyPrefix is the visible "sk_test_aB3xY9K2..." stored
//     for dashboard display.
//   - Key.Permissions must be a []string (use []string{} if no
//     permissions — defaults are applied at the API layer, NOT
//     here).
//   - hash must be exactly 32 bytes (SHA-256 output).
//
// Idempotency: NOT idempotent. Calling Create twice with the same
// hash returns ErrApiKeyHashCollided; calling with a different hash
// is the expected rollback-and-retry path.
func (r *ApiKeyRepository) Create(key *models.ApiKey, hash []byte) error {
	if len(hash) != 32 {
		return fmt.Errorf("api key hash must be 32 bytes (sha256); got %d", len(hash))
	}
	if key.OrganizationID <= 0 {
		return errors.New("api key organization_id is required")
	}
	if key.CreatedBy <= 0 {
		return errors.New("api key created_by is required")
	}
	// Default the permissions slice to []string{} if the caller
	// left it nil. SQL TEXT[] default '{}' would also work, but
	// being explicit at the boundary saves a roundtrip through
	// the pq driver for every NULL-value write.
	perms := key.Permissions
	if perms == nil {
		perms = []string{}
	}
	err := r.db.QueryRow(
		`INSERT INTO api_keys
		    (organization_id, project_id, created_by, name, environment,
		     key_prefix, key_hash, permissions, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id, created_at, updated_at`,
		key.OrganizationID, nullableProjectID(key.ProjectID), key.CreatedBy,
		key.Name, key.Environment, key.KeyPrefix, hash, perms, key.ExpiresAt,
	).Scan(&key.ID, &key.CreatedAt, &key.UpdatedAt)
	if err != nil {
		// Postgres UNIQUE violation on key_hash → ErrApiKeyHashCollided.
		// Typed check against lib/pq's *pq.Error. SQLSTATE 23505
		// is the specific "unique_violation" subclass under the
		// broader 23000 "integrity_constraint_violation" class —
		// matching on 23505 (not 23000) keeps the dispatch narrow:
		// foreign_key / not_null / check violations all live under
		// 23000 and would falsely collide on a parent-class match.
		// The Constraint field carries the named index (set to
		// "api_keys_key_hash_key" by the schema in migration
		// 017_api_keys.sql), so a future UNIQUE on a different
		// column wouldn't be misclassified.
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" &&
			pqErr.Constraint == "api_keys_key_hash_key" {
			return fmt.Errorf("%w", ErrApiKeyHashCollided)
		}
		return fmt.Errorf("failed to create api key: %w", err)
	}
	return nil
}

// FindByIDForOrg returns the api_keys row with the given id, scoped
// to orgID. (nil, nil) when no row matches. The orgID filter is
// enforced server-side so a buggy controller cannot leak a key that
// belongs to a different org — the query explicitly joins both
// predicates. Includes the full row (revoked_at / expires_at /
// last_used_at) so the dashboard GET handler can render a truthful
// status. Renamed from FindByID to make the tenant contract obvious
// at every call site.
func (r *ApiKeyRepository) FindByIDForOrg(orgID, id int64) (*models.ApiKey, error) {
	row := r.db.QueryRow(
		`SELECT id, organization_id, project_id, created_by, name,
		        environment, key_prefix, permissions, expires_at,
		        revoked_at, last_used_at, created_at, updated_at
		 FROM api_keys
		 WHERE id = $1 AND organization_id = $2`,
		id, orgID,
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
// when no row matches the hash. Does NOT check revoked_at /
// expires_at — the middleware applies IsActive at the Go layer so
// the SQL stays focused on the hash equality (the unique-index
// lookup is the only thing that matters for performance).
//
// Tenant scoping: this method does NOT take an organization_id
// argument by design. The API key IS the tenant boundary — the row
// that comes back carries its own organization_id, and the caller
// (the Bearer middleware) is responsible for comparing that
// organization_id against the request's authenticated tenant before
// trusting the lookup. See pkg/api/apikeys.go (commit 3) for the
// enforcement point.
func (r *ApiKeyRepository) FindByHash(hash []byte) (*models.ApiKey, error) {
	if len(hash) != 32 {
		return nil, fmt.Errorf("api key hash must be 32 bytes; got %d", len(hash))
	}
	row := r.db.QueryRow(
		`SELECT id, organization_id, project_id, created_by, name,
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

// ListByOrg returns every API key whose organization_id matches.
// Active + revoked rows both flow through (revoked_at NOT NULL
// surfaces as a status flag the dashboard can show). Order:
// created_at DESC so the most recently minted key is at the top
// of the operator's list.
//
// Tenant scoping: mandatory. The controller layer never calls it
// without an authenticated org_id.
func (r *ApiKeyRepository) ListByOrg(orgID int64) ([]models.ApiKey, error) {
	rows, err := r.db.Query(
		`SELECT id, organization_id, project_id, created_by, name,
		        environment, key_prefix, permissions, expires_at,
		        revoked_at, last_used_at, created_at, updated_at
		 FROM api_keys
		 WHERE organization_id = $1
		 ORDER BY created_at DESC`,
		orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list api keys by org: %w", err)
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

// ListByProject returns API keys scoped to a single project. Same
// tenant-scoping contract as ListByOrg (the project_id is naturally
// unique inside an org, but the WHERE clause composes both filters
// for defence in depth).
func (r *ApiKeyRepository) ListByProject(orgID, projectID int64) ([]models.ApiKey, error) {
	rows, err := r.db.Query(
		`SELECT id, organization_id, project_id, created_by, name,
		        environment, key_prefix, permissions, expires_at,
		        revoked_at, last_used_at, created_at, updated_at
		 FROM api_keys
		 WHERE organization_id = $1 AND project_id = $2
		 ORDER BY created_at DESC`,
		orgID, projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list api keys by project: %w", err)
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

// CountByOrg returns the number of API keys (active + revoked) for
// an org. Forward compat: the dashboard plans to display "X of Y
// keys used" against a plan-tier cap; this query is the basis.
// Cheap: streamed COUNT(*) over the FK index.
func (r *ApiKeyRepository) CountByOrg(orgID int64) (int, error) {
	var n int
	err := r.db.QueryRow(
		`SELECT COUNT(*) FROM api_keys WHERE organization_id = $1`,
		orgID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("failed to count api keys by org: %w", err)
	}
	return n, nil
}

// Revoke marks a key as revoked (revoked_at = NOW()). Idempotent on
// the happy path: re-revoking an already-revoked key does NOT bump
// revoked_at again (COALESCE with the existing value), so the
// audit-log timestamp reflects the actual first-revoke event. The
// tenant filter is mandatory: a caller from org A cannot revoke a
// key whose row belongs to org B. Returns ErrApiKeyNotFound when
// the id+org filter matches zero rows — either the id is wrong OR
// the caller crossed tenants (which is indistinguishable by design).
func (r *ApiKeyRepository) Revoke(orgID, id int64) error {
	result, err := r.db.Exec(
		`UPDATE api_keys
		 SET revoked_at = COALESCE(revoked_at, $1),
		     updated_at = $1
		 WHERE id = $2 AND organization_id = $3`,
		time.Now(), id, orgID,
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

// MarkUsed bumps last_used_at to NOW(). Called from the Bearer
// middleware after a successful hash lookup, on the hot path. To
// keep the hot path cheap, this method does NOT update updated_at
// (last_used_at is a distinct timestamp; updated_at is for
// metadata changes).
//
// Tenant filter: defence in depth — a misuse where the hot path is
// wired wrong would still reject. Errors are advisory on the hot
// path (the auth decision is already made; last_used_at is operator
// UX) — callers can choose to log but not abort.
func (r *ApiKeyRepository) MarkUsed(orgID, id int64) error {
	now := time.Now()
	_, err := r.db.Exec(
		`UPDATE api_keys
		 SET last_used_at = $1
		 WHERE id = $2 AND organization_id = $3`,
		now, id, orgID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark api key used: %w", err)
	}
	return nil
}

// UpdateName renames an active API key. Tenant filter is mandatory.
// Returns ErrApiKeyNotFound when the id+org filter matches zero
// rows (either the id is wrong or a cross-tenant attempt).
func (r *ApiKeyRepository) UpdateName(orgID, id int64, name string) error {
	if name == "" {
		return errors.New("api key name cannot be empty")
	}
	result, err := r.db.Exec(
		`UPDATE api_keys
		 SET name = $1, updated_at = $2
		 WHERE id = $3 AND organization_id = $4`,
		name, time.Now(), id, orgID,
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

// --- internal helpers --------------------------------------------------------

// nullableProjectID converts a *int64 into a value suitable for a
// nullable BIGINT INSERT parameter. nil → SQL NULL (pq driver maps
// untyped nil into NULL); non-nil → the int64 itself. Centralised
// here so the Create path stays one-line clean.
func nullableProjectID(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// scanApiKeyRow reads one row from a SELECT returning the canonical
// api_keys column list (same shape as FindByID / FindByHash). Used
// by ListByOrg and ListByProject to keep the SELECT/Scan contract
// in lockstep — a column-list drift would surface as a Scan error
// here for every list query, immediately visible to the caller.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanApiKeyRow(r rowScanner) (*models.ApiKey, error) {
	key := &models.ApiKey{}
	var projectID sql.NullInt64
	if err := r.Scan(&key.ID, &key.OrganizationID, &projectID, &key.CreatedBy,
		&key.Name, &key.Environment, &key.KeyPrefix, &key.Permissions,
		&key.ExpiresAt, &key.RevokedAt, &key.LastUsedAt,
		&key.CreatedAt, &key.UpdatedAt); err != nil {
		return nil, fmt.Errorf("failed to scan api key: %w", err)
	}
	if projectID.Valid {
		pid := projectID.Int64
		key.ProjectID = &pid
	}
	return key, nil
}
