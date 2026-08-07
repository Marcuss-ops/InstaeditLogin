// Package editorlaunch implements the short-lived, project-scoped token used
// to enter the separately deployed InstaEditor.
//
// The token is deliberately distinct from the normal InstaEdit session JWT:
// it is valid only for the editor audience, carries one opaque project handle
// and editor scopes, and expires quickly. It is safe to transport in a URL
// fragment because the fragment is not sent in HTTP requests; the editor
// frontend extracts it once and keeps it in memory.
package editorlaunch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	ExpectedIssuer   = "instaedit"
	ExpectedAudience = "velox"
	ScopeRead        = "editor.read"
	ScopeWrite       = "editor.write"
	TokenTTL         = 2 * time.Minute
	SessionTTL       = 30 * time.Minute
	MinimumSecretLen = 32
	TokenTypeLaunch  = "editor_launch"
	TokenTypeSession = "editor_session"
)

var (
	ErrSecretNotConfigured = errors.New("editor launch token secret is not configured")
	ErrInvalidToken        = errors.New("invalid editor launch token")
	ErrExpired             = errors.New("editor launch token expired")
	ErrProjectMismatch     = errors.New("editor launch token project mismatch")
)

type Claims struct {
	UserID      int64    `json:"-"`
	WorkspaceID int64    `json:"workspace_id"`
	ProjectID   string   `json:"project_id"`
	Scopes      []string `json:"scopes"`
	ExpiresAt   int64    `json:"exp"`
	IssuedAt    int64    `json:"iat"`
	JTI         string   `json:"jti"`
	TokenType   string   `json:"token_type"`
}

type Manager struct {
	secret    []byte
	now       func() time.Time
	consumeMu sync.Mutex
	consumed  map[string]int64
}

type claimsContextKey struct{}

func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey{}, claims)
}

func ClaimsFromContext(ctx context.Context) *Claims {
	claims, _ := ctx.Value(claimsContextKey{}).(*Claims)
	return claims
}

type Option func(*Manager)

func WithClock(now func() time.Time) Option {
	return func(m *Manager) {
		if now != nil {
			m.now = now
		}
	}
}

func New(secret string, opts ...Option) (*Manager, error) {
	if len([]byte(secret)) < MinimumSecretLen {
		return nil, fmt.Errorf("%w: need at least %d bytes", ErrSecretNotConfigured, MinimumSecretLen)
	}
	m := &Manager{secret: []byte(secret), now: time.Now, consumed: make(map[string]int64)}
	for _, opt := range opts {
		opt(m)
	}
	return m, nil
}

func (m *Manager) Issue(userID, workspaceID int64, projectID string, scopes []string) (string, Claims, error) {
	return m.issue(userID, workspaceID, projectID, scopes, TokenTypeLaunch, TokenTTL)
}

func (m *Manager) IssueSession(userID, workspaceID int64, projectID string, scopes []string) (string, Claims, error) {
	return m.issue(userID, workspaceID, projectID, scopes, TokenTypeSession, SessionTTL)
}

func (m *Manager) issue(userID, workspaceID int64, projectID string, scopes []string, tokenType string, ttl time.Duration) (string, Claims, error) {
	if m == nil || len(m.secret) == 0 {
		return "", Claims{}, ErrSecretNotConfigured
	}
	projectID = strings.TrimSpace(projectID)
	if userID <= 0 || workspaceID <= 0 || !validProjectID(projectID) {
		return "", Claims{}, fmt.Errorf("%w: invalid identity or project", ErrInvalidToken)
	}
	if len(scopes) == 0 {
		return "", Claims{}, fmt.Errorf("%w: scopes are required", ErrInvalidToken)
	}
	for _, scope := range scopes {
		if scope != ScopeRead && scope != ScopeWrite {
			return "", Claims{}, fmt.Errorf("%w: unsupported scope %q", ErrInvalidToken, scope)
		}
	}
	now := m.now().UTC()
	exp := now.Add(ttl)
	jti, err := randomJTI()
	if err != nil {
		return "", Claims{}, fmt.Errorf("issue editor launch token: %w", err)
	}
	claims := jwt.MapClaims{
		"iss":          ExpectedIssuer,
		"aud":          ExpectedAudience,
		"sub":          strconv.FormatInt(userID, 10),
		"workspace_id": workspaceID,
		"project_id":   projectID,
		"scopes":       scopes,
		"iat":          now.Unix(),
		"exp":          exp.Unix(),
		"jti":          jti,
		"token_type":   tokenType,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", Claims{}, fmt.Errorf("sign editor launch token: %w", err)
	}
	return signed, Claims{UserID: userID, WorkspaceID: workspaceID, ProjectID: projectID, Scopes: append([]string(nil), scopes...), IssuedAt: now.Unix(), ExpiresAt: exp.Unix(), JTI: jti, TokenType: tokenType}, nil
}

func (m *Manager) Verify(raw, projectID string, requiredScope string) (*Claims, error) {
	return m.verify(raw, projectID, requiredScope, TokenTypeLaunch)
}

func (m *Manager) VerifySession(raw, projectID string, requiredScope string) (*Claims, error) {
	return m.verify(raw, projectID, requiredScope, TokenTypeSession)
}

// Consume verifies a launch token and atomically marks its jti as used.
// Launch tokens are intentionally one-time credentials: the browser
// exchanges the fragment once for a longer-lived in-memory editor session.
func (m *Manager) Consume(raw, projectID string, requiredScope string) (*Claims, error) {
	claims, err := m.verify(raw, projectID, requiredScope, TokenTypeLaunch)
	if err != nil {
		return nil, err
	}
	m.consumeMu.Lock()
	defer m.consumeMu.Unlock()
	now := m.now().Unix()
	for jti, exp := range m.consumed {
		if exp <= now {
			delete(m.consumed, jti)
		}
	}
	if _, exists := m.consumed[claims.JTI]; exists {
		return nil, ErrInvalidToken
	}
	m.consumed[claims.JTI] = claims.ExpiresAt
	return claims, nil
}

func (m *Manager) verify(raw, projectID string, requiredScope, expectedType string) (*Claims, error) {
	if m == nil || len(m.secret) == 0 {
		return nil, ErrSecretNotConfigured
	}
	if strings.TrimSpace(raw) == "" {
		return nil, ErrInvalidToken
	}
	parsed, err := jwt.Parse(raw, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("%w: unexpected signing method", ErrInvalidToken)
		}
		return m.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithoutClaimsValidation())
	if err != nil || parsed == nil || !parsed.Valid {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	mapClaims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrInvalidToken
	}
	issuer, _ := mapClaims["iss"].(string)
	audience, _ := mapClaims["aud"].(string)
	if issuer != ExpectedIssuer || audience != ExpectedAudience {
		return nil, ErrInvalidToken
	}
	userID, err := claimInt64(mapClaims["sub"])
	if err != nil || userID <= 0 {
		return nil, ErrInvalidToken
	}
	workspaceID, err := claimInt64(mapClaims["workspace_id"])
	if err != nil || workspaceID <= 0 {
		return nil, ErrInvalidToken
	}
	claimProject, _ := mapClaims["project_id"].(string)
	if !validProjectID(claimProject) || (strings.TrimSpace(projectID) != "" && strings.TrimSpace(projectID) != claimProject) {
		return nil, ErrProjectMismatch
	}
	exp, err := claimInt64(mapClaims["exp"])
	if err != nil || exp <= 0 {
		return nil, ErrInvalidToken
	}
	if !m.now().Before(time.Unix(exp, 0)) {
		return nil, fmt.Errorf("%w: %s", ErrExpired, time.Unix(exp, 0).UTC().Format(time.RFC3339))
	}
	issuedAt, _ := claimInt64(mapClaims["iat"])
	maxTTL := TokenTTL
	if expectedType == TokenTypeSession {
		maxTTL = SessionTTL
	}
	if issuedAt <= 0 || time.Unix(exp, 0).Sub(time.Unix(issuedAt, 0)) > maxTTL {
		return nil, ErrInvalidToken
	}
	jti, _ := mapClaims["jti"].(string)
	tokenType, _ := mapClaims["token_type"].(string)
	if jti == "" || tokenType != expectedType {
		return nil, ErrInvalidToken
	}
	scopes := claimStrings(mapClaims["scopes"])
	if requiredScope != "" && !contains(scopes, requiredScope) {
		return nil, fmt.Errorf("%w: missing %s", ErrInvalidToken, requiredScope)
	}
	return &Claims{UserID: userID, WorkspaceID: workspaceID, ProjectID: claimProject, Scopes: scopes, IssuedAt: issuedAt, ExpiresAt: exp, JTI: jti, TokenType: tokenType}, nil
}

func validProjectID(projectID string) bool {
	return len(projectID) >= 4 && len(projectID) <= 128 && (strings.HasPrefix(projectID, "ve_") || strings.HasPrefix(projectID, "vx_")) && !strings.ContainsAny(projectID, "/\\\r\n")
}

func claimInt64(value interface{}) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case string:
		return strconv.ParseInt(v, 10, 64)
	default:
		return 0, errors.New("not an integer claim")
	}
}

func claimStrings(value interface{}) []string {
	values, ok := value.([]interface{})
	if !ok {
		if strings, ok := value.([]string); ok {
			return append([]string(nil), strings...)
		}
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func randomJTI() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
