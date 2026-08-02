package services

import (
	"context"
	"errors"
	"strings"
	"testing"

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
func TestAuthorizeChannel_HappyPath(t *testing.T) {
	svc, mock, binder, cleanup := newSvcHarness(t)
	defer cleanup()

	const accountID, userID int64 = 7, 99
	const oauthConnID int64 = 555

	mock.ExpectBegin()
	expectLoadAccount(mock, accountID, userID, "youtube", "UCabcdefghijklmnopqrstuv", models.AccountStatusPendingAuthorization)
	expectUpsertOCR(mock, userID, "youtube", "UCabcdefghijklmnopqrstuv", []string{"https://www.googleapis.com/auth/youtube.upload"}, oauthConnID)
	expectInsertTokenTx(mock, true)
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

// TestAuthorizeChannel_YouTubeSubjectSharesGrantAcrossChannels pins the
// modern YouTube path: different platform-account resources use the same
// stable Google subject and therefore execute a subject-keyed OAuth upsert.
func TestAuthorizeChannel_YouTubeSubjectSharesGrantAcrossChannels(t *testing.T) {
	svc, mock, _, cleanup := newSvcHarness(t)
	defer cleanup()

	const userID, oauthConnID int64 = 99, 555
	scopes := []string{"https://www.googleapis.com/auth/youtube.upload"}

	for _, account := range []struct {
		id      int64
		channel string
		status  string
	}{
		{7, "UCchannelA", models.AccountStatusPendingAuthorization},
		{8, "UCchannelB", models.AccountStatusPendingAuthorization},
	} {
		mock.ExpectBegin()
		expectLoadAccount(mock, account.id, userID, models.PlatformYouTube, account.channel, account.status)
		expectSubjectUpsertOCR(mock, userID, models.PlatformYouTube, "google-subject-1", account.channel, scopes, oauthConnID)
		expectInsertTokenTx(mock, true)
		expectPromoteAccount(mock, oauthConnID, account.id)
		mock.ExpectCommit()

		got, err := svc.AuthorizeChannel(context.Background(), account.id, account.channel, scopes, &models.TokenData{
			AccessToken:       "fresh-access-" + account.channel,
			RefreshToken:      "fresh-refresh",
			ProviderSubjectID: "google-subject-1",
			TokenType:         models.TokenTypeBearer,
			ExpiresIn:         3600,
			Scopes:            scopes,
		})
		if err != nil {
			t.Fatalf("AuthorizeChannel(%s): %v", account.channel, err)
		}
		if got != oauthConnID {
			t.Fatalf("AuthorizeChannel(%s) returned oauth_connection_id=%d want %d", account.channel, got, oauthConnID)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
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
	mock.ExpectQuery(expectTokenInsertSQL).
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
			AccessToken:  "fresh-access",
			RefreshToken: "fresh-refresh",
			TokenType:    models.TokenTypeBearer,
			ExpiresIn:    3600,
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
	mock.ExpectQuery(expectTokenInsertSQL).
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
