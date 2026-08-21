package main

import (
	"net/http"
)

type editorSessionResponse struct {
	SessionID      string `json:"session_id"`
	VeloxProjectID string `json:"velox_project_id"`
	EditorURL      string `json:"editor_url"`
	YouTubeVideoID string `json:"youtube_video_id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	PrivacyStatus  string `json:"privacy_status"`
}

type publishResponse struct {
	Status            string `json:"status"`
	PublicURL         string `json:"public_url"`
	VideoID           string `json:"video_id"`
	PrivacyStatus     string `json:"privacy_status"`
	ActualPrivacy     string `json:"actual_privacy"`
	YouTubeSyncStatus string `json:"youtube_sync_status"`
}

func getEditorSession(c *client, sessionID string) (*editorSessionResponse, error) {
	var resp editorSessionResponse
	if err := c.request(http.MethodGet, "/api/v1/youtube/editor-sessions/"+sessionID, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func createEditorSession(c *client, workspaceID, accountID int64, youtubeVideoID string) (*editorSessionResponse, error) {
	var resp editorSessionResponse
	err := c.request(http.MethodPost, "/api/v1/youtube/editor-sessions", map[string]any{
		"workspace_id":        workspaceID,
		"platform_account_id": accountID,
		"youtube_video_id":    youtubeVideoID,
	}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// setThumbnail links an already-uploaded media asset to the session.
func setThumbnail(c *client, sessionID, mediaID string) error {
	var resp struct {
		SessionID        string `json:"session_id"`
		ThumbnailMediaID string `json:"thumbnail_media_id"`
		ThumbnailStatus  string `json:"thumbnail_status"`
	}
	return c.request(http.MethodPost, "/api/v1/youtube/editor-sessions/"+sessionID+"/thumbnail",
		map[string]any{"thumbnail_media_id": mediaID}, &resp)
}

// setThumbnailFromFile normalizes, uploads and attaches a cover image.
// Returns the media asset id of the uploaded thumbnail.
func setThumbnailFromFile(c *client, sessionID, imagePath string) (string, error) {
	jpegBytes, err := normalizeThumbnail(imagePath)
	if err != nil {
		return "", err
	}
	mediaID, err := uploadBytes(c, "thumbnail.jpg", "image/jpeg", jpegBytes)
	if err != nil {
		return "", err
	}
	if err := setThumbnail(c, sessionID, mediaID); err != nil {
		return "", err
	}
	return mediaID, nil
}

func publishVideo(c *client, sessionID, privacyStatus, title, description string) (*publishResponse, error) {
	body := map[string]any{
		"privacy_status": privacyStatus,
	}
	if title != "" {
		body["title"] = title
	}
	if description != "" {
		body["description"] = description
	}
	var resp publishResponse
	if err := c.request(http.MethodPost, "/api/v1/youtube/editor-sessions/"+sessionID+"/publish", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func coverAndPublish(c *client, sessionID, coverPath, privacyStatus, title, description string) (*publishResponse, error) {
	if _, err := setThumbnailFromFile(c, sessionID, coverPath); err != nil {
		return nil, err
	}
	return publishVideo(c, sessionID, privacyStatus, title, description)
}
