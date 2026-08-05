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

// TestAuthorizeChannel_DoD_Reconnect10Times_SingleActiveConnection
// certifies the reconnect DoD line at the service layer: re-linking the
// SAME channel 10 times (alternating pools A/B) must return the SAME
// oauth_connection_id every time — one channel → one active
// connection → one canonical refresh token, never a pile of parallel
// grants. Each pass issues exactly one subject-keyed UPSERT (the
// DO UPDATE SET flips oauth_client_key in place); sqlmock's
// byte-exact expectations would fail if a second connection row were
// ever inserted.
func TestAuthorizeChannel_DoD_Reconnect10Times_SingleActiveConnection(t *testing.T) {
	svc, mock, _, cleanup := newSvcHarness(t)
	defer cleanup()

	const userID, oauthConnID int64 = 99, 777
	const channel = "UC012345678901234567890123"
	scopes := []string{"https://www.googleapis.com/auth/youtube.upload"}

	// 10 reconnect passes. The first pass promotes the account from
	// pending_authorization; every later pass finds it already active.
	for i := 0; i < 10; i++ {
		key := "youtube_pool_a"
		if i%2 == 1 {
			key = "youtube_pool_b"
		}
		status := models.AccountStatusPendingAuthorization
		if i > 0 {
			status = models.AccountStatusActive
		}
		mock.ExpectBegin()
		expectLoadAccount(mock, 7, userID, models.PlatformYouTube, channel, status)
		expectSubjectUpsertOCR(mock, userID, models.PlatformYouTube, "google-subject-1", channel, key, scopes, oauthConnID)
		expectInsertTokenTx(mock, true)
		expectPromoteAccount(mock, oauthConnID, 7)
		mock.ExpectCommit()
	}

	token := &models.TokenData{
		AccessToken:       "fresh-access",
		RefreshToken:      "fresh-refresh",
		ProviderSubjectID: "google-subject-1",
		TokenType:         models.TokenTypeBearer,
		ExpiresIn:         3600,
		Scopes:            scopes,
	}

	prev := int64(0)
	for i := 0; i < 10; i++ {
		key := "youtube_pool_a"
		if i%2 == 1 {
			key = "youtube_pool_b"
		}
		got, err := svc.AuthorizeChannel(context.Background(), 7, channel, key, scopes, token)
		if err != nil {
			t.Fatalf("reconnect pass %d: %v", i+1, err)
		}
		if got != oauthConnID {
			t.Fatalf("reconnect pass %d returned oauth_connection_id=%d want %d — a second grant was created", i+1, got, oauthConnID)
		}
		if i > 0 && got != prev {
			t.Fatalf("connection id drifted between passes: pass %d=%d pass %d=%d", i, prev, i+1, got)
		}
		prev = got
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v (10 reconnects must produce exactly 10 single-row upserts, no duplicate grants)", err)
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
