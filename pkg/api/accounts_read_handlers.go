package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// accountListItem is the wire shape returned by handleListAccounts.
// We deliberately do NOT return the PlatformAccount struct directly:
// it leaks user_id, last_error_code/message, metadata blob, and
// every internal audit column the SPA does not need. The 6 fields
// below are the SPEC'd response contract: id, platform,
// platform_user_id, username, status, created_at.
type accountListItem struct {
	ID             int64     `json:"id"`
	Platform       string    `json:"platform"`
	PlatformUserID string    `json:"platform_user_id"`
	Username       string    `json:"username"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

// handleListAccounts returns the authenticated user's connected
// social accounts. SPRINT 7.1 (P0#14) closure: identity comes ONLY
// from the JWT (deposited by r.protected → r.auth.Middleware); never
// from query params, body, or path. WorkspaceID from the identity
// is captured for tenant-scoping future work (Taglio 1.4 audit
// log) but is NOT used as a SQL filter — PlatformAccount is currently
// user-scoped in the schema (a single social identity serves every
// workspace the user is a member of; this matches the Taglio 2.4
// "OAuth is one identity per user, not per workspace" contract).
//
// Response always uses the {"accounts": [...]} wrapper so the SPA's
// JSON decoder can iterate unconditionally — never nil-vs-empty,
// always an array (possibly empty).
func (r *Router) handleListAccounts(w http.ResponseWriter, req *http.Request) {
	id := auth.IdentityFromContext(req.Context())
	if id == nil || id.UserID() <= 0 {
		// Defence-in-depth: r.protected() should have already
		// rejected this with 401. If a future refactor accidentally
		// wires this handler without the middleware, refuse the
		// request rather than silently returning any user's data.
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	_ = id.WorkspaceID() // tenancy captured for audit; not used as SQL filter (see godoc)

	accounts, err := r.userRepo.ListPlatformAccountsByUser(id.UserID(), "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list accounts: "+err.Error())
		return
	}
	items := make([]accountListItem, 0, len(accounts))
	for _, a := range accounts {
		items = append(items, accountListItem{
			ID:             a.ID,
			Platform:       a.Platform,
			PlatformUserID: a.PlatformUserID,
			Username:       a.Username,
			Status:         a.Status,
			CreatedAt:      a.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"accounts": items})
}

// loadOwnAccountByID centralises the auth + load + ownership check
// shared by all four /accounts/{id} handlers. Returns the loaded
// account + identity on success; writes 401/404/500 directly to w
// and returns (nil, nil, false) on failure. The 404 (not 403) for
// cross-tenant probes is critical: a malicious probe MUST NOT be
// able to enumerate which account ids exist in other users by
// observing the 403 vs 404 response shape.
func (r *Router) loadOwnAccountByID(w http.ResponseWriter, req *http.Request, id int64) (*models.PlatformAccount, auth.Identity, bool) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return nil, nil, false
	}
	account, err := r.userRepo.FindPlatformAccountByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find account: "+err.Error())
		return nil, nil, false
	}
	if account == nil || account.UserID != identity.UserID() {
		// No existence leak: 404 covers both nil and cross-tenant.
		writeError(w, http.StatusNotFound, "account not found")
		return nil, nil, false
	}
	return account, identity, true
}


// handleGetAccount returns a single platform account owned by the
// authenticated user. When the provider implements AccountDetailsProvider
// and a cached snapshot exists, the response includes a "resource" field
// with rich details (metrics, branding, stats). The base 6-field shape
// is always present for backward compatibility.
func (r *Router) handleGetAccount(w http.ResponseWriter, req *http.Request) {
	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, _, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}

	type accountMetric struct {
		Key          string `json:"key"`
		Label        string `json:"label"`
		Value        int64  `json:"value"`
		DisplayValue string `json:"display_value"`
	}
	type accountResource struct {
		ResourceType string          `json:"resource_type"`
		ExternalID   string          `json:"external_id"`
		DisplayName  string          `json:"display_name"`
		Handle       string          `json:"handle,omitempty"`
		Description  string          `json:"description,omitempty"`
		AvatarURL    string          `json:"avatar_url,omitempty"`
		BannerURL    string          `json:"banner_url,omitempty"`
		PublicURL    string          `json:"public_url,omitempty"`
		Metrics      []accountMetric `json:"metrics"`
		Properties   map[string]any  `json:"properties,omitempty"`
		FetchedAt    time.Time       `json:"fetched_at"`
	}
	type accountDetailResponse struct {
		accountListItem
		Resource *accountResource `json:"resource,omitempty"`
	}

	resp := accountDetailResponse{
		accountListItem: accountListItem{
			ID:             account.ID,
			Platform:       account.Platform,
			PlatformUserID: account.PlatformUserID,
			Username:       account.Username,
			Status:         account.Status,
			CreatedAt:      account.CreatedAt,
		},
	}

	const snapshotMaxAge = 10 * time.Minute

	// Shortcut: no snapshot store wired → return base account without resource.
	if r.snapshotStore == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Try to enrich with cached snapshot data. When the snapshot is fresh
	// (< 10 min) we serve it directly; when it's stale or missing, we
	// reach out to the provider, persist a fresh snapshot, and serve that.
	stale, err := r.snapshotStore.IsSnapshotStale(account.ID, snapshotMaxAge)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "snapshot freshness check failed: "+err.Error())
		return
	}

	if stale {
		// Cache miss or stale — fetch fresh details from the provider.
		if detailsProvider, ok := r.capabilities.AccountDetails(account.Platform); ok {
			token, tokenErr := r.vault.Get(req.Context(), account.ID, models.TokenTypeBearer)
			if tokenErr != nil {
				token, tokenErr = r.vault.Get(req.Context(), account.ID, models.TokenTypeLongLived)
				if tokenErr != nil {
					token, tokenErr = r.vault.Get(req.Context(), account.ID, models.TokenTypeShortLived)
				}
			}
			if tokenErr == nil {
				details, detailsErr := detailsProvider.GetAccountDetails(req.Context(), token.AccessToken, account.PlatformUserID)
				if detailsErr == nil {
					// Build and persist the snapshot.
					snap := &repository.AccountResourceSnapshot{
						PlatformAccountID: account.ID,
						ResourceType:      details.ResourceType,
						Profile: map[string]any{
							"display_name": details.DisplayName,
							"handle":       details.Handle,
							"description":  details.Description,
							"avatar_url":   details.AvatarURL,
							"banner_url":   details.BannerURL,
							"public_url":   details.PublicURL,
							"external_id":  details.ExternalID,
						},
						FetchedAt: details.FetchedAt,
					}
					stats := make(map[string]any)
					for _, m := range details.Metrics {
						stats[m.Key] = map[string]any{
							"label":         m.Label,
							"value":         m.Value,
							"display_value": m.DisplayValue,
						}
					}
					snap.Statistics = stats
					if details.Properties != nil {
						snap.Content = details.Properties
					}
					// Best-effort save — if it fails we're already holding the
					// fresh data in memory and can serve it.
					_ = r.snapshotStore.UpsertSnapshot(snap)

					// Persist the daily metric history row. This is also best-
					// effort: a failure here should not break the request.
					if r.metricHistoryStore != nil {
						_ = r.metricHistoryStore.UpsertDaily(account.ID, details.FetchedAt, metricsToPoint(details.Metrics))
					}

					// Build resource from the fresh details.
					res := &accountResource{
						ResourceType: details.ResourceType,
						ExternalID:   details.ExternalID,
						DisplayName:  details.DisplayName,
						Handle:       details.Handle,
						Description:  details.Description,
						AvatarURL:    details.AvatarURL,
						BannerURL:    details.BannerURL,
						PublicURL:    details.PublicURL,
						FetchedAt:    details.FetchedAt,
					}
					for _, m := range details.Metrics {
						res.Metrics = append(res.Metrics, accountMetric{
							Key:          m.Key,
							Label:        m.Label,
							Value:        m.Value,
							DisplayValue: m.DisplayValue,
						})
					}
					if details.Properties != nil {
						res.Properties = details.Properties
					}
					resp.Resource = res
					writeJSON(w, http.StatusOK, resp)
					return
				}
			}
		}
		// Fall through: provider call failed or platform doesn't support
		// details — serve whatever stale snapshot (if any) is still in the DB.
	}

	// Serve from cache (fresh snapshot, or stale snapshot as fallback).
	snap, snapErr := r.snapshotStore.GetSnapshot(account.ID)
	if snapErr == nil && snap != nil {
		res := &accountResource{
			ResourceType: snap.ResourceType,
			FetchedAt:    snap.FetchedAt,
		}
		if v, ok := snap.Profile["external_id"].(string); ok {
			res.ExternalID = v
		}
		if v, ok := snap.Profile["display_name"].(string); ok {
			res.DisplayName = v
		}
		if v, ok := snap.Profile["handle"].(string); ok {
			res.Handle = v
		}
		if v, ok := snap.Profile["description"].(string); ok {
			res.Description = v
		}
		if v, ok := snap.Profile["avatar_url"].(string); ok {
			res.AvatarURL = v
		}
		if v, ok := snap.Profile["banner_url"].(string); ok {
			res.BannerURL = v
		}
		if v, ok := snap.Profile["public_url"].(string); ok {
			res.PublicURL = v
		}

		for key, val := range snap.Statistics {
			if m, ok := val.(map[string]any); ok {
				am := accountMetric{Key: key}
				if v, ok := m["label"].(string); ok {
					am.Label = v
				}
				if v, ok := m["value"].(float64); ok {
					am.Value = int64(v)
				}
				if v, ok := m["display_value"].(string); ok {
					am.DisplayValue = v
				}
				res.Metrics = append(res.Metrics, am)
			}
		}

		if snap.Content != nil {
			res.Properties = snap.Content
		}

		resp.Resource = res
	}

	writeJSON(w, http.StatusOK, resp)
}

// metricsToPoint extracts numeric metrics from the provider details and
// maps them to a repository point. Unknown keys are ignored, so the
// helper is safe for any platform that returns a subset of the keys.
func metricsToPoint(metrics []models.AccountMetric) repository.AccountMetricPoint {
	p := repository.AccountMetricPoint{}
	for _, m := range metrics {
		switch m.Key {
		case "subscribers":
			p.Subscribers = m.Value
		case "views":
			p.Views = m.Value
		case "videos":
			p.Videos = m.Value
		}
	}
	return p
}

// handleAccountContent returns a paginated list of content items
// (videos, posts) for a connected account. The provider must implement
// AccountContentProvider. Supports ?cursor and ?query.limit parameters.
func (r *Router) handleAccountContent(w http.ResponseWriter, req *http.Request) {
	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, _, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}

	contentProvider, ok := r.capabilities.AccountContent(account.Platform)
	if !ok {
		writeError(w, http.StatusBadRequest, "platform "+account.Platform+" does not support account content")
		return
	}

	// Retrieve the access token from the vault.
	token, err := r.vault.Get(req.Context(), account.ID, models.TokenTypeBearer)
	if err != nil {
		token, err = r.vault.Get(req.Context(), account.ID, models.TokenTypeLongLived)
		if err != nil {
			token, err = r.vault.Get(req.Context(), account.ID, models.TokenTypeShortLived)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "no valid token found for this account")
				return
			}
		}
	}

	cursor := req.URL.Query().Get("cursor")
	limit := 20
	if l := req.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}

	page, err := contentProvider.ListAccountContent(req.Context(), token.AccessToken, account.PlatformUserID, cursor, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list account content: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, page)
}
