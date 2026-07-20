package services

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// serviceTime is a sentinel time for sqlmock WillReturnRows created_at
// columns. The production TokenRepository.SaveTokenTx scans the row
// into *time.Time — passing nil through AddRow makes sql.Scan fail
// (it can't decode NULL into a *time.Time that doesn't implement
// sql.Scanner). Tests that exercise SaveTokenTx through sqlmock set
// created_at = serviceTime so the scan target is a real time.Time;
// the actual value is not asserted.
var serviceTime = time.Date(2026, 7, 20, 18, 0, 0, 0, time.UTC)

// ---- helpers --------------------------------------------------------------

// fakeBinder captures the (accessToken, expectedChannelID) pair the
// service passes through ValidateChannelBinding so the test can assert
// the channel pre-flight was run. validateErr is the error to return.
type fakeBinder struct {
	name            string
	validateCalls   atomic.Int32
	lastAccessToken atomic.Value
	lastExpected    atomic.Value
	validateErr     error
}

func (b *fakeBinder) Name() string         { return b.name }
func (b *fakeBinder) provideName() string  { return b.name } // satisfies any near-interface typo guard

func (b *fakeBinder) ValidateChannelBinding(_ context.Context, accessToken, expectedChannelID string) error {
	b.validateCalls.Add(1)
	b.lastAccessToken.Store(accessToken)
	b.lastExpected.Store(expectedChannelID)
	return b.validateErr
}

// var _ YouTubeChannelBinder enforces at compile time that fakeBinder
// satisfies the production capability interface. Any future drift in
// the interface (e.g. adding a third method) fails the build here
// instead of breaking the service test at runtime.
var _ YouTubeChannelBinder = (*fakeBinder)(nil)

// newSvcHarness builds a fresh sqlmock DB, a real TokenRepository
// wired against that same DB, the production Encryptor (deterministic
// test key), and the service. The cleanup func closes the sqlmock DB.
func newSvcHarness(t *testing.T) (*ChannelAuthorizationService, sqlmock.Sqlmock, *fakeBinder, func()) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	enc, err := crypto.NewEncryptor(1, map[uint32]string{1: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="})
	if err != nil {
		t.Fatalf("crypto.NewEncryptor: %v", err)
	}
	repo := repository.NewTokenRepository(db)
	binder := &fakeBinder{name: "fake-youtube"}
	svc := NewChannelAuthorizationService(db, enc, repo, binder)
	return svc, mock, binder, func() { _ = db.Close() }
}

// expectLoadAccount is the SELECT platform_accounts WHERE id=$1 step.
func expectLoadAccount(mock sqlmock.Sqlmock, id, userID int64, platform, platformUserID, status string) {
	mock.ExpectQuery(`SELECT platform, platform_user_id, user_id, status
		   FROM platform_accounts
		  WHERE id = $1`).
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "platform_user_id", "user_id", "status"}).
			AddRow(platform, platformUserID, userID, status))
}

// expectUpsertOCR is the INSERT ... RETURNING id step for
// oauth_connections.
func expectUpsertOCR(mock sqlmock.Sqlmock, userID int64, provider, puID string, scopes []string, returnsID int64) {
	mock.ExpectQuery(
		`INSERT INTO oauth_connections (user_id, provider, provider_resource_id, scopes, last_validated_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (user_id, provider, provider_resource_id)
		 DO UPDATE SET scopes = EXCLUDED.scopes,
		               last_validated_at = NOW(),
		               updated_at = NOW()
		 RETURNING id`,
	).
		WithArgs(userID, provider, puID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(returnsID))
}

// expectInsertTokenTx is the real INSERT inside TokenRepository.SaveTokenTx.
// It returns an empty id from RETURNING because the service does NOT
// require the inserted id to be propagated back to the Token row (the
// flow stamps ID but the service ignores it after).
func expectInsertTokenTx(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`INSERT INTO tokens (platform_account_id, oauth_connection_id, token_type, encrypted_token, encrypted_refresh_token, expires_at, scopes)
	 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at`).
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(1), serviceTime))
	// Pruner (DELETE older rows same oauth_connection_id + token_type).
	mock.ExpectExec(`DELETE FROM tokens WHERE oauth_connection_id = $1 AND token_type = $2 AND id <> $3`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
}

// expectPromoteAccount is the UPDATE platform_accounts step.
func expectPromoteAccount(mock sqlmock.Sqlmock, oauthConnID, accountID int64) {
	mock.ExpectExec(`UPDATE platform_accounts
		    SET oauth_connection_id = $1,
		        status             = 'active',
		        connected_at       = NOW(),
		        last_validated_at  = NOW(),
		        reauth_required_at = NULL,
		        last_error_code    = NULL,
		        last_error_message = NULL,
		        updated_at         = NOW()
		  WHERE id = $2`).
		WithArgs(oauthConnID, accountID).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

// ---- tests ----------------------------------------------------------------

// TestAuthorizeChannel_HappyPath is the canonical happy path:
// pending_authorization + token + expectedChannelID matching → return
// oauthConnID and issue BEGIN / load / upsertOCR / insertToken /
// promote / COMMIT in that exact order. No ROLLBACK.
func TestAuthorizeChannel_HappyPath(t *testing.T) {
	svc, mock, binder, cleanup := newSvcHarness(t)
	defer cleanup()

	const accountID, userID int64 = 7, 99
	const oauthConnID int64 = 555

	mock.ExpectBegin()
	expectLoadAccount(mock, accountID, userID, "youtube", "UCabcdefghijklmnopqrstuv", models.AccountStatusPendingAuthorization)
	expectUpsertOCR(mock, userID, "youtube", "UCabcdefghijklmnopqrstuv", []string{"https://www.googleapis.com/auth/youtube.upload"}, oauthConnID)
	expectInsertTokenTx(mock)
	expectPromoteAccount(mock, oauthConnID, accountID)
	mock.ExpectCommit()

	got, err := svc.AuthorizeChannel(context.Background(),
		accountID,
		"UCabcdefghijklmnopqrstuv",
		[]string{"https://www.googleapis.com/auth/youtube.upload"},
		&models.TokenData{
			AccessToken:  "fresh-access",
			RefreshToken: "fresh-refresh",
			TokenType:    models.TokenTypeBearer,
			ExpiresIn:    3600,
			Scopes:       []string{"youtube.upload"},
		},
	)
	if err != nil {
		t.Fatalf("AuthorizeChannel: %v", err)
	}
	if got != oauthConnID {
		t.Errorf("returned oauth_connection_id: want %d, got %d", oauthConnID, got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
	if calls := binder.validateCalls.Load(); calls != 1 {
		t.Errorf("binder.ValidateChannelBinding calls: want 1, got %d", calls)
	}
	if got, want := binder.lastExpected.Load().(string), "UCabcdefghijklmnopqrstuv"; got != want {
		t.Errorf("binder received expected channel: want %q, got %q", want, got)
	}
	if got, want := binder.lastAccessToken.Load().(string), "fresh-access"; got != want {
		t.Errorf("binder received access token: want %q, got %q", want, got)
	}
}

// TestAcceptance_VaultFailureRollsBackAndStatusNotFlipped is the
// ACCEPTANCE TEST the user spec explicitly requires: when the
// encrypted-token INSERT fails mid-flow, the platform_account MUST
// NOT flip to 'active'. The test asserts this both by SQL sequence
// (no UPDATE platform_accounts after ROLLBACK) and by observing
// the returned error wraps the token-store failure.
func TestAcceptance_VaultFailureRollsBackAndStatusNotFlipped(t *testing.T) {
	svc, mock, binder, cleanup := newSvcHarness(t)
	defer cleanup()

	const accountID, userID int64 = 9, 100

	mock.ExpectBegin()
	expectLoadAccount(mock, accountID, userID, "youtube", "UCabcdefghijklmnopqrstuv", models.AccountStatusPendingAuthorization)
	expectUpsertOCR(mock, userID, "youtube", "UCabcdefghijklmnopqrstuv", []string{"https://www.googleapis.com/auth/youtube.upload"}, 777)
	// The token INSERT fails. EXACTLY here. No UPDATE on
	// platform_accounts follows — sqlmock's lack of an
	// ExpectExec for the UPDATE + ExpectCommit would catch a
	// regression where the service issues them anyway.
	mock.ExpectQuery(`INSERT INTO tokens (platform_account_id, oauth_connection_id, token_type, encrypted_token, encrypted_refresh_token, expires_at, scopes)
	 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at`).
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnError(errors.New("simulated token write failure"))
	mock.ExpectRollback()

	_, err := svc.AuthorizeChannel(context.Background(),
		accountID,
		"UCabcdefghijklmnopqrstuv",
		[]string{"https://www.googleapis.com/auth/youtube.upload"},
		&models.TokenData{
			AccessToken: "fresh-access",
			TokenType:   models.TokenTypeBearer,
			ExpiresIn:   3600,
		},
	)
	if err == nil {
		t.Fatal("AuthorizeChannel must propagate the token write failure")
	}
	if !strings.Contains(err.Error(), "simulated token write failure") {
		t.Errorf("error must wrap the underlying token-store failure; got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v (ROLLBACK MUST be the last statement; no UPDATE or COMMIT after the failed INSERT)", err)
	}
	// Binder ran exactly once (the channel guard is part of the
	// atomic flow — even when downstream fails, the check executes).
	if calls := binder.validateCalls.Load(); calls != 1 {
		t.Errorf("binder.ValidateChannelBinding calls: want 1 (pre-tx guard), got %d", calls)
	}
}

// TestAcceptance_VaultFailureWithEmptyExpectedChannel is the same
// guarantee in the no-binder path (non-YouTube provider, empty
// expectedChannelID): a token-write failure aborts the flip. The
// binder is nil and VerifyChannelBinding must NOT run.
func TestAcceptance_VaultFailureWithNilBinder(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	enc, err := crypto.NewEncryptor(1, map[uint32]string{1: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="})
	if err != nil {
		t.Fatalf("crypto.NewEncryptor: %v", err)
	}
	repo := repository.NewTokenRepository(db)
	svc := NewChannelAuthorizationService(db, enc, repo, nil) // nil binder — non-YouTube path

	mock.ExpectBegin()
	expectLoadAccount(mock, 11, 123, "facebook", "1234567890", models.AccountStatusPendingAuthorization)
	expectUpsertOCR(mock, 123, "facebook", "1234567890", []string{"pages_show_list"}, 888)
	mock.ExpectQuery(`INSERT INTO tokens (platform_account_id, oauth_connection_id, token_type, encrypted_token, encrypted_refresh_token, expires_at, scopes)
	 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at`).
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnError(errors.New("crash"))
	mock.ExpectRollback()

	_, err = svc.AuthorizeChannel(context.Background(),
		11,
		"", // no expectedChannelID — binder path skipped
		[]string{"pages_show_list"},
		&models.TokenData{
			AccessToken: "user-token",
			TokenType:   models.TokenTypeLongLived,
			ExpiresIn:   86400,
		},
	)
	if err == nil {
		t.Fatal("AuthorizeChannel must propagate failure")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

// TestAuthorizeChannel_ChannelMismatchPreTxExit asserts the
// channels.list guard rejection path is FAST — no BEGIN, no DB
// writes, no token encryption, no token SQL.
func TestAuthorizeChannel_ChannelMismatchPreTxExit(t *testing.T) {
	svc, mock, binder, cleanup := newSvcHarness(t)
	defer cleanup()
	binder.validateErr = ErrYouTubeChannelMismatch

	_, err := svc.AuthorizeChannel(context.Background(),
		1,
		"UCaaaaaaaaaaaaaaaaaaaaaZ", // wrong channel
		nil,
		&models.TokenData{AccessToken: "bearer", TokenType: models.TokenTypeBearer, ExpiresIn: 60},
	)
	if err == nil {
		t.Fatal("AuthorizeChannel must error on channel mismatch")
	}
	if !errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("error chain must include ErrYouTubeChannelMismatch; got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v (mismatch must abort BEFORE BEGIN)", err)
	}
}

// TestAuthorizeChannel_IneligibleStatusRejects proves the eligibility
// gate: an 'expired' / 'revoked' / 'disconnected' / 'error' row must
// NOT silently flip to active. The surface error must mention the
// offending status.
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

// TestAuthorizeChannel_MultiTokenAtomicallyPersisted exercises the
// variadic-token path: principal (user long-lived) + supplemental
// (Page access). Both must be saved in the SAME tx; a failure on
// the second token rolls back the first AND the oauth_connections
// row.
func TestAuthorizeChannel_MultiTokenAtomicallyPersisted(t *testing.T) {
	svc, mock, _, cleanup := newSvcHarness(t)
	defer cleanup()

	const accountID, userID int64 = 17, 300
	const oauthConnID int64 = 999

	mock.ExpectBegin()
	expectLoadAccount(mock, accountID, userID, "facebook", "1234567890", models.AccountStatusPendingAuthorization)
	expectUpsertOCR(mock, userID, "facebook", "1234567890", []string{"pages_show_list"}, oauthConnID)
	expectInsertTokenTx(mock)
	expectInsertTokenTx(mock)
	expectPromoteAccount(mock, oauthConnID, accountID)
	mock.ExpectCommit()

	got, err := svc.AuthorizeChannel(context.Background(),
		accountID,
		"", // Facebook flow has no channels.list check
		[]string{"pages_show_list"},
		&models.TokenData{ // principal user token (long-lived)
			AccessToken: "user-token",
			TokenType:   models.TokenTypeLongLived,
			ExpiresIn:   60 * 24 * 3600,
		},
		&models.TokenData{ // supplemental Page token
			AccessToken: "page-token",
			TokenType:   models.TokenTypePageAccess,
			ExpiresIn:   60 * 24 * 3600,
		},
	)
	if err != nil {
		t.Fatalf("AuthorizeChannel: %v", err)
	}
	if got != oauthConnID {
		t.Errorf("returned oauth_connection_id: want %d, got %d", oauthConnID, got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

// TestAuthorizeChannel_SecondTokenFailureRollsBackFirstAndOCR is the
// negative half of the multi-token acceptance: a failure on the
// SECOND INSERT must roll back the first row AND the oauth_connections
// row. No UPDATE on platform_accounts, no COMMIT.
func TestAuthorizeChannel_SecondTokenFailureRollsBackFirstAndOCR(t *testing.T) {
	svc, mock, _, cleanup := newSvcHarness(t)
	defer cleanup()

	const accountID, userID int64 = 19, 350

	mock.ExpectBegin()
	expectLoadAccount(mock, accountID, userID, "facebook", "1234567890", models.AccountStatusPendingAuthorization)
	expectUpsertOCR(mock, userID, "facebook", "1234567890", []string{"pages_show_list"}, 1111)
	// First token: succeeds.
	expectInsertTokenTx(mock)
	// Second token's INSERT: fails.
	mock.ExpectQuery(`INSERT INTO tokens (platform_account_id, oauth_connection_id, token_type, encrypted_token, encrypted_refresh_token, expires_at, scopes)
	 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at`).
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnError(errors.New("page token write crash"))
	mock.ExpectRollback()

	_, err := svc.AuthorizeChannel(context.Background(),
		accountID,
		"",
		nil,
		&models.TokenData{AccessToken: "user-token", TokenType: models.TokenTypeLongLived, ExpiresIn: 86400},
		&models.TokenData{AccessToken: "page-token", TokenType: models.TokenTypePageAccess, ExpiresIn: 86400},
	)
	if err == nil {
		t.Fatal("AuthorizeChannel must propagate the second-token failure")
	}
	if !strings.Contains(err.Error(), "page token write crash") {
		t.Errorf("error must wrap the second-token failure; got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}
