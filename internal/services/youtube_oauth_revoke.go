package services

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Revoke calls Google's OAuth 2.0 token revocation endpoint. It remains as
// the compatibility method used by the legacy single-account disconnect path.
func (s *YouTubeOAuthService) Revoke(ctx context.Context, token string) error {
	return s.revokeToken(ctx, token)
}

// RevokeGrant implements the complete-grant revocation capability. The
// caller supplies the decoded refresh token from the credential vault; this
// method never logs or includes that token in an error.
func (s *YouTubeOAuthService) RevokeGrant(ctx context.Context, token string) error {
	return s.revokeToken(ctx, token)
}

func (s *YouTubeOAuthService) revokeToken(ctx context.Context, token string) error {
	if token == "" {
		return &OAuthGrantRevocationError{
			Class: OAuthGrantRevocationPermanent,
			Cause: errors.New("empty revocation token"),
		}
	}
	body := url.Values{}
	body.Set("token", token)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://oauth2.googleapis.com/revoke",
		strings.NewReader(body.Encode()))
	if err != nil {
		return &OAuthGrantRevocationError{Class: OAuthGrantRevocationPermanent, Cause: err}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &OAuthGrantRevocationError{
			Class: OAuthGrantRevocationTransient,
			Cause: err,
		}
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	if resp.StatusCode == http.StatusBadRequest {
		var envelope struct {
			Code string `json:"error"`
		}
		_ = json.Unmarshal(responseBody, &envelope)
		envelope.Code = safeOAuthRevocationCode(envelope.Code)
		if envelope.Code == "invalid_token" {
			// Google has already rejected the grant. Treat this as an
			// idempotent success: local cleanup is safe and a retry after a
			// partially completed disconnect must not be blocked forever.
			return OAuthGrantRevocationAlreadyCompleted
		}
		return &OAuthGrantRevocationError{
			StatusCode: resp.StatusCode,
			Code:       envelope.Code,
			Class:      OAuthGrantRevocationPermanent,
		}
	}

	class := OAuthGrantRevocationPermanent
	if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		class = OAuthGrantRevocationTransient
	}
	return &OAuthGrantRevocationError{
		StatusCode: resp.StatusCode,
		Class:      class,
		RetryAfter: ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
	}
}

// safeOAuthRevocationCode whitelists the OAuth error codes that are safe
// to surface in logs/errors; anything else is redacted.
func safeOAuthRevocationCode(code string) string {
	switch code {
	case "invalid_token", "invalid_request", "invalid_client":
		return code
	default:
		return ""
	}
}
