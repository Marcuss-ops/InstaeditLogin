package models

import "time"

// Organization status constants — mirror the status column accepted
// by migration 013_organizations.sql. ENUM-style values stored as TEXT
// (not a Postgres type) so adding values does not require an ALTER TYPE
// migration — the application is the source of truth for valid values.
const (
	OrganizationStatusActive    = "active"
	OrganizationStatusSuspended = "suspended"
	OrganizationStatusArchived  = "archived"
)

// Organization plan constants — billing anchor. New orgs default to
// "free" until the billing flow lands in a follow-up Taglio. Cheap
// to add new plans on the application side; no DB migration needed
// because the column is TEXT.
const (
	OrganizationPlanFree       = "free"
	OrganizationPlanStarter    = "starter"
	OrganizationPlanPro        = "pro"
	OrganizationPlanEnterprise = "enterprise"
	OrganizationPlanCustom     = "custom"
)

// Organization is the top-level tenant container per Zernio's
// multi-tenant hierarchy. Each user-account-session scope lives
// inside ONE organization; cross-tenant queries are blocked at the
// repository layer by the tenant filter clause.
//
// Mirrors the `organizations` table introduced by migration
// 013_organizations.sql.
type Organization struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Status    string    `json:"status"`
	Plan      string    `json:"plan"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
