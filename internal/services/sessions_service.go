// Package services — SessionsService.
//
// Owns the lifecycle of `sessions` rows. Cooperates with the JWT
// Manager (issues access/refresh tokens) and the SessionsRepository
// (persists and rotates rows). All paths are idempotent where the
// caller cares: rotating an already-rotated refresh token triggers
// family-wide revocation + ErrSessionReuse, which the handler maps
// to 401.

package services

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// Sentinel errors exposed to handlers.
var (
	ErrSessionNotFound  = errors.New("session not found")
	ErrSessionReuse     = errors.New("refresh token reuse detected")
	ErrSessionForbidden = errors.New("session belongs to a different user")
)

// StartSessionRequest carries the inputs needed to mint a new session.
type StartSessionRequest struct {
	UserID      int64
	WorkspaceID int64
	UserAgent   string
	IP          string
}

// StartSessionResult is what the login/refresh handlers return to set
// cookies on the response.
type StartSessionResult struct {
	SessionID       int64
	AccessToken     string
	AccessJTI       string
	AccessExpiresAt time.Time
	RefreshToken    string
	RefreshHash     []byte
	RefreshExpiresAt time.Time
}

// SessionsService coordinates session creation, rotation, and revocation.
type SessionsService struct {
	repo  *repository.SessionRepository
	jwt   *auth.Manager
	clock func() time.Time
}

// NewSessionsService wires the service. clock is injectable for tests.
func NewSessionsService(repo *repository.SessionRepository, jwt *auth.Manager) *SessionsService {
	return &SessionsService{repo: repo, jwt: jwt, clock: time.Now}
}

// Start creates a brand-new session and returns the access + refresh
// tokens to put in cookies. familyID may be empty to auto-generate a
// new one. Caller is responsible for setting the cookies.
func (s *SessionsService) Start(req StartSessionRequest) (*StartSessionResult, error) {
	if req.UserID <= 0 || req.WorkspaceID <= 0 {
		return nil, fmt.Errorf("invalid ids: user=%d ws=%d", req.UserID, req.WorkspaceID)
	}
	family, err := auth.RandomHex(16)
	if err != nil {
		return nil, fmt.Errorf("family id: %w", err)
	}
	refreshPlain, refreshHash, refreshExp, err := s.jwt.IssueRefresh()
	if err != nil {
		return nil, fmt.Errorf("issue refresh: %w", err)
	}
	// Issue a placeholder access token to learn the JTI; we'll sign
	// the real one AFTER persisting the row so the JTI matches.
	// IssueAccess is idempotent only at the (userID, wsID, sessionID)
	// level — but we don't have a sessionID yet. The first access
	// token is bound to sessionID=0 (placeholder) and reissued once
	// the row exists. Alternative: use a server-side JTI, persist,
	// then sign. That's the approach we take.
	accessJTI, err := auth.RandomHex(16)
	if err != nil {
		return nil, fmt.Errorf("access jti: %w", err)
	}
	now := s.clock()
	row := &repository.Session{
		UserID:           req.UserID,
		WorkspaceID:      req.WorkspaceID,
		TokenFamilyID:    family,
		AccessJTI:        accessJTI,
		RefreshTokenHash: refreshHash,
		UserAgent:        req.UserAgent,
		IPHash:           hashIP(req.IP),
		ExpiresAt:        now.Add(s.jwt.AccessTTL()),
		RefreshExpiresAt: refreshExp,
	}
	if err := s.repo.Create(row); err != nil {
		return nil, fmt.Errorf("persist session: %w", err)
	}
	// Sign the real access token with the freshly-allocated session id.
	access, _, accessExp, err := s.jwt.IssueAccess(req.UserID, req.WorkspaceID, row.ID)
	if err != nil {
		// Best-effort cleanup: revoke the orphan row so the refresh
		// token doesn't dangle.
		_ = s.repo.Revoke(row.ID, "access_sign_failed")
		return nil, fmt.Errorf("sign access: %w", err)
	}
	return &StartSessionResult{
		SessionID:        row.ID,
		AccessToken:      access,
		AccessJTI:        accessJTI,
		AccessExpiresAt:  accessExp,
		RefreshToken:     refreshPlain,
		RefreshHash:      refreshHash,
		RefreshExpiresAt: refreshExp,
	}, nil
}

// RefreshRequest is the input to /auth/refresh.
type RefreshRequest struct {
	RefreshPlaintext string
	UserAgent        string
	IP               string
}

// Refresh rotates the refresh token and returns a new access + refresh
// pair bound to the same family. If the supplied refresh plaintext
// matches a row whose revoked_at is already set (i.e. the row was
// rotated earlier), the ENTIRE family is revoked (theft detection)
// and ErrSessionReuse is returned.
func (s *SessionsService) Refresh(req RefreshRequest) (*StartSessionResult, error) {
	if req.RefreshPlaintext == "" {
		return nil, ErrSessionNotFound
	}
	hash := auth.HashRefreshToken(req.RefreshPlaintext)

	// Pre-fetch the row so we can craft a 401/403 with the right
	// state without re-querying after the rotate. The repository's
	// Rotate path still does its own SELECT FOR UPDATE for the
	// race-safe check.
	row, err := s.repo.FindByRefreshHash(hash)
	if err != nil {
		if errors.Is(err, repository.ErrSessionNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("find session: %w", err)
	}
	if row.RevokedAt != nil {
		// Refresh was already rotated (or otherwise revoked). Treat
		// as reuse: revoke the whole family.
		if _, err := s.repo.RevokeFamily(row.TokenFamilyID, "refresh_reuse_detected"); err != nil {
			return nil, fmt.Errorf("revoke family: %w", err)
		}
		return nil, ErrSessionReuse
	}
	// Build the replacement session.
	refreshPlain, refreshHash, refreshExp, err := s.jwt.IssueRefresh()
	if err != nil {
		return nil, fmt.Errorf("issue refresh: %w", err)
	}
	accessJTI, err := auth.RandomHex(16)
	if err != nil {
		return nil, fmt.Errorf("access jti: %w", err)
	}
	now := s.clock()
	newRow := &repository.Session{
		TokenFamilyID:    row.TokenFamilyID,
		AccessJTI:        accessJTI,
		RefreshTokenHash: refreshHash,
		UserAgent:        req.UserAgent,
		IPHash:           hashIP(req.IP),
		ExpiresAt:        now.Add(s.jwt.AccessTTL()),
		RefreshExpiresAt: refreshExp,
	}
	if _, err := s.repo.Rotate(hash, newRow); err != nil {
		if errors.Is(err, repository.ErrSessionReuse) {
			return nil, ErrSessionReuse
		}
		if errors.Is(err, repository.ErrSessionNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("rotate: %w", err)
	}
	access, _, accessExp, err := s.jwt.IssueAccess(row.UserID, row.WorkspaceID, newRow.ID)
	if err != nil {
		_ = s.repo.Revoke(newRow.ID, "access_sign_failed")
		return nil, fmt.Errorf("sign access: %w", err)
	}
	return &StartSessionResult{
		SessionID:        newRow.ID,
		AccessToken:      access,
		AccessJTI:        accessJTI,
		AccessExpiresAt:  accessExp,
		RefreshToken:     refreshPlain,
		RefreshHash:      refreshHash,
		RefreshExpiresAt: refreshExp,
	}, nil
}

// Revoke marks a single session as revoked. ownerUserID is the
// authenticated user; if the session does not belong to them, the
// call returns ErrSessionForbidden without mutating any row.
func (s *SessionsService) Revoke(sessionID, ownerUserID int64, reason string) error {
	if sessionID <= 0 || ownerUserID <= 0 {
		return ErrSessionNotFound
	}
	// List is small (per user); we do a fetch-then-check to surface a
	// 403 cleanly. The query is bounded by ListByUser.
	rows, err := s.repo.ListByUser(ownerUserID)
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for _, r := range rows {
		if r.ID == sessionID {
			return s.repo.Revoke(sessionID, reason)
		}
	}
	return ErrSessionForbidden
}

// RevokeAll marks every active session for the user as revoked.
func (s *SessionsService) RevokeAll(userID int64, reason string) (int64, error) {
	return s.repo.RevokeAllForUser(userID, reason)
}

// List returns the active sessions for a user (revoked + active,
// ordered by last_used_at DESC). Callers usually filter.
func (s *SessionsService) List(userID int64) ([]repository.Session, error) {
	return s.repo.ListByUser(userID)
}

// hashIP returns a hex-encoded SHA-256 of the IP. PII minimization:
// the IP itself is never persisted; the hash is recoverable only by
// the user who knows the IP. Salt is omitted intentionally — we
// treat the hash as a stable correlation handle for audit, not as
// a security boundary.
func hashIP(ip string) string {
	if ip == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(sum[:])
}

// WithdrawFromCookie extracts the refresh cookie value. Exposed so
// the /auth/logout handler can do a cookie-anchored revoke (deletes
// the row matching the current refresh hash, so the cookie is
// invalid even if the user keeps the same cookie string around).
func (s *SessionsService) WithdrawFromCookie(refreshPlain string) error {
	if refreshPlain == "" {
		return nil
	}
	hash := auth.HashRefreshToken(refreshPlain)
	row, err := s.repo.FindByRefreshHash(hash)
	if err != nil {
		if errors.Is(err, repository.ErrSessionNotFound) {
			return nil // already gone
		}
		return err
	}
	return s.repo.Revoke(row.ID, "logout")
}

// IsNoSession is exported for handlers that want a 401 vs 404 split.
func IsNoSession(err error) bool {
	return errors.Is(err, ErrSessionNotFound)
}

// _ keeps http imported for the future Set-Cookie helper that
// the api package implements; suppress the unused import warning.
var _ = http.MethodPost

