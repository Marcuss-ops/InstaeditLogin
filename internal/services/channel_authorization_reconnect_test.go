package services

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestAuthorizeChannel_ReconnectSameSubjectReusesConnection is the R7
// idempotent-reconnect certification at the service layer: re-running
// the OAuth dance for the SAME (channel, subject) — through ANY pool
// client — MUST return the SAME oauth_connection_id (the subject-keyed
// UPSERT updates the existing row in place instead of inserting a
// parallel grant). The second pass also proves the DO UPDATE SET
// carries the new oauth_client_key: the same row flips from
// youtube_pool_a to youtube_pool_b without ever creating a second
// active connection (one channel → one connection → one canonical
// refresh token).
func TestAuthorizeChannel_ReconnectSameSubjectReusesConnection(t *testing.T) {
	svc, mock, _, cleanup := newSvcHarness(t)
	defer cleanup()

	const userID, oauthConnID int64 = 99, 555
	scopes := []string{"https://www.googleapis.com/auth/youtube.upload"}
	const channel = "UC012345678901234567890123"

	// First pass — brand-new grant issued by pool A.
	mock.ExpectBegin()
	expectLoadAccount(mock, 7, userID, models.PlatformYouTube, channel, models.AccountStatusPendingAuthorization)
	expectSubjectUpsertOCR(mock, userID, models.PlatformYouTube, "google-subject-1", channel, "youtube_pool_a", scopes, oauthConnID)
	expectInsertTokenTx(mock, true)
	expectPromoteAccount(mock, oauthConnID, 7)
	mock.ExpectCommit()

	// Second pass — reconnect through pool B. Same (channel, subject):
	// the UPSERT returns the SAME id and the row flips to pool B
	// (oauth_client_key = EXCLUDED.oauth_client_key), never a new row.
	mock.ExpectBegin()
	expectLoadAccount(mock, 7, userID, models.PlatformYouTube, channel, models.AccountStatusActive)
	expectSubjectUpsertOCR(mock, userID, models.PlatformYouTube, "google-subject-1", channel, "youtube_pool_b", scopes, oauthConnID)
	expectInsertTokenTx(mock, true)
	expectPromoteAccount(mock, oauthConnID, 7)
	mock.ExpectCommit()

	token := &models.TokenData{
		AccessToken:       "fresh-access",
		RefreshToken:      "fresh-refresh",
		ProviderSubjectID: "google-subject-1",
		TokenType:         models.TokenTypeBearer,
		ExpiresIn:         3600,
		Scopes:            scopes,
	}

	got1, err := svc.AuthorizeChannel(context.Background(), 7, channel, "youtube_pool_a", scopes, token)
	if err != nil {
		t.Fatalf("first AuthorizeChannel: %v", err)
	}
	got2, err := svc.AuthorizeChannel(context.Background(), 7, channel, "youtube_pool_b", scopes, token)
	if err != nil {
		t.Fatalf("second AuthorizeChannel (reconnect): %v", err)
	}
	if got1 != oauthConnID || got2 != oauthConnID {
		t.Fatalf("reconnect must reuse the SAME oauth_connection: first=%d second=%d want=%d", got1, got2, oauthConnID)
	}
	if got1 != got2 {
		t.Fatalf("reconnect drifted to a new connection: first=%d second=%d", got1, got2)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

// TestAuthorizeChannel_EmptyClientKeyDefaultsToPoolA pins the legacy
// fallback: a YouTube subject-keyed authorize that carries no pool
// client key (legacy single-client caller) must persist the migration
// 099 default label youtube_pool_a — never an empty string — so the
// refresh side (vault → registry.Resolve) can still resolve it.
func TestAuthorizeChannel_EmptyClientKeyDefaultsToPoolA(t *testing.T) {
	svc, mock, _, cleanup := newSvcHarness(t)
	defer cleanup()

	const userID, oauthConnID int64 = 99, 556
	scopes := []string{"https://www.googleapis.com/auth/youtube.upload"}

	mock.ExpectBegin()
	expectLoadAccount(mock, 7, userID, models.PlatformYouTube, "UC012345678901234567890123", models.AccountStatusPendingAuthorization)
	// clientKey "" must reach the UPSERT as the default youtube_pool_a.
	expectSubjectUpsertOCR(mock, userID, models.PlatformYouTube, "google-subject-1", "UC012345678901234567890123", "youtube_pool_a", scopes, oauthConnID)
	expectInsertTokenTx(mock, true)
	expectPromoteAccount(mock, oauthConnID, 7)
	mock.ExpectCommit()

	got, err := svc.AuthorizeChannel(context.Background(), 7, "UC012345678901234567890123", "", scopes, &models.TokenData{
		AccessToken:       "fresh-access",
		RefreshToken:      "fresh-refresh",
		ProviderSubjectID: "google-subject-1",
		TokenType:         models.TokenTypeBearer,
		ExpiresIn:         3600,
		Scopes:            scopes,
	})
	if err != nil {
		t.Fatalf("AuthorizeChannel: %v", err)
	}
	if got != oauthConnID {
		t.Fatalf("oauth_connection_id: want %d, got %d", oauthConnID, got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}
