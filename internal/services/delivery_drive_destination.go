package services

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
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

// ErrDriveReinitiateBudgetExhausted is the typed sentinel Deliver returns
// when a session key has burned its re-initiate budget (the row kept dying
// past driveReinitiateCap consecutive sessions). The upload stops looping;
// the session row is left in state='failed' and
// delivery_drive_reinitiate_loops_total increments for the dashboard alarm.
var ErrDriveReinitiateBudgetExhausted = errors.New("ERR_DRIVE_REINITIATE_BUDGET_EXHAUSTED")

// driveReinitiateCap is the re-initiate budget per delivery-session key:
// how many consecutive dead sessions (TTL expiry / chunk failure → reset →
// fresh initiate) one logical delivery may burn before the destination
// declares the upload permanently failing and caps the loop. Matches the
// repo-wide max_attempts default of 5.
const driveReinitiateCap = 5

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
		errorCode := "drive_token_unavailable"
		if errors.Is(err, ErrDriveNoRefreshToken) {
			errorCode = "drive_auth_required"
		}
		return &models.DeliveryResult{
			ProviderName: d.Name(),
			Status:       "retrying",
			Metadata: map[string]string{
				"idempotency_key": idempotencyKey,
				"error_code":      errorCode,
				"error":           err.Error(),
			},
		}, nil
	}

	// Find-or-create the session row.
	row, findErr := d.sessionStore.FindByIdempotencyKey(ctx, d.Name(), idempotencyKey)
	if findErr != nil && !errors.Is(findErr, repository.ErrDeliverySessionNotFound) {
		return nil, fmt.Errorf("GoogleDriveDestination.Deliver: %w", newDeliveryStageError("FindByIdempotencyKey", findErr))
	}

	if row == nil {
		// Fresh delivery. POST initiate + insert.
		sessionURI, postErr := d.postInitiateSession(ctx, accessToken, folderID, filename, mimeType, asset.SizeBytes, idempotencyKey)
		if postErr != nil {
			return nil, fmt.Errorf("GoogleDriveDestination.Deliver: %w", newDeliveryStageError("postInitiateSession", postErr))
		}
		cipher, encErr := d.encryptor.Encrypt(sessionURI)
		if encErr != nil {
			return nil, fmt.Errorf("GoogleDriveDestination.Deliver: %w", newDeliveryStageError("encryptor.Encrypt", encErr))
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
			return nil, fmt.Errorf("GoogleDriveDestination.Deliver: %w", newDeliveryStageError("sessionStore.Create", err))
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
	// Recover path: RESET the row in place (ResetForReinitiate) so the
	// fresh-initiate branch below can proceed immediately. The previous
	// delete + re-Create cycle reset attempt_count to 0 on every
	// recovery, so a permanently failing upload looped forever (one
	// Drive initiate + one worker tick per cycle). The in-place reset
	// keeps attempt_count monotonic, which makes the re-initiate budget
	// below enforceable.
	var needsFreshInitiate bool
	if row != nil && row.State == models.DeliverySessionStateExpired {
		needsFreshInitiate = true
	}
	if row != nil && row.ExpiresAt != nil && row.ExpiresAt.Before(d.clock()) {
		needsFreshInitiate = true
	}
	if needsFreshInitiate {
		// Reset the dead row IN PLACE (no delete, no re-Create):
		// attempt_count stays monotonic, which is what makes the
		// re-initiate budget below enforceable. CAS loss means a peer
		// already reset/completed the row — re-read so we never act on
		// a stale snapshot.
		newAttempts, resetErr := d.sessionStore.ResetForReinitiate(ctx, row.ID, row.Version, "publish_worker_post_completion")
		if resetErr != nil && !errors.Is(resetErr, repository.ErrDeliverySessionVersionMismatch) {
			return nil, fmt.Errorf("GoogleDriveDestination.Deliver: %w", newDeliveryStageError("sessionStore", resetErr))
		}
		refreshed, findErr := d.sessionStore.FindByIdempotencyKey(ctx, d.Name(), idempotencyKey)
		if findErr != nil {
			return nil, fmt.Errorf("GoogleDriveDestination.Deliver: %w", newDeliveryStageError("FindByIdempotencyKey", findErr))
		}
		row = refreshed
		if resetErr == nil && newAttempts > driveReinitiateCap {
			// Budget exhausted: this key has failed to complete
			// driveReinitiateCap consecutive sessions. Cap the loop
			// (row goes to 'failed' so the reconciler stops re-picking
			// it every tick) and alarm on the dashboard.
			metrics.RecordDriveReinitiateLoop(d.Name())
			if markErr := d.sessionStore.MarkFailed(ctx, row.ID, row.Version, "drive_reinitiate_budget_exhausted",
				fmt.Sprintf("re-initiate budget exhausted after %d consecutive dead sessions (permanent upload failure suspected)", newAttempts-1),
				"publish_worker_post_completion"); markErr != nil && !errors.Is(markErr, repository.ErrDeliverySessionVersionMismatch) {
				slog.Warn("google drive destination: could not stamp failed state after re-initiate budget exhaustion",
					"idempotency_key", idempotencyKey, "error", markErr)
			}
			return nil, fmt.Errorf("GoogleDriveDestination.Deliver: %w",
				newDeliveryStageError("sessionStore", fmt.Errorf("%w: re-initiate budget exhausted after %d attempts for key %s", ErrDriveReinitiateBudgetExhausted, newAttempts-1, idempotencyKey)))
		}
		// Fall through with the REFRESHED row (state='initiated', empty
		// session URI) so the fresh-initiate branch below re-POSTs and
		// UpdateProgress stamps the new URI — no Create, no attempt_count
		// reset, no panic (the previous code set row = nil here and the
		// decrypt step below dereferenced it).
	}

	// Fresh-initiate branch: a row with no usable session URI (freshly
	// reset or newly created below) must POST initiate BEFORE the chunk
	// loop can run.
	if row.SessionURIEncrypted == "" {
		sessionURIFresh, postErr := d.postInitiateSession(ctx, accessToken, folderID, filename, mimeType, asset.SizeBytes, idempotencyKey)
		if postErr != nil {
			return nil, fmt.Errorf("GoogleDriveDestination.Deliver: %w", newDeliveryStageError("postInitiateSession", postErr))
		}
		cipherFresh, encErr := d.encryptor.Encrypt(sessionURIFresh)
		if encErr != nil {
			return nil, fmt.Errorf("GoogleDriveDestination.Deliver: %w", newDeliveryStageError("encryptor.Encrypt", encErr))
		}
		cipherEnc := base64.StdEncoding.EncodeToString(cipherFresh)
		if err := d.sessionStore.UpdateProgress(ctx, row.ID, row.Version, cipherEnc, row.UploadedBytes, "publish_worker_post_completion"); err != nil {
			return nil, fmt.Errorf("GoogleDriveDestination.Deliver: %w", newDeliveryStageError("sessionStore.UpdateProgress", err))
		}
		// UpdateProgress bumped the server-side version; keep the
		// in-memory snapshot in lockstep so the chunk loop's first
		// persistProgress CAS does not spuriously abort.
		row.Version++
		row.SessionURIEncrypted = cipherEnc
	}

	// 4. Stream chunks. Decrypt session URI + chunk loop.
	sessionURI, decodeErr := d.decryptSessionURI(row.SessionURIEncrypted)
	if decodeErr != nil {
		return nil, fmt.Errorf("GoogleDriveDestination.Deliver: %w", newDeliveryStageError("decrypt session URI", decodeErr))
	}

	sourceURL := dest.RemoteURL
	if sourceURL == "" {
		return nil, fmt.Errorf("GoogleDriveDestination.Deliver: %w",
			newDeliveryStageError("decrypt session URI", errors.New("dest.RemoteURL empty (asset source must be reachable)")))
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
		// (ResetForReinitiate + re-POST fresh initiate).
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
		return nil, fmt.Errorf("GoogleDriveDestination.Deliver: %w", newDeliveryStageError("sessionStore.MarkCompleted", err))
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

// Compile-time assertion: *GoogleDriveDestination satisfies
// DeliveryProvider. Triggers at vet time if the interface drifts.
var _ DeliveryProvider = (*GoogleDriveDestination)(nil)
