package services

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// postTokenRequest is the shared POST /token helper used by both the
// authorization-code exchange (exchangeCodeForToken) and the refresh
// flow (RefreshOAuthToken). The two flows differ only in the form
// body; the transport, status check and JSON parse are identical, so
// they live in one place to keep error semantics + quota behaviour
// consistent across both callers.
func (s *YouTubeOAuthService) postTokenRequest(ctx context.Context, body url.Values) (*youtubeTokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", "https://oauth2.googleapis.com/token",
		strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		// Preserve the provider's stable OAuth error code in a typed,
		// redacted error. OAuthTokenError.Unwrap maps invalid_grant to
		// credentials.ErrInvalidGrant without exposing error_description.
		return nil, credentials.ParseOAuthTokenError(resp.StatusCode, respBody)
	}

	var tr youtubeTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("token parse: %w", err)
	}
	return &tr, nil
}
