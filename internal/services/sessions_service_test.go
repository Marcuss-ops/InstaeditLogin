package services_test

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/golang-jwt/jwt/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

func newTestSessionsService(t *testing.T) (*services.SessionsService, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	cleanup := func() { _ = db.Close() }
	sessionRepo := repository.NewSessionRepository(db)
	jwtMgr := auth.NewManager("test-secret", 1).WithEnv("test")
	svc := services.NewSessionsService(sessionRepo, jwtMgr)
	return svc, mock, cleanup
}

func TestSessionsService_Start_AccessJTIMatchesJWT(t *testing.T) {
	svc, mock, cleanup := newTestSessionsService(t)
	defer cleanup()

	now := time.Now()

	mock.ExpectQuery(
		`INSERT INTO sessions
		   (user_id, workspace_id, token_family_id, access_jti,
		    refresh_token_hash, user_agent, ip_hash, expires_at, refresh_expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id, created_at, last_used_at`,
	).WithArgs(
		int64(1),
		int64(7),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
	).WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "last_used_at"}).
		AddRow(1, now, now))

	result, err := svc.Start(services.StartSessionRequest{
		UserID:      1,
		WorkspaceID: 7,
		UserAgent:   "unit-test",
		IP:          "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if result.AccessToken == "" {
		t.Fatal("expected non-empty access token")
	}
	if result.AccessJTI == "" {
		t.Fatal("expected non-empty access JTI")
	}

	claims := &auth.Claims{}
	_, err = jwt.ParseWithClaims(result.AccessToken, claims, func(_ *jwt.Token) (interface{}, error) {
		return []byte("test-secret"), nil
	})
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}

	if claims.ID != result.AccessJTI {
		t.Fatalf("JWT JTI mismatch: claims.ID=%q, result.AccessJTI=%q", claims.ID, result.AccessJTI)
	}
	if claims.SessionID != 1 {
		t.Fatalf("session id mismatch: got %d, want 1", claims.SessionID)
	}
	if claims.UserID != 1 || claims.WorkspaceID != 7 {
		t.Fatalf("unexpected claims: uid=%d ws=%d", claims.UserID, claims.WorkspaceID)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestSessionsService_IsActive(t *testing.T) {
	svc, mock, cleanup := newTestSessionsService(t)
	defer cleanup()

	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	cases := []struct {
		name     string
		rows     func() *sqlmock.Rows
		expected bool
	}{
		{
			name: "active",
			rows: func() *sqlmock.Rows {
				return sqlmock.NewRows([]string{
					"id", "user_id", "workspace_id", "token_family_id", "access_jti",
					"refresh_token_hash", "user_agent", "ip_hash", "created_at", "expires_at",
					"refresh_expires_at", "last_used_at", "revoked_at", "revoke_reason",
				}).AddRow(1, int64(1), int64(7), "family", "jti", []byte("hash"), "", "", now, future, future, now, nil, "")
			},
			expected: true,
		},
		{
			name: "revoked",
			rows: func() *sqlmock.Rows {
				return sqlmock.NewRows([]string{
					"id", "user_id", "workspace_id", "token_family_id", "access_jti",
					"refresh_token_hash", "user_agent", "ip_hash", "created_at", "expires_at",
					"refresh_expires_at", "last_used_at", "revoked_at", "revoke_reason",
				}).AddRow(1, int64(1), int64(7), "family", "jti", []byte("hash"), "", "", now, future, future, now, &now, "logout")
			},
			expected: false,
		},
		{
			name: "access_expired",
			rows: func() *sqlmock.Rows {
				return sqlmock.NewRows([]string{
					"id", "user_id", "workspace_id", "token_family_id", "access_jti",
					"refresh_token_hash", "user_agent", "ip_hash", "created_at", "expires_at",
					"refresh_expires_at", "last_used_at", "revoked_at", "revoke_reason",
				}).AddRow(1, int64(1), int64(7), "family", "jti", []byte("hash"), "", "", now, past, future, now, nil, "")
			},
			expected: false,
		},
		{
			name: "refresh_expired",
			rows: func() *sqlmock.Rows {
				return sqlmock.NewRows([]string{
					"id", "user_id", "workspace_id", "token_family_id", "access_jti",
					"refresh_token_hash", "user_agent", "ip_hash", "created_at", "expires_at",
					"refresh_expires_at", "last_used_at", "revoked_at", "revoke_reason",
				}).AddRow(1, int64(1), int64(7), "family", "jti", []byte("hash"), "", "", now, future, past, now, nil, "")
			},
			expected: false,
		},
		{
			name: "missing",
			rows: func() *sqlmock.Rows {
				return sqlmock.NewRows([]string{
					"id", "user_id", "workspace_id", "token_family_id", "access_jti",
					"refresh_token_hash", "user_agent", "ip_hash", "created_at", "expires_at",
					"refresh_expires_at", "last_used_at", "revoked_at", "revoke_reason",
				})
			},
			expected: false,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			mock.ExpectQuery(
				`SELECT id, user_id, workspace_id, token_family_id, access_jti,
		        refresh_token_hash, user_agent, COALESCE(ip_hash, ''),
		        created_at, expires_at, refresh_expires_at, last_used_at,
		        revoked_at, COALESCE(revoke_reason, '')
		 FROM sessions
		 WHERE id = $1`,
			).WithArgs(int64(1)).WillReturnRows(c.rows())

			active, err := svc.IsActive(1)
			if err != nil {
				t.Fatalf("IsActive: %v", err)
			}
			if active != c.expected {
				t.Fatalf("IsActive() = %v, want %v", active, c.expected)
			}
		})
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestSessionsService_Refresh_AccessJTIMatchesJWT(t *testing.T) {
	svc, mock, cleanup := newTestSessionsService(t)
	defer cleanup()

	now := time.Now()
	mgr := auth.NewManager("test-secret", 1).WithEnv("test")
	refreshPlain, refreshHash, _, err := mgr.IssueRefresh()
	if err != nil {
		t.Fatalf("IssueRefresh: %v", err)
	}

	// FindByRefreshHash returns the existing session row.
	mock.ExpectQuery(
		`SELECT id, user_id, workspace_id, token_family_id, access_jti,
		        refresh_token_hash, user_agent, COALESCE(ip_hash, ''),
		        created_at, expires_at, refresh_expires_at, last_used_at,
		        revoked_at, COALESCE(revoke_reason, '')
		 FROM sessions
		 WHERE refresh_token_hash = $1`,
	).WithArgs(refreshHash).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "workspace_id", "token_family_id", "access_jti",
			"refresh_token_hash", "user_agent", "ip_hash", "created_at", "expires_at",
			"refresh_expires_at", "last_used_at", "revoked_at", "revoke_reason",
		}).
			AddRow(1, int64(1), int64(7), "family-1", "old-access-jti",
				refreshHash, "unit-test", "", now, now, now, now, nil, ""))

	// Rotate: begin transaction, SELECT FOR UPDATE of the old row.
	mock.ExpectBegin()
	mock.ExpectQuery(
		`SELECT id, user_id, workspace_id, token_family_id, revoked_at,
		        COALESCE(revoke_reason, '')
		 FROM sessions
		 WHERE refresh_token_hash = $1
		 FOR UPDATE`,
	).WithArgs(refreshHash).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "workspace_id", "token_family_id", "revoked_at", "revoke_reason"}).
			AddRow(1, int64(1), int64(7), "family-1", nil, ""))

	// Rotate: mark the old row revoked.
	mock.ExpectExec(
		`UPDATE sessions
		 SET revoked_at = NOW(), revoke_reason = 'rotated'
		 WHERE id = $1`,
	).WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Rotate: insert the new row.
	mock.ExpectQuery(
		`INSERT INTO sessions
		   (user_id, workspace_id, token_family_id, access_jti,
		    refresh_token_hash, user_agent, ip_hash, expires_at, refresh_expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id, created_at, last_used_at`,
	).WithArgs(
		int64(1),
		int64(7),
		"family-1",
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
	).WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "last_used_at"}).
		AddRow(2, now, now))

	mock.ExpectCommit()

	result, err := svc.Refresh(services.RefreshRequest{
		RefreshPlaintext: refreshPlain,
		UserAgent:        "unit-test",
		IP:               "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if result.AccessToken == "" {
		t.Fatal("expected non-empty access token")
	}
	if result.AccessJTI == "" {
		t.Fatal("expected non-empty access JTI")
	}

	claims := &auth.Claims{}
	_, err = jwt.ParseWithClaims(result.AccessToken, claims, func(_ *jwt.Token) (interface{}, error) {
		return []byte("test-secret"), nil
	})
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}

	if claims.ID != result.AccessJTI {
		t.Fatalf("JWT JTI mismatch: claims.ID=%q, result.AccessJTI=%q", claims.ID, result.AccessJTI)
	}
	if claims.SessionID != 2 {
		t.Fatalf("session id mismatch: got %d, want 2", claims.SessionID)
	}
	if claims.UserID != 1 || claims.WorkspaceID != 7 {
		t.Fatalf("unexpected claims: uid=%d ws=%d", claims.UserID, claims.WorkspaceID)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
