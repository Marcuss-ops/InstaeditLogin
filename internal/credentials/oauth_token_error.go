package credentials

import (
	"encoding/json"
	"fmt"
)

// OAuthTokenError is the typed, redacted representation of an OAuth token
// endpoint failure. Description is retained for in-process diagnostics, but
// Error deliberately omits it because provider descriptions may contain
// credential-adjacent or personal data.
type OAuthTokenError struct {
	StatusCode  int
	Code        string
	Description string
}

// Error returns only stable, non-sensitive error metadata. Callers should use
// errors.Is(err, ErrInvalidGrant) rather than inspecting this message.
func (e *OAuthTokenError) Error() string {
	if e == nil {
		return "oauth token request failed"
	}
	if e.Code == "" {
		return fmt.Sprintf("oauth token request failed (status %d)", e.StatusCode)
	}
	return fmt.Sprintf("oauth token request failed (status %d, code %s)", e.StatusCode, e.Code)
}

// Unwrap maps RFC 6749 invalid_grant to the domain sentinel consumed by the
// vault, workers, and HTTP handlers. Other OAuth error codes remain typed but
// do not request reauthorization automatically.
func (e *OAuthTokenError) Unwrap() error {
	if e != nil && e.Code == "invalid_grant" {
		return ErrInvalidGrant
	}
	return nil
}

// ErrorKindName implements metrics.ErrorKindCarrier: token-endpoint failures
// are announced as auth (or network for transport-shaped statuses) instead of
// being guessed from message text — the provider description is deliberately
// redacted from Error(), so the substring heuristic never sees useful text.
func (e *OAuthTokenError) ErrorKindName() string {
	if e == nil {
		return "internal"
	}
	switch {
	case e.StatusCode == 0:
		return "internal"
	case e.StatusCode == 429 || e.StatusCode >= 500:
		return "network"
	default:
		return "auth"
	}
}

// ParseOAuthTokenError decodes a provider token-endpoint error envelope. It
// always returns a typed error for a non-success response, including malformed
// or non-JSON bodies; only the stable `error` enum is retained in Error().
func ParseOAuthTokenError(statusCode int, body []byte) *OAuthTokenError {
	var envelope struct {
		Code        string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &envelope)
	return &OAuthTokenError{
		StatusCode:  statusCode,
		Code:        envelope.Code,
		Description: envelope.Description,
	}
}
