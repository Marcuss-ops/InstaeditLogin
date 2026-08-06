package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// accountSnapshotMaxAge is the freshness window for cached account
// resource snapshots. Shared by the list endpoint (batch staleness stamp)
// and the detail endpoint (on-demand refresh decision).
const accountSnapshotMaxAge = 10 * time.Minute

// AccountState is the stable lifecycle vocabulary exposed to clients.
// Status remains in the response for backward compatibility; clients that
// need to decide whether publishing is safe should use AccountState and
// IsPublishable instead of interpreting provider-specific status strings.
type AccountState string

const (
	AccountStateValid             AccountState = "valid"
	AccountStateReconnectRequired AccountState = "reconnect_required"
	AccountStateSuspended         AccountState = "suspended"
	AccountStateDeleted           AccountState = "deleted"
)

// classifyAccountStatus normalizes the persisted lifecycle values into the
// four states the UI needs. The deleted aliases cover old rows and the
// existing soft-disconnect path without changing stored data.
func classifyAccountStatus(status string) (AccountState, bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case models.AccountStatusActive, "connected":
		return AccountStateValid, true
	case models.AccountStatusSuspended:
		return AccountStateSuspended, false
	case models.AccountStatusDisconnected, models.AccountStatusRevoked, "deleted", "cancelled", "canceled":
		return AccountStateDeleted, false
	case models.AccountStatusExpired, models.AccountStatusReauthRequired, models.AccountStatusPendingAuthorization, models.AccountStatusError, "":
		return AccountStateReconnectRequired, false
	default:
		// Unknown lifecycle values are fail-closed: they need attention
		// before they can be used as a publishing target.
		return AccountStateReconnectRequired, false
	}
}

// accountListItem is the wire shape returned by account read endpoints.
// We deliberately do NOT return PlatformAccount directly because it leaks
// internal ownership, error and metadata columns.
type accountListItem struct {
	ID             int64        `json:"id"`
	Platform       string       `json:"platform"`
	PlatformUserID string       `json:"platform_user_id"`
	Username       string       `json:"username"`
	AvatarURL      string       `json:"avatar_url,omitempty"`
	Language       string       `json:"language,omitempty"`
	Status         string       `json:"status"`
	AccountState   AccountState `json:"account_state"`
	IsPublishable  bool         `json:"is_publishable"`
	// SnapshotStale reports whether the cached remote resource snapshot
	// is missing or older than accountSnapshotMaxAge. Set by the
	// aggregated list endpoint via one batched query; the UI uses it to
	// render fresh/refreshable badges without fetching per-account
	// details.
	SnapshotStale    bool       `json:"snapshot_stale"`
	ReauthRequiredAt *time.Time `json:"reauth_required_at,omitempty"`
	LastErrorCode    string     `json:"last_error_code,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

func accountListItemFromAccount(account *models.PlatformAccount) accountListItem {
	if account == nil {
		return accountListItem{}
	}
	state, publishable := classifyAccountStatus(account.Status)
	return accountListItem{
		ID:               account.ID,
		Platform:         models.NormalizePlatformIdentifier(account.Platform),
		PlatformUserID:   account.PlatformUserID,
		Username:         account.Username,
		AvatarURL:        avatarURLFromMetadata(account),
		Language:         accountLanguage(account),
		Status:           account.Status,
		AccountState:     state,
		IsPublishable:    publishable,
		ReauthRequiredAt: account.ReauthRequiredAt,
		LastErrorCode:    safeAccountErrorCode(account),
		CreatedAt:        account.CreatedAt,
	}
}

// accountLanguage exposes the user-editable language preference through a
// dedicated response field. The rest of the provider metadata stays private.
func safeAccountErrorCode(account *models.PlatformAccount) string {
	if account == nil || account.Status != models.AccountStatusReauthRequired {
		return ""
	}
	switch account.LastErrorCode {
	case "SHARED_GRANT_REAUTH_REQUIRED", "youtube_channel_mismatch":
		return account.LastErrorCode
	default:
		return ""
	}
}

func accountLanguage(account *models.PlatformAccount) string {
	if account == nil || account.Metadata == nil {
		return ""
	}
	language, ok := account.Metadata["language"].(string)
	if !ok || strings.TrimSpace(language) == "" {
		return ""
	}
	return strings.TrimSpace(language)
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
// P0 (account-lifecycle audit): accounts classified as
// account_state="deleted" (status disconnected / revoked / legacy
// deleted aliases) are hidden by default so a soft-disconnected
// channel stops surfacing in every app view. Pass
// ?include_deleted=true to include them — needed by audit / admin
// surfaces and reconnect flows that require the full history.
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

	includeDeleted := false
	switch strings.ToLower(req.URL.Query().Get("include_deleted")) {
	case "true", "1", "yes":
		includeDeleted = true
	}
	limit, rawCursor, err := parseListPageWithBounds(req.URL.Query(), 100, 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cursorContext := "active"
	if includeDeleted {
		cursorContext = "all"
	}
	cursorTime, cursorID, cursorNull, err := decodeListCursorDetails(rawCursor, "accounts", cursorContext)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if cursorNull {
		writeError(w, http.StatusBadRequest, "invalid list cursor: account cursor timestamp is required")
		return
	}
	var accountRows []*repository.AccountWithSnapshot
	hasMore := false
	if paged, ok := r.userRepo.(interface {
		ListPlatformAccountsWithSnapshotsByUserPage(context.Context, int64, string, bool, *time.Time, int64, int) ([]*repository.AccountWithSnapshot, bool, error)
	}); ok {
		var afterTime *time.Time
		var afterID int64
		if rawCursor != "" {
			afterTime = &cursorTime
			var scanErr error
			afterID, scanErr = strconv.ParseInt(cursorID, 10, 64)
			if scanErr != nil || afterID <= 0 {
				writeError(w, http.StatusBadRequest, "invalid list cursor")
				return
			}
		}
		accountRows, hasMore, err = paged.ListPlatformAccountsWithSnapshotsByUserPage(req.Context(), id.UserID(), "", includeDeleted, afterTime, afterID, limit)
	} else {
		// Compatibility for lightweight test stores and older injected implementations.
		// They cannot seek in SQL; reject a continuation rather than silently
		// returning page one again.
		if rawCursor != "" {
			writeError(w, http.StatusNotImplemented, "cursor pagination is not supported by this account store")
			return
		}
		var all []*repository.AccountWithSnapshot
		all, err = r.userRepo.ListPlatformAccountsWithSnapshotsByUser(id.UserID(), "")
		if err == nil && !includeDeleted {
			visible := all[:0]
			for _, row := range all {
				if state, _ := classifyAccountStatus(row.Account.Status); state != AccountStateDeleted {
					visible = append(visible, row)
				}
			}
			all = visible
		}
		if len(all) > limit {
			hasMore = true
			all = all[:limit]
		}
		accountRows = all
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list accounts: "+err.Error())
		return
	}
	items := make([]accountListItem, 0, len(accountRows))
	staleAccountIDs := make([]int64, 0, len(accountRows))
	for _, row := range accountRows {
		item := accountListItemFromAccount(row.Account)
		if item.AccountState == AccountStateDeleted && !includeDeleted {
			continue
		}
		// Aggregated N+1 fix: the snapshot data arrived in the SAME
		// LEFT JOIN query (repository.AccountWithSnapshot). avatar_url
		// falls back to the cached snapshot profile when the account
		// metadata has none; snapshot_stale is stamped here. The page
		// load runs exactly ONE SQL query — no per-account reads, no
		// Vault access, no provider (YouTube) calls.
		if row.Snapshot != nil {
			if item.AvatarURL == "" {
				if v, ok := row.Snapshot.Profile["avatar_url"].(string); ok {
					item.AvatarURL = v
				}
			}
			item.SnapshotStale = time.Since(row.Snapshot.FetchedAt) > repository.SnapshotFreshnessTTL(item.ID, accountSnapshotMaxAge)
		} else {
			item.SnapshotStale = true
		}
		if item.SnapshotStale && item.AccountState != AccountStateReconnectRequired {
			staleAccountIDs = append(staleAccountIDs, item.ID)
		}
		items = append(items, item)
	}
	if len(staleAccountIDs) > 0 {
		if batcher, ok := r.snapshotStore.(interface {
			MarkSnapshotsRefreshPending([]int64, time.Time) error
		}); ok {
			// One batched local write makes missing/stale rows visible to
			// the worker without introducing a per-account SQL fan-out.
			_ = batcher.MarkSnapshotsRefreshPending(staleAccountIDs, time.Now())
		}
	}

	response := map[string]interface{}{"accounts": items, "has_more": hasMore}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		response["next_cursor"] = encodeListCursorForContext("accounts", cursorContext, last.CreatedAt, strconv.FormatInt(last.ID, 10))
	}
	writeJSON(w, http.StatusOK, response)
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
	// Normalize once at the shared account boundary so every handler that
	// reuses this helper dispatches capabilities and emits JSON with the
	// canonical provider identifier, including legacy `platform='x'` rows.
	account.Platform = models.NormalizePlatformIdentifier(account.Platform)
	return account, identity, true
}

// handleGetAccount returns a single platform account owned by the
// authenticated user. The response always carries the base account shape
// plus, when a cached resource snapshot exists, a "resource" field with
// rich details (metrics, branding, stats). STRICT RULE: this handler
// NEVER calls the provider (YouTube) — opening a channel page only reads
// PostgreSQL. Stale snapshots are served from cache and recorded as
// refresh_pending for the background worker; explicit refreshes live on
// POST /accounts/{id}/sync (handleSyncAccount).
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
		accountListItem: accountListItemFromAccount(account),
	}

	// Shortcut: no snapshot store wired → return base account without resource.
	if r.snapshotStore == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Serve from the cache ONLY. A fresh snapshot (< accountSnapshotMaxAge)
	// is returned as-is; a stale or missing one is served as a cached
	// fallback and flagged refresh_pending so the background worker
	// refreshes it asynchronously — never a synchronous provider call.
	stale, err := r.snapshotStore.IsSnapshotStale(account.ID, repository.SnapshotFreshnessTTL(account.ID, accountSnapshotMaxAge))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "snapshot freshness check failed: "+err.Error())
		return
	}
	resp.SnapshotStale = stale

	if stale && resp.AccountState != AccountStateReconnectRequired {
		// STRICT RULE: opening a channel page must never call the provider
		// (YouTube). Serve the cached value immediately and record
		// refresh_pending so the background worker refreshes the snapshot
		// asynchronously. Explicit refreshes stay on
		// POST /accounts/{id}/sync (handleSyncAccount).
		_ = r.snapshotStore.MarkSnapshotRefreshPending(account.ID, time.Now())
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
	return mergeEarningsIntoPoint(metrics, nil)
}

// mergeEarningsIntoPoint builds a repository point from public metrics
// and optionally copies over monetary fields from an separate earnings
// point (used when analytics data is fetched separately).
func mergeEarningsIntoPoint(metrics []models.AccountMetric, earnings *repository.AccountMetricPoint) repository.AccountMetricPoint {
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
	if earnings != nil {
		p.RevenueCents = earnings.RevenueCents
		p.RPMCents = earnings.RPMCents
		p.CPMCents = earnings.CPMCents
	}
	return p
}

// storeYouTubeEarnings fetches the last 30 days of YouTube Analytics
// earnings for a monetized channel and stores the monetary metrics.
// It is best-effort: non-monetized channels, missing scopes, or API
// errors are logged and skipped so they never break the sync flow.
func (r *Router) storeYouTubeEarnings(ctx context.Context, account *models.PlatformAccount, accessToken string) {
	if r.youTubeSvc == nil || r.metricHistoryStore == nil {
		return
	}
	if account.Platform != models.PlatformYouTube {
		return
	}

	// LEGACY PRESERVE (Blocco Bug #3 — YT OAuth scope cleanup): new
	// OAuth grants no longer request yt-analytics-monetary.readonly,
	// so HasMonetary is naturally false for new tokens and the
	// earnings sync correctly no-ops. Tokens issued before the
	// canonical scope cleanup may still carry the grant; we continue
	// to honour them. Removing this gate would re-introduce
	// revenue/RPM/CPM fetches that would fail-permission for every
	// new user.
	info, err := r.youTubeSvc.GetTokenInfo(ctx, accessToken)
	if err != nil || !info.HasMonetary {
		return
	}

	points, err := r.youTubeSvc.FetchEarnings(ctx, accessToken, account.PlatformUserID, 30)
	if err != nil {
		slog.Debug("youtube earnings fetch skipped", "account_id", account.ID, "reason", err.Error())
		return
	}

	for _, p := range points {
		if err := r.metricHistoryStore.UpsertMonetary(account.ID, p.Date, p); err != nil {
			slog.Warn("failed to upsert monetary metrics", "account_id", account.ID, "date", p.Date, "error", err)
		}
	}
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
				// The InstaEdit session is valid; only the provider credential
				// for this account is missing or expired. Do not return 401,
				// otherwise the SPA treats a YouTube token problem as a global
				// logout and sends the user back to /login.
				writeError(w, http.StatusFailedDependency, "no valid token found for this account")
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
	privacy := req.URL.Query().Get("privacy")
	if privacy != "" && privacy != "private" && privacy != "public" && privacy != "unlisted" {
		writeError(w, http.StatusBadRequest, "invalid privacy value: must be one of private, public, unlisted")
		return
	}

	page, err := contentProvider.ListAccountContent(req.Context(), token.AccessToken, account.PlatformUserID, cursor, limit, privacy)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list account content: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, page)
}
