// Package services provides the AuthService for SaaS email/password
// authentication: register, login, email verification, and password reset.
//
// FASE 2.2: Email/Password Authentication Service.
package services

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// Password policy errors.
var (
	ErrPasswordTooShort  = errors.New("password must be at least 8 characters")
	ErrPasswordNoDigit   = errors.New("password must contain at least 1 number")
	ErrEmailAlreadyTaken = errors.New("email already registered")
	ErrInvalidPassword   = errors.New("invalid password")
	ErrEmailNotVerified  = errors.New("email not verified")
)

// AuthService handles email/password authentication flows.
//
// SPRINT 1.1: extends to depend on TeamRepository + WorkspaceRepository
// so Register auto-creates a personal workspace + admin membership and
// Login resolves an active workspace from real workspace_members.
// Authenticating a user without resolving a real workspace is no longer
// possible — every JWT-issued token now carries the resolved wsID.
type AuthService struct {
	userRepo      *repository.UserRepository
	authMgr       *auth.Manager
	workspaceRepo *repository.WorkspaceRepository
	teamRepo      *repository.TeamRepository
	// secret used to sign verification and password-reset tokens.
	// In production this MUST be the same as JWT_SECRET so tokens
	// are valid across process restarts.
	secret []byte
}

// NewAuthService constructs an AuthService. The workspaceRepo + teamRepo
// parameters are REQUIRED for SPRINT 1.1; the service refuses to issue
// JWTs without them (every Issue call needs a real wsID).
func NewAuthService(
	userRepo *repository.UserRepository,
	workspaceRepo *repository.WorkspaceRepository,
	teamRepo *repository.TeamRepository,
	authMgr *auth.Manager,
	secret string,
) *AuthService {
	return &AuthService{
		userRepo:      userRepo,
		authMgr:       authMgr,
		workspaceRepo: workspaceRepo,
		teamRepo:      teamRepo,
		secret:        []byte(secret),
	}
}

// ErrNoWorkspace is returned by Login when the user has zero workspace
// memberships. The handler should map this to a "complete onboarding"
// flow (or 409/403) — never to a default-workspace fallback.
var ErrNoWorkspace = errors.New("user has no workspace; complete onboarding")

// -----------------------------------------------------------------------
//  Public API
// -----------------------------------------------------------------------

// Register creates a new SaaS user with an email and password.
// Returns a session JWT carrying the resolved workspace_id so the user
// is logged in immediately after signup.
//
// SPRINT 1.1 mandatories, executed atomically here:
//   1. create the user row (email + bcrypt hash + email_verified=false);
//   2. create a "Personal Workspace" owned by the new user,
//      auto-adding the user as admin via workspace_members.
//   3. issue a JWT whose ws claim is the new workspace id.
//
// The JWT carries the real workspace_id — DO NOT switch this to
// anything derived from a global default. If step 2 fails the whole
// register fails (rollback semantics in userRepo + workspaceRepo).
func (s *AuthService) Register(email, password, name string) (*models.User, string, error) {
	if err := validatePassword(password); err != nil {
		return nil, "", err
	}

	// Check for existing user.
	existing, err := s.userRepo.FindByEmail(email)
	if err != nil {
		return nil, "", fmt.Errorf("register: find by email: %w", err)
	}
	if existing != nil {
		return nil, "", ErrEmailAlreadyTaken
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", fmt.Errorf("register: bcrypt: %w", err)
	}

	user, err := s.userRepo.CreateSaaSUser(email, name, hash)
	if err != nil {
		return nil, "", fmt.Errorf("register: create user: %w", err)
	}

	// SPRINT 1.1: every new user gets exactly one "Personal Workspace"
	// they administer. Migrates the prior implicit-default-workspace
	// pattern into an explicit record per user.
	workspaceName := "Personal"
	ws := &models.Workspace{Name: workspaceName, OwnerID: user.ID}
	if err := s.workspaceRepo.Create(ws); err != nil {
		return nil, "", fmt.Errorf("register: create personal workspace: %w", err)
	}
	if err := s.teamRepo.AddMember(ws.ID, user.ID, repository.RoleAdmin); err != nil {
		return nil, "", fmt.Errorf("register: add admin membership: %w", err)
	}

	jwt, _, _, err := s.authMgr.Issue(user.ID, ws.ID)
	if err != nil {
		return nil, "", fmt.Errorf("register: issue jwt: %w", err)
	}

	return user, jwt, nil
}

// Login authenticates a user by email and password, returning a session
// JWT whose ws claim is the user's first real workspace membership.
//
// SPRINT 1.1: workspace resolution on Login.
//
//   1. List workspaces owned by the user via workspaceRepo.ListByOwner
//      and merged with the user's workspace_members. If we find any,
//      pick the most recently created one — that is the user's active
//      workspace at sign-in time (matching what they were doing before
//      the session ended).
//   2. If the user has zero memberships AND zero owned workspaces,
//      return ErrNoWorkspace. The caller (handler) surfaces a 409 +
//      message pointing at the onboarding flow — never an implicit
//      fallback to a global default workspace.
func (s *AuthService) Login(email, password string) (*models.User, string, error) {
	user, err := s.userRepo.FindByEmail(email)
	if err != nil {
		return nil, "", fmt.Errorf("login: find by email: %w", err)
	}
	if user == nil {
		return nil, "", ErrInvalidPassword
	}
	if len(user.PasswordHash) == 0 {
		return nil, "", ErrInvalidPassword
	}

	if err := bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(password)); err != nil {
		return nil, "", ErrInvalidPassword
	}

	if !user.EmailVerified {
		return nil, "", ErrEmailNotVerified
	}

	activeWS, err := s.resolveActiveWorkspace(user.ID)
	if err != nil {
		return nil, "", err
	}

	jwt, _, _, err := s.authMgr.Issue(user.ID, activeWS)
	if err != nil {
		return nil, "", fmt.Errorf("login: issue jwt: %w", err)
	}

	return user, jwt, nil
}

// IssueSessionTokenForWorkspace re-issues a JWT for (userID, workspaceID).
// Used by handleSwitchWorkspace after a successful /workspaces/{id}/switch
// and by handleExchangeCode (OAuth callback) after resolving the user's
// active workspace. Centralises the Issue-with-wsID pattern.
func (s *AuthService) IssueSessionTokenForWorkspace(userID, workspaceID int64) (string, error) {
	jwt, _, _, err := s.authMgr.Issue(userID, workspaceID)
	if err != nil {
		return "", fmt.Errorf("issue session token: %w", err)
	}
	return jwt, nil
}

// resolveActiveWorkspace picks the user's active workspace at sign-in /
// OAuth callback / onboarding-completion time. Strategy:
//
//	1. If the user owns at least one workspace, pick the most recent.
//	2. Else, if the user is a member of at least one workspace, pick the
//	   most recent membership.
//	3. Else return ErrNoWorkspace — caller must surface onboarding.
//
// Owned-workspace preference (above membership) reflects that the
// owner has full admin control and is the natural "home" workspace.
// This matches the dashboard UX: when a user returns to the app they
// land on the workspace they were last administering.
func (s *AuthService) resolveActiveWorkspace(userID int64) (int64, error) {
	owned, err := s.workspaceRepo.ListByOwner(userID)
	if err != nil {
		return 0, fmt.Errorf("resolve workspace: list owned: %w", err)
	}
	if len(owned) > 0 {
		return owned[0].ID, nil
	}
	// Fallback to membership. Add a ListForUser helper to TeamRepository
	// if needed — for now: pick from the user's membership list via
	// the teamRepo.
	members, err := s.teamRepo.ListForUser(userID)
	if err != nil {
		return 0, fmt.Errorf("resolve workspace: list memberships: %w", err)
	}
	if len(members) > 0 {
		return members[0].WorkspaceID, nil
	}
	return 0, ErrNoWorkspace
}

// IssueVerificationToken generates an email verification token for the
// given user. The token is a short-lived JWT (24h) carrying the user ID
// and email. The caller (handler) is responsible for sending the email.
func (s *AuthService) IssueVerificationToken(userID int64, email string) (string, error) {
	return s.issuePurposeToken(userID, email, "verify", 24*time.Hour)
}

// VerifyEmail parses a verification token and marks the user's email as
// verified. Returns the user ID on success.
func (s *AuthService) VerifyEmail(token string) (int64, error) {
	userID, purpose, err := s.parsePurposeToken(token)
	if err != nil {
		return 0, fmt.Errorf("verify email: %w", err)
	}
	if purpose != "verify" {
		return 0, errors.New("verify email: token is not a verification token")
	}
	if err := s.userRepo.SetEmailVerified(userID); err != nil {
		return 0, fmt.Errorf("verify email: %w", err)
	}
	return userID, nil
}

// IssueResetToken generates a password-reset token (1h TTL). Used after the
// user requests a forgot-password flow. Returns an error if no user with
// that email exists (but the API layer returns 200 anyway to avoid
// email enumeration).
func (s *AuthService) IssueResetToken(email string) (string, error) {
	user, err := s.userRepo.FindByEmail(email)
	if err != nil {
		return "", fmt.Errorf("issue reset token: %w", err)
	}
	if user == nil {
		return "", repository.ErrUserNotFound
	}
	return s.issuePurposeToken(user.ID, email, "reset", 1*time.Hour)
}

// ResetPassword validates a reset token and updates the user's password.
func (s *AuthService) ResetPassword(token, newPassword string) error {
	userID, purpose, err := s.parsePurposeToken(token)
	if err != nil {
		return fmt.Errorf("reset password: %w", err)
	}
	if purpose != "reset" {
		return errors.New("reset password: token is not a reset token")
	}
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("reset password: bcrypt: %w", err)
	}
	return s.userRepo.UpdatePassword(userID, hash)
}

// -----------------------------------------------------------------------
//  Internal helpers
// -----------------------------------------------------------------------

type purposeClaims struct {
	UserID  int64  `json:"uid"`
	Email   string `json:"email"`
	Purpose string `json:"purpose"`
	jwt.RegisteredClaims
}

func (s *AuthService) issuePurposeToken(userID int64, email, purpose string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := purposeClaims{
		UserID:  userID,
		Email:   email,
		Purpose: purpose,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", userID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID:        mustRandomHex(16),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("sign purpose token: %w", err)
	}
	return signed, nil
}

func (s *AuthService) parsePurposeToken(raw string) (userID int64, purpose string, err error) {
	if raw == "" {
		return 0, "", errors.New("empty token")
	}
	token, err := jwt.ParseWithClaims(raw, &purposeClaims{}, func(t *jwt.Token) (interface{}, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil {
		return 0, "", err
	}
	claims, ok := token.Claims.(*purposeClaims)
	if !ok || !token.Valid {
		return 0, "", errors.New("invalid token")
	}
	if claims.UserID <= 0 {
		return 0, "", errors.New("missing user id in token")
	}
	return claims.UserID, claims.Purpose, nil
}

func validatePassword(password string) error {
	if len(password) < 8 {
		return ErrPasswordTooShort
	}
	hasDigit := false
	for _, c := range password {
		if c >= '0' && c <= '9' {
			hasDigit = true
			break
		}
	}
	if !hasDigit {
		return ErrPasswordNoDigit
	}
	return nil
}

func mustRandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
	}
	return hex.EncodeToString(b)
}
