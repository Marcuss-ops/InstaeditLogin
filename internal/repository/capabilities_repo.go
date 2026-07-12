package repository

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// AccountCapabilitiesRepository owns the account_capabilities cache
// table (migration 024, Taglio 5.0 LEVEL 2). The cache holds real
// per-account caps discovered by the per-platform
// CapabilityDiscoverer service, with the effective
// (= Intersect(theoretical, actual)) precomputed at write time so
// the worker reads a single column on every tick.
//
// Methods:
//   - FindByAccountID: worker hot-path read; returns one row.
//   - Upsert: Discoverer's write path; ON CONFLICT DO UPDATE.
//   - Invalidate: on Reauth / Disconnect / TTL reaper (L3).
//
// Taglio 5.0 LEVEL 2: only the worker + Meta Discoverers consume this
// in this commit. Follow-ups add HTTP handler reads + per-platform
// Discoverer integrations for LinkedIn, TikTok, Twitter, YouTube.
type AccountCapabilitiesRepository struct {
	db *sql.DB
}

// NewAccountCapabilitiesRepository wires the repo to the shared *sql.DB.
func NewAccountCapabilitiesRepository(db *sql.DB) *AccountCapabilitiesRepository {
	return &AccountCapabilitiesRepository{db: db}
}

// ErrAccountCapabilitiesNotFound is returned by FindByAccountID when no
// cached row exists for the given account ID. Workers treat this as a
// signal to call the per-platform Discoverer once and write a fresh row.
var ErrAccountCapabilitiesNotFound = errors.New("account capabilities not found")

// FindByAccountID returns the cached row for an account. Returns
// ErrAccountCapabilitiesNotFound when no row exists, so callers can
// distinguish "cache miss" from "DB error" cleanly.
//
// Actual *CapabilitySet and LastError are nullable in the DB; the
// scanner fills them as nil/"" when the column is NULL.
func (r *AccountCapabilitiesRepository) FindByAccountID(platformAccountID int64) (*models.AccountCapabilities, error) {
	row := r.db.QueryRow(`
		SELECT platform_account_id,
		       theoretical,
		       actual,
		       effective,
		       source_discoverer,
		       last_fetched_at,
		       expires_at,
		       last_error,
		       revision
		  FROM account_capabilities
		 WHERE platform_account_id = $1
	`, platformAccountID)

	var c models.AccountCapabilities
	var actualJSON sql.NullString
	var lastErrStr sql.NullString
	err := row.Scan(
		&c.PlatformAccountID,
		&c.Theoretical,
		&actualJSON,
		&c.Effective,
		&c.SourceDiscoverer,
		&c.LastFetchedAt,
		&c.ExpiresAt,
		&lastErrStr,
		&c.Revision,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAccountCapabilitiesNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("account_capabilities find by id=%d: %w", platformAccountID, err)
	}

	if actualJSON.Valid && actualJSON.String != "" {
		var act models.CapabilitySet
		if err := act.Scan(actualJSON.String); err != nil {
			return nil, fmt.Errorf("account_capabilities scan actual for id=%d: %w", platformAccountID, err)
		}
		c.Actual = &act
	}
	if lastErrStr.Valid {
		c.LastError = lastErrStr.String
	}
	return &c, nil
}

// Upsert writes the cached row. On conflict (same platform_account_id)
// every column is overwritten AND revision is incremented by 1 --
// callers can use revision to detect concurrent writes (currently
// v1 trusts Postgres serialisation; L3 may add CAS check).
//
// actualJSONNullable and lastError are both nullable; pass nil to
// insert NULL.
func (r *AccountCapabilitiesRepository) Upsert(c *models.AccountCapabilities) error {
	if c == nil {
		return errors.New("account_capabilities Upsert: nil row")
	}

	var actualParam any
	if c.Actual != nil {
		v, err := c.Actual.Value()
		if err != nil {
			return fmt.Errorf("account_capabilities marshal actual: %w", err)
		}
		actualParam = v
	} else {
		actualParam = nil
	}
	lastErrParam := nullableString(c.LastError)

	_, err := r.db.Exec(`
		INSERT INTO account_capabilities
		    (platform_account_id, theoretical, actual, effective,
		     source_discoverer, last_fetched_at, expires_at, last_error, revision)
		VALUES ($1, $2::jsonb, $3::jsonb, $4::jsonb, $5, $6, $7, $8, $9)
		ON CONFLICT (platform_account_id) DO UPDATE SET
		    theoretical       = EXCLUDED.theoretical,
		    actual            = EXCLUDED.actual,
		    effective         = EXCLUDED.effective,
		    source_discoverer  = EXCLUDED.source_discoverer,
		    last_fetched_at   = EXCLUDED.last_fetched_at,
		    expires_at        = EXCLUDED.expires_at,
		    last_error        = EXCLUDED.last_error,
		    revision          = account_capabilities.revision + 1
	`, c.PlatformAccountID, c.Theoretical, actualParam, c.Effective,
		c.SourceDiscoverer, c.LastFetchedAt, c.ExpiresAt,
		lastErrParam, c.Revision)
	if err != nil {
		return fmt.Errorf("account_capabilities upsert id=%d: %w", c.PlatformAccountID, err)
	}
	return nil
}

// Invalidate deletes the cached row for an account. Called by:
//   - Account lifecycle handlers on Disconnect/Reauth-required.
//   - HTTP GET /accounts/{id}/capabilities?refresh=true forces a
//     re-DISCOVERY rather than an invalidate; invalidate is for
//     reaper / forced-fresh semantics.
//   - Future L3 TTL reaper (cron sweep on expires_at < now()).
func (r *AccountCapabilitiesRepository) Invalidate(platformAccountID int64) error {
	_, err := r.db.Exec(`DELETE FROM account_capabilities WHERE platform_account_id = $1`, platformAccountID)
	if err != nil {
		return fmt.Errorf("account_capabilities invalidate id=%d: %w", platformAccountID, err)
	}
	return nil
}

// nullableString converts "" -> nil driver value so we can store NULL
// instead of the empty string in last_error.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
