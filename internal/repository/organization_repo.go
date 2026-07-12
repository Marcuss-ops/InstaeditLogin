package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// OrganizationRepository handles CRUD for organizations.
//
// Style intentionally mirrors UserRepository / WorkspaceRepository:
// no context.Context parameter, not-found returns (nil, nil), errors
// wrapped with fmt.Errorf("%w", err). The only deviation is the
// dedicated ErrOrganizationNotFound sentinel so callers can use
// errors.Is for explicit "organization does not exist" paths
// (e.g. soft-delete checks), matching the pattern established by
// ErrUserNotFound and ErrWorkspaceNotFound.
//
// Tenant scoping: methods on this repository do NOT enforce an
// organization-scoped query because the organizations table is the
// TENANT ROOT — every callers must explicitly pass the org id.
// Cross-tenant safety on dependent tables (projects,
// organization_members, platform_accounts, posts, …) is enforced
// in their respective repositories.
type OrganizationRepository struct {
	db *sql.DB
}

// NewOrganizationRepository constructs a new repository bound to db.
func NewOrganizationRepository(db *sql.DB) *OrganizationRepository {
	return &OrganizationRepository{db: db}
}

// ErrOrganizationNotFound is the sentinel for zero-row Update/Delete
// operations on organizations. Use errors.Is to dispatch on it.
var ErrOrganizationNotFound = errors.New("organization not found")

// Create inserts a new organization and assigns id/created_at/updated_at.
// The SQL DEFAULTs on status/plan (see migration 013) handle empty-string
// callers — we intentionally do NOT pre-fill default values in Go so the
// SQL DEFAULT is the single source of truth (matches PostRepository's
// pattern). After RETURNING, FindByID is unnecessary because the Scan
// below populates the struct directly.
func (r *OrganizationRepository) Create(org *models.Organization) error {
	err := r.db.QueryRow(
		`INSERT INTO organizations (name, slug, status, plan)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, status, plan, created_at, updated_at`,
		org.Name, org.Slug, org.Status, org.Plan,
	).Scan(&org.ID, &org.Status, &org.Plan, &org.CreatedAt, &org.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create organization: %w", err)
	}
	return nil
}

// FindByID returns the organization with the given id, or (nil, nil)
// when no row matches. The (nil, nil) convention lets callers write
//
//	if org == nil { /* create-new path */ } else { /* use existing */ }
//
// without inspecting sql.ErrNoRows.
func (r *OrganizationRepository) FindByID(id int64) (*models.Organization, error) {
	org := &models.Organization{}
	err := r.db.QueryRow(
		`SELECT id, name, slug, status, plan, created_at, updated_at
		 FROM organizations
		 WHERE id = $1`,
		id,
	).Scan(&org.ID, &org.Name, &org.Slug, &org.Status, &org.Plan,
		&org.CreatedAt, &org.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find organization by id: %w", err)
	}
	return org, nil
}

// FindBySlug returns the organization with the given unique slug, or
// (nil, nil) when no row matches. Used by the dashboard URL resolver
// /o/{slug}/... pattern.
func (r *OrganizationRepository) FindBySlug(slug string) (*models.Organization, error) {
	org := &models.Organization{}
	err := r.db.QueryRow(
		`SELECT id, name, slug, status, plan, created_at, updated_at
		 FROM organizations
		 WHERE slug = $1`,
		slug,
	).Scan(&org.ID, &org.Name, &org.Slug, &org.Status, &org.Plan,
		&org.CreatedAt, &org.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find organization by slug: %w", err)
	}
	return org, nil
}

// Update modifies the editable fields of an organization. Status
// transitions (e.g. active→suspended) and plan changes go through here.
// Returns ErrOrganizationNotFound wrapped with id context when zero
// rows match — the API layer can map this to 404 via errors.Is.
func (r *OrganizationRepository) Update(org *models.Organization) error {
	result, err := r.db.Exec(
		`UPDATE organizations
		 SET name = $1, slug = $2, status = $3, plan = $4, updated_at = $5
		 WHERE id = $6`,
		org.Name, org.Slug, org.Status, org.Plan, time.Now(), org.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update organization: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrOrganizationNotFound, org.ID)
	}
	return nil
}

// Delete removes the organization with the given id. The FK
// ON DELETE CASCADE on projects + organization_members (set in
// migrations 014 and 015) purges child rows in a single transaction.
// Returns ErrOrganizationNotFound wrapped with id context when zero
// rows match.
func (r *OrganizationRepository) Delete(id int64) error {
	result, err := r.db.Exec(`DELETE FROM organizations WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete organization: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrOrganizationNotFound, id)
	}
	return nil
}

// ListAll returns every organization, ordered by created_at DESC.
// Intended for the operator-level dashboard (superadmin view);
// tenant-scoped UIs query ListByMember via OrganizationMemberRepository.
func (r *OrganizationRepository) ListAll() ([]models.Organization, error) {
	rows, err := r.db.Query(
		`SELECT id, name, slug, status, plan, created_at, updated_at
		 FROM organizations
		 ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list organizations: %w", err)
	}
	defer rows.Close()

	var orgs []models.Organization
	for rows.Next() {
		o := models.Organization{}
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.Status, &o.Plan,
			&o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan organization: %w", err)
		}
		orgs = append(orgs, o)
	}
	return orgs, nil
}
