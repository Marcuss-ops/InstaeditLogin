package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

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
//
//	The fan-out uses a bounded semaphore (groupYouTubeVideosFanoutConcurrency)
//	so a group with N channels incurs at most N / 4 simultaneous
//	YouTube API chains, regardless of how many channels the operator
//	has piled into one folder. Each individual chain is wrapped in a
//	per-account timeout (groupYouTubeVideosPerAccountTimeout) so a
//	single slow channel does not stall the whole request.
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
	cfg := r.youtubeGroupVideosConfig.normalized()
	offset, limit, err := parseGroupVideosPagination(req, cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	recencyDays, err := parseGroupVideosRecency(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

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
		writeJSON(w, http.StatusOK, groupYouTubeVideosResponse{
			Videos:  []groupYouTubeVideoEntry{},
			Summary: groupYouTubeVideosSummary{},
		})
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
		writeJSON(w, http.StatusOK, groupYouTubeVideosResponse{
			Videos:  []groupYouTubeVideoEntry{},
			Summary: groupYouTubeVideosSummary{},
		})
		return
	}
	if len(accountLookup) > cfg.MaxAccounts {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"group resolves to %d accounts (max %d) — narrow the group or split subfolders",
			len(accountLookup), cfg.MaxAccounts,
		))
		return
	}

	// 5. Fetch existing editor sessions in a single SQL query.
	// The query is workspace-scoped + account-id-ANY so a hostile
	// caller cannot bypass the gate (the handler already verified
	// the workspace is theirs; this is defence-in-depth).
	accountIDs = make([]int64, 0, len(accountLookup))
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
		// ListByWorkspaceAccountIDs returns rows ORDER BY updated_at
		// DESC, so the FIRST occurrence in this loop is the newest.
		// Keep the newest; ignore older duplicates. (Without this
		// guard the map would end up with the OLDEST session for
		// any (account, video) tuple that was ever edited twice.)
		key := sessionKey(s.PlatformAccountID, s.YouTubeVideoID)
		if _, exists := sessionMap[key]; !exists {
			sessionMap[key] = s
		}
	}

	// 6. Fan-out: per-account YouTube listing. Bounded concurrency
	// via semaphore on a buffered channel; bounded per-account
	// timeout so a stuck channel doesn't stall the request.
	type fetchResult struct {
		accountID    int64
		items        []models.YouTubeVideoDetails
		warning      string
		invalidToken bool
	}
	accountIDs = accountIDs[:0]
	for aid := range accountLookup {
		accountIDs = append(accountIDs, aid)
	}
	sort.Slice(accountIDs, func(i, j int) bool { return accountIDs[i] < accountIDs[j] })
	sem := make(chan struct{}, groupYouTubeVideosFanoutConcurrency)
	results := make(chan fetchResult, len(accountLookup))
	var wg sync.WaitGroup
	for _, aid := range accountIDs {
		entry := accountLookup[aid]
		wg.Add(1)
		sem <- struct{}{}
		go func(aid int64, acc *models.PlatformAccount) {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(req.Context(), groupYouTubeVideosPerAccountTimeout)
			defer cancel()
			items, ferr := r.fetchCachedAccountEditableVideos(ctx, acc, cfg)
			if ferr != nil {
				results <- fetchResult{
					accountID:    aid,
					warning:      fmt.Sprintf("account %d: %s", aid, ferr.Error()),
					invalidToken: isInvalidYouTubeTokenError(ferr),
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
	resultsByAccount := make(map[int64]fetchResult, len(accountLookup))
	for res := range results {
		resultsByAccount[res.accountID] = res
	}
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
		language := ""
		if lookup.account.Metadata != nil {
			if value, ok := lookup.account.Metadata["language"].(string); ok {
				language = strings.TrimSpace(value)
			}
		}
		for _, v := range res.items {
			unconfirmed := "unconfirmed"
			entry := groupYouTubeVideoEntry{
				YouTubeVideoID:    v.ID,
				Title:             v.Title,
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
			emittedKeys[sessionKey(res.accountID, v.ID)] = struct{}{}
			if s, ok := sessionMap[sessionKey(res.accountID, v.ID)]; ok {
				sid := s.ID
				entry.EditorSessionID = &sid
				vid := s.VeloxProjectID
				entry.VeloxProjectID = &vid
				u := r.editorURLForProject(s.VeloxProjectID)
				entry.EditorURL = &u
				entry.EditorStatus = s.Status
				entry.DesiredPrivacy = s.DesiredPrivacy
				entry.PublishAt = s.PublishAt
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

	// 7.5. Phantom emission: published sessions whose
	// (account_id, youtube_video_id) tuple has no matching YouTube
	// row in the fan-out. ListEditableVideos filters out
	// privacy=public videos, so a session we just published as
	// public disappears from the YouTube side of the join — without
	// this pass it would also vanish from the group's video grid.
	//
	// The thumbnail URL is YouTube's public CDN
	// (i.ytimg.com/vi/{ID}/hqdefault.jpg), which works for any
	// public/unlisted video without an extra API call. A deleted
	// video would surface a grey placeholder; that's an acceptable
	// edge case for a recently-published video.
	//
	// Cross-group guard: only emit phantoms for sessions whose
	// PlatformAccountID is in the current group's account set, so
	// a leaked account row cannot surface a phantom entry in
	// another group's response.
	//
	// Recency filter: sessions updated more than
	// groupYouTubeVideosPhantomMaxAge ago are skipped to avoid
	// saturating the response with year-old publishes.
	now := time.Now()
	for _, s := range sessions {
		if s.Status != "published" {
			continue
		}
		if now.Sub(s.UpdatedAt) > groupYouTubeVideosPhantomMaxAge {
			continue
		}
		key := sessionKey(s.PlatformAccountID, s.YouTubeVideoID)
		if _, emitted := emittedKeys[key]; emitted {
			// Race: YouTube briefly included the public video
			// before the privacy flip took effect. The fan-out
			// already emitted a regular entry; don't double-count.
			continue
		}
		if _, inGroup := accountLookup[s.PlatformAccountID]; !inGroup {
			continue
		}
		emittedKeys[key] = struct{}{}
		chName := strings.TrimSpace(accountLookup[s.PlatformAccountID].account.Username)
		if chName == "" {
			chName = accountLookup[s.PlatformAccountID].account.PlatformUserID
		}
		thumbnailURL := fmt.Sprintf("https://i.ytimg.com/vi/%s/hqdefault.jpg", s.YouTubeVideoID)
		title := ""
		if s.DraftTitle != nil {
			title = strings.TrimSpace(*s.DraftTitle)
		}
		if title == "" {
			title = "(Titolo sconosciuto \u2014 Pubblicato)"
		}
		// Resolve the displayed privacy from YouTube's read-back when
		// available. A phantom has already disappeared from the editable
		// listing, so public is the conservative fallback for a terminal
		// public publish whose read-back omitted the field. This value is
		// kept separate from ActualPrivacy: the latter remains nil until
		// YouTube explicitly confirms it.
		privacy := s.ActualPrivacy
		if privacy == nil || *privacy == "" {
			if s.DesiredPrivacy != "" {
				dp := s.DesiredPrivacy
				privacy = &dp
			}
		}
		if privacy == nil || *privacy == "" {
			p := "public"
			privacy = &p
		}
		sid := s.ID
		vid := s.VeloxProjectID
		u := r.editorURLForProject(s.VeloxProjectID)
		entries = append(entries, groupYouTubeVideoEntry{
			YouTubeVideoID:    s.YouTubeVideoID,
			Title:             title,
			ThumbnailURL:      thumbnailURL,
			PrivacyStatus:     *privacy,
			ProcessingStatus:  "processed",
			PlatformAccountID: s.PlatformAccountID,
			ChannelName:       chName,
			EditorSessionID:   &sid,
			VeloxProjectID:    &vid,
			EditorURL:         &u,
			EditorStatus:      "published",
			DesiredPrivacy:    s.DesiredPrivacy,
			PublishAt:         s.PublishAt,
			ActualPrivacy:     s.ActualPrivacy,
			YouTubeSyncStatus: s.YouTubeSyncStatus,
			Phantom:           true,
		})
	}

	// Apply the aggregate page after the account fan-out. Authorization
	// and YouTube reads therefore happen before slicing, while callers
	// receive a bounded response independent of group size.
	lenEntriesBeforeCap := len(entries)
	totalVideos := lenEntriesBeforeCap
	if totalVideos > cfg.MaxVideos {
		totalVideos = cfg.MaxVideos
		entries = entries[:cfg.MaxVideos]
	}
	if offset > totalVideos {
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
		resp.NextOffset = end
	}
	if len(warnings) == len(accountLookup) && len(accountLookup) > 0 {
		// Every per-account fetch failed → propagate as 502, while
		// retaining the structured summary so the UI can distinguish
		// accounts requiring reconnection from a general outage.
		resp.Error = "youtube list failed for every account in the group"
		resp.Warnings = warnings
		writeJSON(w, http.StatusBadGateway, resp)
		return
	}
	if len(warnings) > 0 {
		resp.Warnings = warnings
	}
	writeJSON(w, http.StatusOK, resp)
}
