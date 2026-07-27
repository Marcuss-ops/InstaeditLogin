package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// groupYouTubeVideosFanoutConcurrency bounds the number of concurrent
// YouTube `channels.list → playlistItems.list → videos.list` chains
// performed per request. Empirically YouTube tolerates ~10
// simultaneous requests per access token, but the dark-editor dashboard
// renders this endpoint on every click — keeping the fan-out at 4
// keeps the upstream pressure at a fraction of the quota while still
// aggregating a group with ~16 channels well under the 5s SLA.
const groupYouTubeVideosFanoutConcurrency = 4

// groupYouTubeVideosPerAccountTimeout caps each per-account YouTube
// fetch so a single slow/stuck channel does not stall the whole
// response (the handler is on the dashboard refresh path).
const groupYouTubeVideosPerAccountTimeout = 15 * time.Second

// groupYouTubeVideosMaxAccounts caps the number of distinct accounts
// a single group request can fan-out against. Defense-in-depth: a
// hostile / misconfigured caller cannot trigger 500+ YouTube calls
// in one click. Adjustable via future config; sized to the largest
// observed customer group (≈80 channels).
const groupYouTubeVideosMaxAccounts = 200

// groupYouTubeVideosMaxTotalVideos caps the aggregated response size
// across all channels. The SPA renders the result as a card grid;
// more than 500 cards exceeds the first-paint budget so the cap
// truncates the response with the most-recent per-channel order
// preserved (YouTube's ListEditableVideos returns uploads-playlist
// order = newest first per channel).
const groupYouTubeVideosMaxTotalVideos = 500

// groupYouTubeVideoEntry is the per-row JSON shape returned by GET
// /api/v1/groups/{group_id}/youtube/videos. The shape mirrors
// models.YouTubeVideoDetails (from YouTube list) joined with the
// existing per-video editor_session row (if any). Fields are
// optional/omitempty so the same DTO can carry:
//   - "freshly discovered" videos (no editor_session yet) — all
//     editor_* fields omitted;
//   - "in editing" videos (session.status='editing' or 'failed');
//   - "published" videos (session.status='published').
//
// youtube_sync_status is the YouTube-side read projection the SPA
// uses to colour the privacy badge — for now it's always
// "unconfirmed" until the reconciler-side sync lands. The DTO
// already carries the field so the SPA implements the badge logic
// once and the reconciler can flip the value remotely.
//
// PLACEHOLDER FIELDS: actual_privacy and youtube_sync_status are
// placeholders today (mirror desired_privacy + always "unconfirmed").
// Two follow-up commits flip them into live values:
//   1. The reconciler (P0#7) updates actual_privacy + sync_status
//      from YouTube videos.list after a publish round-trip;
//   2. The publish endpoint (P0#7) stamps actual_privacy
//      synchronously instead of mirroring desired_privacy.
// Until those land, do NOT build UI logic that trusts these fields
// — the verdict explicitly calls them out as placeholders.
type groupYouTubeVideoEntry struct {
	YouTubeVideoID    string `json:"youtube_video_id"`
	Title             string `json:"title"`
	ThumbnailURL      string `json:"thumbnail_url"`
	PrivacyStatus     string `json:"privacy_status"`
	ProcessingStatus  string `json:"processing_status"`
	PlatformAccountID int64  `json:"platform_account_id"`
	ChannelName       string `json:"channel_name"`
	// Editor session fields. All three are omitted when no
	// youtube_video_edits row exists yet for this (account, video)
	// tuple — that means the user hasn't opened the editor yet and
	// the SPA will route the click to POST /editor-sessions.
	EditorSessionID *string `json:"editor_session_id,omitempty"`
	VeloxProjectID  *string `json:"velox_project_id,omitempty"`
	EditorURL       *string `json:"editor_url,omitempty"`
	// EditorStatus: "editing" | "failed" | "publishing" | "published"
	// when a session exists, else "ready" (no session yet).
	EditorStatus string `json:"editor_status"`
	// DesiredPrivacy: what the operator chose on the editor's "Pubblica"
	// panel (publish flow will use it). Empty when no session exists.
	DesiredPrivacy string `json:"desired_privacy,omitempty"`
	// ActualPrivacy: YouTube's confirmed privacy right after our publish
	// call. Empty when no session exists OR when the publish flow
	// hasn't stamped it yet. For now this field mirrors
	// DesiredPrivacy on existing sessions — the reconciler of a
	// follow-up commit will keep it strictly in sync with YouTube's
	// videos.list read.
	ActualPrivacy string `json:"actual_privacy,omitempty"`
	// YouTubeSyncStatus: "unconfirmed" until the reconciler of a
	// follow-up commit confirms the privacy via videos.list. Always
	// "unconfirmed" today (the SPA uses it as a UI hint).
	YouTubeSyncStatus string `json:"youtube_sync_status,omitempty"`
}

// groupYouTubeVideosResponse is the envelope. `videos: []` is
// returned (NOT 404) when no videos match — the SPA's card grid
// renders an empty-state banner rather than treating "nothing to do"
// as an error. `warnings: []` surfaces per-account fetch failures
// so the operator can debug stale-token issues from the UI without
// inspecting server logs.
type groupYouTubeVideosResponse struct {
	Videos   []groupYouTubeVideoEntry `json:"videos"`
	Warnings []string                 `json:"warnings,omitempty"`
}

// handleListGroupYouTubeVideos is the HTTP entry point for
// GET /api/v1/groups/{group_id}/youtube/videos. The SPA's group
// dashboard card grid calls this on load (and after each refresh).
//
// Behaviour:
//   - 401 when no JWT identity is on the context.
//   - 400 when {group_id} is not a positive integer, when
//     ?include_subgroups is not the literal "true"/"false",
//     OR when the resolved group has more than
//     groupYouTubeVideosMaxAccounts distinct accounts.
//   - 404 when the group is unknown OR the caller does not own
//     its workspace. Both branches return the SAME 404 + message
//     so a cross-tenant probe cannot distinguish "no such group"
//     from "group exists but not yours" (defence-in-depth on top
//     of the SQL workspace-ownership guard).
//   - 501 when groups are not configured on this server (the
//     GroupStore nil-guard mirrors the other feature-flag nil
//     pattern).
//   - 502 when YouTube returns an error for EVERY account in the
//     group (graceful degradation per-account: partial success is
//     200 + videos + warnings; total failure is 502 so the SPA
//     surfaces a hard "couldn't reach YouTube" toast).
//   - 200 + {"videos": [...]} in every other case, capped at
//     groupYouTubeVideosMaxTotalVideos rows ordered by
//     per-channel newest-first.
//
// Concurrency:
//   The fan-out uses a bounded semaphore (groupYouTubeVideosFanoutConcurrency)
//   so a group with N channels incurs at most N / 4 simultaneous
//   YouTube API chains, regardless of how many channels the operator
//   has piled into one folder. Each individual chain is wrapped in a
//   per-account timeout (groupYouTubeVideosPerAccountTimeout) so a
//   single slow channel does not stall the whole request.
func (r *Router) handleListGroupYouTubeVideos(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	groupIDRaw := strings.TrimSpace(chi.URLParam(req, "group_id"))
	groupID, err := parsePositiveQueryInt(groupIDRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "group_id path parameter must be a positive integer")
		return
	}

	// ?include_subgroups: opt-in to walk the subtree. Anything other
	// than the literal "true" is treated as false so a typo'd URL
	// ("yes", "1", "True" in capitals) still defaults to the
	// expected behaviour.
	includeSubgroups := strings.EqualFold(strings.TrimSpace(req.URL.Query().Get("include_subgroups")), "true")

	if r.groupStore == nil {
		writeError(w, http.StatusNotImplemented, "groups not configured on this server")
		return
	}
	if r.workspaceStore == nil {
		writeError(w, http.StatusServiceUnavailable, "workspace store not configured")
		return
	}
	if r.userRepo == nil {
		writeError(w, http.StatusServiceUnavailable, "user store not configured")
		return
	}

	// 1. Group lookup + workspace ownership check
	// (combined: unknown group OR transitively foreign group both
	// collapse into the same 404 to avoid an enumeration oracle).
	group, err := r.groupStore.FindByID(groupID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find group: "+err.Error())
		return
	}
	if group == nil {
		writeError(w, http.StatusNotFound, "group not found")
		return
	}
	workspace, err := r.workspaceStore.FindByID(group.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return
	}
	if workspace == nil || !r.userCanAccessWorkspace(identity.UserID(), workspace) {
		writeError(w, http.StatusNotFound, "group not found")
		return
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
			return
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
type accountEntry struct {
	account *models.PlatformAccount
}
accountLookup := make(map[int64]accountEntry)
for _, g := range groupsInScope {
	aids, err := r.groupStore.ListAccountsInGroup(g.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			fmt.Sprintf("list accounts in group %d: %s", g.ID, err.Error()))
		return
	}
	for _, aid := range aids {
		if _, seen := accountLookup[aid]; seen {
			continue
		}
		accountLookup[aid] = accountEntry{
			account: nil, // resolved in step 4
		}
	}
}
if len(accountLookup) == 0 {
	writeJSON(w, http.StatusOK, groupYouTubeVideosResponse{Videos: []groupYouTubeVideoEntry{}})
	return
}

// 4. Resolve each account_id to *PlatformAccount and filter to
// YouTube only. We MATERIALISE the resolver result here so an
// account that turned out to be non-YouTube gets discarded entirely
// without leaving a no-op entry in accountLookup.
accountIDs := make([]int64, 0, len(accountLookup))
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
	accountIDs = append(accountIDs, aid)
}
if len(accountLookup) == 0 {
	writeJSON(w, http.StatusOK, groupYouTubeVideosResponse{Videos: []groupYouTubeVideoEntry{}})
	return
}
if len(accountLookup) > groupYouTubeVideosMaxAccounts {
	writeError(w, http.StatusBadRequest, fmt.Sprintf(
		"group resolves to %d accounts (max %d) — narrow the group or split subfolders",
		len(accountLookup), groupYouTubeVideosMaxAccounts,
	))
	return
}
	if len(accountLookup) == 0 {
		writeJSON(w, http.StatusOK, groupYouTubeVideosResponse{Videos: []groupYouTubeVideoEntry{}})
		return
	}
	if len(accountLookup) > groupYouTubeVideosMaxAccounts {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"group resolves to %d accounts (max %d) — narrow the group or split subfolders",
			len(accountLookup), groupYouTubeVideosMaxAccounts,
		))
		return
	}

	// 5. Fetch existing editor sessions in a single SQL query.
	// The query is workspace-scoped + account-id-ANY so a hostile
	// caller cannot bypass the gate (the handler already verified
	// the workspace is theirs; this is defence-in-depth).
	accountIDs := make([]int64, 0, len(accountLookup))
	for aid := range accountLookup {
		accountIDs = append(accountIDs, aid)
	}
	var sessions []*models.YouTubeVideoEdit
	if r.youtubeVideoEditStore != nil {
		sessions, err = r.youtubeVideoEditStore.ListByWorkspaceAccountIDs(req.Context(), group.WorkspaceID, accountIDs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list editor sessions: "+err.Error())
			return
		}
	}
	sessionKey := func(accountID int64, videoID string) string {
		return fmt.Sprintf("%d|%s", accountID, videoID)
	}
	sessionMap := make(map[string]*models.YouTubeVideoEdit, len(sessions))
	for _, s := range sessions {
		sessionMap[sessionKey(s.PlatformAccountID, s.YouTubeVideoID)] = s
	}

	// 6. Fan-out: per-account YouTube listing. Bounded concurrency
	// via semaphore on a buffered channel; bounded per-account
	// timeout so a stuck channel doesn't stall the request.
	type fetchResult struct {
		accountID int64
		items     []models.YouTubeVideoDetails
		warning   string
	}
	sem := make(chan struct{}, groupYouTubeVideosFanoutConcurrency)
	results := make(chan fetchResult, len(accountLookup))
	var wg sync.WaitGroup
	for aid, entry := range accountLookup {
		wg.Add(1)
		sem <- struct{}{}
		go func(aid int64, acc *models.PlatformAccount) {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(req.Context(), groupYouTubeVideosPerAccountTimeout)
			defer cancel()
			items, ferr := r.fetchAccountEditableVideos(ctx, acc)
			if ferr != nil {
				results <- fetchResult{
					accountID: aid,
					warning:   fmt.Sprintf("account %d: %s", aid, ferr.Error()),
				}
				return
			}
			results <- fetchResult{accountID: aid, items: items}
		}(aid, entry.account)
	}
	wg.Wait()
	close(results)

	// 7. Aggregate. Each YouTube row joins with the existing session
	// map by (account_id, youtube_video_id) — O(1) per row.
	entries := make([]groupYouTubeVideoEntry, 0, 64)
	warnings := make([]string, 0)
	for res := range results {
		if res.warning != "" {
			warnings = append(warnings, res.warning)
			continue
		}
		lookup := accountLookup[res.accountID]
		chName := strings.TrimSpace(lookup.account.Username)
		if chName == "" {
			chName = lookup.account.PlatformUserID
		}
		for _, v := range res.items {
			entry := groupYouTubeVideoEntry{
				YouTubeVideoID:    v.ID,
				Title:             v.Title,
				ThumbnailURL:      v.ThumbnailURL,
				PrivacyStatus:     v.Privacy,
				ProcessingStatus:  v.UploadStatus,
				PlatformAccountID: res.accountID,
				ChannelName:       chName,
				EditorStatus:      "ready",
				YouTubeSyncStatus: "unconfirmed",
			}
			if s, ok := sessionMap[sessionKey(res.accountID, v.ID)]; ok {
				sid := s.ID
				entry.EditorSessionID = &sid
				vid := s.VeloxProjectID
				entry.VeloxProjectID = &vid
				u := r.editorURLForProject(s.VeloxProjectID)
				entry.EditorURL = &u
				entry.EditorStatus = s.Status
				entry.DesiredPrivacy = s.DesiredPrivacy
				entry.ActualPrivacy = s.DesiredPrivacy // placeholder; reconciler will overwrite once videos.list confirms
			}
			entries = append(entries, entry)
		}
	}
	// Hard cap on response size. The slice is already in
	// per-channel newest-first order (post-fan-out concatenation
	// preserves each channel's order), so the cap truncates the
	// tail conservatively.
	if len(entries) > groupYouTubeVideosMaxTotalVideos {
		entries = entries[:groupYouTubeVideosMaxTotalVideos]
	}

	resp := groupYouTubeVideosResponse{Videos: entries}
	if len(warnings) == len(accountLookup) && len(accountLookup) > 0 {
		// Every per-account fetch failed → propagate as 502 so the
		// SPA surfaces a hard error toast instead of an empty grid
		// (which the operator would otherwise mis-read as "no
		// videos left").
		writeError(w, http.StatusBadGateway,
			"youtube list failed for every account in the group ("+strings.Join(warnings, "; ")+")")
		return
	}
	if len(warnings) > 0 {
		resp.Warnings = warnings
	}
	writeJSON(w, http.StatusOK, resp)
}

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

// fetchAccountEditableVideos resolves a valid access token for the
// YouTube account (Bearer → LongLived → ShortLived fallback chain,
// identical to the editor-session create flow) and returns the
// first page of private/unlisted/processed videos. Error semantics:
// (nil, err) for any failure mode (no token / channel mismatches /
// transport) — the handler skips the account and surfaces the err
// in the warnings[] / 502 envelope.
func (r *Router) fetchAccountEditableVideos(ctx context.Context, acc *models.PlatformAccount) ([]models.YouTubeVideoDetails, error) {
	if r.vault == nil {
		return nil, fmt.Errorf("vault not configured")
	}
	if r.youTubeSvc == nil {
		return nil, fmt.Errorf("youtube service not configured")
	}
	token, err := r.vault.Get(ctx, acc.ID, models.TokenTypeBearer)
	if err != nil {
		token, err = r.vault.Get(ctx, acc.ID, models.TokenTypeLongLived)
		if err != nil {
			token, err = r.vault.Get(ctx, acc.ID, models.TokenTypeShortLived)
			if err != nil {
				return nil, fmt.Errorf("no valid token: %w", err)
			}
		}
	}
	page, err := r.youTubeSvc.ListEditableVideos(ctx, token.AccessToken, acc.PlatformUserID, "")
	if err != nil {
		return nil, fmt.Errorf("youtube list: %w", err)
	}
	return page.Items, nil
}
