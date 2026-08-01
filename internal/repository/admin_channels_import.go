package repository

import (
	"context"

	"github.com/Marcuss-ops/InstaeditLogin/internal/channelimport"
)

// UpsertPendingChannel (P2 — admin CSV import) bulk-writes
// pre-resolved channel rows into platform_accounts at
// status='pending_authorization'. Pure delegation to
// channelimport.ImportToDB so the parse → DB-write contract stays
// in one place; the HTTP handler and the offline CLI share the
// exact same SQL shape via this method.
//
// ownerUserID MUST be > 0. Caller is responsible for resolving the
// owner_email (HTTP form field OR CLI --owner flag OR env) to a
// concrete user_id BEFORE calling this method; the AdminStore
// interface does not accept an email to keep the surface narrow.
//
// Returns the aggregate Result. Per-row DB failures surface in
// Result.Errors as channelimport.RowError slices (last-write-wins
// UPSERT) so partial-success visibility is preserved when an
// operator uploads 500-channel sheets.
func (r *AdminRepository) UpsertPendingChannel(ctx context.Context, ownerUserID int64, rows []channelimport.ImportRow) (channelimport.Result, error) {
	return channelimport.ImportToDB(ctx, r.db, ownerUserID, rows)
}
