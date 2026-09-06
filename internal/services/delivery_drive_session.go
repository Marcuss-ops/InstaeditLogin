package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// verifyUploadedSize calls GET /drive/v3/files/<id>?fields=size and
// confirms the server's size equals expectedSize.
func (d *GoogleDriveDestination) verifyUploadedSize(
	ctx context.Context,
	accessToken, fileID string,
	expectedSize int64,
	idempotencyKey string,
) error {
	u := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s?fields=id,size,md5Checksum", url.PathEscape(fileID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("google drive destination: build verify GET: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("google drive destination: verify GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return fmt.Errorf("verify GET: %s: %w", string(body),
			newDeliveryHTTPError(resp.StatusCode, errors.New("verify GET failed")))
	}

	var parsed struct {
		Id          string `json:"id"`
		Size        string `json:"size"`
		MD5Checksum string `json:"md5Checksum"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("google drive destination: decode verify GET: %w", err)
	}
	got, err := strconv.ParseInt(parsed.Size, 10, 64)
	if err != nil {
		return fmt.Errorf("google drive destination: parse verify size %q: %w", parsed.Size, err)
	}
	if got != expectedSize {
		return fmt.Errorf("size mismatch: server=%d expected=%d (md5=%q)", got, expectedSize, parsed.MD5Checksum)
	}
	slog.Info("google drive destination: post-upload size verified",
		"idempotency_key", idempotencyKey,
		"file_id", fileID,
		"size", got,
		"md5_checksum", parsed.MD5Checksum)
	return nil
}

// lookupByAppProperty GETs /drive/v3/files?q=appProperties has{...}.
// Returns the first hit; or ("", "", nil) on 0 matches.
func (d *GoogleDriveDestination) lookupByAppProperty(
	ctx context.Context,
	driveAccountID int64,
	idempotencyKey string,
) (string, string, error) {
	accessToken, err := d.tokenProvider.GetAccessToken(ctx, driveAccountID)
	if err != nil {
		return "", "", fmt.Errorf("app-property lookup: tokenProvider: %w", newDeliveryStageError("lookupByAppProperty", err))
	}

	q := fmt.Sprintf("appProperties has { key='instaedit_delivery_id' and value='%s' }",
		url.QueryEscape(idempotencyKey))
	u := "https://www.googleapis.com/drive/v3/files?q=" + url.QueryEscape(q) +
		"&fields=files(id,webViewLink)&supportsAllDrives=true&includeItemsFromAllDrives=true"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "", fmt.Errorf("app-property lookup: build GET: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("app-property lookup: GET: %w", newDeliveryStageError("lookupByAppProperty", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return "", "", fmt.Errorf("app-property lookup: %s: %w", string(body),
			&DeliveryError{Stage: "lookupByAppProperty", Status: resp.StatusCode, Err: errors.New("server returned non-200")})
	}

	var parsed struct {
		Files []struct {
			Id          string `json:"id"`
			WebViewLink string `json:"webViewLink"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", "", fmt.Errorf("app-property lookup: decode: %w", err)
	}
	if len(parsed.Files) == 0 {
		return "", "", nil
	}
	if len(parsed.Files) > 1 {
		slog.Warn("google drive destination: app-property lookup found >1 files; using first and flagging for dedupe",
			"idempotency_key", idempotencyKey,
			"hits", len(parsed.Files))
	}
	return parsed.Files[0].Id, parsed.Files[0].WebViewLink, nil
}

// decryptSessionURI reverses the SessionEncryptor.Encrypt + base64
// used in Deliver.
func (d *GoogleDriveDestination) decryptSessionURI(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("decrypt session URI: base64: %w", newDeliveryStageError("decrypt session URI", err))
	}
	plaintext, err := d.encryptor.Decrypt(raw)
	if err != nil {
		return "", fmt.Errorf("decrypt session URI: decrypt: %w", newDeliveryStageError("decrypt session URI", err))
	}
	return plaintext, nil
}

// persistProgress advances the row's offset + re-encrypts the
// session URI. The offset is the next byte to send; the encrypted
// session URI is refreshed every step in case the worker is
// resumed in a future tick with a different encryption keyring.
func (d *GoogleDriveDestination) persistProgress(
	ctx context.Context,
	row *models.DeliverySession,
	sessionURI string,
	newOffset int64,
) error {
	if row == nil {
		return errors.New("google drive destination: persistProgress: nil row")
	}
	if sessionURI == "" {
		return errors.New("google drive destination: persistProgress: empty sessionURI")
	}
	if newOffset < 0 {
		return fmt.Errorf("google drive destination: persistProgress: negative newOffset (%d)", newOffset)
	}
	cipher, err := d.encryptor.Encrypt(sessionURI)
	if err != nil {
		return fmt.Errorf("google drive destination: persistProgress: encrypt: %w", err)
	}
	return d.sessionStore.UpdateProgress(
		ctx,
		row.ID,
		row.Version,
		base64.StdEncoding.EncodeToString(cipher),
		newOffset,
		"publish_worker_post_completion",
	)
}

// driveResolveFilename renders a simple template. {title} → asset.ID;
// {date} → today's UTC date in YYYY-MM-DD. Empty template returns
// asset.ID + ".mp4".
func driveResolveFilename(template string, asset *models.MediaAsset) (string, error) {
	if template == "" {
		if asset == nil || asset.ID == "" {
			return "", errors.New("driveResolveFilename: empty template and empty asset.ID")
		}
		return asset.ID + ".mp4", nil
	}
	if asset == nil {
		return "", errors.New("driveResolveFilename: nil asset")
	}
	out := template
	out = strings.ReplaceAll(out, "{title}", asset.ID)
	out = strings.ReplaceAll(out, "{date}", time.Now().UTC().Format("2006-01-02"))
	return out, nil
}
