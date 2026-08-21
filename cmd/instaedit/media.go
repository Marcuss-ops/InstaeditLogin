package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// extToContentType maps the InstaEdit upload allowlist to MIME types.
// Kept explicit (instead of relying on mime.TypeByExtension) so the
// mapping exactly matches the server allowlist.
const maxMediaUploadBytes int64 = 200 * 1024 * 1024

var extToContentType = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
	".mp4":  "video/mp4",
	".mov":  "video/quicktime",
}

var allowedContentTypes = map[string]bool{
	"image/jpeg":      true,
	"image/png":       true,
	"image/webp":      true,
	"video/mp4":       true,
	"video/quicktime": true,
}

type presignRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256,omitempty"`
}

type presignResponse struct {
	AssetID       string            `json:"asset_id"`
	UploadURL     string            `json:"upload_url"`
	UploadMethod  string            `json:"upload_method"`
	UploadHeaders map[string]string `json:"upload_headers"`
	ExpiresAt     string            `json:"expires_at"`
	ContentType   string            `json:"content_type"`
	MaxSizeBytes  int64             `json:"max_size_bytes"`
}

type mediaAsset struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256,omitempty"`
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// uploadBytes performs the three-step media pipeline:
// presign -> PUT to storage -> complete. Returns the media asset id.
func uploadBytes(c *client, filename, contentType string, data []byte) (string, error) {
	if filename == "" {
		return "", fmt.Errorf("filename is required")
	}
	if len(data) == 0 {
		return "", fmt.Errorf("file must not be empty")
	}
	if int64(len(data)) > maxMediaUploadBytes {
		return "", fmt.Errorf("file size %d bytes exceeds the %d MiB limit", len(data), maxMediaUploadBytes/(1024*1024))
	}
	if !allowedContentTypes[contentType] {
		return "", fmt.Errorf("unsupported MIME type %q", contentType)
	}

	var grant presignResponse
	if err := c.request(http.MethodPost, "/api/v1/media/presign", presignRequest{
		Filename:    filename,
		ContentType: contentType,
		SizeBytes:   int64(len(data)),
		SHA256:      sha256Hex(data),
	}, &grant); err != nil {
		return "", err
	}

	if err := putBytes(grant.UploadURL, grant.UploadHeaders, data); err != nil {
		return "", err
	}

	var asset mediaAsset
	if err := c.request(http.MethodPost, "/api/v1/media/"+grant.AssetID+"/complete", map[string]any{}, &asset); err != nil {
		return "", err
	}
	return asset.ID, nil
}

// uploadFile uploads a local file into the Media Library.
func uploadFile(c *client, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("file path is required")
	}
	ext := strings.ToLower(filepath.Ext(path))
	contentType := extToContentType[ext]
	if contentType == "" {
		return "", fmt.Errorf("unsupported MIME type for extension %q (allowed: jpeg, png, webp, mp4, mov)", ext)
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() <= 0 {
		return "", fmt.Errorf("file must not be empty: %s", path)
	}
	if info.Size() > maxMediaUploadBytes {
		return "", fmt.Errorf("file %s is %d bytes; maximum is %d MiB", path, info.Size(), maxMediaUploadBytes/(1024*1024))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return uploadBytes(c, filepath.Base(path), contentType, data)
}
