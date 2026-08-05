package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestResolvePublishToken_FallsBackOnlyWhenModernGrantIsMissing(t *testing.T) {
	var renewedTypes []string
	vault := &mockCredentialVault{
		renewFn: func(_ context.Context, _ int64, tokenType string, _ credentials.TokenRefresher) (*models.OAuthToken, error) {
			renewedTypes = append(renewedTypes, tokenType)
			if tokenType == models.TokenTypeBearer {
				return nil, errors.Join(errors.New("canonical row absent"), credentials.ErrModernGrantMissing)
			}
			return &models.OAuthToken{AccessToken: "legacy-access", TokenType: models.TokenTypeLongLived}, nil
		},
	}
	posts := &mockPostStore{}
	users := &mockUserStore{}
	worker := newTestWorkerWithoutThrottle(posts, users, models.PlatformInstagram, &mockProvider{
		baseMockProvider: baseMockProvider{platform: models.PlatformInstagram},
	}, vault)
	account := &models.PlatformAccount{ID: 42, Platform: models.PlatformInstagram, PlatformUserID: "account"}
	target := &models.PostTarget{ID: 1, PostID: 2, PlatformAccountID: account.ID}
	oauth := &mockProvider{baseMockProvider: baseMockProvider{platform: models.PlatformInstagram}}

	got, _, stop, err := worker.resolvePublishToken(context.Background(), target, account, oauth)
	if stop || err != nil {
		t.Fatalf("missing modern grant should use legacy token: stop=%v err=%v", stop, err)
	}
	if got == nil || got.AccessToken != "legacy-access" {
		t.Fatalf("token: want legacy-access, got %#v", got)
	}
	want := []string{models.TokenTypeBearer, models.TokenTypeLongLived}
	if len(renewedTypes) != len(want) || renewedTypes[0] != want[0] || renewedTypes[1] != want[1] {
		t.Fatalf("renew types: want %v, got %v", want, renewedTypes)
	}
}

func TestResolvePublishToken_HardOAuthErrorsNeverUseLegacyFallback(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{name: "invalid_grant", err: credentials.ErrInvalidGrant},
		{name: "grant_reauth_required", err: errors.New("oauth grant status is reauth_required")},
		{name: "audience_mismatch", err: errors.New("oauth audience mismatch")},
		{name: "scope_insufficient", err: errors.New("oauth insufficient scope")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var renewedTypes []string
			vault := &mockCredentialVault{
				renewFn: func(_ context.Context, _ int64, tokenType string, _ credentials.TokenRefresher) (*models.OAuthToken, error) {
					renewedTypes = append(renewedTypes, tokenType)
					return nil, tc.err
				},
			}
			posts := &mockPostStore{}
			users := &mockUserStore{}
			worker := newTestWorkerWithoutThrottle(posts, users, models.PlatformInstagram, &mockProvider{
				baseMockProvider: baseMockProvider{platform: models.PlatformInstagram},
			}, vault)
			account := &models.PlatformAccount{ID: 42, Platform: models.PlatformInstagram, PlatformUserID: "account"}
			target := &models.PostTarget{ID: 1, PostID: 2, PlatformAccountID: account.ID}
			oauth := &mockProvider{baseMockProvider: baseMockProvider{platform: models.PlatformInstagram}}

			_, _, stop, err := worker.resolvePublishToken(context.Background(), target, account, oauth)
			if !stop || err == nil {
				t.Fatalf("hard OAuth error must stop publish: stop=%v err=%v", stop, err)
			}
			if len(renewedTypes) != 1 || renewedTypes[0] != models.TokenTypeBearer {
				t.Fatalf("hard OAuth error %q must not try legacy token; renew types=%v", tc.name, renewedTypes)
			}
		})
	}
}
