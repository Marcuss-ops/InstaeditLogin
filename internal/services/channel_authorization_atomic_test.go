package services

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestAuthorizeChannel_MultiTokenAtomicallyPersisted(t *testing.T) {
	svc, mock, _, cleanup := newSvcHarness(t)
	defer cleanup()

	const accountID, userID int64 = 17, 300
	const oauthConnID int64 = 999

	mock.ExpectBegin()
	expectLoadAccount(mock, accountID, userID, "facebook", "1234567890", models.AccountStatusPendingAuthorization)
	expectUpsertOCR(mock, userID, "facebook", "1234567890", []string{"pages_show_list"}, oauthConnID)
	expectInsertTokenTx(mock, false)
	expectInsertTokenTx(mock, false)
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
	expectInsertTokenTx(mock, false)
	// Second token's INSERT: fails.
	mock.ExpectQuery(expectTokenInsertSQL).
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

// TestAuthorizeChannel_ReauthKeepsSameOAuthConnection exercises the
// "doppia autorizzazione stesso canale" hardening requirement: when
// the operator re-runs the OAuth dance for a channel that is already
// 'active' (refresh-token rotation), the atomic flow MUST:
//
//  1. Reuse the SAME oauth_connections row (UPSERT keyed on the
//     (user_id, provider, provider_resource_id) tuple returns the
//     existing id, not a brand-new one).
//  2. Fire the token pruner DELETE on the older tokens so the new
//     grant does not silently accumulate.
//  3. Re-promote the platform_account (status='active' is a stable
//     re-write — same status, refreshed connected_at + last_validated_at).
//
// If the UPSERT returned a NEW id, refresh-token rotation would
// silently orphan the previous oauth_connection and leave two rows
// for the same platform_user_id — a 'refresh-tokened channel now has
// credentials stored against TWO oauth_connection rows' bug class.
// The test asserts both that the function returns the same id from
// both calls AND that the second call's transition is from
// 'active' (per the eligibility gate), not 'pending_authorization'.
func TestAuthorizeChannel_ReauthKeepsSameOAuthConnection(t *testing.T) {
	svc, mock, _, cleanup := newSvcHarness(t)
	defer cleanup()

	const accountID, userID, oauthConnID int64 = 7, 99, 555
	scopes := []string{"https://www.googleapis.com/auth/youtube.upload"}
	token := &models.TokenData{
		AccessToken:  "rotated-access",
		RefreshToken: "rotated-refresh",
		TokenType:    models.TokenTypeBearer,
		ExpiresIn:    3600,
		Scopes:       scopes,
	}

	// First pass: from pending_authorization → active.
	mock.ExpectBegin()
	expectLoadAccount(mock, accountID, userID, "youtube", "UCabcdefghijklmnopqrstuv", models.AccountStatusPendingAuthorization)
	expectUpsertOCR(mock, userID, "youtube", "UCabcdefghijklmnopqrstuv", scopes, oauthConnID)
	expectInsertTokenTx(mock, true)
	expectPromoteAccount(mock, oauthConnID, accountID)
	mock.ExpectCommit()

	// Second pass: from active → active (refresh-token rotation).
	// ExpectUpsertOCR returns the SAME oauthConnID (555) to signal
	// the UPSERT keyed on the natural composite key recognised
	// the existing row.
	mock.ExpectBegin()
	expectLoadAccount(mock, accountID, userID, "youtube", "UCabcdefghijklmnopqrstuv", models.AccountStatusActive)
	expectUpsertOCR(mock, userID, "youtube", "UCabcdefghijklmnopqrstuv", scopes, oauthConnID)
	expectInsertTokenTx(mock, true) // includes pruner DELETE
	expectPromoteAccount(mock, oauthConnID, accountID)
	mock.ExpectCommit()

	got1, err1 := svc.AuthorizeChannel(context.Background(),
		accountID, "", scopes, token,
	)
	if err1 != nil {
		t.Fatalf("first AuthorizeChannel: %v", err1)
	}
	if got1 != oauthConnID {
		t.Fatalf("first AuthorizeChannel returned oauth_connection_id: want %d, got %d", oauthConnID, got1)
	}

	got2, err2 := svc.AuthorizeChannel(context.Background(),
		accountID, "", scopes, token,
	)
	if err2 != nil {
		t.Fatalf("second AuthorizeChannel (re-auth): %v", err2)
	}
	if got2 != oauthConnID {
		t.Fatalf("second AuthorizeChannel returned oauth_connection_id: want %d (SAME as first — refresh-token rotation MUST reuse the row), got %d", oauthConnID, got2)
	}
	if got1 != got2 {
		t.Errorf("re-auth drifted away from first oauth_connection_id: first=%d, second=%d", got1, got2)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v (the second flow MUST reuse the same UPSERT SQL (same ocr ID) AND fire the pruner DELETE)", err)
	}
}

// TestAuthorizeChannel_EligibilityGateActuallyCalled_RejectsInlineMapRegression
// closes the SECOND class of regression that the file-level var _
// compile-time guard + the 4 status-rejection integration tests
// cannot catch at their respective layers:
//
//	"A future refactor replaces the production `eligibilityGate(...)`
//	 call site with an inline `eligible := map[string]bool{...}` +
//	 `if !eligible[currentStatus]` block, while leaving the
//	 `var _ = IsEligibleForActivePromotion` package-level
//	 reference AND the production initial value of `eligibilityGate
//	 = IsEligibleForActivePromotion` untouched."
//
// Why a NEW test:
//   - var _ guard catches wholesale reference deletion (compile-time).
//   - 5 status-rejection integration tests assert REJECTION behaviour
//     but not WHICH gate produced it; an inline map would re-reject
//     the same statuses and they pass transparently.
//   - This test swaps `eligibilityGate` for a spy at test time and
//     asserts the spy was invoked with the loaded status. If
//     AuthorizeChannel ever routes around the pointer indirection,
//     the spy is NEVER called and this test fails (TEST LAYER
//     CATCHES THE REWIRE-AT-CALL-SITE REGRESSION CLASS).
//
// Mechanic: the spy stores `called=true` + the status argument, then
// returns false to short-circuit AuthorizeChannel through its
// eligibility-rejection branch (no UPSERT/INSERT/UPDATE/COMMIT fires;
// sqlmock catches a regression that accidentally proceeds anyway).
//
// We deliberately load a status the REAL allow-list ACCEPTS
// (pending_authorization) so the test asserts positive routing — if
// production routed through the spy, we'd see called=true; if it
// doesn't, called=false regardless of which status the load
// returned. Loading 'pending_authorization' as an input that should
// pass makes the spy the SOLE red signal: a regression that returns
// true from the inline map AND also bypasses the spy would still
// fail because called=false.
//
// defer-restore pattern is mandatory: `eligibilityGate` is package-
// level mutable state; a panic deep inside AuthorizeChannel must not
// leak the spy to subsequent tests. The other tests in this file do
// not touch `eligibilityGate`, so even without defer-restore the
// test would not corrupt them, but the defer makes the contract
// explicit.
func TestAuthorizeChannel_EligibilityGateActuallyCalled_RejectsInlineMapRegression(t *testing.T) {
	svc, mock, _, cleanup := newSvcHarness(t)
	defer cleanup()

	const accountID, userID int64 = 41, 800

	// Spy: records invocation + the status argument the gate saw.
	// Returns false unconditionally — the status we load is one the
	// REAL allow-list accepts (pending_authorization), so the spy is
	// the SOLE reason AuthorizeChannel rejects via this code path.
	var (
		spyCalled atomic.Int32
		spyArg    atomic.Value
	)
	origGate := eligibilityGate
	eligibilityGate = func(status string) bool {
		spyCalled.Add(1)
		spyArg.Store(status)
		return false
	}
	defer func() { eligibilityGate = origGate }()

	mock.ExpectBegin()
	expectLoadAccount(mock, accountID, userID, "youtube", "UCabcdefghijklmnopqrstuv", models.AccountStatusPendingAuthorization)
	mock.ExpectRollback()

	_, err := svc.AuthorizeChannel(context.Background(),
		accountID,
		"", // no expectedChannelID — binder path skipped
		nil,
		&models.TokenData{AccessToken: "x", TokenType: models.TokenTypeBearer, ExpiresIn: 60},
	)
	if err == nil {
		t.Fatal("AuthorizeChannel must reject (spy returns false regardless of status)")
	}
	if !strings.Contains(err.Error(), "not eligible for active promotion") {
		t.Errorf("error must mention eligibility gate; got %v", err)
	}
	if !strings.Contains(err.Error(), models.AccountStatusPendingAuthorization) {
		t.Errorf("error must surface the loaded status %q — proves AuthorizeChannel routed the loaded row through the gate; got %v",
			models.AccountStatusPendingAuthorization, err)
	}
	if calls := spyCalled.Load(); calls != 1 {
		t.Fatalf("eligibilityGate spy invocation count: want 1, got %d — production code did NOT route through the eligibilityGate package variable (inline-map regression?)", calls)
	}
	if got, want := spyArg.Load().(string), models.AccountStatusPendingAuthorization; got != want {
		t.Errorf("eligibilityGate spy received status: want %q, got %q (status passed through AuthorizeChannel into the gate mismatch)", want, got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v (load then ROLLBACK — NO upsert / INSERT / UPDATE / COMMIT after the spy rejection)", err)
	}
}
