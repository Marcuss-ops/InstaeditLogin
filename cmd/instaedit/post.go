package main

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type postCreateRequest struct {
	WorkspaceID int64              `json:"workspace_id"`
	Content     postContent        `json:"content"`
	Targets     []postCreateTarget `json:"targets"`
}

type mediaRef struct {
	AssetID string `json:"asset_id"`
}

type postContent struct {
	Title   string     `json:"title,omitempty"`
	Caption string     `json:"caption,omitempty"`
	Media   []mediaRef `json:"media,omitempty"`
}

type postCreateTarget struct {
	PlatformAccountID int64 `json:"platform_account_id"`
}

type postTargetResponse struct {
	ID             int64  `json:"id"`
	PostID         int64  `json:"post_id"`
	Status         string `json:"status"`
	PlatformPostID string `json:"platform_post_id"`
	RemotePostID   string `json:"remote_post_id"`
	RemotePostURL  string `json:"remote_post_url"`
	ErrorMessage   string `json:"error_message"`
}

type postCreateResponse struct {
	ID   int64 `json:"id"`
	Post struct {
		ID int64 `json:"id"`
	} `json:"post"`
	Targets []postTargetResponse `json:"targets"`
}

type localYouTubeUploadResult struct {
	MediaID        string           `json:"media_id"`
	PostID         int64            `json:"post_id"`
	TargetID       int64            `json:"target_id"`
	YouTubeVideoID string           `json:"youtube_video_id"`
	YouTubeURL     string           `json:"youtube_url,omitempty"`
	SessionID      string           `json:"session_id"`
	Publish        *publishResponse `json:"publish,omitempty"`
}

func createPost(c *client, workspaceID, accountID int64, mediaID, title, caption string) (*postCreateResponse, error) {
	var response postCreateResponse
	body := postCreateRequest{
		WorkspaceID: workspaceID,
		Content: postContent{
			Title:   title,
			Caption: caption,
			Media:   []mediaRef{{AssetID: mediaID}},
		},
		Targets: []postCreateTarget{{PlatformAccountID: accountID}},
	}
	// A deterministic key makes a retried command safe when it reuses
	// the same uploaded media asset and target tuple.
	idempotencyKey := "instaedit-" + mediaID + "-" + strconv.FormatInt(workspaceID, 10) + "-" + strconv.FormatInt(accountID, 10)
	if err := c.requestWithHeaders(http.MethodPost, "/api/v1/posts/", body, &response, map[string]string{
		"Idempotency-Key": idempotencyKey,
	}); err != nil {
		return nil, err
	}
	if response.ID == 0 {
		response.ID = response.Post.ID
	}
	if response.ID == 0 {
		return nil, fmt.Errorf("create post response did not contain a post id")
	}
	if len(response.Targets) == 0 || response.Targets[0].ID == 0 {
		return nil, fmt.Errorf("create post response did not contain a post target id")
	}
	return &response, nil
}

func publishPost(c *client, postID int64) error {
	return c.request(http.MethodPost, "/api/v1/posts/"+strconv.FormatInt(postID, 10)+"/publish", nil, nil)
}

// waitForPostTarget waits until the publishing worker exposes the real
// YouTube video id. It returns as soon as the provider post id is present;
// the editor-session creation that follows performs the authoritative
// processed/channel/privacy checks before changing the video.
func waitForPostTarget(c *client, targetID int64, timeout, interval time.Duration) (*postTargetResponse, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("poll timeout must be positive")
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		var target postTargetResponse
		if err := c.request(http.MethodGet, "/api/v1/post-targets/"+strconv.FormatInt(targetID, 10), nil, &target); err != nil {
			return nil, err
		}
		if target.RemotePostID == "" {
			target.RemotePostID = target.PlatformPostID
		}
		if target.RemotePostID != "" {
			return &target, nil
		}
		switch target.Status {
		case "failed", "dlq", "blocked_auth":
			if target.ErrorMessage == "" {
				target.ErrorMessage = "target reached terminal status: " + target.Status
			}
			return nil, fmt.Errorf("youtube upload failed: %s", target.ErrorMessage)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("timed out waiting for YouTube video id on target %d", targetID)
		}
		if interval > remaining {
			interval = remaining
		}
		time.Sleep(interval)
	}
}

func uploadAndPublishYouTube(
	c *client,
	workspaceID, accountID int64,
	videoPath, coverPath, title, description, privacy string,
	pollTimeout time.Duration,
) (*localYouTubeUploadResult, error) {
	mediaID, err := uploadFile(c, videoPath)
	if err != nil {
		return nil, err
	}

	post, err := createPost(c, workspaceID, accountID, mediaID, title, description)
	if err != nil {
		return nil, err
	}
	target := post.Targets[0]
	if err := publishPost(c, post.ID); err != nil {
		return nil, err
	}

	publishedTarget, err := waitForPostTarget(c, target.ID, pollTimeout, 2*time.Second)
	if err != nil {
		return nil, err
	}

	session, err := createEditorSession(c, workspaceID, accountID, publishedTarget.RemotePostID)
	if err != nil {
		return nil, err
	}
	if coverPath != "" {
		if _, err := setThumbnailFromFile(c, session.SessionID, coverPath); err != nil {
			return nil, err
		}
	}
	publish, err := publishVideo(c, session.SessionID, privacy, title, description)
	if err != nil {
		return nil, err
	}

	return &localYouTubeUploadResult{
		MediaID:        mediaID,
		PostID:         post.ID,
		TargetID:       target.ID,
		YouTubeVideoID: publishedTarget.RemotePostID,
		YouTubeURL:     publishedTarget.RemotePostURL,
		SessionID:      session.SessionID,
		Publish:        publish,
	}, nil
}
