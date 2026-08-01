package api

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// driveAccessToken fetches a fresh access token for a Drive account
// via the central credential vault (uses the platform's refresh flow
// when the stored token is expired).
func driveAccessToken(ctx context.Context, vault credentials.VaultAPI, importer services.DriveImporter, accountID int64) (string, error) {
	oauth, err := vault.Renew(ctx, accountID, models.TokenTypeBearer,
		func(c context.Context, refresh string) (*models.TokenData, error) {
			return importer.RefreshOAuthToken(c, refresh)
		})
	if err != nil {
		return "", err
	}
	return oauth.AccessToken, nil
}

// randomDurationInRange returns a uniformly random integer in
// [minSeconds, maxSeconds] inclusive and renders it as a time.Duration.
// Uses crypto/rand for the source so the jitter doesn't follow a
// deterministic pseudo-random pattern (which social platforms' spam
// detection could pick up on).
func randomDurationInRange(minSeconds, maxSeconds int) (time.Duration, error) {
	if minSeconds > maxSeconds {
		return 0, fmt.Errorf("randomDurationInRange: min (%d) > max (%d)", minSeconds, maxSeconds)
	}
	span := int64(maxSeconds - minSeconds)
	n, err := rand.Int(rand.Reader, big.NewInt(span+1))
	if err != nil {
		return 0, fmt.Errorf("crypto/rand Int: %w", err)
	}
	secs := int64(minSeconds) + n.Int64()
	return time.Duration(secs) * time.Second, nil
}
