package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
)

// MetaService handles calls to the Meta Graph API for publishing content.
type MetaService struct {
	cfg        *config.Config
	httpClient *http.Client
}

// NewMetaService creates a new MetaService.
func NewMetaService(cfg *config.Config) *MetaService {
	return &MetaService{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// PublishPhoto publishes a photo to an Instagram Business account.
func (s *MetaService) PublishPhoto(accessToken, instagramUserID, imageURL, caption string) (string, error) {
	slog.Info("Publishing photo to Instagram", "instagram_user_id", instagramUserID)

	// Step 1: Create media container
	containerID, err := s.createMediaContainer(accessToken, instagramUserID, imageURL, caption, "IMAGE")
	if err != nil {
		return "", fmt.Errorf("failed to create media container: %w", err)
	}

	// Step 2: Publish the media container
	mediaID, err := s.publishMediaContainer(accessToken, instagramUserID, containerID)
	if err != nil {
		return "", fmt.Errorf("failed to publish media: %w", err)
	}

	slog.Info("Photo published successfully", "media_id", mediaID)
	return mediaID, nil
}

// PublishVideo publishes a video/reel to an Instagram Business account.
func (s *MetaService) PublishVideo(accessToken, instagramUserID, videoURL, caption string) (string, error) {
	slog.Info("Publishing video to Instagram", "instagram_user_id", instagramUserID)

	// Step 1: Create media container (video)
	containerID, err := s.createMediaContainer(accessToken, instagramUserID, videoURL, caption, "REELS")
	if err != nil {
		return "", fmt.Errorf("failed to create video container: %w", err)
	}

	// Step 2: Poll until container is ready
	if err := s.waitForContainerReady(accessToken, containerID); err != nil {
		return "", fmt.Errorf("video container not ready: %w", err)
	}

	// Step 3: Publish
	mediaID, err := s.publishMediaContainer(accessToken, instagramUserID, containerID)
	if err != nil {
		return "", fmt.Errorf("failed to publish video: %w", err)
	}

	slog.Info("Video published successfully", "media_id", mediaID)
	return mediaID, nil
}

// createMediaContainer creates a media container for Instagram publishing.
func (s *MetaService) createMediaContainer(accessToken, instagramUserID, mediaURL, caption, mediaType string) (string, error) {
	body := map[string]string{
		"media_type": mediaType,
		"caption":    caption,
	}

	if mediaType == "IMAGE" {
		body["image_url"] = mediaURL
	} else {
		body["video_url"] = mediaURL
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("failed to marshal body: %w", err)
	}

	url := fmt.Sprintf("https://graph.facebook.com/v19.0/%s/media?access_token=%s", instagramUserID, accessToken)
	resp, err := s.httpClient.Post(url, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("media container request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read media container response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("media container failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to parse media container response: %w", err)
	}

	return result.ID, nil
}

// publishMediaContainer publishes a ready media container.
func (s *MetaService) publishMediaContainer(accessToken, instagramUserID, containerID string) (string, error) {
	body := map[string]string{
		"creation_id": containerID,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("failed to marshal body: %w", err)
	}

	url := fmt.Sprintf("https://graph.facebook.com/v19.0/%s/media_publish?access_token=%s", instagramUserID, accessToken)
	resp, err := s.httpClient.Post(url, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("media publish request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read media publish response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("media publish failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to parse media publish response: %w", err)
	}

	return result.ID, nil
}

// waitForContainerReady polls the container status until it's ready for publishing.
func (s *MetaService) waitForContainerReady(accessToken, containerID string) error {
	maxAttempts := 30
	for i := 0; i < maxAttempts; i++ {
		time.Sleep(2 * time.Second)

		url := fmt.Sprintf(
			"https://graph.facebook.com/v19.0/%s?fields=status_code&access_token=%s",
			containerID, accessToken,
		)

		resp, err := s.httpClient.Get(url)
		if err != nil {
			slog.Warn("Container status check failed, retrying...", "error", err, "attempt", i+1)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result struct {
			StatusCode string `json:"status_code"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			continue
		}

		slog.Debug("Container status", "status_code", result.StatusCode, "attempt", i+1)

		switch result.StatusCode {
		case "FINISHED":
			return nil
		case "ERROR":
			return fmt.Errorf("container processing failed: %s", string(body))
		}
		// IN_PROGRESS or EXPIRED — keep polling or fail
	}

	return fmt.Errorf("container not ready after %d attempts", maxAttempts)
}
