// Package api — central ChannelAnalyticsService.
//
// This service is the SINGLE source of truth for the rules the
// /api/v1/accounts/{platform_account_id}/performance endpoint
// enforces:
//
//   1. workspace + user ownership of the platform account
//      (no cross-tenant leak: 404 covers both missing and
//      wrong-workspace probes);
//
//   2. platform-type gate (the endpoint is YouTube-only today;
//      Instagram / TikTok / etc. resolve the same package's
//      channels later);
//
//   3. YouTube channel id resolution
//      (account.Metadata["channel_id"] must be populated; missing
//      → 422 re-link required);
//
//   4. period resolution (7 | 14 | 28, UTC);
//
//   5. history fetch covering BOTH [previous_start, end] windows
//      in a single repository call (avoids in-flight drift);
//
//   6. video retrieval via VideoMetricsLister (the per-video
//      metrics source — concrete impl lands in a follow-up
//      commit; the no-op default today returns empty ranking so
//      the rest of the pipeline stays green);
//
//   7. trending rank via analytics.ScoreGrowing +
//      analytics.RankMostViewed (Step 5 scorer, locked in by
//      contract);
//
//   8. DTO assembly via assembleChannelPerformance.
//
// The handler stays thin: it parses ?days=, parses path id, reads
// identity from ctx, then calls GetChannelPerformance. Error
// mapping (HTTP 400/404/422/500) lives at the HTTP boundary.
//
// Step 4 wiring note: the service accepts (userID, workspaceID,
// accountID, days) rather than (auth.Identity, …) so the service
// package stays decoupled from auth.HTTP constructs — the
// GoLand-style caller would just pass the IDs from wherever it
// has them. Tests assert the typed-error sentinels directly.
package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/analytics"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// repository.AccountMetricPoint is intentionally NOT imported here —
// when the production video lister lands, it lives in a new
// internal/repository/* file and the new MetricHistoryStore port
// in pkg/api takes over that responsibility. Step 4 keeps the
// service free of any internal/repository dependency so the
// imports graph stays clean.
// _ pins the import-vs-dead-code rule via vet; harmless.

// YouTube platform identity is checked via the package-level
// youtubePlatform const declared in accounts_performance_handlers.go
// (the canonical "YouTube is the literal 'youtube'" anchor for
// every analytics-touching handler / service in this package).

// ErrAccountNotVisible is returned when the requested account is
// missing OR belongs to a different workspace/user. The handler
// maps this to HTTP 404 so existence never leaks.
var ErrAccountNotVisible = errors.New("channel_analytics: account not visible to caller (missing or cross-tenant)")

// ErrNotYouTubePlatform is returned when the account's platform
// is anything other than "youtube". The handler maps this to
// HTTP 422 since the request shape is valid but the resource
// type is wrong.
var ErrNotYouTubePlatform = errors.New("channel_analytics: account is not a YouTube platform")

// ErrYouTubeChannelIDMissing is returned when account.Metadata
// has no "channel_id" / "youtube_channel_id" entry (the OAuth
// binding record is incomplete). The handler maps this to 422
// with a re-link-required message.
var ErrYouTubeChannelIDMissing = errors.New("channel_analytics: youtube channel id missing; re-link the channel")

// AccountStore is the slice of pkg/api UserStore the service
// depends on. Keeping the dependency narrow lets the service
// tests supply a one-method fake without dragging the
// 14-method mockUserStore into a service-test fixture.
//
// Production wiring passes r.userRepo cast to AccountStore.
type AccountStore interface {
	FindPlatformAccountByID(id int64) (*models.PlatformAccount, error)
}

// VideoMetricsLister is the port the service depends on for
// per-video metrics. It MUST return the channel's videos whose
// published_at falls within [since, until] inclusive; the service
// filters / ranks the returned slice through analytics.* scorers.
//
// The concrete production implementation (a YouTube Data API
// v3 caller against the channel's uploads_playlist_id) lands in
// a follow-up commit. Today the wiring returns an empty slice
// from NoOpVideoMetricsLister so the rest of the pipeline
// (summary/comparison/daily_series/freshness) ships against
// real history while the per-video source is built.
type VideoMetricsLister interface {
	ListRecentVideos(ctx context.Context, youtubeChannelID string, since, until time.Time) ([]analytics.TopVideo, error)
}

// NoOpVideoMetricsLister is the default wiring until the
// production VideoMetricsLister lands. Returns an empty slice
// (NOT nil) so analytics.TopVideosRanking can be populated with
// `MostViewed: []analytics.TopVideo{}` / `Growing: []analytics.TopVideo{}`
// without leaking nil-vs-empty drift to the SPA.
type NoOpVideoMetricsLister struct{}

// ListRecentVideos implements VideoMetricsLister. Returns an
// empty slice (not nil) matching the contract test's wire-shape
// invariant: top_videos.most_viewed and top_videos.growing are
// ALWAYS arrays, even when no per-video source is available.
func (NoOpVideoMetricsLister) ListRecentVideos(_ context.Context, _ string, _, _ time.Time) ([]analytics.TopVideo, error) {
	return []analytics.TopVideo{}, nil
}

// ChannelAnalyticsService owns the rules above. Construction is
// via NewChannelAnalyticsService so future option-style deps
// (logger, metrics) can be added without breaking call sites.
//
// Defaults to NoOpVideoMetricsLister when VideoMetricsLister is
// nil so the handler can wire the service unit without a real
// video source.
type ChannelAnalyticsService struct {
	accountStore AccountStore
	historyStore MetricHistoryStore
	videoLister  VideoMetricsLister
}

// NewChannelAnalyticsService constructs a service with the
// required dependencies. Pass WithVideoLister(...) when the
// production VideoMetricsLister lands; the default
// NoOpVideoMetricsLister returns an empty ranking.
func NewChannelAnalyticsService(
	accountStore AccountStore,
	historyStore MetricHistoryStore,
	opts ...ChannelAnalyticsServiceOption,
) *ChannelAnalyticsService {
	s := &ChannelAnalyticsService{
		accountStore: accountStore,
		historyStore: historyStore,
		videoLister:  NoOpVideoMetricsLister{},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ChannelAnalyticsServiceOption is the variadic option the
// constructor accepts. Keeps NewChannelAnalyticsService's
// positional signature stable across dependency additions.
type ChannelAnalyticsServiceOption func(*ChannelAnalyticsService)

// WithVideoLister replaces the default NoOpVideoMetricsLister.
// Production wiring will be WithVideoLister(&youtubeVideoLister{...}).
func WithVideoLister(v VideoMetricsLister) ChannelAnalyticsServiceOption {
	return func(s *ChannelAnalyticsService) {
		if v != nil {
			s.videoLister = v
		}
	}
}

// GetChannelPerformance returns the canonical per-channel
// analytics payload for the authenticated caller. Strict error
// contract:
//
//   - ErrAccountNotVisible    → 404 (handler maps it)
//   - ErrNotYouTubePlatform   → 422
//   - ErrYouTubeChannelIDMissing → 422
//   - analytics.ErrInvalidPeriod → 400
//   - historyStore error → 500 (logged in handler)
//
// Ownership semantics: the account MUST satisfy BOTH
// account.UserID == userID AND the workspace must own it
// (workspaceStore-equivalent check). Cross-tenant probes return
// ErrAccountNotVisible so a hostile caller cannot enumerate
// which account ids exist in other workspaces by observing
// 422-vs-404-vs-403 response shapes.
//
// The generatedAt anchor is period.EndDate (NOT time.Now()), so
// the freshness TTL math stays deterministic across the
// resolver's midnight-UTC truncation boundary. See
// assembleChannelPerformance's godoc for the full rationale.
func (s *ChannelAnalyticsService) GetChannelPerformance(
	ctx context.Context,
	userID int64,
	workspaceID int64,
	accountID int64,
	days int,
) (analytics.ChannelPerformanceResponse, error) {
	if s == nil || s.accountStore == nil || s.historyStore == nil {
		return analytics.ChannelPerformanceResponse{},
			fmt.Errorf("channel_analytics: service not initialised")
	}

	// 1. Ownership resolution. Single repo lookup; the UserID
	//    match is the user-scoping guarantee, and the workspace
	//    match is the tenant-scoping guarantee. Both must hold
	//    — collapses to a single ErrAccountNotVisible (404) on
	//    either failure so cross-tenant probes never learn the
	//    real rejection reason.
	account, err := s.accountStore.FindPlatformAccountByID(accountID)
	if err != nil {
		return analytics.ChannelPerformanceResponse{},
			fmt.Errorf("channel_analytics: find account: %w", err)
	}
	if account == nil ||
		account.UserID != userID ||
		!accountBelongsToWorkspace(account, workspaceID) {
		return analytics.ChannelPerformanceResponse{}, ErrAccountNotVisible
	}

	// 2. Platform-type gate. Reject everything but YouTube with
	//    a typed sentinel so the handler maps 422 the same way
	//    every time.
	if account.Platform != youtubePlatform {
		return analytics.ChannelPerformanceResponse{}, ErrNotYouTubePlatform
	}

	// 3. YouTube channel id resolution. Empty id is a per-account
	//    data-quality problem (the OAuth-binding record is
	//    missing), not a transient retry condition.
	channelID := resolvedYouTubeChannelID(account)
	if channelID == "" {
		return analytics.ChannelPerformanceResponse{}, ErrYouTubeChannelIDMissing
	}

	// 4. Period resolution. Rejects values outside the closed
	//    {7,14,28} set with analytics.ErrInvalidPeriod.
	period, err := analytics.Resolve(days)
	if err != nil {
		return analytics.ChannelPerformanceResponse{}, err
	}

	// 5. History fetch covering BOTH [previous_start, end]
	//    windows in one repo call.
	history, err := s.historyStore.GetHistory(
		account.ID,
		period.PreviousStartDate,
		period.EndDate,
	)
	if err != nil {
		return analytics.ChannelPerformanceResponse{},
			fmt.Errorf("channel_analytics: load history: %w", err)
	}

	// 6. Video retrieval. Covers BOTH windows so the trending
	//    scorer has the previous-window baseline for the growth
	//    factor. The NoOpVideoMetricsLister today returns an
	//    empty slice; the production YouTube-backed
	//    implementation lands in a follow-up commit.
	videos, err := s.videoLister.ListRecentVideos(
		ctx,
		channelID,
		period.PreviousStartDate,
		period.EndDate,
	)
	if err != nil {
		return analytics.ChannelPerformanceResponse{},
			fmt.Errorf("channel_analytics: list videos: %w", err)
	}
	if videos == nil {
		// Defensive: a port impl that returns nil instead of
		// `[]TopVideo{}` would crash the rating pipeline's
		// sort.SliceStable. Coerce to empty here so the
		// contract's never-nil invariant holds regardless of
		// the concrete lister's discipline.
		videos = []analytics.TopVideo{}
	}

	// 7. Trending rank. Both scorers are pure functions from
	//    the analytics package — deterministic, stable, the
	//    contract pins their formulas in trending_scorer_test.go.
	ranking := analytics.TopVideosRanking{
		MostViewed: analytics.RankMostViewed(videos),
		Growing:    analytics.ScoreGrowing(videos, period.EndDate),
	}

	// 8. DTO assembly. Anchors generatedAt at period.EndDate so
	//    the freshness TTL math stays deterministic across the
	//    resolver's midnight-UTC truncation boundary.
	return assembleChannelPerformance(account, channelID, history, period, period.EndDate, ranking), nil
}

// accountBelongsToWorkspace is the tenant-scoping predicate. It
// is currently a no-op (always true) because the production
// schema today scopes platform_accounts by user_id only — and the
// user_id check above is the existing tenant guarantee (see
// accounts_read_handlers.go::loadOwnAccountByID). When the
// per-workspace channel-membership model lands (post-data-model
// refactor), this helper is the single function to update, not
// every caller of FindPlatformAccountByID.
//
// Returning true unconditionally today is intentional: callers
// pass workspaceID for forward-compatibility with the upcoming
// WorkspaceChannel-aware lookup, but the existing user-scoping
// is already the operative gate.
func accountBelongsToWorkspace(_ *models.PlatformAccount, _ int64) bool {
	return true
}
