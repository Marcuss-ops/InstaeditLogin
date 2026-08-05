package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestAuthorizeChannel_IneligibleStatusRejects(t *testing.T) {
	svc, mock, _, cleanup := newSvcHarness(t)
	defer cleanup()

	const accountID, userID int64 = 13, 200

	mock.ExpectBegin()
	expectLoadAccount(mock, accountID, userID, "youtube", "UCabcdefghijklmnopqrstuv", models.AccountStatusRevoked)
	mock.ExpectRollback()

	_, err := svc.AuthorizeChannel(context.Background(),
		accountID,
		"",
		"",
		nil,
		&models.TokenData{AccessToken: "x", TokenType: models.TokenTypeBearer, ExpiresIn: 60},
	)
	if err == nil {
		t.Fatal("AuthorizeChannel must reject accounts in non-eligible statuses")
	}
	if !strings.Contains(err.Error(), "not eligible for active promotion") {
		t.Errorf("error must mention eligibility gate; got %v", err)
	}
	if !strings.Contains(err.Error(), models.AccountStatusRevoked) {
		t.Errorf("error must surface the offending status; got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

// TestAuthorizeChannel_DisconnectedStatusRejects is the
// symmetric sibling of TestAuthorizeChannel_IneligibleStatusRejects
// for status='disconnected'. It exercises the same authorise-channel
// eligibility gate (production allow-list: pending_authorization,
// active, reauth_required — see channel_authorization.go 'eligible'
// map ~228-232); 'disconnected' is OUTSIDE that allow-list, so the
// gate trips before any UPSERT/INSERT/UPDATE/COMMIT fires. ROLLBACK
// MUST be the last statement — sqlmock.ExpectationsWereMet() catches
// any regression that silently widens the gate to include
// 'disconnected' (e.g., a future operator who disconnects a channel
// mid-flight must not be able to re-promote it via this code path).
func TestAuthorizeChannel_DisconnectedStatusRejects(t *testing.T) {
	svc, mock, _, cleanup := newSvcHarness(t)
	defer cleanup()

	const accountID, userID int64 = 29, 600

	mock.ExpectBegin()
	expectLoadAccount(mock, accountID, userID, "youtube", "UCabcdefghijklmnopqrstuv", models.AccountStatusDisconnected)
	mock.ExpectRollback()

	_, err := svc.AuthorizeChannel(context.Background(),
		accountID,
		"",
		"",
		nil,
		&models.TokenData{AccessToken: "x", TokenType: models.TokenTypeBearer, ExpiresIn: 60},
	)
	if err == nil {
		t.Fatal("AuthorizeChannel must reject accounts in status='disconnected'")
	}
	if !strings.Contains(err.Error(), "not eligible for active promotion") {
		t.Errorf("error must mention eligibility gate; got %v", err)
	}
	if !strings.Contains(err.Error(), models.AccountStatusDisconnected) {
		t.Errorf("error must surface the 'disconnected' status; got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

// TestAuthorizeChannel_ErrorStatusRejects is the symmetric sibling
// for status='error'. An OAuth callback for a row whose prior sync
// walled on a transient (and was stamped 'error' as a sentinel) MUST
// NOT silently flip to active on a fresh code-exchange — the stale
// error context would be lost and the next publish would race the
// same transient that originally stamped 'error'. The eligibility
// gate catches this; the test wires the same BEGIN → load → ROLLBACK
// sequence and asserts both error text and the absence of any
// upsert/INSERT/UPDATE/COMMIT after the load.
func TestAuthorizeChannel_ErrorStatusRejects(t *testing.T) {
	svc, mock, _, cleanup := newSvcHarness(t)
	defer cleanup()

	const accountID, userID int64 = 31, 700

	mock.ExpectBegin()
	expectLoadAccount(mock, accountID, userID, "youtube", "UCabcdefghijklmnopqrstuv", models.AccountStatusError)
	mock.ExpectRollback()

	_, err := svc.AuthorizeChannel(context.Background(),
		accountID,
		"",
		"",
		nil,
		&models.TokenData{AccessToken: "x", TokenType: models.TokenTypeBearer, ExpiresIn: 60},
	)
	if err == nil {
		t.Fatal("AuthorizeChannel must reject accounts in status='error'")
	}
	if !strings.Contains(err.Error(), "not eligible for active promotion") {
		t.Errorf("error must mention eligibility gate; got %v", err)
	}
	if !strings.Contains(err.Error(), models.AccountStatusError) {
		t.Errorf("error must surface the 'error' status; got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

// TestAuthorizeChannel_ReauthFromExpiredStatusRejected is the
// negative sym-pair to TestAuthorizeChannel_ReauthKeepsSameOAuthConnection.
// It anchors the production-code rationale documented at
// AuthorizeChannel (internal/services/channel_authorization.go
// 'eligible' map lines ~228-232 + the rejection return ~234-236):
// 'expired' is intentionally NOT in the eligibility allow-list
// (pending_authorization, active, reauth_required), so the reauth
// path that passes a fresh token MUST still be rejected — a
// regression that widened the allow-list would silently resurrect a
// stale grant whose refresh-token stream has been lost in the worker.
// Surface error must mention both the eligibility gate AND the
// 'expired' status. No upsert / insert / promote / commit fires.
func TestAuthorizeChannel_ReauthFromExpiredStatusRejected(t *testing.T) {
	svc, mock, _, cleanup := newSvcHarness(t)
	defer cleanup()

	const accountID, userID int64 = 23, 400

	mock.ExpectBegin()
	expectLoadAccount(mock, accountID, userID, "youtube", "UCabcdefghijklmnopqrstuv", models.AccountStatusExpired)
	mock.ExpectRollback()

	_, err := svc.AuthorizeChannel(context.Background(),
		accountID,
		"",
		"",
		[]string{"https://www.googleapis.com/auth/youtube.upload"},
		&models.TokenData{AccessToken: "rotated-access", RefreshToken: "rotated-refresh", TokenType: models.TokenTypeBearer, ExpiresIn: 3600},
	)
	if err == nil {
		t.Fatal("AuthorizeChannel must reject reauth from 'expired' status — only reauth_required/active/pending_authorization are allowed")
	}
	if !strings.Contains(err.Error(), "not eligible for active promotion") {
		t.Errorf("error must mention eligibility gate; got %v", err)
	}
	if !strings.Contains(err.Error(), models.AccountStatusExpired) {
		t.Errorf("error must surface the 'expired' status; got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v (BEGIN then load('expired') then ROLLBACK — NO upsert / INSERT / UPDATE / COMMIT after the load)", err)
	}
}

// TestAuthorizeChannel_FirstYouTubeAuthorizationWithoutRefreshTokenRequiresReauth
// proves a new YouTube row cannot be promoted to active without an offline
// refresh grant. The rejection occurs after loading the pending row but before
// the oauth_connections upsert, token insert, or active status update.
func TestAuthorizeChannel_FirstYouTubeAuthorizationWithoutRefreshTokenRequiresReauth(t *testing.T) {
	svc, mock, _, cleanup := newSvcHarness(t)
	defer cleanup()

	const accountID, userID int64 = 37, 900
	mock.ExpectBegin()
	expectLoadAccount(mock, accountID, userID, models.PlatformYouTube, "UCabcdefghijklmnopqrstuv", models.AccountStatusPendingAuthorization)
	mock.ExpectRollback()

	_, err := svc.AuthorizeChannel(context.Background(), accountID, "", "", nil, &models.TokenData{
		AccessToken: "youtube-access",
		TokenType:   models.TokenTypeBearer,
		ExpiresIn:   3600,
	})
	if !errors.Is(err, ErrOAuthRefreshTokenRequired) {
		t.Fatalf("missing first-connection refresh token: want ErrOAuthRefreshTokenRequired, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v (missing refresh must rollback before upsert/insert/active update)", err)
	}
}

// TestAuthorizeChannel_ReconnectWithoutRefreshTokenKeepsExistingGrant verifies
// that a reconnect is not rejected merely because Google omits refresh_token.
// The repository INSERT uses COALESCE to preserve the existing ciphertext.
func TestAuthorizeChannel_ReconnectWithoutRefreshTokenKeepsExistingGrant(t *testing.T) {
	svc, mock, _, cleanup := newSvcHarness(t)
	defer cleanup()

	const accountID, userID, oauthConnID int64 = 38, 901, 556
	scopes := []string{"youtube.upload"}
	mock.ExpectBegin()
	expectLoadAccount(mock, accountID, userID, models.PlatformYouTube, "UCabcdefghijklmnopqrstuv", models.AccountStatusReauthRequired)
	expectUpsertOCR(mock, userID, models.PlatformYouTube, "UCabcdefghijklmnopqrstuv", scopes, oauthConnID)
	expectInsertTokenTx(mock, true)
	expectPromoteAccount(mock, oauthConnID, accountID)
	mock.ExpectCommit()

	got, err := svc.AuthorizeChannel(context.Background(), accountID, "", "", scopes, &models.TokenData{
		AccessToken: "youtube-reconnected-access",
		TokenType:   models.TokenTypeBearer,
		ExpiresIn:   3600,
		Scopes:      scopes,
		// Google omitted RefreshToken. SaveTokenTx must preserve the
		// existing encrypted refresh token through SQL COALESCE.
	})
	if err != nil {
		t.Fatalf("reconnect without refresh token: %v", err)
	}
	if got != oauthConnID {
		t.Fatalf("oauth_connection_id: want %d, got %d", oauthConnID, got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

// TestAuthorizeChannel_MultiTokenAtomicallyPersisted exercises the
// variadic-token path: principal (user long-lived) + supplemental
// (Page access). Both must be saved in the SAME tx; a failure on
// the second token rolls back the first AND the oauth_connections
// row.
