package services

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

func (s *YouTubeOAuthService) headVideo(ctx context.Context, videoURL string) (size int64, contentType string, err error) {
	req, err := http.NewRequestWithContext(ctx, "HEAD", videoURL, nil)
	if err != nil {
		return 0, "", fmt.Errorf("HEAD request creation failed: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("HEAD request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return s.headViaRange(ctx, videoURL)
	}

	return resp.ContentLength, resp.Header.Get("Content-Type"), nil
}

func (s *YouTubeOAuthService) headViaRange(ctx context.Context, videoURL string) (int64, string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", videoURL, nil)
	req.Header.Set("Range", "bytes=0-0")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		return 0, "", fmt.Errorf("unable to determine video size (status %d)", resp.StatusCode)
	}

	contentRange := resp.Header.Get("Content-Range")
	parts := strings.Split(contentRange, "/")
	if len(parts) != 2 {
		return 0, resp.Header.Get("Content-Type"), fmt.Errorf("unexpected Content-Range: %s", contentRange)
	}

	var total int64
	if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &total); err != nil {
		return 0, "", fmt.Errorf("failed to parse total size: %w", err)
	}

	return total, resp.Header.Get("Content-Type"), nil
}
