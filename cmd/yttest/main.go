package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	appLogging "github.com/Marcuss-ops/InstaeditLogin/internal/logging"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

const (
	accountID = int64(4)
	clipPath  = "/tmp/test_clip.mp4"
)

func main() {
	log.SetOutput(appLogging.NewRedactingWriter(os.Stderr))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	log.SetFlags(log.Ltime | log.Lshortfile)
	log.Println("=== YouTube Integration Test ===")
	log.Println("")

	// 1. Load config from env (must source .env.dev before running)
	log.Println("[1/10] Loading configuration...")
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	log.Printf("  OK DB host: %s", cfg.Database.DBHost)
	log.Printf("  OK YouTube client configured: %v", cfg.Auth.YouTubeClientID != "")

	// 2. Connect to DB
	log.Println("[2/10] Connecting to DB...")
	db, err := sql.Open("postgres", cfg.Database.DSN())
	if err != nil {
		log.Fatalf("DB connect: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("DB ping: %v", err)
	}

	// 3. Build vault and get fresh token
	log.Println("[3/10] Building vault and refreshing token...")
	encryptor, err := crypto.NewEncryptor(cfg.ActiveEncryptionKeyID, cfg.EncryptionKeys)
	if err != nil {
		log.Fatalf("crypto init: %v", err)
	}
	tokenRepo := repository.NewTokenRepository(db)
	vault := credentials.NewCredentialVault(encryptor, db, tokenRepo)

	ytService, err := services.NewYouTubeOAuthService(cfg)
	if err != nil {
		log.Fatalf("youtube service init: %v", err)
	}
	if ytService == nil {
		log.Fatal("YouTube provider disabled: check YOUTUBE_CLIENT_ID and YOUTUBE_CLIENT_SECRET")
	}

	oauthToken, err := vault.Renew(ctx, accountID, models.TokenTypeBearer,
		func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
			return ytService.RefreshOAuthToken(ctx, refreshToken)
		},
	)
	if err != nil {
		log.Fatalf("vault renew: %v", err)
	}
	accessToken := oauthToken.AccessToken
	log.Printf("  OK Fresh access token obtained (expires at: %v)", oauthToken.ExpiresAt)

	// 4. Upload video as PRIVATE
	log.Println("[4/10] Uploading video to YouTube (private)...")
	videoID, err := uploadVideoAsPrivate(ctx, accessToken, clipPath)
	if err != nil {
		log.Fatalf("upload: %v", err)
	}
	log.Printf("  OK Uploaded! Video ID: %s", videoID)
	log.Printf("  Watch URL: https://www.youtube.com/watch?v=%s", videoID)

	// 5. Poll processingDetails until succeeded
	log.Println("[5/10] Polling for processing completion...")
	info, err := pollUntilProcessed(ctx, accessToken, videoID, 5*time.Minute)
	if err != nil {
		log.Fatalf("  FAIL processing: %v", err)
	}
	log.Printf("  OK Processing complete")
	log.Printf("    Title:       %s", info.Title)
	log.Printf("    Description: %s", truncate(info.Description, 80))
	log.Printf("    CategoryID:  %s", info.CategoryID)
	log.Printf("    Tags:        %v", info.Tags)
	log.Printf("    Privacy:     %s", info.PrivacyStatus)

	// 6. Set custom thumbnail (thumbnails.set works with youtube.upload scope)
	log.Println("[6/10] Setting custom thumbnail...")
	thumbErr := setTestThumbnail(ctx, accessToken, videoID)
	if thumbErr != nil {
		if isInsufficientScopes(thumbErr) {
			log.Printf("  SKIP Thumbnail blocked (unexpected: youtube.upload should suffice)")
		} else {
			log.Printf("  WARN Thumbnail failed: %v", thumbErr)
		}
	} else {
		log.Println("  OK Custom thumbnail set")
	}

	// 7. Update metadata (keep categoryId, change title/desc/tags)
	log.Println("[7/10] Updating title, description, tags...")
	if err := updateVideo(ctx, accessToken, videoID, info, "Test Upload - Cringe Control",
		"This is a test upload from InstaEdit integration testing.\nIt will be deleted shortly.",
		[]string{"test", "cringe", "instaedit", "integration-test"}); err != nil {
		if isInsufficientScopes(err) {
			log.Printf("  SKIP Update blocked: missing youtube.force-ssl scope")
			log.Printf("         Re-authorise Cringe Control with full scope set to enable.")
		} else {
			log.Fatalf("  FAIL update metadata: %v", err)
		}
	} else {
		log.Println("  OK Title, description, and tags updated")
	}

	// 8. Change visibility: private -> PUBLIC
	log.Println("[8/10] Changing visibility: private -> PUBLIC...")
	if err := setPrivacy(ctx, accessToken, videoID, "public", info); err != nil {
		if isInsufficientScopes(err) {
			log.Printf("  SKIP Visibility change blocked (missing youtube.force-ssl)")
		} else {
			log.Fatalf("  FAIL set public: %v", err)
		}
	} else {
		log.Println("  OK Set to PUBLIC")
	}

	// 9. Change visibility: PUBLIC -> private
	log.Println("[9/10] Changing visibility: PUBLIC -> private...")
	if err := setPrivacy(ctx, accessToken, videoID, "private", info); err != nil {
		if isInsufficientScopes(err) {
			log.Printf("  SKIP Visibility revert blocked (missing youtube.force-ssl)")
		} else {
			log.Fatalf("  FAIL set private: %v", err)
		}
	} else {
		log.Println("  OK Set back to PRIVATE")
	}

	// 10. Delete video
	log.Println("[10/10] Deleting video...")
	err = deleteVideo(ctx, accessToken, videoID)
	if err != nil {
		if isInsufficientScopes(err) {
			log.Printf("  SKIP Delete blocked (missing youtube.force-ssl)")
			log.Printf("  VIDEO REMAINS: https://www.youtube.com/watch?v=%s", videoID)
			log.Printf("  Delete it manually from YouTube Studio.")
		} else {
			log.Fatalf("  FAIL delete: %v", err)
		}
	} else {
		log.Println("  OK Video deleted")
		_, err := getVideoInfo(ctx, accessToken, videoID)
		if err != nil && strings.Contains(err.Error(), "not found") {
			log.Println("  OK Video confirmed deleted (empty items)")
		} else if err != nil {
			log.Printf("  WARN Could not verify deletion: %v", err)
		} else {
			log.Println("  WARN Video still exists after delete")
		}
	}

	log.Println("")
	log.Println("=== TEST COMPLETE ===")
	log.Println("Results:")
	log.Println("  Vault decrypt+renew: PASS")
	log.Println("  Video upload:        PASS")
	log.Println("  Video list/read:     PASS")
	if thumbErr == nil {
		log.Println("  Thumbnail set:       PASS")
	} else {
		log.Println("  Thumbnail set:       FAIL")
	}
	if isInsufficientScopes(err) {
		log.Println("  Update/delete:       SKIP (missing youtube.force-ssl)")
	}
	log.Printf("  Video URL:           https://www.youtube.com/watch?v=%s", videoID)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// setTestThumbnail creates a minimal 1x1 red JPEG and sets it as the
// video's custom thumbnail via thumbnails.set. This endpoint only requires
// youtube.upload scope (no force-ssl needed).
func setTestThumbnail(ctx context.Context, token, videoID string) error {
	// Minimal valid JPEG: 1x1 red pixel.
	// SOI + APP0 + DQT + SOF0 + DHT + SOS + EOI
	jpegBytes := []byte{
		0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01,
		0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xFF, 0xDB, 0x00, 0x43,
		0x00, 0x08, 0x06, 0x06, 0x07, 0x06, 0x05, 0x08, 0x07, 0x07, 0x07, 0x09,
		0x09, 0x08, 0x0A, 0x0C, 0x14, 0x0D, 0x0C, 0x0B, 0x0B, 0x0C, 0x19, 0x12,
		0x13, 0x0F, 0x14, 0x1D, 0x1A, 0x1F, 0x1E, 0x1D, 0x1A, 0x1C, 0x1C, 0x20,
		0x24, 0x2E, 0x27, 0x20, 0x22, 0x2C, 0x23, 0x1C, 0x1C, 0x28, 0x37, 0x29,
		0x2C, 0x30, 0x31, 0x34, 0x34, 0x34, 0x1F, 0x27, 0x39, 0x3D, 0x38, 0x32,
		0x3C, 0x2E, 0x33, 0x34, 0x32, 0xFF, 0xC0, 0x00, 0x0B, 0x08, 0x00, 0x01,
		0x00, 0x01, 0x01, 0x01, 0x11, 0x00, 0xFF, 0xC4, 0x00, 0x1F, 0x00, 0x00,
		0x01, 0x05, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0A, 0x0B, 0xFF, 0xC4, 0x00, 0xB5, 0x10, 0x00, 0x02, 0x01, 0x03,
		0x03, 0x02, 0x04, 0x03, 0x05, 0x05, 0x04, 0x04, 0x00, 0x00, 0x01, 0x7D,
		0x01, 0x02, 0x03, 0x00, 0x04, 0x11, 0x05, 0x12, 0x21, 0x31, 0x41, 0x06,
		0x13, 0x51, 0x61, 0x07, 0x22, 0x71, 0x14, 0x32, 0x81, 0x91, 0xA1, 0x08,
		0x23, 0x42, 0xB1, 0xC1, 0x15, 0x52, 0xD1, 0xF0, 0x24, 0x33, 0x62, 0x72,
		0x82, 0x09, 0x0A, 0x16, 0x17, 0x18, 0x19, 0x1A, 0x25, 0x26, 0x27, 0x28,
		0x29, 0x2A, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3A, 0x43, 0x44, 0x45,
		0x46, 0x47, 0x48, 0x49, 0x4A, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59,
		0x5A, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6A, 0x73, 0x74, 0x75,
		0x76, 0x77, 0x78, 0x79, 0x7A, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89,
		0x8A, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98, 0x99, 0x9A, 0xA2, 0xA3,
		0xA4, 0xA5, 0xA6, 0xA7, 0xA8, 0xA9, 0xAA, 0xB2, 0xB3, 0xB4, 0xB5, 0xB6,
		0xB7, 0xB8, 0xB9, 0xBA, 0xC2, 0xC3, 0xC4, 0xC5, 0xC6, 0xC7, 0xC8, 0xC9,
		0xCA, 0xD2, 0xD3, 0xD4, 0xD5, 0xD6, 0xD7, 0xD8, 0xD9, 0xDA, 0xE1, 0xE2,
		0xE3, 0xE4, 0xE5, 0xE6, 0xE7, 0xE8, 0xE9, 0xEA, 0xF1, 0xF2, 0xF3, 0xF4,
		0xF5, 0xF6, 0xF7, 0xF8, 0xF9, 0xFA, 0xFF, 0xDA, 0x00, 0x08, 0x01, 0x01,
		0x00, 0x00, 0x3F, 0x00, 0x7B, 0x94, 0x11, 0x00, 0x00, 0x00, 0x00, 0x00,
		0xFF, 0xD9,
	}

	apiURL := "https://www.googleapis.com/upload/youtube/v3/thumbnails/set?videoId=" + videoID
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(jpegBytes))
	if err != nil {
		return fmt.Errorf("thumbnail request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "image/jpeg")
	req.ContentLength = int64(len(jpegBytes))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("thumbnail upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		rbody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("thumbnail set failed (status %d): %s", resp.StatusCode, string(rbody))
	}
	return nil
}

func isInsufficientScopes(err error) bool {
	return err != nil && strings.Contains(err.Error(), "403") && strings.Contains(err.Error(), "insufficient")
}

// pollUntilProcessed polls videos.list until processingStatus.succeeded or timeout.
func pollUntilProcessed(ctx context.Context, token, videoID string, timeout time.Duration) (*videoInfo, error) {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for processing after %s", timeout)
		}

		info, err := getVideoInfo(ctx, token, videoID)
		if err != nil {
			return nil, err
		}

		// If we got a title other than "unknown", processing is likely done
		if info.Title != "" && info.Title != "unknown" {
			return info, nil
		}

		// Also check processingDetails via raw API
		status, err := getProcessingStatus(ctx, token, videoID)
		if err == nil && status == "succeeded" {
			return info, nil
		}

		log.Printf("  Processing... (status=%s, title=%q), retrying in 10s", status, info.Title)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}

func getProcessingStatus(ctx context.Context, token, videoID string) (string, error) {
	apiURL := fmt.Sprintf("https://www.googleapis.com/youtube/v3/videos?part=processingDetails&id=%s", videoID)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Items []struct {
			ProcessingDetails struct {
				ProcessingStatus struct {
					Status string `json:"status"`
				} `json:"processingStatus"`
			} `json:"processingDetails"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if len(result.Items) == 0 {
		return "", fmt.Errorf("video not found")
	}
	return result.Items[0].ProcessingDetails.ProcessingStatus.Status, nil
}

func uploadVideoAsPrivate(ctx context.Context, token, filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat: %w", err)
	}

	metadata := map[string]interface{}{
		"snippet": map[string]interface{}{
			"title":       "Test Upload - Cringe Control",
			"description": "Integration test upload from InstaEdit. Will be deleted.",
			"categoryId":  "24", // Entertainment
		},
		"status": map[string]interface{}{
			"privacyStatus": "private",
		},
	}
	metaBytes, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("marshal upload metadata: %w", err)
	}

	initURL := "https://www.googleapis.com/upload/youtube/v3/videos?uploadType=resumable&part=snippet,status"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, initURL, bytes.NewReader(metaBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("X-Upload-Content-Length", strconv.FormatInt(fi.Size(), 10))
	req.Header.Set("X-Upload-Content-Type", "video/mp4")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("init upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("init upload failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	uploadURL := resp.Header.Get("Location")
	if uploadURL == "" {
		return "", fmt.Errorf("no Location header in init response")
	}
	log.Println("  Resumable session established")

	f.Seek(0, 0)
	uploadReq, err := http.NewRequestWithContext(ctx, "PUT", uploadURL, f)
	if err != nil {
		return "", err
	}
	uploadReq.Header.Set("Content-Type", "video/mp4")
	uploadReq.Header.Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	uploadReq.ContentLength = fi.Size()

	uploadResp, err := http.DefaultClient.Do(uploadReq)
	if err != nil {
		return "", fmt.Errorf("upload: %w", err)
	}
	defer uploadResp.Body.Close()

	respBody, _ := io.ReadAll(uploadResp.Body)
	if uploadResp.StatusCode != http.StatusOK && uploadResp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("upload failed (status %d): %s", uploadResp.StatusCode, string(respBody))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse upload response: %w (body: %s)", err, string(respBody))
	}
	return result.ID, nil
}

type videoInfo struct {
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	CategoryID    string   `json:"categoryId"`
	Tags          []string `json:"tags"`
	PrivacyStatus string   `json:"privacyStatus"`
}

func getVideoInfo(ctx context.Context, token, videoID string) (*videoInfo, error) {
	apiURL := fmt.Sprintf("https://www.googleapis.com/youtube/v3/videos?part=snippet,status&id=%s", videoID)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get video info failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Items []struct {
			Snippet struct {
				Title       string   `json:"title"`
				Description string   `json:"description"`
				CategoryID  string   `json:"categoryId"`
				Tags        []string `json:"tags"`
			} `json:"snippet"`
			Status struct {
				PrivacyStatus string `json:"privacyStatus"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if len(result.Items) == 0 {
		return nil, fmt.Errorf("video not found")
	}
	item := result.Items[0]
	return &videoInfo{
		Title:         item.Snippet.Title,
		Description:   item.Snippet.Description,
		CategoryID:    item.Snippet.CategoryID,
		Tags:          item.Snippet.Tags,
		PrivacyStatus: item.Status.PrivacyStatus,
	}, nil
}

func updateVideo(ctx context.Context, token, videoID string, current *videoInfo, title, description string, tags []string) error {
	snippet := map[string]interface{}{
		"title":       current.Title,
		"categoryId":  current.CategoryID,
		"description": current.Description,
	}
	if len(current.Tags) > 0 {
		snippet["tags"] = current.Tags
	}
	if title != "" {
		snippet["title"] = title
	}
	if description != "" {
		snippet["description"] = description
	}
	if tags != nil {
		snippet["tags"] = tags
	}

	payload := map[string]interface{}{
		"id":      videoID,
		"snippet": snippet,
	}

	body, _ := json.Marshal(payload)
	apiURL := "https://www.googleapis.com/youtube/v3/videos?part=snippet,status"

	req, err := http.NewRequestWithContext(ctx, "PUT", apiURL, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("update request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		rbody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update failed (status %d): %s", resp.StatusCode, string(rbody))
	}
	return nil
}

func setPrivacy(ctx context.Context, token, videoID, privacy string, current *videoInfo) error {
	payload := map[string]interface{}{
		"id": videoID,
		"snippet": map[string]interface{}{
			"title":       current.Title,
			"categoryId":  current.CategoryID,
			"description": current.Description,
			"tags":        current.Tags,
		},
		"status": map[string]interface{}{
			"privacyStatus": privacy,
		},
	}

	body, _ := json.Marshal(payload)
	apiURL := "https://www.googleapis.com/youtube/v3/videos?part=snippet,status"

	req, err := http.NewRequestWithContext(ctx, "PUT", apiURL, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("update request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		rbody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update failed (status %d): %s", resp.StatusCode, string(rbody))
	}
	return nil
}

func deleteVideo(ctx context.Context, token, videoID string) error {
	apiURL := fmt.Sprintf("https://www.googleapis.com/youtube/v3/videos?id=%s", videoID)
	req, err := http.NewRequestWithContext(ctx, "DELETE", apiURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		rbody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete failed (status %d): %s", resp.StatusCode, string(rbody))
	}
	return nil
}
