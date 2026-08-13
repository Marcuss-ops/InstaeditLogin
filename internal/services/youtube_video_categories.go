package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// YouTubeVideoCategory is one videoCategories.list item projected to
// the shape every category select in the SPA consumes ({id, label}).
type YouTubeVideoCategory struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// videoCategoriesResponse is the wire shape of videoCategories.list.
type videoCategoriesResponse struct {
	Items []struct {
		ID      string `json:"id"`
		Snippet struct {
			Title string `json:"title"`
		} `json:"snippet"`
	} `json:"items"`
}

// ListVideoCategories fetches the YouTube videoCategories.list
// projection for a region. regionCode is an ISO 3166-1 alpha-2 country
// code; the empty string falls back to YouTube's global default region.
//
// The endpoint is NOT channel-scoped — any valid OAuth token of a
// connected account serves the list — so callers only need to mint a
// token for SOME account (the handler resolves the first active YouTube
// account of the workspace). Labels are requested in Italian (hl=it) so
// the projection stays consistent with the canonical YOUTUBE_CATEGORIES
// snapshot the SPA serves as its 404 fallback.
//
// Upstream failures surface as *YouTubeAPIError (0 = network / 401 /
// 403 / 429 / 5xx) so the handler can map quota and server errors
// without leaking provider internals.
func (s *YouTubeOAuthService) ListVideoCategories(ctx context.Context, accessToken, regionCode string) ([]YouTubeVideoCategory, error) {
	params := url.Values{}
	params.Set("part", "snippet")
	params.Set("hl", "it")
	if regionCode != "" {
		params.Set("regionCode", strings.ToUpper(regionCode))
	}
	reqURL := "https://www.googleapis.com/youtube/v3/videoCategories?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("youtube list video categories: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, &YouTubeAPIError{
			StatusCode: 0,
			Category:   "network",
			Message:    fmt.Sprintf("youtube list video categories: request failed: %v", err),
		}
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return nil, &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "auth", Message: "youtube list video categories: unauthorized (status " + fmt.Sprint(resp.StatusCode) + ")"}
		case resp.StatusCode == http.StatusTooManyRequests:
			return nil, &YouTubeAPIError{StatusCode: http.StatusTooManyRequests, Category: "rate_limit", Message: "youtube list video categories: rate limited (status 429)"}
		case resp.StatusCode >= 500:
			return nil, &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "server_error", Message: fmt.Sprintf("youtube list video categories: server error (status %d)", resp.StatusCode)}
		default:
			return nil, &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "unexpected", Message: fmt.Sprintf("youtube list video categories: unexpected status %d", resp.StatusCode)}
		}
	}

	var payload videoCategoriesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("youtube list video categories: decode response: %w", err)
	}
	resp.Body.Close()

	categories := make([]YouTubeVideoCategory, 0, len(payload.Items))
	for _, item := range payload.Items {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		categories = append(categories, YouTubeVideoCategory{
			ID:    item.ID,
			Label: strings.TrimSpace(item.Snippet.Title),
		})
	}
	return categories, nil
}
