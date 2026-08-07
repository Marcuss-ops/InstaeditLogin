package services

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// GoogleOAuthClientRefreshTokenLimit is Google's documented cap of
// refresh tokens per (Google account, OAuth client) pair. The exact
// value is 100; the pool's recommended per-client capacity (50) is half
// of it so reconnects, migrations and recovery never ride the hard cap.
const GoogleOAuthClientRefreshTokenLimit int64 = 100

// OAuthPoolUsage is one pool client's capacity report for a Google
// subject, as surfaced by GetUsage (and destined for the operator
// dashboard). ActiveRefreshTokens is the count Google's cap is
// measured against; RecommendedCapacity is the soft per-client ceiling
// (default 50); ProviderLimit is Google's hard cap (100).
type OAuthPoolUsage struct {
	OAuthClientKey      string `json:"oauth_client_key"`
	ActiveRefreshTokens int64  `json:"active_refresh_tokens"`
	RecommendedCapacity int64  `json:"recommended_capacity"`
	ProviderLimit       int64  `json:"provider_limit"`
}

// OAuthPoolSelection is the result of SelectPool: the pool client that
// should issue the next grant, with the capacity math that picked it.
// ClientID is included so the caller can build the consent URL; the
// client secret is deliberately absent (never leaves the registry).
type OAuthPoolSelection struct {
	Key                 string
	ClientID            string
	ActiveRefreshTokens int64
	RecommendedCapacity int64
	RemainingCapacity   int64
}

// OAuthTokenCapacityCounter is the storage capability the capacity
// manager needs: the per-(subject, client) count (same shape as the
// registry's OAuthClientUsageCounter) plus the grouped per-client list
// that backs GetUsage. The repository implementation is
// *repository.OAuthTokenCapacityRepository.
type OAuthTokenCapacityCounter interface {
	OAuthClientUsageCounter
	ListPoolUsage(ctx context.Context, providerSubjectID string) ([]repository.OAuthPoolUsageRow, error)
}

// OAuthTokenCapacityManager is the YouTube OAuth Client Pool capacity
// decision-maker: SelectPool picks the least-loaded pool client for a
// Google subject (never random alternation — a new grant must land on
// the same pool across retries), GetUsage reports per-client headroom
// for the operator dashboard.
//
// The Google subject is known only AFTER the operator picks an account
// on the consent screen, so SelectPool requires a providerSubjectID
// (reconnect flows pass the existing grant's subject). The login-time
// subject-less selection is handled by the registry's fallback path.
type OAuthTokenCapacityManager interface {
	// SelectPool returns the pool client that should issue the next
	// OAuth grant for the given Google subject. Clients over the
	// critical threshold (90 active grants) are excluded; if every
	// client is blocked it returns ErrYouTubeOAuthClientPoolExhausted.
	SelectPool(ctx context.Context, provider, providerSubjectID string) (*OAuthPoolSelection, error)
	// GetUsage returns the capacity report for every configured pool
	// client for the given Google subject, zero-filling clients with
	// no active grants so the dashboard always shows the full pool.
	GetUsage(ctx context.Context, providerSubjectID string) ([]OAuthPoolUsage, error)
}

type oauthTokenCapacityManager struct {
	counter  OAuthTokenCapacityCounter
	registry *YouTubeOAuthClientRegistry
}

// NewOAuthTokenCapacityManager wires the capacity manager to a storage
// counter and the pool registry. Both are required: a capacity manager
// without counting cannot select (and a nil registry would silently
// select nothing).
func NewOAuthTokenCapacityManager(counter OAuthTokenCapacityCounter, registry *YouTubeOAuthClientRegistry) (OAuthTokenCapacityManager, error) {
	if counter == nil {
		return nil, fmt.Errorf("oauth token capacity manager: usage counter is required")
	}
	if registry == nil || len(registry.clients) == 0 {
		return nil, ErrYouTubeOAuthClientPoolEmpty
	}
	return &oauthTokenCapacityManager{counter: counter, registry: registry}, nil
}

// SelectPool implements OAuthTokenCapacityManager.SelectPool.
func (m *oauthTokenCapacityManager) SelectPool(ctx context.Context, provider, providerSubjectID string) (*OAuthPoolSelection, error) {
	if provider != models.PlatformYouTube {
		return nil, fmt.Errorf("oauth token capacity manager: unsupported provider %q (capacity is YouTube-only)", provider)
	}
	if m.registry == nil || len(m.registry.clients) == 0 {
		return nil, ErrYouTubeOAuthClientPoolEmpty
	}
	if m.counter == nil {
		return nil, fmt.Errorf("oauth token capacity manager: usage counter not configured")
	}
	selected, used, err := selectLeastLoadedPoolClient(ctx, m.registry.clients, m.counter, providerSubjectID)
	if err != nil {
		return nil, err
	}
	return &OAuthPoolSelection{
		Key:                 selected.Key,
		ClientID:            selected.ClientID,
		ActiveRefreshTokens: used,
		RecommendedCapacity: int64(selected.RecommendedCapacity),
		RemainingCapacity:   int64(selected.RecommendedCapacity) - used,
	}, nil
}

// GetUsage implements OAuthTokenCapacityManager.GetUsage. Every
// configured pool client appears in the result, in registration order,
// with active-grant counts zero-filled for clients the subject has no
// grants on yet.
func (m *oauthTokenCapacityManager) GetUsage(ctx context.Context, providerSubjectID string) ([]OAuthPoolUsage, error) {
	if m.registry == nil || len(m.registry.clients) == 0 {
		return nil, ErrYouTubeOAuthClientPoolEmpty
	}
	if m.counter == nil {
		return nil, fmt.Errorf("oauth token capacity manager: usage counter not configured")
	}
	if providerSubjectID == "" {
		return nil, fmt.Errorf("oauth token capacity manager: providerSubjectID is required")
	}
	rows, err := m.counter.ListPoolUsage(ctx, providerSubjectID)
	if err != nil {
		return nil, fmt.Errorf("oauth token capacity manager: list pool usage: %w", err)
	}
	usedByKey := make(map[string]int64, len(rows))
	for _, row := range rows {
		usedByKey[row.OAuthClientKey] = row.ActiveRefreshTokens
	}
	usage := make([]OAuthPoolUsage, 0, len(m.registry.clients))
	for i := range m.registry.clients {
		c := &m.registry.clients[i]
		usage = append(usage, OAuthPoolUsage{
			OAuthClientKey:      c.Key,
			ActiveRefreshTokens: usedByKey[c.Key],
			RecommendedCapacity: int64(c.RecommendedCapacity),
			ProviderLimit:       GoogleOAuthClientRefreshTokenLimit,
		})
	}
	return usage, nil
}

// CountActiveRefreshTokens implements OAuthClientUsageCounter so the
// manager can be wired directly as the registry's usage counter (via
// WithYouTubeOAuthClientUsageCounter) once the login flow can resolve
// the Google subject. YouTube provider is implied.
func (m *oauthTokenCapacityManager) CountActiveRefreshTokens(ctx context.Context, providerSubjectID, oauthClientKey string) (int64, error) {
	if m.counter == nil {
		return 0, fmt.Errorf("oauth token capacity manager: usage counter not configured")
	}
	return m.counter.CountActiveRefreshTokens(ctx, providerSubjectID, oauthClientKey)
}
