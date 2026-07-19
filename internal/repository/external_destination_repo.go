package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

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
		// SQLSTATE 23505 (unique_violation) +
		// external_destinations_source_system_workspace_id_platform_account_id_key
		// constraint (auto-named from migration 054's UNIQUE clause).
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" &&
			pqErr.Constraint == "external_destinations_source_system_workspace_id_platform_account_id_key" {
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
		// SQLSTATE 23503 (foreign_key_violation) when external_deliveries
		// still references this destination via FK. The handler maps
		// this to HTTP 409 Conflict so the operator sees
		// "destination has in-flight deliveries; disable instead".
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23503" {
			return fmt.Errorf("%w: in-flight deliveries prevent deletion (id=%s)",
				ErrExternalDestinationHasDeliveries, id)
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

// ErrExternalDestinationHasDeliveries is the typed sentinel for the
// ON DELETE RESTRICT violation on external_deliveries. The handler
// surfaces this as 409 Conflict so the operator sees a clean
// "destination is in use" message — pointing them at UpdateEnabled
// instead of Delete.
var ErrExternalDestinationHasDeliveries = errors.New("external destination has in-flight deliveries")

// ExternalDeliveryLockNamespace constant is defined in
// external_delivery_repo.go (used by Insert). Declared here as a
// compile-time cross-package reference would introduce an import
// cycle — so the two repositories intentionally share the same
// package (repository) and access it directly.
