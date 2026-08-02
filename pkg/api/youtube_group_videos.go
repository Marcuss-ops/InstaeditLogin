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

// groupAccountEntry is the per-account record the group video handler
// carries from the resolution phase to the aggregation phase.
type groupAccountEntry struct {
	account *models.PlatformAccount
}

// groupFetchResult is the per-account outcome of the YouTube fan-out.
type groupFetchResult struct {
	accountID    int64
	items        []models.YouTubeVideoDetails
	warning      string
	invalidToken bool
}

// groupSessionKey builds the (account_id, youtube_video_id) join key
// shared by the fan-out aggregation and the phantom pass.
func groupSessionKey(accountID int64, videoID string) string {
	return fmt.Sprintf("%d|%s", accountID, videoID)
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
// The heavy phases are extracted into helpers: account resolution
// (resolveGroupYouTubeAccounts in youtube_group_videos_helpers.go),
// the bounded-concurrency YouTube fan-out (fanOutGroupYouTubeVideos)
// and the published-session phantom pass (appendPhantomGroupEntries).
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

	// Phases 1-4: group lookup + ownership + subgroup traversal +
	// account resolution/filtering. On the two legitimate empty
	// outcomes the helper writes the empty 200 itself.
	group, accountLookup, done := r.resolveGroupYouTubeAccounts(w, identity.UserID(), groupID, includeSubgroups, cfg)
	if done {
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
	sessionMap := make(map[string]*models.YouTubeVideoEdit, len(sessions))
	for _, s := range sessions {
		// ListByWorkspaceAccountIDs returns rows ORDER BY updated_at
		// DESC, so the FIRST occurrence in this loop is the newest.
		// Keep the newest; ignore older duplicates. (Without this
		// guard the map would end up with the OLDEST session for
		// any (account, video) tuple that was ever edited twice.)
		key := groupSessionKey(s.PlatformAccountID, s.YouTubeVideoID)
		if _, exists := sessionMap[key]; !exists {
			sessionMap[key] = s
		}
	}

	// 6. Fan-out: per-account YouTube listing (bounded concurrency +
	// per-account timeout).
	resultsByAccount, sortedAccountIDs := r.fanOutGroupYouTubeVideos(req, accountLookup, cfg)
	if !r.writeGroupVideosOK(w, req, resultsByAccount, sortedAccountIDs, accountLookup, sessions, sessionMap, recencyDays, cfg, offset, limit) {
		return
	}
}

// fanOutGroupYouTubeVideos runs the bounded-concurrency per-account
// YouTube listing (phase 6). Each chain is wrapped in a per-account
// timeout (groupYouTubeVideosPerAccountTimeout) so a single slow
// channel does not stall the whole request; the semaphore caps the
// simultaneous chains at groupYouTubeVideosFanoutConcurrency.
func (r *Router) fanOutGroupYouTubeVideos(
	req *http.Request,
	accountLookup map[int64]groupAccountEntry,
	cfg YouTubeGroupVideosConfig,
) (map[int64]groupFetchResult, []int64) {
	accountIDs := make([]int64, 0, len(accountLookup))
	for aid := range accountLookup {
		accountIDs = append(accountIDs, aid)
	}
	sort.Slice(accountIDs, func(i, j int) bool { return accountIDs[i] < accountIDs[j] })
	sem := make(chan struct{}, groupYouTubeVideosFanoutConcurrency)
	results := make(chan groupFetchResult, len(accountLookup))
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
				results <- groupFetchResult{
					accountID:    aid,
					warning:      fmt.Sprintf("account %d: %s", aid, ferr.Error()),
					invalidToken: isInvalidYouTubeTokenError(ferr),
				}
				return
			}
			results <- groupFetchResult{accountID: aid, items: items}
		}(aid, entry.account)
	}
	wg.Wait()
	close(results)

	resultsByAccount := make(map[int64]groupFetchResult, len(accountLookup))
	for res := range results {
		resultsByAccount[res.accountID] = res
	}
	return resultsByAccount, accountIDs
}

// appendPhantomGroupEntries is phase 7.5: published sessions whose
// (account_id, youtube_video_id) tuple has no matching YouTube row in
// the fan-out. ListEditableVideos filters out privacy=public videos,
// so a session we just published as public disappears from the YouTube
// side of the join — without this pass it would also vanish from the
// group's video grid.
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
func (r *Router) appendPhantomGroupEntries(
	entries []groupYouTubeVideoEntry,
	sessions []*models.YouTubeVideoEdit,
	accountLookup map[int64]groupAccountEntry,
	emittedKeys map[string]struct{},
) []groupYouTubeVideoEntry {
	now := time.Now()
	for _, s := range sessions {
		if s.Status != "published" {
			continue
		}
		if now.Sub(s.UpdatedAt) > groupYouTubeVideosPhantomMaxAge {
			continue
		}
		key := groupSessionKey(s.PlatformAccountID, s.YouTubeVideoID)
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
	return entries
}
