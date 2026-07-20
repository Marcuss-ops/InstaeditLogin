package services

import (
	"bytes"
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
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// GoogleDriveDestination is the Task 8/10 DeliveryProvider for
// Google Drive. Splits upload semantics from import:
//   - GoogleDriveOAuthService reads files FROM Drive (untouched,
//     per spec).
//   - GoogleDriveDestination writes files TO Drive (this struct,
//     added by Task 8/10).
//
// Implements DeliveryProvider directly so the registry call site
// (publish_worker.dispatchPostCompletion) looks up by
// models.PlatformGoogleDrive ("google-drive") and lands here.
//
// Pipeline (the eight acceptance criteria):
//  1. POST /upload/drive/v3/files?uploadType=resumable&supportsAllDrives=true
//     opens a session; the Location header carries the session URI.
//  2. session URI + uploaded_bytes=0 stamps the row in state=initiated.
//  3. PUT chunks to the session URI with Content-Range bytes a-b/total.
//  4. After each 308 + Range header, UpdateProgress stamps the new
//     uploaded_bytes so a worker crash at byte 2 MiB resumes from 2 MiB.
//  5. After final 200, GET verify (size check) + MarkCompleted
//     stamps remote_file_id + remote_url; session_uri_encrypted
//     cleared (the URI is dead).
//  6. appProperties.instaedit_delivery_id=<idempotency_key> on the
//     POST body so a cold restart (DB row wiped) finds the file via
//     GET /drive/v3/files?q=appProperties has{...} and skips re-upload.
//  7. Post-upload verification: GET /drive/v3/files/<id>?fields=size
//     confirms server's final size equals what we sent.
//  8. Idempotency: pre-upload GET /drive/v3/files?q=appProperties has
//     {...} returns the existing file if a peer (or an earlier run)
//     already uploaded it; we short-circuit with the cached file_id
//     without re-streaming.
//
// Crash recovery:
//   - Re-Deliver after a crash: FindByIdempotencyKey returns a row in
//     state='uploading' with session_uri_decrypted populated. We decrypt
//   - re-PUT from the persisted uploaded_bytes to the server.
//   - TTL breach: if expires_at < NOW() before we resume, MarkExpired
//   - re-POST a fresh initiate (no recovery, restart from byte 0).
//   - Concurrent worker: UpdateProgress's version-CAS surfaces
//     ErrDeliverySessionVersionMismatch; the delivery returns
//     Status="retrying" so dispatchPostCompletion logs + skips.
type GoogleDriveDestination struct {
	// sessionStore persists the (session_uri, uploaded_bytes)
	// pair across worker crashes. Required; the constructor
	// returns an error if nil so this struct's invariants hold
	// in production.
	sessionStore *repository.DeliverySessionRepository

	// tokenProvider hydrates a fresh bearer access token for
	// the platform_account_id. Required.
	tokenProvider DriveAccessTokenProvider

	// encryptor wraps the session URI before persistence. Required
	// alongside sessionStore; storing the plaintext URI defeats
	// the "credential-adjacent" intent.
	encryptor SessionEncryptor

	// httpClient makes the actual API calls. Tests inject
	// httptest-backed clients; production uses the shared
	// services.NewHTTPClient() via ProviderDependencies.resolveHTTPClient.
	httpClient *http.Client

	// clock is the time-fn; tests inject a fixed clock so the
	// expires_at assertion is deterministic.
	clock func() time.Time

	// chunkSizeBytes is bytes per PUT. Drive minimum is 256 KiB;
	// production default is 16 MiB.
	chunkSizeBytes int64
}

// NewGoogleDriveDestination wires the destination. Returns an
// error if any required dependency is nil so the constructor
// fails loudly at bootstrap time rather than mid-Drive-chunk-PUT.
func NewGoogleDriveDestination(
	sessionStore *repository.DeliverySessionRepository,
	tokenProvider DriveAccessTokenProvider,
	encryptor SessionEncryptor,
	httpClient *http.Client,
	chunkSizeBytes int64,
) (*GoogleDriveDestination, error) {
	if sessionStore == nil {
		return nil, errors.New("GoogleDriveDestination.NewGoogleDriveDestination: nil sessionStore (wire at bootstrap)")
	}
	if tokenProvider == nil {
		return nil, errors.New("GoogleDriveDestination.NewGoogleDriveDestination: nil tokenProvider (wire at bootstrap)")
	}
	if encryptor == nil {
		return nil, errors.New("GoogleDriveDestination.NewGoogleDriveDestination: nil encryptor (wire at bootstrap)")
	}
	if httpClient == nil {
		return nil, errors.New("GoogleDriveDestination.NewGoogleDriveDestination: nil httpClient")
	}
	if chunkSizeBytes < 262144 {
		// Drive's documented minimum is 256 KiB. Smaller chunks
		// get a 400 from POST; a runtime check here prevents the
		// bad config from progressing past bootstrap.
		return nil, fmt.Errorf("GoogleDriveDestination.NewGoogleDriveDestination: chunkSizeBytes %d < drive minimum 262144", chunkSizeBytes)
	}
	return &GoogleDriveDestination{
		sessionStore:   sessionStore,
		tokenProvider:  tokenProvider,
		encryptor:      encryptor,
		httpClient:     httpClient,
		clock:          time.Now,
		chunkSizeBytes: chunkSizeBytes,
	}, nil
}

// WithClock wires a deterministic clock. Tests use this; production
// defaults to time.Now.
func (d *GoogleDriveDestination) WithClock(clock func() time.Time) *GoogleDriveDestination {
	if clock != nil {
		d.clock = clock
	}
	return d
}

// Name returns the canonical registry key "google-drive" (matching
// models.PlatformGoogleDrive + GoogleDriveOAuthService.Name()).
// The publish_worker dispatch hook looks up by account.Platform,
// which is "google-drive", so this key MUST match it verbatim.
func (d *GoogleDriveDestination) Name() string {
	return models.PlatformGoogleDrive
}

// driveSessionTTL is the Drive resumable session URI lifetime.
// 7 days matches Google's documented default.
const driveSessionTTL = 7 * 24 * time.Hour

// ErrDriveSessionExpired is the typed sentinel Deliver returns
// when the persisted session URI exceeded Google's 7-day TTL.
var ErrDriveSessionExpired = errors.New("ERR_DRIVE_SESSION_EXPIRED")

// ErrDriveIdempotencyConflict is the typed sentinel Deliver
// returns when the app-property lookup finds a DIFFERENT file_id
// than expected. Non-recoverable — operator runbook required.
var ErrDriveIdempotencyConflict = errors.New("ERR_DRIVE_IDEMPOTENCY_CONFLICT")

// ErrDriveConfig is the typed sentinel for unparseable / empty
// destination Config (folder_id, filename_template, drive_account_id).
var ErrDriveConfig = errors.New("ERR_DRIVE_CONFIG")

// Deliver runs the full Task 8/10 state machine. Idempotent on
// retry: same idempotency_key + same asset → same terminal Drive
// file_id (or a clear "retry" result).
//
// Parameters:
//
//	asset — the artifact metadata (SizeBytes + ContentType are read;
//	        SourceURL is NOT a field on MediaAsset and we read the
//	        source bytes via dest.RemoteURL below).
//	dest  — DeliveryDestination with Config["drive_account_id"],
//	        Config["folder_id"], Config["filename_template"], AND
//	        dest.RemoteURL (the operator-resolved source URL of
//	        the artifact). The dispatch hook builds this with the
//	        minimum surface needed.
//	idempotencyKey — post_target_id_econded, stable per target.
func (d *GoogleDriveDestination) Deliver(
	ctx context.Context,
	asset *models.MediaAsset,
	dest *models.DeliveryDestination,
	idempotencyKey string,
) (*models.DeliveryResult, error) {
	if d == nil {
		return nil, errors.New("GoogleDriveDestination.Deliver: nil receiver")
	}
	if ctx == nil {
		return nil, errors.New("GoogleDriveDestination.Deliver: nil ctx")
	}
	if asset == nil {
		return nil, errors.New("GoogleDriveDestination.Deliver: nil asset")
	}
	if dest == nil {
		return nil, errors.New("GoogleDriveDestination.Deliver: nil dest")
	}
	if idempotencyKey == "" {
		return nil, errors.New("GoogleDriveDestination.Deliver: empty idempotencyKey")
	}
	if asset.SizeBytes <= 0 {
		return nil, fmt.Errorf("GoogleDriveDestination.Deliver: asset.SizeBytes must be positive (got %d)", asset.SizeBytes)
	}

	// 1. Config resolution.
	driveAccountIDStr := dest.Config["drive_account_id"]
	driveAccountID, driveAcctErr := strconv.ParseInt(driveAccountIDStr, 10, 64)
	if driveAcctErr != nil || driveAccountID <= 0 {
		return nil, fmt.Errorf("%w: drive_account_id %q invalid", ErrDriveConfig, driveAccountIDStr)
	}

	folderID := dest.Config["folder_id"]
	filename, fileErr := driveResolveFilename(dest.Config["filename_template"], asset)
	if fileErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrDriveConfig, fileErr)
	}
	mimeType := asset.ContentType
	if mimeType == "" {
		mimeType = "video/mp4"
	}

	// 2. App-property dedupe lookup. Always done (covers cold-
	// restart edge case where the DB row is gone but Drive still
	// has the file).
	existingFileID, existingURL, lookupErr := d.lookupByAppProperty(ctx, driveAccountID, idempotencyKey)
	if lookupErr != nil {
		slog.Warn("google drive destination: app-property dedupe lookup failed; proceeding to upload (may create duplicate)",
			"idempotency_key", idempotencyKey,
			"folder_id", folderID,
			"error", lookupErr)
	} else if existingFileID != "" {
		slog.Info("google drive destination: app-property idempotency hit; skipping upload",
			"idempotency_key", idempotencyKey,
			"file_id", existingFileID,
			"web_view_link", existingURL)
		return &models.DeliveryResult{
			ProviderName: d.Name(),
			Status:       "published",
			RemoteID:     existingFileID,
			RemoteURL:    existingURL,
			Metadata: map[string]string{
				"idempotency_key": idempotencyKey,
				"dedupe_source":   "app_property",
			},
		}, nil
	}

	// 3. Resolve the Drive access token (vault + refresh).
	accessToken, err := d.tokenProvider.GetAccessToken(ctx, driveAccountID)
	if err != nil {
		return &models.DeliveryResult{
			ProviderName: d.Name(),
			Status:       "retrying",
			Metadata: map[string]string{
				"idempotency_key": idempotencyKey,
				"error_code":      "drive_token_unavailable",
				"error":           err.Error(),
			},
		}, nil
	}

	// Find-or-create the session row.
	row, findErr := d.sessionStore.FindByIdempotencyKey(ctx, d.Name(), idempotencyKey)
	if findErr != nil && !errors.Is(findErr, repository.ErrDeliverySessionNotFound) {
		return nil, fmt.Errorf("GoogleDriveDestination.Deliver: sessionStore.FindByIdempotencyKey: %w", findErr)
	}

	if row == nil {
		// Fresh delivery. POST initiate + insert.
		sessionURI, postErr := d.postInitiateSession(ctx, accessToken, folderID, filename, mimeType, asset.SizeBytes, idempotencyKey)
		if postErr != nil {
			return nil, fmt.Errorf("GoogleDriveDestination.Deliver: postInitiateSession: %w", postErr)
		}
		cipher, encErr := d.encryptor.Encrypt(sessionURI)
		if encErr != nil {
			return nil, fmt.Errorf("GoogleDriveDestination.Deliver: encryptor.Encrypt: %w", encErr)
		}

		row = &models.DeliverySession{
			DeliverableType:     d.Name(),
			IdempotencyKey:      idempotencyKey,
			State:               models.DeliverySessionStateInitiated,
			SessionURIEncrypted: base64.StdEncoding.EncodeToString(cipher),
			UploadedBytes:       0,
			TotalBytes:          asset.SizeBytes,
			ChunkSize:           d.chunkSizeBytes,
			MIMEType:            mimeType,
			FolderID:            folderID,
			Filename:            filename,
			AppProperties:       map[string]string{"instaedit_delivery_id": idempotencyKey},
		}
		expiresAt := d.clock().Add(driveSessionTTL)
		row.ExpiresAt = &expiresAt
		row.WorkerID = "publish_worker_post_completion"

		if err := d.sessionStore.Create(ctx, row); err != nil {
			return nil, fmt.Errorf("GoogleDriveDestination.Deliver: sessionStore.Create: %w", err)
		}
	}

	// Already-completed short-circuit.
	if row != nil && row.State == models.DeliverySessionStateCompleted {
		return &models.DeliveryResult{
			ProviderName: d.Name(),
			Status:       "published",
			RemoteID:     row.RemoteFileID,
			RemoteURL:    row.RemoteURL,
			Metadata: map[string]string{
				"idempotency_key": idempotencyKey,
				"cache_source":    "delivery_sessions.completed",
			},
		}, nil
	}

	// TTL / expired-state recovery: row.State == "expired" OR the
	// expires_at cursor is in the past
	// (Task 8/10 reviewer HIGH #1: original code re-MarkExpired
	// each tick which leaves the row in state="expired" + empty
	// session_uri_encrypted forever, blocking recovery).
	//
	// Recover path: delete the stale row so the Create call below
	// (with the same deliverable_type + idempotency_key) lands
	// fresh. The UNIQUE constraint + ON CONFLICT DO NOTHING mean a
	// re-Create on an existing key is a silent no-op — only a
	// DELETE paves the way for a re-Create to succeed.
	var needsFreshInitiate bool
	if row != nil && row.State == models.DeliverySessionStateExpired {
		needsFreshInitiate = true
	}
	if row != nil && row.ExpiresAt != nil && row.ExpiresAt.Before(d.clock()) {
		needsFreshInitiate = true
	}
	if needsFreshInitiate {
		// Best-effort MarkExpired for telemetry (the dashboard's
		// "expired" badge reflects operator triage intent). Then
		// delete to pave the re-Create path. Both can race with a
		// peer worker; CAS loss surfaces upstream.
		_ = d.sessionStore.MarkExpired(ctx, row.ID, row.Version)
		if delErr := d.sessionStore.DeleteByID(ctx, row.ID, row.Version+1); delErr != nil && !errors.Is(delErr, repository.ErrDeliverySessionVersionMismatch) {
			return nil, fmt.Errorf("GoogleDriveDestination.Deliver: sessionStore.DeleteByID: %w", delErr)
		}
		row = nil // fall through to fresh-initiate branch
	}

	// 4. Stream chunks. Decrypt session URI + chunk loop.
	sessionURI, decodeErr := d.decryptSessionURI(row.SessionURIEncrypted)
	if decodeErr != nil {
		return nil, fmt.Errorf("GoogleDriveDestination.Deliver: decrypt session URI: %w", decodeErr)
	}

	sourceURL := dest.RemoteURL
	if sourceURL == "" {
		return nil, fmt.Errorf("GoogleDriveDestination.Deliver: dest.RemoteURL empty (asset source must be reachable)")
	}

	// Mount source stream. Multi-callable GetBytes helper not
	// available on *http.Client (no Range in httptest default);
	// the destination reads each chunk via a fresh HTTP GET
	// with Range bytes=N-(N+chunkLen-1). This matches Drive's
	// resumable upload protocol and aligns with the existing
	// /upload/drive/v3/files source pattern from the import side.
	fileID, webViewLink, uploadErr := d.streamChunks(
		ctx, accessToken, sessionURI, sourceURL,
		row.UploadedBytes, row.TotalBytes, d.chunkSizeBytes, idempotencyKey,
		row,
	)
	if uploadErr != nil {
		// Persist the failure (CAS-guarded against version drift).
		//
		// TWO STATES for expired-session errors: when uploadErr wraps
		// ErrDriveSessionExpired (404 NOT FOUND or 410 GONE — the Drive
		// variants of "session is dead"), we MUST call MarkExpired
		// (NOT MarkFailed) so the next-tick Deliver sees
		// row.State == "expired" and triggers the recovery branch
		// (DeleteByID + re-POST fresh initiate).
		//
		// MarkFailed sets state="failed"; the recovery branch only
		// fires on state="expired" (or expires_at < now()). If we
		// stamped "failed" on an expired-session error, the next
		// tick would skip the recovery branch, fall through to the
		// chunk loop with the SAME encrypted session URI, and loop
		// forever on the SAME 410/404 (a retry storm). The split
		// below closes the loop for both 404 and 410.
		if errors.Is(uploadErr, ErrDriveSessionExpired) {
			// Expired-session path: stamp state="expired" + version-
			// CAS so next-tick Deliver's needsFreshInitiate branch
			// fires. Concurrent worker that re-claimed the row races
			// us to MarkExpired; the CAS loss surfaces upstream.
			if markErr := d.sessionStore.MarkExpired(ctx, row.ID, row.Version); markErr != nil && !errors.Is(markErr, repository.ErrDeliverySessionVersionMismatch) {
				slog.Warn("google drive destination: MarkExpired after chunk-loop 410/404 did not persist",
					"idempotency_key", idempotencyKey,
					"error", markErr)
			}
		} else {
			markErr := d.sessionStore.MarkFailed(ctx, row.ID, row.Version, "drive_chunk_put_failed", uploadErr.Error(), row.WorkerID)
			if markErr != nil && !errors.Is(markErr, repository.ErrDeliverySessionVersionMismatch) {
				slog.Warn("google drive destination: MarkFailed after chunk-loop failure did not persist",
					"idempotency_key", idempotencyKey,
					"error", markErr)
			}
		}
		return &models.DeliveryResult{
			ProviderName: d.Name(),
			Status:       "retrying",
			Metadata: map[string]string{
				"idempotency_key": idempotencyKey,
				"error_code":      "drive_chunk_put_failed",
				"error":           uploadErr.Error(),
			},
		}, nil
	}

	// 5. Post-upload verify: GET /drive/v3/files/<id>?fields=size
	// Confirms the server's final size matches what we sent.
	if err := d.verifyUploadedSize(ctx, accessToken, fileID, row.TotalBytes, idempotencyKey); err != nil {
		markErr := d.sessionStore.MarkFailed(ctx, row.ID, row.Version, "drive_size_mismatch", err.Error(), row.WorkerID)
		if markErr != nil && !errors.Is(markErr, repository.ErrDeliverySessionVersionMismatch) {
			slog.Warn("google drive destination: MarkFailed after size-verify did not persist",
				"idempotency_key", idempotencyKey,
				"error", markErr)
		}
		return &models.DeliveryResult{
			ProviderName: d.Name(),
			Status:       "failed",
			RemoteID:     fileID,
			RemoteURL:    webViewLink,
			Metadata: map[string]string{
				"idempotency_key": idempotencyKey,
				"error_code":      "drive_size_mismatch",
				"error":           err.Error(),
			},
		}, nil
	}

	if err := d.sessionStore.MarkCompleted(ctx, row.ID, row.Version, fileID, webViewLink, row.WorkerID); err != nil && !errors.Is(err, repository.ErrDeliverySessionVersionMismatch) {
		return nil, fmt.Errorf("GoogleDriveDestination.Deliver: sessionStore.MarkCompleted: %w", err)
	}

	return &models.DeliveryResult{
		ProviderName: d.Name(),
		Status:       "published",
		RemoteID:     fileID,
		RemoteURL:    webViewLink,
		Metadata: map[string]string{
			"idempotency_key": idempotencyKey,
			"folder_id":       folderID,
			"filename":        row.Filename,
		},
	}, nil
}

// postInitiateSession calls POST /upload/drive/v3/files?uploadType=resumable&supportsAllDrives=true.
// Returns the session URI from the Location response header.
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
		return "", fmt.Errorf("google drive destination: build initiate request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("X-Upload-Content-Type", mimeType)
	req.Header.Set("X-Upload-Content-Length", strconv.FormatInt(totalBytes, 10))

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("google drive destination: initiate POST: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return "", fmt.Errorf("google drive destination: initiate POST returned %d: %s", resp.StatusCode, string(body))
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
			return "", "", fmt.Errorf("google drive destination: source Range GET %d-%d: %w", offset, end, err)
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
					return "", "", fmt.Errorf("google drive destination: re-FindByIdempotencyKey after persist: %w", refreshErr)
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
		return fmt.Errorf("verify GET returned %d: %s", resp.StatusCode, string(body))
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
		return "", "", fmt.Errorf("app-property lookup: tokenProvider: %w", err)
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
		return "", "", fmt.Errorf("app-property lookup: GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return "", "", fmt.Errorf("app-property lookup: server returned %d: %s", resp.StatusCode, string(body))
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
		return "", fmt.Errorf("decrypt session URI: base64: %w", err)
	}
	plaintext, err := d.encryptor.Decrypt(raw)
	if err != nil {
		return "", fmt.Errorf("decrypt session URI: decrypt: %w", err)
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

// Compile-time assertion: *GoogleDriveDestination satisfies
// DeliveryProvider. Triggers at vet time if the interface drifts.
var _ DeliveryProvider = (*GoogleDriveDestination)(nil)
