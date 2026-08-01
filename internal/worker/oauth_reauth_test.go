package worker

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type grantAwareUserStore struct {
	*mockUserStore
	grantCalls        int
	grantConnectionID int64
}

func (s *grantAwareUserStore) MarkOAuthConnectionAccountsReauthRequired(_ context.Context, oauthConnectionID int64, code, message string) error {
	s.grantCalls++
	s.grantConnectionID = oauthConnectionID
	if code != YouTubeReauthCode || message != YouTubeReauthMessage {
		return testingErr("unexpected grant reauth payload")
	}
	return nil
}

type testingErr string

func (e testingErr) Error() string { return string(e) }

func TestMarkYouTubeGrantReauth_UsesSingleGrantWideUpdate(t *testing.T) {
	connectionID := int64(45)
	store := &grantAwareUserStore{mockUserStore: &mockUserStore{}}
	account := &models.PlatformAccount{
		ID:                11,
		Platform:          models.PlatformYouTube,
		OAuthConnectionID: &connectionID,
	}

	markYouTubeGrantReauth(context.Background(), store, nil, account)

	if store.grantCalls != 1 {
		t.Fatalf("grant-wide update calls: want 1, got %d", store.grantCalls)
	}
	if store.grantConnectionID != connectionID {
		t.Fatalf("oauth connection id: want %d, got %d", connectionID, store.grantConnectionID)
	}
	if store.markReauthRequiredCalls != 0 {
		t.Fatalf("per-account fallback calls: want 0 on successful grant-wide update, got %d", store.markReauthRequiredCalls)
	}
}

func TestMarkYouTubeGrantReauth_FallsBackToCurrentAccountOnGrantWriteError(t *testing.T) {
	connectionID := int64(45)
	store := &grantAwareUserStore{mockUserStore: &mockUserStore{
		markReauthRequiredFn: func(context.Context, int64, string, string) error { return nil },
	}}
	store.grantCalls = 0
	account := &models.PlatformAccount{
		ID:                11,
		Platform:          models.PlatformYouTube,
		OAuthConnectionID: &connectionID,
	}

	// Replace the grant-aware method behavior with an error while retaining
	// the same production-shaped fallback store.
	failingStore := &grantWriteErrorStore{grantAwareUserStore: store}
	markYouTubeGrantReauth(context.Background(), failingStore, nil, account)

	if store.markReauthRequiredCalls != 1 {
		t.Fatalf("per-account fallback calls: want 1, got %d", store.markReauthRequiredCalls)
	}
}

type grantWriteErrorStore struct {
	*grantAwareUserStore
}

func (*grantWriteErrorStore) MarkOAuthConnectionAccountsReauthRequired(context.Context, int64, string, string) error {
	return testingErr("grant write unavailable")
}

var _ PublisherUserStore = (*grantAwareUserStore)(nil)
var _ OAuthConnectionReauthStore = (*grantAwareUserStore)(nil)
var _ OAuthConnectionReauthStore = (*grantWriteErrorStore)(nil)
