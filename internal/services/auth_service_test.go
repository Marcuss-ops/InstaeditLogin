package services_test

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"golang.org/x/crypto/bcrypt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// newTestAuthService creates an AuthService wired with sqlmock.
func newTestAuthService(t *testing.T) (*services.AuthService, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	cleanup := func() { _ = db.Close() }
	userRepo := repository.NewUserRepository(db)
	authMgr := auth.NewManager("test-secret-key-for-auth-service-tests", 24)
	// SPRINT 1.1: NewAuthService now requires workspaceRepo + teamRepo
	// (Register auto-creates a Personal Workspace, Login resolves an
	// active workspace). Tests that exercise ONLY IssueVerificationToken /
	// VerifyEmail / IssueResetToken / ResetPassword don't touch the repos
	// and so can pass nil; tests that hit Register / Login / MagicLink
	// must wire real (or fake) repos. SPRINT 2.1 follow-up: add
	// panic-on-nil guards in NewAuthService so the runtime surface is
	// self-documenting.
	svc := services.NewAuthService(userRepo, nil, nil, authMgr, "test-secret-key-for-auth-service-tests")
	return svc, mock, cleanup
}

func TestAuthService_Register_WeakPassword(t *testing.T) {
	svc, _, cleanup := newTestAuthService(t)
	defer cleanup()

	_, _, err := svc.Register("test@example.com", "abc", "Test")
	if err != services.ErrPasswordTooShort {
		t.Errorf("want ErrPasswordTooShort, got %v", err)
	}

	_, _, err = svc.Register("test@example.com", "abcdefgh", "Test")
	if err != services.ErrPasswordNoDigit {
		t.Errorf("want ErrPasswordNoDigit, got %v", err)
	}
}

func TestAuthService_Register_HappyPath(t *testing.T) {
	svc, mock, cleanup := newTestAuthService(t)
	defer cleanup()

	now := time.Now()

	mock.ExpectQuery(
		`SELECT id, email, name, COALESCE(password_hash, '') AS password_hash, COALESCE(email_verified, false),
		       created_at, updated_at FROM users WHERE email = $1`,
	).WithArgs("test@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "name", "password_hash", "email_verified", "created_at", "updated_at"}))

	mock.ExpectQuery(
		`INSERT INTO users (email, name, password_hash)
		 VALUES ($1, $2, $3) RETURNING id, created_at, updated_at`,
	).WithArgs("test@example.com", "Test User", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(1, now, now))

	user, jwt, err := svc.Register("test@example.com", "password1", "Test User")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if user.ID != 1 {
		t.Errorf("user.ID: want 1, got %d", user.ID)
	}
	if jwt == "" {
		t.Error("jwt should not be empty")
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

func TestAuthService_Login_HappyPath(t *testing.T) {
	svc, mock, cleanup := newTestAuthService(t)
	defer cleanup()

	now := time.Now()
	hash, _ := bcrypt.GenerateFromPassword([]byte("password1"), bcrypt.DefaultCost)

	mock.ExpectQuery(
		`SELECT id, email, name, COALESCE(password_hash, '') AS password_hash, COALESCE(email_verified, false),
		       created_at, updated_at FROM users WHERE email = $1`,
	).WithArgs("login@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "name", "password_hash", "email_verified", "created_at", "updated_at"}).
			AddRow(1, "login@example.com", "Login User", hash, true, now, now))

	user, jwt, err := svc.Login("login@example.com", "password1")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if user.ID != 1 {
		t.Errorf("user.ID: want 1, got %d", user.ID)
	}
	if jwt == "" {
		t.Error("jwt should not be empty")
	}
}

func TestAuthService_Login_EmailNotVerified(t *testing.T) {
	svc, mock, cleanup := newTestAuthService(t)
	defer cleanup()

	now := time.Now()
	hash, _ := bcrypt.GenerateFromPassword([]byte("password1"), bcrypt.DefaultCost)

	mock.ExpectQuery(
		`SELECT id, email, name, COALESCE(password_hash, '') AS password_hash, COALESCE(email_verified, false),
		       created_at, updated_at FROM users WHERE email = $1`,
	).WithArgs("unverified@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "name", "password_hash", "email_verified", "created_at", "updated_at"}).
			AddRow(1, "unverified@example.com", "User", hash, false, now, now))

	_, _, err := svc.Login("unverified@example.com", "password1")
	if err != services.ErrEmailNotVerified {
		t.Errorf("want ErrEmailNotVerified, got %v", err)
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

func TestAuthService_TokenRoundTrip(t *testing.T) {
	svc, _, cleanup := newTestAuthService(t)
	defer cleanup()

	verifTok, err := svc.IssueVerificationToken(42, "verify@example.com")
	if err != nil {
		t.Fatalf("IssueVerificationToken: %v", err)
	}
	if verifTok == "" {
		t.Fatal("verification token empty")
	}

	// Garbage token fails.
	_, err = svc.VerifyEmail("not.a.token")
	if err == nil {
		t.Error("expected error for garbage token")
	}

	// Verification token used for password reset fails (wrong purpose).
	err = svc.ResetPassword(verifTok, "newpassword2")
	if err == nil {
		t.Error("expected error using verification token for reset")
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
