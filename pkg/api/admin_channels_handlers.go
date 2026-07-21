package api

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/channelimport"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// AdminChannelsResponse is the JSON body for GET /admin/channels.
// Per-row + headline counts so the dashboard renders "active 187 /
// reauth_required 13" alongside the table without a second roundtrip.
type AdminChannelsResponse struct {
	Counts    repository.AdminChannelCounts `json:"counts"`
	Channels  []repository.AdminChannelRow  `json:"channels"`
	Generated int64                         `json:"generated_at_unix"`
}

// handleAdminChannels (GET /admin/channels) returns the per-platform
// account table plus the headline counts. Filters via query
// parameters:
//
//	?status=<active|expired|reauth_required|revoked|disconnected|error>
//	?platform=<instagram|youtube|...>
//
// Unrecognised values yield a 422 so a typo doesn't silently return
// the whole table. The default (no filters) is the full
// reauth-required-priority sort — operators see who needs attention
// first, then can drill into a specific platform with ?platform=youtube.
//
// Authz: requireAdmin (gates all /admin/* routes).
func (r *Router) handleAdminChannels(w http.ResponseWriter, req *http.Request) {
	if r.adminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	statusFilter := req.URL.Query().Get("status")
	platformFilter := req.URL.Query().Get("platform")

	if statusFilter != "" {
		switch statusFilter {
		case "active", "expired", "reauth_required", "revoked",
			"disconnected", "error", "pending_authorization":
			// valid
		default:
			writeError(w, http.StatusUnprocessableEntity, "unknown status filter: "+statusFilter)
			return
		}
	}

	counts, err := r.adminStore.ChannelCounts(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load channel counts: "+err.Error())
		return
	}
	channels, err := r.adminStore.ListChannelsForOps(req.Context(), statusFilter, platformFilter, 500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list channels: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, AdminChannelsResponse{
		Counts:    counts,
		Channels:  channels,
		Generated: nowUnix(),
	})
}

// handleAdminChannelsCSV (GET /admin/channels.csv) streams the same
// rows as a CSV via the D4.a streaming helper. Same query parameters
// as the JSON handler; the file's header row is the union of
// AdminChannelRow's wire-friendly fields.
func (r *Router) handleAdminChannelsCSV(w http.ResponseWriter, req *http.Request) {
	if r.adminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	statusFilter := req.URL.Query().Get("status")
	platformFilter := req.URL.Query().Get("platform")
	channels, err := r.adminStore.ListChannelsForOps(req.Context(), statusFilter, platformFilter, 500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list channels: "+err.Error())
		return
	}

	_, csvw, flush, err := writeAdminCSV(w, "channels")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "csv writer init failed")
		return
	}
	_ = csvw.Write([]string{
		"platform_account_id", "user_id", "platform", "username", "status",
		"connected_at", "last_validated_at", "last_refresh_at", "reauth_required_at",
		"last_error_code", "last_error_message",
	})
	for _, ch := range channels {
		_ = csvw.Write([]string{
			itoa(ch.PlatformAccountID),
			itoa(ch.UserID),
			ch.Platform,
			ch.Username,
			ch.Status,
			formatTimePtr(ch.ConnectedAt),
			formatTimePtr(ch.LastValidatedAt),
			formatTimePtr(ch.LastRefreshAt),
			formatTimePtr(ch.ReauthRequiredAt),
			ch.LastErrorCode,
			ch.LastErrorMessage,
		})
	}
	if err := flush(); err != nil {
		slogCSVStreamError("channels", err)
	}
}

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
// Workspace names in the CSV are resolved via r.workspaceStore
// (workspace_id is the FK on platform_accounts). Unresolvable
// names surface as RowErrors with reason "no such workspace: NAME".
// See internal/channelimport for the parse + upsert contract.
func (r *Router) handleAdminImportChannelsCSV(w http.ResponseWriter, req *http.Request) {
	if r.adminStore == nil {
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
			res, err := r.runImportFromCSV(req, file, id)
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
	ownerID, err := r.resolveUserIDByOwnerEmail(req, ownerEmail)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "could not resolve owner_email: "+err.Error())
		return
	}
	res, err := r.runImportFromCSV(req, file, ownerID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleAdminPendingChannels (GET /admin/channels/pending) is the
// canonical "what still needs an OAuth dance" dashboard readout.
// Hard-codes status='pending_authorization' on the existing
// ListChannelsForOps path \u2014 no new SQL needed, no new admin_repo
// method. Optional ?platform= filter persists from handleAdminChannels
// so a fleet grouped by platform remains filterable here too.
//
// Authz: requireAdmin (gated by the adminAuthMiddleware in Setup()).
func (r *Router) handleAdminPendingChannels(w http.ResponseWriter, req *http.Request) {
	if r.adminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	platformFilter := req.URL.Query().Get("platform")
	channels, err := r.adminStore.ListChannelsForOps(req.Context(), "pending_authorization", platformFilter, 500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list pending channels: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, AdminChannelsResponse{
		Counts:    repository.AdminChannelCounts{Total: len(channels)},
		Channels:  channels,
		Generated: nowUnix(),
	})
}

// runImportFromCSV is the shared driver used both by the HTTP
// handler and (via the same package in tests) by the offline CLI
// later (scripts/import_channels_csv.go). The flow is:
//
//  1. Build a workspace-ID lookup from r.workspaceStore.ListByOwner
//     so the parser can resolve CSV `workspace` names to FK ids.
//  2. channelimport.Parse the stream with that lookup.
//  3. channelimport.ImportToDB the rows via r.adminStore.
//  4. Return an AdminImportChannelsResponse with the breakdown.
//
// WorkspaceStore.ListByOwner returns only the admin's OWNED
// workspaces today; for cross-tenant onboarding (rare; the
// bootstrap admin can delegate workspace names to operators
// later via the workspaces API), a follow-up pass should add
// ListByMember. Today's contract is intentionally narrow so a
// misconfigured import can't accidentally stamp operator-fleet
// rows onto another tenant's workspace.
func (r *Router) runImportFromCSV(req *http.Request, file io.Reader, ownerUserID int64) (AdminImportChannelsResponse, error) {
	res := AdminImportChannelsResponse{OwnerID: ownerUserID}

	if r.workspaceStore == nil {
		return res, fmt.Errorf("workspace store not configured (admin imports require workspace resolution)")
	}
	// Build a name -> id lookup for workspaces owned by the admin
	// who is running this import. We pull the admin's identity from
	// the request context (adminAuthMiddleware deposited it).
	adminID := adminIdentityUserID(req)
	if adminID == 0 {
		return res, fmt.Errorf("admin identity missing from request context (adminAuthMiddleware must run first)")
	}
	owned, err := r.workspaceStore.ListByOwner(adminID)
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

	dbRes, err := r.adminStore.UpsertPendingChannel(req.Context(), ownerUserID, rows)
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
// Implementation note: we call r.userRepo.FindPlatformAccount
// indirectly via a small wrapper that uses UserStore to look up
// the user's email. Since UserStore is defined here as a narrow
// interface, we add a single method to it: FindUserIDByEmail.
// Production wiring in internal/bootstrap/app.go already provides
// a *repository.UserRepository; the implementation satisfies the
// extended interface (see handlers.go compile-time assertion).
func (r *Router) resolveUserIDByOwnerEmail(req *http.Request, email string) (int64, error) {
	if r.userRepo == nil {
		return 0, fmt.Errorf("user repository not configured")
	}
	return r.userRepo.FindUserIDByEmail(req.Context(), email)
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

// AdminConnectLinkResponse is the JSON body for a successful
// POST /admin/channels/{channel_id}/connect-link. The admin's
// SPA/CLI shows the operator the connect_url verbatim (link
// target) + the expires_in_seconds so the UI can render a
// countdown. The state JWT inside the URL is opaque to callers
// — its semantic content (expected_channel_id, nonce, exp) is
// only meaningful to the /api/v1/auth/youtube/callback handler.
//
// ChannelID + Platform + ManagerEmailHint are echoed back so
// the SPA can render a confirmation card ("You're about to
// link YouTube channel UC… to owner you@example.com") without a
// second roundtrip.
type AdminConnectLinkResponse struct {
	ConnectURL         string `json:"connect_url"`
	StateExpiresInSecs int    `json:"expires_in_seconds"`
	ChannelID          int64  `json:"channel_id"`
	Platform           string `json:"platform"`
	ManagerEmailHint   string `json:"manager_email_hint,omitempty"`
}

// handleAdminChannelConnectLink (POST /admin/channels/{channel_id}/connect-link)
// issues a signed OAuth URL the manager (or operator) can open in a
// browser to complete the OAuth dance for a single pre-resolved
// channel row.
//
// The URL carries:
//   - state=<JWT> signed HS256 with the same secret as the auth
//     tokens. The /api/v1/auth/youtube/callback handler detects
//     the JWT shape (2 dots), calls auth.Manager.VerifyConnectLinkState,
//     extracts the expected_channel_id, and validates the actual
//     channels.list(mine=true) call against it — a discovery that
//     returns a DIFFERENT channel ID is rejected with 422 rather
//     than silently re-attaching the token to whatever channel the
//     grant happens to target.
//   - prompt=select_account consent (both flags via OAuthLoginOptions).
//     Forces the operator to re-pick the manager's Google account
//     AND re-consent so a previously-cached grant cannot bind to a
//     different Brand Account silently.
//   - login_hint=<manager_email_hint> — Google pre-fills the
//     account-picker; the OAuth server still verifies identity
//     cryptographically (login_hint is NOT authentication per
//     Google's OAuth docs).
//
// The JWT TTL is 30 minutes — long enough for the manager to
// complete the OAuth dance, short enough that an intercepted URL
// has a tight replay window.
//
// Authz: adminAuthMiddleware runs FIRST and rejects non-admin
// callers with 403; this handler assumes the admin identity is
// already present in the request context.
//
// Allowed states for the channel_id:
//   - pending_authorization: the canonical import-csv state; the
//     operator clicks connect-link to drive the OAuth dance forward.
//   - reauth_required: the operator wants to re-consent a previously
//     connected channel (e.g. refresh-token rotation).
//   - active: rare; allowed because an admin may genuinely need to
//     link a different Google account onto an already-active row
//     (the token rotation / channel-takeover runbook).
//
// Disallowed: expired, revoked, disconnected, error — surface as
// 422 with an explicit reason so the SPA can prompt the operator
// to clean up the row first (DELETE /api/v1/accounts/{id} +
// re-import).
func (r *Router) handleAdminChannelConnectLink(w http.ResponseWriter, req *http.Request) {
	if r.adminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	if r.userRepo == nil {
		writeError(w, http.StatusNotImplemented, "user store not configured")
		return
	}
	channelIDStr := chi.URLParam(req, "channel_id")
	channelID, err := strconv.ParseInt(channelIDStr, 10, 64)
	if err != nil || channelID <= 0 {
		writeError(w, http.StatusBadRequest, "channel_id must be a positive integer")
		return
	}

	// Look up the platform_account row the operator is linking.
	account, err := r.userRepo.FindPlatformAccountByID(channelID)
	if err != nil {
		writeError(w, http.StatusNotFound, "channel not found: "+err.Error())
		return
	}
	if account == nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}

	// The connect-link feature is YouTube-specific — the
	// metadata.bind path is Google-only and other providers
	// (Facebook Pages, Instagram Business) carry their own
	// multi-account model. Surface as 422 if the operator
	// imports a non-YouTube row.
	if account.Platform != models.PlatformYouTube {
		writeError(w, http.StatusUnprocessableEntity,
			"connect-link supports only YouTube channels (got "+account.Platform+")")
		return
	}

	// Allowed-state gate. Disallowed states return 422 with an
	// explicit reason rather than 409 / 403 so the SPA can
	// distinguish "your channel row needs cleanup first" from
	// "your admin token is invalid".
	switch account.Status {
	case "active", "pending_authorization", "reauth_required":
		// ok
	default:
		writeError(w, http.StatusUnprocessableEntity,
			"channel row is in state '"+account.Status+"' which is not eligible for connect-link (allowed: active, pending_authorization, reauth_required)")
		return
	}

	// manager_email_hint lives in platform_accounts.Metadata — the
	// CSV-import path stamps it as "manager_email_hint" (see
	// internal/channelimport.Parse). Without it we cannot produce
	// the OAuth login_hint (Google's account-picker pre-fill);
	// surface as 422 so the operator can fix the source row.
	managerHint := ""
	if v, ok := account.Metadata["manager_email_hint"]; ok {
		if s, ok := v.(string); ok {
			managerHint = strings.TrimSpace(s)
		}
	}
	if managerHint == "" {
		writeError(w, http.StatusUnprocessableEntity,
			"channel row is missing metadata.manager_email_hint — re-import via /admin/channels/import-csv with the manager_email_hint column filled in")
		return
	}

	// Resolve the OAuth provider via capabilities. The router
	// wires the same CapabilityRouter used by /api/v1/auth/{p}
	// /login, so any provider supported there is supported here.
	p, ok := r.capabilities.OAuth(account.Platform)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity,
			"no OAuth provider is configured for platform "+account.Platform)
		return
	}

	// Issue the JWT carry the expected channel_id. 30-minute TTL.
	jwtState, err := r.auth.IssueConnectLinkState(account.PlatformUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not issue connect-link state: "+err.Error())
		return
	}

	// Build the URL: prompt=select_account consent + login_hint
	// = manager_email_hint. The YouTubeOAuthService composes the
	// full Google authorize endpoint; other providers get the
	// same call pattern translated to their own endpoints.
	connectURL := p.GetLoginURLWithOptions(jwtState, services.OAuthLoginOptions{
		SelectAccount: true,
		ForceConsent:  true,
		LoginHint:     managerHint,
	})

	// Never log the URL — it carries a signed JWT that, while
	// short-lived, is one verify-call away from binding the wrong
	// account if replayed.
	slog.Info("admin: connect-link issued",
		"channel_id", account.ID,
		"platform", account.Platform,
		"channel_id_youtube", account.PlatformUserID,
	)

	writeJSON(w, http.StatusOK, AdminConnectLinkResponse{
		ConnectURL:         connectURL,
		StateExpiresInSecs: 1800,
		ChannelID:          account.ID,
		Platform:           account.Platform,
		ManagerEmailHint:   managerHint,
	})
}

// handleAdminYouTubeFleetReadiness (GET /admin/youtube/fleet_readiness)
// is the Definition-of-Done rollout snapshot endpoint. On each
// call the AdminRepository aggregates the 12 DoD counters in one
// COUNT(*) FILTER roundtrip and persists one row per YouTube
// platform_account into fleet_readiness_snapshot_channels so a
// later diff against the prior snapshot highlights which channels
// transitioned between states. The 12 fields + snapshot_id flow
// back to the operator dashboard as JSON.
//
// Authz:
//   - non-admin callers -> 403 (adminAuthMiddleware short-circuits
//     upstream of this handler; the defensive IsAdmin re-check here
//     catches any future wiring that drops the middleware on a per-
//     route basis).
//   - adminStore nil -> 501 (mounting the admin repo is a deliberate
//     subset-setup; tests can omit it without 500-ing the endpoint).
//
// The handler is intentionally read-only + idempotent: a hostile
// retry of the same call yields a NEW snapshot row + the same JSON
// counts (calls converge on idempotency at the counts layer; the
// audit history diverges by taken_at).
func (r *Router) handleAdminYouTubeFleetReadiness(w http.ResponseWriter, req *http.Request) {
	if r.adminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || !identity.IsAdmin() {
		writeError(w, http.StatusForbidden, "requires admin privileges")
		return
	}
	adminID := identity.UserID()
	if adminID == 0 {
		writeError(w, http.StatusForbidden, "requires authenticated admin identity")
		return
	}

	snap, err := r.adminStore.CreateFleetReadinessSnapshot(req.Context(), adminID)
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"could not take fleet readiness snapshot: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, snap)
}
