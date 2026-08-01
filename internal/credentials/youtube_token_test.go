package credentials

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type youtubeTokenVaultStub struct {
	renewFn func(context.Context, int64, string, TokenRefresher) (*models.OAuthToken, error)
}

func (s *youtubeTokenVaultStub) Save(context.Context, int64, *models.TokenData) error {
	return errors.New("unexpected Save")
}

func (s *youtubeTokenVaultStub) Get(context.Context, int64, string) (*models.OAuthToken, error) {
	return nil, errors.New("unexpected Get")
}

func (s *youtubeTokenVaultStub) Rotate(context.Context, int64, *models.TokenData) error {
	return errors.New("unexpected Rotate")
}

func (s *youtubeTokenVaultStub) Renew(ctx context.Context, accountID int64, tokenType string, refresher TokenRefresher) (*models.OAuthToken, error) {
	return s.renewFn(ctx, accountID, tokenType, refresher)
}

func (s *youtubeTokenVaultStub) Revoke(context.Context, int64) error {
	return errors.New("unexpected Revoke")
}

func TestRenewYouTubeToken_UsesCanonicalBearer(t *testing.T) {
	var types []string
	vault := &youtubeTokenVaultStub{
		renewFn: func(_ context.Context, _ int64, tokenType string, _ TokenRefresher) (*models.OAuthToken, error) {
			types = append(types, tokenType)
			return &models.OAuthToken{AccessToken: "canonical-access", TokenType: models.TokenTypeBearer}, nil
		},
	}

	got, err := RenewYouTubeToken(context.Background(), vault, 42, nil, nil)
	if err != nil {
		t.Fatalf("RenewYouTubeToken: %v", err)
	}
	if got.AccessToken != "canonical-access" {
		t.Fatalf("access token: want canonical-access, got %q", got.AccessToken)
	}
	if len(types) != 1 || types[0] != models.TokenTypeBearer {
		t.Fatalf("renew types: want [%q], got %v", models.TokenTypeBearer, types)
	}
}

func TestRenewYouTubeToken_LegacyFallbackIsTemporaryAndRedacted(t *testing.T) {
	const upstreamSecret = "refresh-token-secret-value"
	var types []string
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	vault := &youtubeTokenVaultStub{
		renewFn: func(_ context.Context, _ int64, tokenType string, _ TokenRefresher) (*models.OAuthToken, error) {
			types = append(types, tokenType)
			if tokenType == models.TokenTypeBearer {
				return nil, errors.New("vault: no token for account 42 (type: bearer)")
			}
			return &models.OAuthToken{AccessToken: "legacy-access", TokenType: models.TokenTypeLongLived}, nil
		},
	}

	got, err := RenewYouTubeToken(context.Background(), vault, 42, nil, logger)
	if err != nil {
		t.Fatalf("RenewYouTubeToken: %v", err)
	}
	if got.AccessToken != "legacy-access" {
		t.Fatalf("access token: want legacy-access, got %q", got.AccessToken)
	}
	wantTypes := []string{models.TokenTypeBearer, models.TokenTypeLongLived}
	if len(types) != len(wantTypes) || types[0] != wantTypes[0] || types[1] != wantTypes[1] {
		t.Fatalf("renew types: want %v, got %v", wantTypes, types)
	}
	if logs.Len() == 0 {
		t.Fatal("expected a compatibility warning")
	}
	for _, forbidden := range []string{upstreamSecret, "access_token", "refresh_token"} {
		if strings.Contains(logs.String(), forbidden) {
			t.Errorf("log contains forbidden credential material %q: %s", forbidden, logs.String())
		}
	}
}

func TestRenewYouTubeToken_LegacyInvalidGrantIsClassified(t *testing.T) {
	var types []string
	vault := &youtubeTokenVaultStub{
		renewFn: func(_ context.Context, _ int64, tokenType string, _ TokenRefresher) (*models.OAuthToken, error) {
			types = append(types, tokenType)
			if tokenType == models.TokenTypeBearer {
				return nil, errors.New("no token for account 42")
			}
			return nil, errors.New("oauth provider: invalid_grant")
		},
	}

	_, err := RenewYouTubeToken(context.Background(), vault, 42, nil, nil)
	if !errors.Is(err, ErrYouTubeInvalidGrant) {
		t.Fatalf("error: want ErrYouTubeInvalidGrant, got %v", err)
	}
	wantTypes := []string{models.TokenTypeBearer, models.TokenTypeLongLived}
	if len(types) != len(wantTypes) || types[0] != wantTypes[0] || types[1] != wantTypes[1] {
		t.Fatalf("renew types: want %v, got %v", wantTypes, types)
	}
	if strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("redacted classification must not expose provider error text: %v", err)
	}
}

func TestRenewYouTubeToken_DoesNotFallbackOnNonMissingCanonicalError(t *testing.T) {
	const upstreamSecret = "provider-secret-value"
	var types []string
	vault := &youtubeTokenVaultStub{
		renewFn: func(_ context.Context, _ int64, tokenType string, _ TokenRefresher) (*models.OAuthToken, error) {
			types = append(types, tokenType)
			return nil, errors.New("vault: decrypt failed: " + upstreamSecret)
		},
	}

	_, err := RenewYouTubeToken(context.Background(), vault, 42, nil, nil)
	if !errors.Is(err, ErrYouTubeTokenRenewal) {
		t.Fatalf("error: want ErrYouTubeTokenRenewal, got %v", err)
	}
	if len(types) != 1 || types[0] != models.TokenTypeBearer {
		t.Fatalf("renew types: want only [%q], got %v", models.TokenTypeBearer, types)
	}
	if strings.Contains(err.Error(), upstreamSecret) {
		t.Fatalf("returned error leaked provider secret: %v", err)
	}
}
