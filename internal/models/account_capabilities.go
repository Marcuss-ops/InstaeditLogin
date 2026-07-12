package models

import (
	"time"
)

// AccountCapabilities is one row in the account_capabilities cache
// table (migration 024, Taglio 5.0 LEVEL 2).
//
// Each row is the per-account effective capability set discovered by
// the platform's CapabilityDiscoverer, intersected with the
// theoretical matrix row at write time so the worker reads a single
// precomputed column instead of running AND/min/intersect on every
// publish tick.
//
// Lifecycle:
//  1. Discovery runs (Meta-family: GET /<ig_business_id>?fields=...
//     or /<page_id>?fields=... or /<user_id>?fields=...).
//  2. Theoretical = matrix.For(platform) at write time.
//  3. Actual = *Discoverer output (nullable when discovery failed).
//  4. Effective = models.Intersect(theoretical, actual).
//  5. Row written via repository.Upsert before Publish is invoked.
//
// Read paths:
//   - worker.publishTarget (PrePublishCheckWithEffective).
//   - HTTP GET /api/v1/accounts/{id}/capabilities (operator UI).
//
// TTL semantics (decision gamma in the L2 design):
//   - While expires_at > NOW(): row is fresh; worker + endpoint use it
//     even if last_error is set (operator can `?refresh=true` to retry).
//   - On TTL expiry: worker falls back to L1 PrePublishCheck
//     (matrix-only).
//   - Endpoint shows last_error either way.
type AccountCapabilities struct {
	PlatformAccountID int64          `json:"platform_account_id"`
	Theoretical       CapabilitySet  `json:"theoretical"`
	Actual            *CapabilitySet `json:"actual,omitempty"`
	Effective         CapabilitySet  `json:"effective"`
	SourceDiscoverer  string         `json:"source_discoverer"`
	LastFetchedAt     time.Time      `json:"last_fetched_at"`
	ExpiresAt         time.Time      `json:"expires_at"`
	LastError         string         `json:"last_error,omitempty"`
	Revision          int            `json:"revision"`
}
