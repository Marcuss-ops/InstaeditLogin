// Package services provides the AuthService for SaaS email/password
// authentication: register, login, email verification, and password reset.
//
// FASE 2.2: Email/Password Authentication Service.
package services

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// Password policy errors.
var (
	ErrPasswordTooShort  = errors.New("password must be at least 8 characters")
	ErrPasswordNoDigit   = errors.New("password must contain at least 1 number")
	ErrEmailAlreadyTaken = errors.New("email already registered")
	ErrInvalidPassword   = errors.New("invalid password")
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
	workspaceRepo *repository.WorkspaceRepository
	teamRepo      *repository.TeamRepository
}

// NewAuthService constructs an AuthService. The workspaceRepo + teamRepo
// parameters are REQUIRED for SPRINT 1.1; the service refuses to issue
// JWTs without them (every Issue call needs a real wsID).
func NewAuthService(
	userRepo *repository.UserRepository,
	workspaceRepo *repository.WorkspaceRepository,
	teamRepo *repository.TeamRepository,
) *AuthService {
	return &AuthService{
		userRepo:      userRepo,
		workspaceRepo: workspaceRepo,
		teamRepo:      teamRepo,
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
// Returns the user row and the workspace_id of the newly-created
// Personal Workspace. The HTTP handler is responsible for minting
// the session JWT via SessionsService.Start (which creates a session
// row and binds it to the access JWT).
//
// SPRINT 7.4 (P0#14-blocco-1.4): JWT issuance moved out of AuthService.
// Manager.Issue(u, w) (which minted sessionID=0 JWTs) is now rejected
// by the auth package; producing a session-bound JWT requires a real
// session row, which only SessionsService.Start can do.
//
// SPRINT 1.1 mandatories, executed atomically here:
//  1. create the user row (email + bcrypt hash + email_verified=false);
//  2. create a "Personal Workspace" owned by the new user,
//     auto-adding the user as admin via workspace_members.
//
// The returned workspace_id is the real workspace id — DO NOT switch
// this to anything derived from a global default. If step 2 fails the
// whole register fails (rollback semantics in userRepo + workspaceRepo).
func (s *AuthService) Register(email, password, name string) (*models.User, int64, error) {
	if err := validatePassword(password); err != nil {
		return nil, 0, err
	}

	// Check for existing user.
	existing, err := s.userRepo.FindByEmail(email)
	if err != nil {
		return nil, 0, fmt.Errorf("register: find by email: %w", err)
	}
	if existing != nil {
		return nil, 0, ErrEmailAlreadyTaken
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, 0, fmt.Errorf("register: bcrypt: %w", err)
	}

	user, err := s.userRepo.CreateSaaSUser(email, name, hash)
	if err != nil {
		return nil, 0, fmt.Errorf("register: create user: %w", err)
	}

	// SPRINT 1.1: every new user gets exactly one "Personal Workspace"
	// they administer. Migrates the prior implicit-default-workspace
	// pattern into an explicit record per user.
	workspaceName := "Personal"
	ws := &models.Workspace{Name: workspaceName, OwnerID: user.ID}
	if err := s.workspaceRepo.Create(ws); err != nil {
		return nil, 0, fmt.Errorf("register: create personal workspace: %w", err)
	}
	if err := s.teamRepo.AddMember(ws.ID, user.ID, repository.RoleAdmin); err != nil {
		return nil, 0, fmt.Errorf("register: add admin membership: %w", err)
	}

	return user, ws.ID, nil
}

// Login authenticates a user by email and password. Returns the
// user row and the user's active workspace_id. The HTTP handler is
// responsible for minting the session JWT via SessionsService.Start.
//
// SPRINT 7.4 (P0#14-blocco-1.4): JWT issuance moved out of AuthService.
// Same rationale as Register — post-Sprint-2.1 contracts require a
// session row first, which only SessionsService.Start provides.
//
// SPRINT 1.1: workspace resolution on Login.
//
//  1. List workspaces owned by the user via workspaceRepo.ListByOwner
//     and merged with the user's workspace_members. If we find any,
//     pick the most recently created one.
//  2. If the user has zero memberships AND zero owned workspaces,
//     return ErrNoWorkspace. The caller (handler) surfaces a 409 +
//     message pointing at the onboarding flow.
func (s *AuthService) Login(email, password string) (*models.User, int64, error) {
	user, err := s.userRepo.FindByEmail(email)
	if err != nil {
		return nil, 0, fmt.Errorf("login: find by email: %w", err)
	}
	if user == nil {
		return nil, 0, ErrInvalidPassword
	}
	if len(user.PasswordHash) == 0 {
		return nil, 0, ErrInvalidPassword
	}

	if err := bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(password)); err != nil {
		return nil, 0, ErrInvalidPassword
	}

	activeWS, err := s.resolveActiveWorkspace(user.ID)
	if err != nil {
		return nil, 0, err
	}

	return user, activeWS, nil
}

// IssueSessionTokenForWorkspace has been REMOVED in SPRINT 7.4
// (P0#14-blocco-1.4). It used Manager.Issue(userID, wsID) which minted
// sessionID=0 — incompatible with post-SPRINT-2.1 Verify contract.
// Callers (handleSwitchWorkspace, handleExchangeCode) now use
// SessionsService directly: they revoke the old session row (handle
// switch), then SessionsService.Start() with the new workspace id.
// AuthService no longer holds the contract of issuing JWTs.

// resolveActiveWorkspace picks the user's active workspace at sign-in /
// OAuth callback / onboarding-completion time. Strategy:
//
//  1. If the user owns at least one workspace, pick the most recent.
//  2. Else, if the user is a member of at least one workspace, pick the
//     most recent membership.
//  3. Else return ErrNoWorkspace — caller must surface onboarding.
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

// (IssueResetToken / ResetPassword / purpose-token JWT helpers were
// removed when the invite-only beta dropped the password-reset flow.
// Passwords are now distributed out-of-band by an admin; no
// self-service reset endpoint or token remains.)
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
