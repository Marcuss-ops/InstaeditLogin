package services

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

var serviceTime = time.Date(2026, 7, 20, 18, 0, 0, 0, time.UTC)

const expectTokenInsertSQL = `INSERT INTO tokens (
                platform_account_id, oauth_connection_id, token_type,
                encrypted_access_token, encrypted_token, encrypted_refresh_token,
                access_token_expires_at, expires_at, refresh_token_expires_at)
         VALUES (NULLIF($1::BIGINT, 0), $2::BIGINT, $3::VARCHAR, $4::BYTEA, $4::BYTEA,
                 COALESCE($5::BYTEA, (SELECT encrypted_refresh_token FROM tokens WHERE oauth_connection_id = $2::BIGINT AND token_type = $3::VARCHAR ORDER BY created_at DESC LIMIT 1)),
                 $6::TIMESTAMPTZ, $6::TIMESTAMPTZ,
                 COALESCE($7::TIMESTAMPTZ, (SELECT refresh_token_expires_at FROM tokens WHERE oauth_connection_id = $2::BIGINT AND token_type = $3::VARCHAR ORDER BY created_at DESC LIMIT 1)))
         ON CONFLICT (oauth_connection_id, token_type) DO UPDATE SET
                 platform_account_id = EXCLUDED.platform_account_id,
                 encrypted_access_token = EXCLUDED.encrypted_access_token,
                 encrypted_token = EXCLUDED.encrypted_token,
                 encrypted_refresh_token = COALESCE(EXCLUDED.encrypted_refresh_token, tokens.encrypted_refresh_token),
                 access_token_expires_at = EXCLUDED.access_token_expires_at,
                 expires_at = EXCLUDED.expires_at,
                 refresh_token_expires_at = COALESCE(EXCLUDED.refresh_token_expires_at, tokens.refresh_token_expires_at)
         RETURNING id, created_at`

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

func (b *fakeBinder) Name() string        { return b.name }
func (b *fakeBinder) provideName() string { return b.name } // satisfies any near-interface typo guard

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
		`INSERT INTO oauth_connections (user_id, provider, provider_resource_id, scopes, granted_scopes, last_validated_at)
		 VALUES ($1, $2, $3, $4, $4, NOW())
		 ON CONFLICT (user_id, provider, provider_resource_id) WHERE provider_subject_id = ''
		 DO UPDATE SET scopes = EXCLUDED.scopes,
		               granted_scopes = EXCLUDED.granted_scopes,
		               last_validated_at = NOW(),
		               updated_at = NOW()
		 RETURNING id`,
	).
		WithArgs(userID, provider, puID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(returnsID))
}

func expectSubjectUpsertOCR(mock sqlmock.Sqlmock, userID int64, provider, subject, resource string, scopes []string, returnsID int64) {
	mock.ExpectQuery(
		`INSERT INTO oauth_connections (user_id, provider, provider_subject_id, provider_resource_id, scopes, granted_scopes, last_validated_at)
		 VALUES ($1, $2, $3, $4, $5, $5, NOW())
		 ON CONFLICT (user_id, provider, provider_subject_id) WHERE provider_subject_id <> ''
		 DO UPDATE SET provider_resource_id = EXCLUDED.provider_resource_id,
		               scopes = EXCLUDED.scopes,
		               granted_scopes = EXCLUDED.granted_scopes,
		               last_validated_at = NOW(),
		               updated_at = NOW()
		 RETURNING id`,
	).
		WithArgs(userID, provider, subject, resource, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(returnsID))
}

// expectInsertTokenTx is the real INSERT inside TokenRepository.SaveTokenTx.
// Grant-scoped tokens pass platform_account_id=0, which the repository
// converts to SQL NULL via NULLIF. It returns an empty id from RETURNING
// because the service does NOT
// require the inserted id to be propagated back to the Token row (the
// flow stamps ID but the service ignores it after).
func expectInsertTokenTx(mock sqlmock.Sqlmock, expectScopes bool) {
	mock.ExpectQuery(expectTokenInsertSQL).
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(1), serviceTime))
	if expectScopes {
		mock.ExpectExec(`UPDATE oauth_connections SET granted_scopes = $2, scopes = $2, updated_at = NOW() WHERE id = $1`).
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
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
