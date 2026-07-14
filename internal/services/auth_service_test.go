package services_test

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"golang.org/x/crypto/bcrypt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// newTestAuthService creates an AuthService wired with sqlmock for
// the user + workspace + team repositories. Register now
// auto-creates a Personal Workspace + admin membership, and Login
// resolves the user's active workspace via
// workspaceRepo.ListByOwner / teamRepo.ListForUser. Every test in
// this file runs against the same shape.
func newTestAuthService(t *testing.T) (*services.AuthService, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	cleanup := func() { _ = db.Close() }
	userRepo := repository.NewUserRepository(db)
	workspaceRepo := repository.NewWorkspaceRepository(db)
	teamRepo := repository.NewTeamRepository(db)
	svc := services.NewAuthService(userRepo, workspaceRepo, teamRepo)
	return svc, mock, cleanup
}

// workspaceCreatePlaceholder is the INSERT pattern emitted by
// workspaceRepo.Create (mirrors the SQL in
// internal/repository/workspace_repo.go). DRY'd here because the
// same expectation appears in Register tests.
const workspaceCreatePlaceholder = `INSERT INTO workspaces (name, owner_id) VALUES ($1, $2)
	 RETURNING id, created_at`

func TestAuthService_Register_WeakPassword(t *testing.T) {
	svc, _, cleanup := newTestAuthService(t)
	defer cleanup()
	// Weak-password path fires BEFORE the repo is touched, so no
	// sqlmock expectations are required.
	_, _, err := svc.Register("test@example.com", "abc", "Test")
	if err != services.ErrPasswordTooShort {
		t.Errorf("want ErrPasswordTooShort, got %v", err)
	}

	_, _, err = svc.Register("test@example.com", "abcdefgh", "Test")
	if err != services.ErrPasswordNoDigit {
		t.Errorf("want ErrPasswordNoDigit, got %v", err)
	}
}

// TestAuthService_Register_HappyPath asserts the SPRINT 7.4 contract:
// Register now returns (*models.User, wsID, error) — NOT a JWT.
// The caller (HTTP handler) is responsible for minting the JWT via
// SessionsService.Start so that the JWT's sessionID is bound to a
// real sessions row (Blocco #1.4 invariant). This test exercises
// the user-create + workspace-create + admin-mem...bership steps in
// order; the JWT mint is verified separately by sessions.Service.
func TestAuthService_Register_HappyPath(t *testing.T) {
	svc, mock, cleanup := newTestAuthService(t)
	defer cleanup()

	now := time.Now()

	// 1) Find existing user (returns no rows).
	mock.ExpectQuery(
		`SELECT id, email, name, COALESCE(password_hash, '') AS password_hash, COALESCE(email_verified, false),
	       created_at, updated_at FROM users WHERE email = $1`,
	).WithArgs("test@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "name", "password_hash", "email_verified", "created_at", "updated_at"}))

	// 2) Insert user, returning id=1.
	mock.ExpectQuery(
		`INSERT INTO users (email, name, password_hash)
		 VALUES ($1, $2, $3) RETURNING id, created_at, updated_at`,
	).WithArgs("test@example.com", "Test User", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(1, now, now))

	// 3) Personal workspace insert (SPRINT 1.1 + SPRINT 7.4): returns id=10.
	mock.ExpectQuery(workspaceCreatePlaceholder).
		WithArgs("Personal", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(10, now))

	// 4) AddMember preflight GetRole: SELECT role FROM workspace_members
	//    WHERE workspace_id=10 AND user_id=1 returns no rows.
	mock.ExpectQuery(
		`SELECT role FROM workspace_members
		 WHERE workspace_id = $1 AND user_id = $2`,
	).WithArgs(10, 1).
		WillReturnRows(sqlmock.NewRows([]string{"role"}))

	// 5) AddMember then INSERT INTO workspace_members.
	mock.ExpectExec(
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ($1, $2, $3)`,
	).WithArgs(10, 1, repository.RoleAdmin).
		WillReturnResult(sqlmock.NewResult(0, 1))

	user, wsID, err := svc.Register("test@example.com", "password1", "Test User")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if user.ID != 1 {
		t.Errorf("user.ID: want 1, got %d", user.ID)
	}
	if wsID != 10 {
		t.Errorf("wsID: want 10 (the Personal workspace id), got %d", wsID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAuthService_Register_DuplicateEmail(t *testing.T) {
	svc, mock, cleanup := newTestAuthService(t)
	defer cleanup()

	now := time.Now()

	mock.ExpectQuery(
		`SELECT id, email, name, COALESCE(password_hash, '') AS password_hash, COALESCE(email_verified, false),
	       created_at, updated_at FROM users WHERE email = $1`,
	).WithArgs("dupe@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "name", "password_hash", "email_verified", "created_at", "updated_at"}).
			AddRow(2, "dupe@example.com", "Existing", []byte("hash"), false, now, now))

	_, _, err := svc.Register("dupe@example.com", "password1", "Second")
	if err != services.ErrEmailAlreadyTaken {
		t.Errorf("want ErrEmailAlreadyTaken, got %v", err)
	}
}

// TestAuthService_Login_HappyPath asserts the SPRINT 7.4 contract:
// Login returns (*models.User, wsID, error) — NOT a JWT.
// The new resolveActiveWorkspace step is mocked to return one owned
// workspace (id=10) so Login can short-circuit on the owned
// workspaces branch and return a non-zero wsID.
func TestAuthService_Login_HappyPath(t *testing.T) {
	svc, mock, cleanup := newTestAuthService(t)
	defer cleanup()

	now := time.Now()
	hash, _ := bcrypt.GenerateFromPassword([]byte("password1"), bcrypt.DefaultCost)

	// 1) Find user by email.
	mock.ExpectQuery(
		`SELECT id, email, name, COALESCE(password_hash, '') AS password_hash, COALESCE(email_verified, false),
	       created_at, updated_at FROM users WHERE email = $1`,
	).WithArgs("login@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "name", "password_hash", "email_verified", "created_at", "updated_at"}).
			AddRow(1, "login@example.com", "Login User", hash, true, now, now))

	// 2) resolveActiveWorkspace: ListByOwner returns one workspace.
	mock.ExpectQuery(
		`SELECT id, name, owner_id, created_at
		 FROM workspaces
		 WHERE owner_id = $1
		 ORDER BY created_at DESC`,
	).WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "owner_id", "created_at"}).
			AddRow(10, "Personal", 1, now))

	user, wsID, err := svc.Login("login@example.com", "password1")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if user.ID != 1 {
		t.Errorf("user.ID: want 1, got %d", user.ID)
	}
	if wsID != 10 {
		t.Errorf("wsID: want 10 (active workspace), got %d", wsID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAuthService_Login_WrongPassword(t *testing.T) {
	svc, mock, cleanup := newTestAuthService(t)
	defer cleanup()

	now := time.Now()
	hash, _ := bcrypt.GenerateFromPassword([]byte("password1"), bcrypt.DefaultCost)

	mock.ExpectQuery(
		`SELECT id, email, name, COALESCE(password_hash, '') AS password_hash, COALESCE(email_verified, false),
	       created_at, updated_at FROM users WHERE email = $1`,
	).WithArgs("wrong@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "name", "password_hash", "email_verified", "created_at", "updated_at"}).
			AddRow(1, "wrong@example.com", "User", hash, true, now, now))

	_, _, err := svc.Login("wrong@example.com", "wrongpassword1")
	if err != services.ErrInvalidPassword {
		t.Errorf("want ErrInvalidPassword, got %v", err)
	}
}

func TestBcryptHashCompatibility(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("password1"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte("password1")); err != nil {
		t.Errorf("CompareHashAndPassword: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte("wrong")); err == nil {
		t.Error("expected failure with wrong password")
	}
}
