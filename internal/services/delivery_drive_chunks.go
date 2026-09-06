package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// postInitiateSession calls POST /upload/drive/v3/files?uploadType=resumable&supportsAllDrives=true.
// Returns the session URI from the Location response header.
// errDriveInitiatePOST is the legacy deliveryErrorCode marker for the
// initiate stage; newDeliveryHTTPError stamps the concrete HTTP status on top.
var errDriveInitiatePOST = errors.New("initiate POST")

func (d *GoogleDriveDestination) postInitiateSession(
	ctx context.Context,
	accessToken, folderID, filename, mimeType string,
	totalBytes int64,
	idempotencyKey string,
) (string, error) {
	if accessToken == "" {
		return "", errors.New("google drive destination: postInitiateSession: empty access token (tokenProvider run cancelled)")
	}

	body := map[string]interface{}{
		"name":          filename,
		"mimeType":      mimeType,
		"appProperties": map[string]string{"instaedit_delivery_id": idempotencyKey},
	}
	if folderID != "" {
		body["parents"] = []string{folderID}
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("google drive destination: marshal metadata body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://www.googleapis.com/upload/drive/v3/files?uploadType=resumable&supportsAllDrives=true",
		bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("google drive destination: %w", newDeliveryStageError("postInitiateSession", err))
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("X-Upload-Content-Type", mimeType)
	req.Header.Set("X-Upload-Content-Length", strconv.FormatInt(totalBytes, 10))

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("google drive destination: %w", newDeliveryStageError("initiate POST", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return "", fmt.Errorf("google drive destination: initiate POST: %s: %w", string(body),
			newDeliveryHTTPError(resp.StatusCode, errDriveInitiatePOST))
	}

	location := resp.Header.Get("Location")
	if location == "" {
		return "", errors.New("google drive destination: initiate POST returned empty Location header")
	}
	return location, nil
}

// streamChunks reads the source bytes via Range GETs and writes
// chunked PUTs to sessionURI. On every 308 ack we persist the
// new offset via UpdateProgress (CAS-guarded). On the final 200
// we return (file_id, webViewLink) from the parsed metadata body.
//
// Parameters:
//
//	startOffset — the byte offset the worker SHOULD resume from
//	              (== row.UploadedBytes at the time of Find call).
//	totalBytes — source file size (== row.TotalBytes).
//	chunkSizeBytes — bytes per PUT (== d.chunkSizeBytes).
func (d *GoogleDriveDestination) streamChunks(
	ctx context.Context,
	accessToken, sessionURI, sourceURL string,
	startOffset, totalBytes, chunkSizeBytes int64,
	idempotencyKey string,
	row *models.DeliverySession,
) (string, string, error) {
	if sessionURI == "" {
		return "", "", errors.New("google drive destination: streamChunks: empty sessionURI")
	}
	if sourceURL == "" {
		return "", "", errors.New("google drive destination: streamChunks: empty sourceURL")
	}

	offset := startOffset
	for offset < totalBytes {
		end := offset + chunkSizeBytes - 1
		if end >= totalBytes-1 {
			end = totalBytes - 1
		}
		chunkLen := end - offset + 1

		// Source bytes via Range GET (works for S3/MinIO + the
		// local HTTP test fixture). For HTTP-only source URLs
		// (the only kind we read from today) the chunk is read
		// in one round-trip; for production S3 we'd swap to a
		// presigned URL + Range header.
		chunkBytes, err := d.sourceRangeGET(ctx, sourceURL, offset, end)
		if err != nil {
			return "", "", fmt.Errorf("google drive destination: source Range GET %d-%d: %w", offset, end,
				newDeliveryStageError("source Range GET", err))
		}
		if int64(len(chunkBytes)) != chunkLen {
			return "", "", fmt.Errorf("google drive destination: source short read: want %d, got %d", chunkLen, len(chunkBytes))
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPut, sessionURI, bytes.NewReader(chunkBytes))
		if err != nil {
			return "", "", fmt.Errorf("google drive destination: build chunk PUT: %w", err)
		}
		req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, end, totalBytes))

		resp, err := d.httpClient.Do(req)
		if err != nil {
			return "", "", fmt.Errorf("google drive destination: PUT chunk %d-%d: %w", offset, end, err)
		}

		switch resp.StatusCode {
		case http.StatusRequestedRangeNotSatisfiable: // 416
			resp.Body.Close()
			return "", "", fmt.Errorf("google drive destination: PUT chunk %d returned 416 (Range not satisfiable)", offset)
		case 308: // Resume incomplete
			rangeHeader := resp.Header.Get("Range")
			resp.Body.Close()
			newOffset := end + 1
			if rangeHeader != "" {
				// "bytes=0-N"; sscanf captures the upper bound N+1.
				var lastByte int64
				if _, scanErr := fmt.Sscanf(rangeHeader, "bytes=%*d-%d", &lastByte); scanErr == nil {
					newOffset = lastByte + 1
				}
			}
			// Persist the new offset (CAS-guarded against version
			// drift from a concurrent worker). After the persist
			// SUCCEEDS, ROW.version is bumped server-side; we MUST
			// re-FindByIdempotencyKey to refresh row.Version for
			// the next iteration (Task 8/10 reviewer HIGH #2:
			// using the stale row.Version on the second 308's
			// UpdateProgress CAS fails, mid-loop CABORT).
			//   - On CAS mismatch → abort the chunk loop (the row
			//     is in a peer's hands, the next Deliver tick
			//     re-claims cleanly).
			//   - On other transient errors → log-warn + continue
			//     (a network blip shouldn't poison the chunk loop).
			persistErr := d.persistProgress(ctx, row, sessionURI, newOffset)
			if persistErr != nil {
				if errors.Is(persistErr, repository.ErrDeliverySessionVersionMismatch) {
					return "", "", fmt.Errorf("version CAS lost mid-chunk-loop (peer re-claimed the row): %w", persistErr)
				}
				slog.Warn("google drive destination: UpdateProgress best-effort persist failed; continuing chunk loop",
					"idempotency_key", idempotencyKey,
					"offset", newOffset,
					"error", persistErr)
			} else {
				// Re-load row to refresh Version post-bump.
				refreshed, refreshErr := d.sessionStore.FindByIdempotencyKey(ctx, d.Name(), idempotencyKey)
				if refreshErr != nil {
					return "", "", fmt.Errorf("google drive destination: re-FindByIdempotencyKey after persist: %w",
						newDeliveryStageError("FindByIdempotencyKey", refreshErr))
				}
				row = refreshed
			}
			offset = newOffset
		case http.StatusOK:
			// Final metadata body.
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
			resp.Body.Close()
			finalID, finalURL, parseErr := parseDriveFinalMetadata(body)
			if parseErr != nil {
				return "", "", fmt.Errorf("google drive destination: parse final metadata: %w", parseErr)
			}
			return finalID, finalURL, nil
		case http.StatusNotFound:
			resp.Body.Close()
			return "", "", fmt.Errorf("%w: chunk %d (HTTP 404)", ErrDriveSessionExpired, offset)
		case http.StatusGone: // 410
			// Drive's resumable session URI is treated as GONE (not
			// NOT FOUND) by some Drive versions + intermediaries when
			// the 7-day TTL elapses server-side. Same recovery
			// semantics as 404: surface ErrDriveSessionExpired so the
			// caller returns Status="retrying" + the existing
			// TTL/expired-state branch in Deliver (above) deletes
			// + re-creates the row with a fresh POST initiate on
			// next worker tick. The HTTP 410 code is preserved in
			// the message for the operator-dashboard drill-down.
			resp.Body.Close()
			return "", "", fmt.Errorf("%w: chunk %d (HTTP 410)", ErrDriveSessionExpired, offset)
		default:
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
			resp.Body.Close()
			return "", "", fmt.Errorf("google drive destination: PUT chunk %d-%d returned %d: %s", offset, end, resp.StatusCode, string(body))
		}
	}
	return "", "", errors.New("google drive destination: streamChunks exited loop without final 200")
}

// sourceRangeGET fetches bytes [start, end] from sourceURL.
// Honors server 206 Partial Content; falls back to full GET +
// slice for sources that don't honor Range (test fixtures often
// don't, so the test fake responds 200 OK to a Range GET too —
// we trim the response to the requested window).
func (d *GoogleDriveDestination) sourceRangeGET(
	ctx context.Context,
	sourceURL string,
	startByte, endByte int64,
) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build source GET: %w", err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", startByte, endByte))
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("source GET: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusPartialContent {
		// 206 — read the body directly.
		return io.ReadAll(io.LimitReader(resp.Body, endByte-startByte+1))
	}
	if resp.StatusCode == http.StatusOK {
		// Non-Range-aware source (test fixture or un-cooperative
		// upstream). Read full body + slice to the requested
		// window. For Task 8/10's correctness this is fine
		// because we control the start/end offsets precisely;
		// the production source (S3/MinIO via presigned URL)
		// honors Range.
		full, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read full source body: %w", err)
		}
		if startByte >= int64(len(full)) {
			return nil, fmt.Errorf("source GET body %d bytes < start %d", len(full), startByte)
		}
		endIdx := endByte + 1
		if int64(len(full)) < endIdx {
			endIdx = int64(len(full))
		}
		return full[startByte:endIdx], nil
	}
	return nil, fmt.Errorf("source GET returned %d", resp.StatusCode)
}

// parseDriveFinalMetadata extracts (id, webViewLink) from the file
// metadata body the chunk loop receives on the final 200.
func parseDriveFinalMetadata(body []byte) (string, string, error) {
	var parsed struct {
		Id          string `json:"id"`
		WebViewLink string `json:"webViewLink"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", "", fmt.Errorf("unmarshal final metadata: %w", err)
	}
	if parsed.Id == "" {
		return "", "", errors.New("final metadata: empty file id")
	}
	return parsed.Id, parsed.WebViewLink, nil
}
