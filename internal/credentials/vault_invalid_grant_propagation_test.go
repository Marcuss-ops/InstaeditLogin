package credentials

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type invalidGrantTxStore struct {
	*mockTokenStore
	called bool
	err    error
}

func (s *invalidGrantTxStore) MarkInvalidGrantTx(_ context.Context, _ *sql.Tx, _ int64, _, _ string) error {
	s.called = true
	return s.err
}

func TestVault_Renew_InvalidGrant_PropagationFailureRollsBack(t *testing.T) {
	v, mock, base := newTestVault(t)
	store := &invalidGrantTxStore{mockTokenStore: base, err: errors.New("propagation failed")}
	v.store = store
	const accountID int64 = 503
	base.seedToken(newEncryptedToken(t, v, accountID, -time.Minute, "refresh-before-revocation"))

	expectOauthConnLookup(mock, accountID, accountID)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).WithArgs(accountID).WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").WithArgs(accountID).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	_, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, func(context.Context, string) (*models.TokenData, error) {
		return nil, fmt.Errorf("wrapped: %w", ErrInvalidGrant)
	})
	if err == nil || !strings.Contains(err.Error(), "propagate invalid_grant state") {
		t.Fatalf("expected propagation failure, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

func TestVault_Renew_InvalidGrant_CommitsGrantWidePropagation(t *testing.T) {
	v, mock, base := newTestVault(t)
	store := &invalidGrantTxStore{mockTokenStore: base}
	v.store = store
	const accountID int64 = 502
	base.seedToken(newEncryptedToken(t, v, accountID, -time.Minute, "refresh-before-revocation"))

	expectOauthConnLookup(mock, accountID, accountID)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).WithArgs(accountID).WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").WithArgs(accountID).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	providerErr := &OAuthTokenError{StatusCode: 400, Code: "invalid_grant", Description: "must stay out of logs"}
	_, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, func(context.Context, string) (*models.TokenData, error) {
		return nil, providerErr
	})
	if err == nil || !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("Renew error: want invalid_grant, got %v", err)
	}
	if !store.called {
		t.Fatal("invalid-grant transactional propagation was not called")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}
