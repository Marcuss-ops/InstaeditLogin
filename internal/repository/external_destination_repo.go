package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// uniqueViolationDestTripleDetailRegex matches the pq.Error Detail
// content for the UNIQUE(source_system, workspace_id,
// platform_account_id) constraint on external_destinations. Anchored
// on the column-list LHS (not the inner tuple) so the regex is
// agnostic to row values and matches any Postgres version detail
// that follows the documented
//
//	Key (source_system, workspace_id, platform_account_id)=(...) already exists.
//
// shape (Postgres 9.6+ user-visible SQLSTATE 23505 message format
// — confirmed stable in the Postgres docs as the "Detailed
// Errors" convention which doesn't change across pg_dump/restore
// or major version upgrades). The dispatch was previously keyed on
// pqErr.Constraint name (auto-generated
// "external_destinations_source_system_workspace_id_platform_account_id_key");
// switching to Detail eliminates the auto-name drift risk (a
// Postgres upgrade, schema rename, or pg_restore into a different
// search_path could rename the auto-NAME without changing the
// semantics).
var uniqueViolationDestTripleDetailRegex = regexp.MustCompile(
	`Key \(source_system, workspace_id, platform_account_id\)=`)

// fkReferencingExternalDestinationsDetailRegex matches the
// pq.Error Detail content for ANY foreign-key violation against
// the external_destinations row being acted on (the FK could be
// referenced from external_deliveries today, from a future
// external_audit_log table, or from any other present-or-future
// table holding REFERENCES external_destinations(id)). The
// canonical Postgres Detail shape for an FK violation on a
// DELETE FROM external_destinations WHERE id=X is:
//
//	Key (id)=(extdst_01JABC) is still referenced from table "<REF>".
//
// The capture group accepts BOTH the QUOTED and the UNQUOTED
// Detail shapes and ALWAYS yields a BARE REFERENCING table
// name (no surrounding quotes inside group 1) so that:
//   - QUOTED FKs (DBA created the identifier with double-quotes)
//     match: `from table "<REF>".`
//   - UNQUOTED FKs (the canonical case for our lowercase
//     identifiers-not-created-with-quotes schema)
//     match: `from table <REF>.`
//
// The wrapping fmt.Errorf applies %q so operators see a tidy
// "external_deliveries" diagnostic in the slog log regardless
// of input Detail shape. The dispatch sentinel
// (ErrExternalDestinationHasDependents) is GENERIC across all
// such FKs so the API handler doesn't need per-table dispatch
// logic — it always maps to 409 Conflict regardless of which
// table is the blocker (or which Postgres version / pg_restore
// variant emitted the Detail).
var fkReferencingExternalDestinationsDetailRegex = regexp.MustCompile(
	`Key \(id\)=.+? is still referenced from table "?([^".\s]+)"?`)

// ExternalDestinationRepository handles persistence for the
// external_destinations table (migration 054_external_destinations.sql).
//
// One row per (source_system, workspace_id, platform_account_id)
// triple. The application owns ULID-style id generation (e.g.
// `extdst_01J...` per the spec); the DB only stores the byte payload.
// Resolves server-side to (workspace, platform_account, channel, OAuth
// token, publish defaults) when Velox submits an upload referenced by
// the opaque id.
//
// Authz is upstream of this layer: pkg/api/admin_velox_destinations.go
// verifies the caller JWT user owns the workspace (workspace.owner_id
// == user.id) BEFORE calling Create/Update/Delete. The repo therefore
// trusts its inputs.
type ExternalDestinationRepository struct {
	db *sql.DB
}

// NewExternalDestinationRepository creates a new
// ExternalDestinationRepository bound to db.
func NewExternalDestinationRepository(db *sql.DB) *ExternalDestinationRepository {
	return &ExternalDestinationRepository{db: db}
}

// ErrExternalDestinationNotFound is the typed sentinel for "no row
// matched" in GetByID + ListByWorkspace + ListBySourceSystem when
// the result set is empty. The handler maps this to HTTP 404 via
// errors.Is. Mirrors ErrWorkspaceNotFound / ErrPostNotFound in the
// repo package.
var ErrExternalDestinationNotFound = errors.New("external destination not found")

// Create inserts a row, with the application-supplied id (ULID-style
// `extdst_01J...`). Returns the row with created_at + updated_at
// populated. The UNIQUE(source_system, workspace_id,
// platform_account_id) constraint surfaces as
// ErrExternalDestinationAlreadyExists via the pq.Error SQLSTATE
// dispatch (23505 unique_violation). The handler maps this sentinel
// to HTTP 409 Conflict so a double-click on the admin Connetti
// button surfaces the pre-existing row instead of "500 server error".
func (r *ExternalDestinationRepository) Create(ctx context.Context, d *models.ExternalDestination) error {
	if d == nil {
		return errors.New("external destination Create: nil record")
	}
	if d.ID == "" {
		return errors.New("external destination Create: id is required (application-side ULID with extdst_ prefix)")
	}
	if d.SourceSystem == "" {
		return errors.New("external destination Create: source_system is required")
	}
	if d.WorkspaceID <= 0 {
		return errors.New("external destination Create: workspace_id must be positive")
	}
	if d.PlatformAccountID <= 0 {
		return errors.New("external destination Create: platform_account_id must be positive")
	}
	// None of the JSON-encode paths can really fail for a map but we
	// guard the failure anyway so an operator-supplied corrupted
	// payload surfaces as a typed error instead of a SQL round-trip
	// that drops the row.
	if len(d.DefaultMetadata) == 0 {
		d.DefaultMetadata = json.RawMessage("{}")
	}
	if !json.Valid(d.DefaultMetadata) {
		return fmt.Errorf("external destination Create: default_metadata is not valid JSON: %s", string(d.DefaultMetadata))
	}

	err := r.db.QueryRowContext(ctx,
		`INSERT INTO external_destinations
		    (id, source_system, workspace_id, platform_account_id, enabled,
		     default_metadata, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		 RETURNING created_at, updated_at`,
		d.ID, d.SourceSystem, d.WorkspaceID, d.PlatformAccountID, d.Enabled,
		[]byte(d.DefaultMetadata),
	).Scan(&d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		// SQLSTATE 23505 (unique_violation) + Detail content
		// matches the UNIQUE(source_system, workspace_id,
		// platform_account_id) triple column-list prefix. Matches
		// any Postgres 13+ Detail-formatted error for this
		// constraint (post-extension the dispatch was keyed on
		// pqErr.Constraint name; switching to Detail-content
		// match avoids auto-name drift across pg_dump/pg_restore
		// or future constraint-renaming migrations).
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" &&
			pqErr.Detail != "" &&
			uniqueViolationDestTripleDetailRegex.MatchString(pqErr.Detail) {
			return fmt.Errorf("%w: source_system=%s workspace_id=%d platform_account_id=%d",
				ErrExternalDestinationAlreadyExists, d.SourceSystem, d.WorkspaceID, d.PlatformAccountID)
		}
		return fmt.Errorf("external destination Create: %w", err)
	}
	return nil
}

// ErrExternalDestinationAlreadyExists is the typed sentinel for the
// UNIQUE(source_system, workspace_id, platform_account_id) collision.
// Maps to HTTP 409 Conflict at the API layer so the operator sees
// "you already linked this channel" instead of "500 server error".
// Mirrors ErrPostTargetDuplicate / ErrGroupDuplicate in the same package.
var ErrExternalDestinationAlreadyExists = errors.New("external destination already linked for this (source_system, workspace_id, platform_account_id) triple")

// GetByID returns the row for the supplied application-issued id, or
// (nil, nil) when no row matches — matches the (nil, nil) not-found
// convention used by the rest of the repository layer.
//
// Used by /internal/v1/destinations/{id}/validate to look up a row
// at request time. The handler-side authz is upstream (Velox is
// trusted because the Bearer-token middleware authenticated the
// request before this call).
func (r *ExternalDestinationRepository) GetByID(ctx context.Context, id string) (*models.ExternalDestination, error) {
	if id == "" {
		return nil, errors.New("external destination GetByID: empty id")
	}
	dest := &models.ExternalDestination{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, source_system, workspace_id, platform_account_id, enabled,
		        default_metadata, created_at, updated_at
		 FROM external_destinations
		 WHERE id = $1`,
		id,
	).Scan(
		&dest.ID, &dest.SourceSystem, &dest.WorkspaceID, &dest.PlatformAccountID,
		&dest.Enabled, &dest.DefaultMetadata, &dest.CreatedAt, &dest.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("external destination GetByID: %w", err)
	}
	return dest, nil
}

// ListByWorkspace returns every destination belonging to a workspace.
// When enabledOnly is true, the partial index
// idx_external_destinations_workspace_enabled is used; otherwise the
// query falls through to a full-workspace scan. The caller is the
// admin "/api/v1/integrations/velox/destinations" dashboard —
// enabledOnly is the right answer for the public-facing list.
//
// Empty list (no rows) returns an empty slice, NOT nil, so the API
// JSON-encodes as `[]` rather than `null` — matches the convention
// from workspace_channels ListChannels.
func (r *ExternalDestinationRepository) ListByWorkspace(ctx context.Context, workspaceID int64, enabledOnly bool) ([]models.ExternalDestination, error) {
	if workspaceID <= 0 {
		return nil, fmt.Errorf("external destination ListByWorkspace: invalid workspace id %d", workspaceID)
	}
	q := `SELECT id, source_system, workspace_id, platform_account_id, enabled,
	             default_metadata, created_at, updated_at
	      FROM external_destinations
	      WHERE workspace_id = $1`
	if enabledOnly {
		q += ` AND enabled = TRUE`
	}
	q += ` ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, q, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("external destination ListByWorkspace: %w", err)
	}
	defer rows.Close()

	out := make([]models.ExternalDestination, 0, 8)
	for rows.Next() {
		var (
			dest     models.ExternalDestination
			metadata []byte
		)
		if err := rows.Scan(
			&dest.ID, &dest.SourceSystem, &dest.WorkspaceID, &dest.PlatformAccountID,
			&dest.Enabled, &metadata, &dest.CreatedAt, &dest.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("external destination ListByWorkspace scan: %w", err)
		}
		dest.DefaultMetadata = metadata
		out = append(out, dest)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("external destination ListByWorkspace iterate: %w", err)
	}
	return out, nil
}

// ListBySourceSystem returns every destination across the fleet for
// the supplied source_system (e.g. "velox"). Used by the
// /admin/integrations dashboard — cross-workspace admin roster.
// NOT gated by workspace_id; authz at the handler must require
// admin role.
//
// Empty source_system is invalid (would match an empty string; the
// handler-side guard is upstream but the repo defends in depth).
func (r *ExternalDestinationRepository) ListBySourceSystem(ctx context.Context, sourceSystem string) ([]models.ExternalDestination, error) {
	if sourceSystem == "" {
		return nil, errors.New("external destination ListBySourceSystem: empty source_system")
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, source_system, workspace_id, platform_account_id, enabled,
		        default_metadata, created_at, updated_at
		 FROM external_destinations
		 WHERE source_system = $1
		 ORDER BY workspace_id ASC, created_at DESC`,
		sourceSystem,
	)
	if err != nil {
		return nil, fmt.Errorf("external destination ListBySourceSystem: %w", err)
	}
	defer rows.Close()

	out := make([]models.ExternalDestination, 0, 16)
	for rows.Next() {
		var (
			dest     models.ExternalDestination
			metadata []byte
		)
		if err := rows.Scan(
			&dest.ID, &dest.SourceSystem, &dest.WorkspaceID, &dest.PlatformAccountID,
			&dest.Enabled, &metadata, &dest.CreatedAt, &dest.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("external destination ListBySourceSystem scan: %w", err)
		}
		dest.DefaultMetadata = metadata
		out = append(out, dest)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("external destination ListBySourceSystem iterate: %w", err)
	}
	return out, nil
}

// UpdateEnabled sets the operator toggle on an existing destination
// (true → false means "stop accepting Velox uploads for this channel";
// false → true re-arms). Returns
// ErrExternalDestinationNotFound wrapped with id context when zero
// rows match — handler maps to 404.
//
// Limited to the enabled toggle to keep the surface tight; richer
// updates (default_metadata) are deliberately NOT exposed here. The
// expectation is that the per-tenant defaults are set ONCE at
// creation; future-fixing them by editing is uncommon. A future
// Taglio can add a separate UpdateMetadata with audit-trail support.
func (r *ExternalDestinationRepository) UpdateEnabled(ctx context.Context, id string, enabled bool) error {
	if id == "" {
		return errors.New("external destination UpdateEnabled: empty id")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE external_destinations
		 SET enabled    = $2,
		     updated_at = NOW()
		 WHERE id = $1`,
		id, enabled,
	)
	if err != nil {
		return fmt.Errorf("external destination UpdateEnabled: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("external destination UpdateEnabled rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s", ErrExternalDestinationNotFound, id)
	}
	return nil
}

// Delete hard-removes the row. Used by the admin "unlink this
// channel" flow. CASCADE on workspace_id + platform_account_id from
// migration 054 ensures dependent external_deliveries rows are NOT
// dropped (ON DELETE RESTRICT for external_deliveries) — the handler
// MUST verify there are no in-flight deliveries for this
// destination before calling Delete; otherwise the SQL will reject
// the operation with FK violation.
//
// Returns ErrExternalDestinationNotFound wrapped with id context
// when zero rows match.
func (r *ExternalDestinationRepository) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("external destination Delete: empty id")
	}
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM external_destinations WHERE id = $1`,
		id,
	)
	if err != nil {
		// SQLSTATE 23503 (foreign_key_violation) AND Detail content
		// matches the canonical
		//   Key (id)=... is still referenced from table "<REF>"
		// shape. The regex doesn't hardcode the REFERENCING table
		// name, so ANY FK that targets external_destinations.id
		// (external_deliveries today; an external_audit_log table
		// tomorrow) surfaces as the same typed sentinel. The
		// capture group extracts the table name verbatim so the
		// wrapped error gives operators a "blocked by table X"
		// diagnostic without forcing the handler to maintain a
		// per-table dispatch map.
		//
		// Note: a future FK direction (external_destinations
		// referencing SOME other table — not the current schema,
		// but forward-compat thought) would NOT match this regex
		// because the Detail would surface the REFERENCED table,
		// not external_destinations. Such a config is impossible
		// today (external_destinations has no outgoing FKs per
		// migration 054), so the regex is currently safe.
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23503" && pqErr.Detail != "" {
			if matches := fkReferencingExternalDestinationsDetailRegex.FindStringSubmatch(pqErr.Detail); len(matches) > 1 {
				referencingTable := matches[1]
				return fmt.Errorf("%w: blocked by referencing table %q (id=%s)",
					ErrExternalDestinationHasDependents, referencingTable, id)
			}
		}
		return fmt.Errorf("external destination Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("external destination Delete rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s", ErrExternalDestinationNotFound, id)
	}
	return nil
}

// ErrExternalDestinationHasDependents is the typed sentinel for
// any ON DELETE RESTRICT violation on external_destinations
// (regardless of which REFERENCING table blocked the delete —
// external_deliveries today, a future external_audit_log table,
// etc.). The handler maps this sentinel to HTTP 409 Conflict so
// the operator sees "destination still has references; disable
// instead". The wrapped error carries the referencing table name
// from the regex capture group so the operator gets a clean
// "blocked by table X" diagnostic without forcing the handler
// layer to maintain a per-table dispatcher.
//
// Historical alias: ErrExternalDestinationHasDeliveries points at
// the same value to keep pkg/api layer code that previously
// typed-dispatched on the old name working without churn. New
// callers should reference ErrExternalDestinationHasDependents.
var (
	ErrExternalDestinationHasDependents = errors.New("external destination has dependent rows (referencing table blocks delete)")
	ErrExternalDestinationHasDeliveries = ErrExternalDestinationHasDependents // legacy alias
)

// ExternalDeliveryLockNamespace constant is defined in
// external_delivery_repo.go (used by Insert). Declared here as a
// compile-time cross-package reference would introduce an import
// cycle — so the two repositories intentionally share the same
// package (repository) and access it directly.
