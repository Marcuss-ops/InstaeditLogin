package api

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/channelimport"
)

// AdminImportChannelsRequest is the multipart shape for
// POST /admin/channels/import-csv. Only two form fields are accepted
// from the caller; everything else lives inside the uploaded CSV
// (channel_id, channel_name, manager_email_hint, workspace, group,
// language, timezone, expected_upload_frequency). The CSV is the
// canonical spec; the envelope exists only to wire auth context
// (admin's session) + owner_email resolution to user_id.
type AdminImportChannelsRequest struct {
	// File is the CSV under the "file" multipart field. Required.
	File io.Reader
	// OwnerEmail resolves to the user_id stamped on every inserted
	// row (FK to users.id). The default for cross-workspace fleet
	// import is a designated admin user the operator ensures exists
	// in the seed; per-workspace imports pass THAT workspace's
	// owner_email so the rows reflect the workspace tenancy.
	OwnerEmail string
}

// AdminImportChannelsResponse is the JSON body for a successful
// POST /admin/channels/import-csv. Imports + Skipped are the
// per-run totals; Errors is a slice (potentially empty) of
// row-level failures with line-number + operator-readable message.
//
// IMPORTANT (CASCADE): re-importing the same CSV is an UPSERT (last-
// write-wins). A re-import never reports Errors=0 + Imported=0 —
// at minimum the rows are reflected. The Errors slice is reserved
// for parse-level failures (missing column, unresolvable workspace)
// and per-row DB write failures.
type AdminImportChannelsResponse struct {
	Imported int                      `json:"imported"`
	Skipped  int                      `json:"skipped"`
	Errors   []channelimport.RowError `json:"errors,omitempty"`
	OwnerID  int64                    `json:"owner_id"`
}

// handleAdminImportChannelsCSV (POST /admin/channels/import-csv)
// parses a multipart upload of a channel-inventory CSV and upserts
// every row at status='pending_authorization' so the operator's
// OAuth dance can proceed in a separate step. Tokens are NEVER
// written here \u2014 the row's whole purpose is to await OAuth.
//
// Multipart shape:
//   - "file": the CSV under this form key. Required.
//   - "owner_email": email of the user stamped on every inserted row.
//     Defaults to the admin's own session email when absent so the
//     one-click import doesn't require a second field.
//
// Workspace names in the CSV are resolved via m.deps.WorkspaceStore
// (workspace_id is the FK on platform_accounts). Unresolvable
// names surface as RowErrors with reason "no such workspace: NAME".
// See internal/channelimport for the parse + upsert contract.
func (m *AdminModule) handleAdminImportChannelsCSV(w http.ResponseWriter, req *http.Request) {
	if m.deps.AdminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	if err := req.ParseMultipartForm(20 << 20); err != nil {
		// 20 MiB cap: a 200-channel CSV is roughly 20 KB; well below.
		writeError(w, http.StatusBadRequest, "could not parse multipart form: "+err.Error())
		return
	}
	file, _, err := req.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing 'file' multipart field: "+err.Error())
		return
	}
	defer file.Close()

	ownerEmail := req.FormValue("owner_email")
	if ownerEmail == "" {
		// Fallback: the admin's session email. Honest default \u2014 a
		// row stamped with the operator's user_id is the closest
		// thing to "your fleet" in the absence of an explicit
		// workspace-level owner.
		if id := adminIdentityUserID(req); id != 0 {
			// We have the user_id already; just use it directly.
			res, err := m.runImportFromCSV(req, file, id)
			if err != nil {
				writeError(w, http.StatusUnprocessableEntity, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, res)
			return
		}
		writeError(w, http.StatusBadRequest, "owner_email form field is required when admin session is anonymous")
		return
	}
	ownerID, err := m.resolveUserIDByOwnerEmail(req, ownerEmail)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "could not resolve owner_email: "+err.Error())
		return
	}
	res, err := m.runImportFromCSV(req, file, ownerID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// runImportFromCSV is the shared driver used both by the HTTP
// handler and (via the same package in tests) by the offline CLI
// later (scripts/import_channels_csv.go). The flow is:
//
//  1. Build a workspace-ID lookup from m.deps.WorkspaceStore.ListByOwner
//     so the parser can resolve CSV `workspace` names to FK ids.
//  2. channelimport.Parse the stream with that lookup.
//  3. channelimport.ImportToDB the rows via m.deps.AdminStore.
//  4. Return an AdminImportChannelsResponse with the breakdown.
//
// WorkspaceStore.ListByOwner returns only the admin's OWNED
// workspaces today; for cross-tenant onboarding (rare; the
// bootstrap admin can delegate workspace names to operators
// later via the workspaces API), a follow-up pass should add
// ListByMember. Today's contract is intentionally narrow so a
// misconfigured import can't accidentally stamp operator-fleet
// rows onto another tenant's workspace.
func (m *AdminModule) runImportFromCSV(req *http.Request, file io.Reader, ownerUserID int64) (AdminImportChannelsResponse, error) {
	res := AdminImportChannelsResponse{OwnerID: ownerUserID}

	if m.deps.WorkspaceStore == nil {
		return res, fmt.Errorf("workspace store not configured (admin imports require workspace resolution)")
	}
	// Build a name -> id lookup for workspaces owned by the admin
	// who is running this import. We pull the admin's identity from
	// the request context (adminAuthMiddleware deposited it).
	adminID := adminIdentityUserID(req)
	if adminID == 0 {
		return res, fmt.Errorf("admin identity missing from request context (adminAuthMiddleware must run first)")
	}
	owned, err := m.deps.WorkspaceStore.ListByOwner(adminID)
	if err != nil {
		return res, fmt.Errorf("list owned workspaces: %w", err)
	}
	lookup := make(map[string]int64, len(owned))
	for _, ws := range owned {
		lookup[ws.Name] = ws.ID
	}

	rows, parseErrs, err := channelimport.Parse(file, "youtube",
		func(name string) (int64, bool) {
			id, ok := lookup[name]
			return id, ok
		})
	if err != nil {
		return res, fmt.Errorf("csv parse: %w", err)
	}
	// Surface parse-level skips to the response BEFORE the DB
	// write so the operator sees them in the same envelope as
	// per-row DB failures.
	for _, pe := range parseErrs {
		res.Skipped++
		res.Errors = append(res.Errors, pe)
	}

	dbRes, err := m.deps.AdminStore.UpsertPendingChannel(req.Context(), ownerUserID, rows)
	if err != nil {
		return res, fmt.Errorf("upsert pending channels: %w", err)
	}
	res.Imported = dbRes.Imported
	res.Skipped += dbRes.Skipped
	res.Errors = append(res.Errors, dbRes.Errors...)

	slog.Info("admin: channels imported",
		"owner_id", ownerUserID,
		"imported", res.Imported,
		"skipped", res.Skipped,
	)
	return res, nil
}

// resolveUserIDByOwnerEmail returns the user_id matching
// ownerEmail. Wired through the UserStore Attach methods surface;
// we use FindPlatformAccount (which actually doesn't fit \u2014 user
// not platform_account) so this helper uses FindUserByEmail via
// the userStore contract.
//
// Implementation note: we call m.deps.UserStore.FindPlatformAccount
// indirectly via a small wrapper that uses UserStore to look up
// the user's email. Since UserStore is defined here as a narrow
// interface, we add a single method to it: FindUserIDByEmail.
// Production wiring in internal/bootstrap/app.go already provides
// a *repository.UserRepository; the implementation satisfies the
// extended interface (see handlers.go compile-time assertion).
func (m *AdminModule) resolveUserIDByOwnerEmail(req *http.Request, email string) (int64, error) {
	if m.deps.UserStore == nil {
		return 0, fmt.Errorf("user repository not configured")
	}
	return m.deps.UserStore.FindUserIDByEmail(req.Context(), email)
}

// adminIdentityUserID extracts the user_id from the JWT identity
// stamped into the request context by Manager.Middleware +
// adminAuthMiddleware. Returns 0 when the middleware chain failed
// (which adminAuthMiddleware itself would have rejected already;
// the 0 is defensive for handler-side fallback paths).
func adminIdentityUserID(req *http.Request) int64 {
	id := auth.IdentityFromContext(req.Context())
	if id == nil {
		return 0
	}
	return id.UserID()
}
