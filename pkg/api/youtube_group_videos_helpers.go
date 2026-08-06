package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// collectDescendantGroups returns the immediate + transitive children
// of rootGroupID (rootGroupID itself is NOT included — the caller
// already appends it). Cycle protection: a `visited` set prevents an
// accidental parent_group_id loop (defence-in-depth on top of the
// GroupRepository's wouldCreateCycle check at write time).
//
// Algorithm: BFS from rootGroupID over a parent-group-by-id index
// built in O(N) over the workspace's group list. Memory: O(N) index
// + O(depth) queue.
func collectDescendantGroups(all []models.Group, rootGroupID int64) []*models.Group {
	byParent := make(map[int64][]int64)
	byID := make(map[int64]models.Group, len(all))
	for _, g := range all {
		byID[g.ID] = g
		if g.ParentGroupID != nil {
			byParent[*g.ParentGroupID] = append(byParent[*g.ParentGroupID], g.ID)
		}
	}
	visited := map[int64]bool{rootGroupID: true}
	queue := []int64{rootGroupID}
	out := make([]*models.Group, 0)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range byParent[cur] {
			if visited[child] {
				continue
			}
			visited[child] = true
			if g, ok := byID[child]; ok {
				out = append(out, &g)
			}
			queue = append(queue, child)
		}
	}
	return out
}

type groupVideosCursor struct {
	Version   int    `json:"v"`
	Context   string `json:"c"`
	AccountID int64  `json:"a"`
	VideoID   string `json:"i"`
}

func encodeGroupVideosCursor(context string, accountID int64, videoID string) string {
	payload, _ := json.Marshal(groupVideosCursor{Version: 1, Context: context, AccountID: accountID, VideoID: videoID})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeGroupVideosCursor(raw, context string) (int64, string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return 0, "", fmt.Errorf("invalid list cursor: encoding")
	}
	var cursor groupVideosCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.Version != 1 || cursor.Context != context || cursor.AccountID <= 0 || strings.TrimSpace(cursor.VideoID) == "" {
		return 0, "", fmt.Errorf("invalid list cursor: malformed token or filter scope")
	}
	return cursor.AccountID, cursor.VideoID, nil
}

func parseGroupVideosPagination(req *http.Request, cfg YouTubeGroupVideosConfig) (int, int, error) {
	q := req.URL.Query()
	offset := 0
	if raw := strings.TrimSpace(q.Get("offset")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return 0, 0, fmt.Errorf("offset must be a non-negative integer")
		}
		offset = value
	}
	limit := cfg.DefaultPageSize
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return 0, 0, fmt.Errorf("limit must be a positive integer")
		}
		limit = value
	}
	if limit > cfg.MaxVideos {
		limit = cfg.MaxVideos
	}
	return offset, limit, nil
}

func parseGroupVideosRecency(req *http.Request) (int, error) {
	days := strings.TrimSpace(req.URL.Query().Get("days"))
	if days == "" {
		return 90, nil
	}
	value, err := strconv.Atoi(days)
	if err != nil || (value != 7 && value != 14 && value != 28 && value != 90) {
		return 0, fmt.Errorf("days must be one of 7, 14, 28, or 90")
	}
	return value, nil
}

func filterRecentYouTubeVideos(items []models.YouTubeVideoDetails, days int) []models.YouTubeVideoDetails {
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	filtered := make([]models.YouTubeVideoDetails, 0, len(items))
	for _, item := range items {
		if item.PublishedAt == nil || !item.PublishedAt.Before(cutoff) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func isInvalidYouTubeTokenError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, credentials.ErrInvalidGrant) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"status 401", "token expired", "token revoked"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// resolveGroupYouTubeAccounts runs phases 1-4 of the group videos
// handler: group lookup + workspace ownership (collapsed 404),
// optional subgroup traversal, per-account resolution, YouTube-only
// filtering, and the MaxAccounts cap. done=true means a response was
// already written (error OR the legitimate empty-group 200).
func (r *Router) resolveGroupYouTubeAccounts(
	w http.ResponseWriter,
	userID, groupID int64,
	includeSubgroups bool,
	cfg YouTubeGroupVideosConfig,
) (group *models.Group, accountLookup map[int64]groupAccountEntry, done bool) {
	// 1. Group lookup + workspace ownership check
	// (combined: unknown group OR transitively foreign group both
	// collapse into the same 404 to avoid an enumeration oracle).
	group, err := r.groupStore.FindByID(groupID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find group: "+err.Error())
		return nil, nil, true
	}
	if group == nil {
		writeError(w, http.StatusNotFound, "group not found")
		return nil, nil, true
	}
	workspace, err := r.workspaceStore.FindByID(group.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return nil, nil, true
	}
	if workspace == nil || !r.userCanAccessWorkspace(userID, workspace) {
		writeError(w, http.StatusNotFound, "group not found")
		return nil, nil, true
	}

	// 2. Collect the set of groups to draw accounts from.
	// includeSubgroups=true traverses the parent_group_id tree in
	// O(N) by indexing the workspace groups by parent_id (we already
	// load the flat list anyway for unrelated callers' throwaway
	// work, so reuse the same round-trip here).
	groupsInScope := []*models.Group{group}
	if includeSubgroups {
		allGroups, err := r.groupStore.ListByWorkspace(group.WorkspaceID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list groups: "+err.Error())
			return nil, nil, true
		}
		descendants := collectDescendantGroups(allGroups, group.ID)
		for _, g := range descendants {
			groupsInScope = append(groupsInScope, g)
		}
	}

	// 3. Aggregate the distinct account_ids across groupsInScope AND
	// record the originating group per account. We cap the per-group
	// lookup at the FIRST occurrence so a dup across direct +
	// sub-group resolves to the "direct" origin (matches the SPA's
	// primary-card-in-folder rendering intent).
	accountLookup = make(map[int64]groupAccountEntry)
	for _, g := range groupsInScope {
		aids, err := r.groupStore.ListAccountsInGroup(g.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError,
				fmt.Sprintf("list accounts in group %d: %s", g.ID, err.Error()))
			return nil, nil, true
		}
		for _, aid := range aids {
			if _, seen := accountLookup[aid]; seen {
				continue
			}
			accountLookup[aid] = groupAccountEntry{
				account: nil, // resolved in step 4
			}
		}
	}
	if len(accountLookup) == 0 {
		writeJSON(w, http.StatusOK, groupYouTubeVideosResponse{
			Videos:  []groupYouTubeVideoEntry{},
			Summary: groupYouTubeVideosSummary{},
		})
		return nil, nil, true
	}

	// 4. Resolve each account_id to *PlatformAccount and filter to
	// YouTube only. We MATERIALISE the resolver result here so an
	// account that turned out to be non-YouTube gets discarded entirely
	// without leaving a no-op entry in accountLookup.
	for aid, entry := range accountLookup {
		acc, err := r.userRepo.FindPlatformAccountByID(aid)
		if err != nil || acc == nil {
			delete(accountLookup, aid)
			continue
		}
		if acc.Platform != models.PlatformYouTube {
			delete(accountLookup, aid)
			continue
		}
		entry.account = acc
		accountLookup[aid] = entry
	}
	if len(accountLookup) == 0 {
		writeJSON(w, http.StatusOK, groupYouTubeVideosResponse{
			Videos:  []groupYouTubeVideoEntry{},
			Summary: groupYouTubeVideosSummary{},
		})
		return nil, nil, true
	}
	if len(accountLookup) > cfg.MaxAccounts {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"group resolves to %d accounts (max %d) — narrow the group or split subfolders",
			len(accountLookup), cfg.MaxAccounts,
		))
		return nil, nil, true
	}
	return group, accountLookup, false
}

// writeGroupVideosOK aggregates the fan-out results (phase 7), runs
// the phantom pass (7.5), applies the pagination window, and writes
// the terminal 200/502 response. Always returns false (the handler's
// tail-call convention: nothing left to do after this).
func (r *Router) writeGroupVideosOK(
	w http.ResponseWriter,
	req *http.Request,
	resultsByAccount map[int64]groupFetchResult,
	accountIDs []int64,
	accountLookup map[int64]groupAccountEntry,
	sessions []*models.YouTubeVideoEdit,
	sessionMap map[string]*models.YouTubeVideoEdit,
	recencyDays int,
	cfg YouTubeGroupVideosConfig,
	offset, limit int,
	cursorMode bool,
	cursorContext string,
	cursorAccountID int64,
	cursorVideoID string,
) bool {
	// 7. Aggregate. Each YouTube row joins with the existing session
	// map by (account_id, youtube_video_id) — O(1) per row.
	entries := make([]groupYouTubeVideoEntry, 0, 64)
	// emittedKeys tracks (account_id, youtube_video_id) tuples
	// that the fan-out already emitted, so the phantom pass below
	// does not double-emit when a race surfaces a public video in
	// both lists (very rare; happens when the privacy flip lands
	// AFTER our ListEditableVideos call but before we finish the
	// aggregation).
	emittedKeys := make(map[string]struct{}, 64)
	warnings := make([]string, 0)
	invalidTokenAccounts := make([]int64, 0)
	accountsWithVideos := 0
	for _, aid := range accountIDs {
		res := resultsByAccount[aid]
		if res.warning != "" {
			warnings = append(warnings, res.warning)
			if res.invalidToken {
				invalidTokenAccounts = append(invalidTokenAccounts, res.accountID)
				if markErr := r.userRepo.MarkReauthRequired(req.Context(), res.accountID, "youtube_token_invalid", res.warning); markErr != nil {
					// Reauth marking is deliberately best-effort: a transient DB
					// failure must not hide the upstream token diagnosis.
				}
			}
			continue
		}
		res.items = filterRecentYouTubeVideos(res.items, recencyDays)
		if len(res.items) > 0 {
			accountsWithVideos++
		}
		lookup := accountLookup[res.accountID]
		chName := strings.TrimSpace(lookup.account.Username)
		if chName == "" {
			chName = lookup.account.PlatformUserID
		}
		// Propagate the account's configured language to every video row.
		// The Groups UI uses this value for the flag; an empty value makes
		// the frontend fall back to English for every channel.
		language := accountLanguage(lookup.account)
		for _, v := range res.items {
			unconfirmed := "unconfirmed"
			entry := groupYouTubeVideoEntry{
				YouTubeVideoID:    v.ID,
				Title:             v.Title,
				Description:       v.Description,
				ThumbnailURL:      v.ThumbnailURL,
				PrivacyStatus:     v.Privacy,
				ProcessingStatus:  v.UploadStatus,
				PublishedAt:       v.PublishedAt,
				PlatformAccountID: res.accountID,
				ChannelName:       chName,
				Language:          language,
				EditorStatus:      "ready",
				YouTubeSyncStatus: &unconfirmed,
			}
			emittedKeys[groupSessionKey(res.accountID, v.ID)] = struct{}{}
			if s, ok := sessionMap[groupSessionKey(res.accountID, v.ID)]; ok {
				sid := s.ID
				entry.EditorSessionID = &sid
				vid := s.VeloxProjectID
				entry.VeloxProjectID = &vid
				u := r.editorURLForProject(s.VeloxProjectID)
				entry.EditorURL = &u
				entry.EditorStatus = s.Status
				entry.DesiredPrivacy = s.DesiredPrivacy
				entry.PublishAt = s.PublishAt
				entry.DraftDescription = s.DraftDescription
				// ActualPrivacy is exclusively the value read back from
				// YouTube. DesiredPrivacy must never be copied here:
				// doing so would make a newly-opened or pending session
				// look confirmed before YouTube has been queried.
				if s.ActualPrivacy != nil {
					entry.ActualPrivacy = s.ActualPrivacy
				}
				if s.YouTubeSyncStatus != nil {
					entry.YouTubeSyncStatus = s.YouTubeSyncStatus
				}
			}
			entries = append(entries, entry)
		}
	}

	// 7.5. Phantom emission for published sessions that vanished from
	// the editable listing (privacy flipped to public).
	entries = r.appendPhantomGroupEntries(entries, sessions, accountLookup, emittedKeys)

	// Apply the aggregate page after the account fan-out. Authorization
	// and YouTube reads therefore happen before slicing, while callers
	// receive a bounded response independent of group size.
	lenEntriesBeforeCap := len(entries)
	if cursorMode {
		// Cursor pages use a deterministic tuple order. This avoids
		// encoding a fragile array offset while retaining the legacy
		// per-account/provider order for offset callers.
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].PlatformAccountID != entries[j].PlatformAccountID {
				return entries[i].PlatformAccountID < entries[j].PlatformAccountID
			}
			return entries[i].YouTubeVideoID < entries[j].YouTubeVideoID
		})
	}
	totalVideos := lenEntriesBeforeCap
	if totalVideos > cfg.MaxVideos {
		totalVideos = cfg.MaxVideos
		entries = entries[:cfg.MaxVideos]
	}
	if cursorMode {
		offset = 0
		for i, entry := range entries {
			if entry.PlatformAccountID > cursorAccountID || (entry.PlatformAccountID == cursorAccountID && entry.YouTubeVideoID > cursorVideoID) {
				offset = i
				break
			}
			offset = len(entries)
		}
	} else if offset > totalVideos {
		offset = totalVideos
	}
	end := offset + limit
	if end > totalVideos {
		end = totalVideos
	}
	pagedEntries := entries[offset:end]
	hasMore := end < totalVideos
	resp := groupYouTubeVideosResponse{
		Videos: pagedEntries,
		Summary: groupYouTubeVideosSummary{
			TotalVideos:          totalVideos,
			Truncated:            len(entries) < lenEntriesBeforeCap,
			Accounts:             len(accountLookup),
			AccountsWithVideos:   accountsWithVideos,
			FailedAccounts:       len(warnings),
			InvalidTokenAccounts: invalidTokenAccounts,
		},
		HasMore: hasMore,
	}
	if hasMore {
		if cursorMode {
			last := pagedEntries[len(pagedEntries)-1]
			resp.NextCursor = encodeGroupVideosCursor(cursorContext, last.PlatformAccountID, last.YouTubeVideoID)
		} else {
			resp.NextOffset = end
		}
	}
	if len(warnings) == len(accountLookup) && len(accountLookup) > 0 {
		// Every per-account fetch failed → propagate as 502, while
		// retaining the structured summary so the UI can distinguish
		// accounts requiring reconnection from a general outage.
		resp.Error = "youtube list failed for every account in the group"
		resp.Warnings = warnings
		// The SPA swallows the response body into a generic "YouTube non
		// risponde temporaneamente" toast, so the per-account reasons
		// (quota, token, transport) are only observable here. Log them so
		// a total-failure episode is diagnosable from the API logs alone.
		slog.Warn("group youtube videos: every account failed (502)",
			"group_id", chi.URLParam(req, "group_id"),
			"total_accounts", len(accountLookup),
			"invalid_token_accounts", invalidTokenAccounts,
			"warnings", warnings,
			"path", req.URL.Path,
		)
		writeJSON(w, http.StatusBadGateway, resp)
		return false
	}
	if len(warnings) > 0 {
		resp.Warnings = warnings
	}
	writeJSON(w, http.StatusOK, resp)
	return false
}
