package api

import (
	"net/http"
	"strconv"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// publishingTargetsResponse is the JSON catalog consumed by publishing
// clients. It deliberately lives in InstaeditLogin, where workspace
// ownership, YouTube accounts and group membership are authoritative; Velox
// receives only the selected target in the job payload.
type publishingTargetsResponse struct {
	WorkspaceID int64                     `json:"workspace_id"`
	Channels    []publishingChannelOption `json:"channels"`
	Groups      []publishingGroupOption   `json:"groups"`
}

type publishingChannelOption struct {
	Type              string `json:"type"`
	PlatformAccountID int64  `json:"platform_account_id"`
	ChannelID         string `json:"channel_id"`
	ChannelName       string `json:"channel_name"`
	Status            string `json:"status"`
	AccountState      string `json:"account_state"`
	IsPublishable     bool   `json:"is_publishable"`
	Enabled           bool   `json:"enabled"`
}

type publishingGroupOption struct {
	Type              string   `json:"type"`
	GroupID           int64    `json:"group_id"`
	GroupName         string   `json:"group_name"`
	ChannelAccountIDs []int64  `json:"channel_account_ids"`
	ChannelIDs        []string `json:"channel_ids"`
}

// handleListPublishingTargets returns the YouTube channels and groups that
// can be selected for a post in one workspace-scoped JSON response.
//
// GET /api/v1/publishing/targets?workspace_id=123
// GET /api/v1/publishing/targets (uses the workspace in the auth identity)
func (r *Router) handleListPublishingTargets(w http.ResponseWriter, req *http.Request) {
	if r.workspaceStore == nil || r.groupStore == nil || r.userRepo == nil {
		writeError(w, http.StatusNotImplemented, "publishing target catalog is not configured")
		return
	}

	workspaceID := publishingWorkspaceID(req)
	if raw := req.URL.Query().Get("workspace_id"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid workspace_id")
			return
		}
		workspaceID = parsed
	}
	if workspaceID <= 0 {
		writeError(w, http.StatusBadRequest, "workspace_id query parameter is required")
		return
	}
	if ok, _ := r.requireWorkspaceOwnership(w, req, workspaceID); !ok {
		return
	}
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	bindings, err := r.workspaceStore.ListChannels(req.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workspace channels: "+err.Error())
		return
	}
	accounts, err := r.userRepo.ListFilteredYouTubeAccounts(identity.UserID(), &workspaceID, "", "", "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list YouTube channels: "+err.Error())
		return
	}

	byAccountID := make(map[int64]*models.PlatformAccount, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		byAccountID[account.ID] = account
	}
	channels := make([]publishingChannelOption, 0, len(bindings))
	availableAccountIDs := make(map[int64]struct{}, len(bindings))
	channelIDByAccountID := make(map[int64]string, len(bindings))
	for _, binding := range bindings {
		if !binding.Enabled {
			continue
		}
		account := byAccountID[binding.PlatformAccountID]
		if account == nil {
			continue
		}
		state, publishable := classifyAccountStatus(account.Status)
		if !publishable {
			continue
		}
		channelName := account.Username
		if channelName == "" {
			channelName = account.PlatformUserID
		}
		channels = append(channels, publishingChannelOption{
			Type:              "channel",
			PlatformAccountID: account.ID,
			ChannelID:         account.PlatformUserID,
			ChannelName:       channelName,
			Status:            account.Status,
			AccountState:      string(state),
			IsPublishable:     publishable,
			Enabled:           binding.Enabled,
		})
		availableAccountIDs[account.ID] = struct{}{}
		channelIDByAccountID[account.ID] = account.PlatformUserID
	}

	groupRows, err := r.groupStore.ListByWorkspaceWithAccounts(workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list YouTube groups: "+err.Error())
		return
	}
	groups := make([]publishingGroupOption, 0, len(groupRows))
	for _, row := range groupRows {
		accountIDs := make([]int64, 0, len(row.AccountIDs))
		channelIDs := make([]string, 0, len(row.AccountIDs))
		for _, accountID := range row.AccountIDs {
			if _, ok := availableAccountIDs[accountID]; !ok {
				continue
			}
			accountIDs = append(accountIDs, accountID)
			channelIDs = append(channelIDs, channelIDByAccountID[accountID])
		}
		if len(accountIDs) == 0 {
			continue
		}
		groups = append(groups, publishingGroupOption{
			Type:              "group",
			GroupID:           row.ID,
			GroupName:         row.Name,
			ChannelAccountIDs: accountIDs,
			ChannelIDs:        channelIDs,
		})
	}

	writeJSON(w, http.StatusOK, publishingTargetsResponse{
		WorkspaceID: workspaceID,
		Channels:    channels,
		Groups:      groups,
	})
}

func publishingWorkspaceID(req *http.Request) int64 {
	if identity := auth.IdentityFromContext(req.Context()); identity != nil {
		return identity.WorkspaceID()
	}
	return 0
}
