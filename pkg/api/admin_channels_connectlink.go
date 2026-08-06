package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

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
func (m *AdminModule) handleAdminChannelConnectLink(w http.ResponseWriter, req *http.Request) {
	if m.deps.AdminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	if m.deps.UserStore == nil {
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
	account, err := m.deps.UserStore.FindPlatformAccountByID(channelID)
	if err != nil {
		writeError(w, http.StatusNotFound, "channel not found: "+err.Error())
		return
	}
	if account == nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	account.Platform = models.NormalizePlatformIdentifier(account.Platform)

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
	p, ok := m.deps.Capabilities.OAuth(account.Platform)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity,
			"no OAuth provider is configured for platform "+account.Platform)
		return
	}

	// Issue the JWT carrying the expected channel_id. 30-minute TTL.
	jwtState, nonce, expiresAt, err := m.deps.AuthManager.IssueConnectLinkState(account.PlatformUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not issue connect-link state: "+err.Error())
		return
	}

	// Persist the nonce so the callback can atomically consume it.
	// The nonce is the single-use handle inside the JWT; persisting
	// it here lets us reject replays even though the JWT itself is
	// stateless. Use the JWT's own expiry so the DB record cannot
	// drift from the JWT validity window.
	if m.deps.ConnectLinkNonceStore == nil {
		writeError(w, http.StatusServiceUnavailable, "connect-link nonce store not configured")
		return
	}
	if saveErr := m.deps.ConnectLinkNonceStore.Create(nonce, account.PlatformUserID, expiresAt); saveErr != nil {
		writeError(w, http.StatusInternalServerError, "could not persist connect-link nonce: "+saveErr.Error())
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
