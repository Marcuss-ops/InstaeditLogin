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
type AuthService struct {
	userRepo *repository.UserRepository
	authMgr  *auth.Manager
	// secret used to sign verification and password-reset tokens.
	// In production this MUST be the same as JWT_SECRET so tokens
	// are valid across process restarts.
	secret []byte
}

// NewAuthService constructs an AuthService.
func NewAuthService(userRepo *repository.UserRepository, authMgr *auth.Manager, secret string) *AuthService {
	return &AuthService{
		userRepo: userRepo,
		authMgr:  authMgr,
		secret:   []byte(secret),
	}
}

// -----------------------------------------------------------------------
//  Public API
// -----------------------------------------------------------------------

// Register creates a new SaaS user with an email and password.
// Returns a session JWT so the user is logged in immediately after signup.
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

	jwt, _, _, err := s.authMgr.Issue(user.ID)
	if err != nil {
		return nil, "", fmt.Errorf("register: issue jwt: %w", err)
	}

	return user, jwt, nil
}

// Login authenticates a user by email and password, returning a session JWT.
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

	jwt, _, _, err := s.authMgr.Issue(user.ID)
	if err != nil {
		return nil, "", fmt.Errorf("login: issue jwt: %w", err)
	}

	return user, jwt, nil
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
