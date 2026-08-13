package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// regionCodePattern mirrors YouTube's ISO 3166-1 alpha-2 requirement
// for videoCategories.list regionCode.
var regionCodePattern = regexp.MustCompile(`^[A-Za-z]{2}$`)

// handleListYouTubeVideoCategories is the HTTP entry point of
// GET /api/v1/youtube/video-categories — the centralized YouTube video
// categories resource shared by EVERY form with a category select (the
// group-video metadata drawer, the livestreams wizard, …).
//
// Behaviour:
//   - 401 without a JWT identity.
//   - 400 for a region_code that is not a two-letter ISO 3166-1
//     alpha-2 code (the parameter is optional; omitted = global
//     default region).
//   - 404 when the caller has no workspace with an ACTIVE YouTube
//     account to mint a token for.
//   - 502 when the token cannot be renewed or YouTube errors out
//     (transient upstream failure); 429 surfaces rate limits.
//   - 200 + { categories: [{ id, label }, …] } on success. Labels are
//     requested in Italian (hl=it) so the projection matches the
//     canonical snapshot the SPA serves as fallback.
//
// videoCategories.list is NOT channel-scoped: any valid OAuth token of
// a connected account serves the list, so the handler resolves the
// first active YouTube account bound to one of the caller's groups
// (first owned workspace first) and mints its bearer token from the
// vault — no group_id / platform_account_id parameter needed.
func (r *Router) handleListYouTubeVideoCategories(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	if r.youTubeSvc == nil || r.vault == nil || r.workspaceStore == nil || r.groupStore == nil || r.userRepo == nil {
		writeError(w, http.StatusServiceUnavailable, "youtube service not configured")
		return
	}

	regionCode := strings.TrimSpace(req.URL.Query().Get("region_code"))
	if regionCode != "" && !regionCodePattern.MatchString(regionCode) {
		writeError(w, http.StatusBadRequest, "region_code must be a two-letter ISO 3166-1 alpha-2 country code")
		return
	}

	account, err := r.firstWorkspaceYouTubeAccount(identity.UserID())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve a YouTube account")
		return
	}
	if account == nil {
		writeError(w, http.StatusNotFound, "nessun account YouTube collegato al workspace")
		return
	}

	token, err := r.vault.Renew(req.Context(), account.ID, models.TokenTypeBearer, r.youTubeSvc.RefreshOAuthToken)
	if err != nil {
		writeError(w, http.StatusBadGateway, "YouTube non risponde temporaneamente. Riprova tra poco.")
		return
	}

	categories, err := r.youTubeSvc.ListVideoCategories(req.Context(), token.AccessToken, regionCode)
	if err != nil {
		var apiErr *services.YouTubeAPIError
		if errors.As(err, &apiErr) {
			switch {
			case apiErr.StatusCode == http.StatusTooManyRequests:
				writeError(w, http.StatusTooManyRequests, "YouTube rate limit raggiunto. Riprova tra poco.")
				return
			case apiErr.StatusCode >= 500 || apiErr.StatusCode == 0:
				writeError(w, http.StatusBadGateway, "YouTube non risponde temporaneamente. Riprova tra poco.")
				return
			}
		}
		writeError(w, http.StatusBadGateway, "impossibile caricare le categorie YouTube")
		return
	}
	if categories == nil {
		categories = []services.YouTubeVideoCategory{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": categories})
}

// firstWorkspaceYouTubeAccount walks the caller's workspaces (first
// owned first) and returns the first ACTIVE YouTube account attached to
// one of their groups. Returns (nil, nil) when no account qualifies.
func (r *Router) firstWorkspaceYouTubeAccount(userID int64) (*models.PlatformAccount, error) {
	workspaces, err := r.workspaceStore.ListByOwner(userID)
	if err != nil {
		return nil, err
	}
	for _, workspace := range workspaces {
		if workspace.ID <= 0 {
			continue
		}
		groups, err := r.groupStore.ListByWorkspace(workspace.ID)
		if err != nil {
			return nil, err
		}
		for _, group := range groups {
			accountIDs, err := r.groupStore.ListAccountsInGroup(group.ID)
			if err != nil {
				return nil, err
			}
			for _, accountID := range accountIDs {
				account, err := r.userRepo.FindPlatformAccountByID(accountID)
				if err != nil {
					return nil, err
				}
				if account != nil && account.Platform == models.PlatformYouTube && account.Status == models.AccountStatusActive {
					return account, nil
				}
			}
		}
	}
	return nil, nil
}
