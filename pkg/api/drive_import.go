package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// driveImportS3UploadTimeout is the HTTP client timeout for streaming
// a Drive file to S3. 30 minutes covers a large clip over a slow
// upstream without blocking forever.
const driveImportS3UploadTimeout = 30 * time.Minute

// DriveImportRequest is the body for POST /api/v1/media/import/drive.
// It imports a clip from the user's Google Drive and creates a post
// that is immediately queued for publishing to the selected targets.
type DriveImportRequest struct {
	// DriveFileID is the Google Drive file id (the part after /d/ in the share URL).
	DriveFileID string `json:"drive_file_id"`
	// DriveAccountID is the local platform_account.id for the linked google-drive account.
	DriveAccountID int64 `json:"drive_account_id"`
	// WorkspaceID is the workspace that will own the post.
	WorkspaceID int64 `json:"workspace_id"`
	// Title of the post (also used as the S3 object name stem).
	Title string `json:"title"`
	// Caption is the post body text.
	Caption string `json:"caption"`
	// Targets are the platform accounts where the clip should be published.
	Targets []CreatePostTarget `json:"targets"`
}

// DriveImportResponse returns the created post and the imported media asset.
type DriveImportResponse struct {
	Post  *models.Post       `json:"post"`
	Asset *models.MediaAsset `json:"asset"`
}

// handleDriveImport imports a video from Google Drive and creates a post.
// POST /api/v1/media/import/drive
//
// Steps:
//  1. Validate request and workspace ownership.
//  2. Verify the google-drive platform account belongs to the user.
//  3. Fetch a fresh Google access token from the vault.
//  4. Fetch Drive file metadata and validate it is a video.
//  5. Create a media_assets row in pending state.
//  6. Stream the file from Drive to S3 using a presigned PUT URL.
//  7. Verify the S3 upload and mark the asset ready.
//  8. Create a post with the internal S3 URL and queue it for publishing.
//  9. Trigger PublishPost so the worker picks it up immediately.
func (r *Router) handleDriveImport(w http.ResponseWriter, req *http.Request) {
	if r.storageProvider == nil || r.mediaStore == nil {
		writeError(w, http.StatusNotImplemented, "media not configured on this server")
		return
	}
	if r.postStore == nil || r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	if r.vault == nil {
		writeError(w, http.StatusNotImplemented, "credential vault not configured")
		return
	}

	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}

	// Read body bytes once for idempotency hashing, then decode from
	// the bytes slice so the original payload can be replayed exactly.
	bodyBytes, bodyErr := idempotencyReadBody(req)
	if bodyErr != nil {
		writeError(w, http.StatusBadRequest, "request body unreadable: "+bodyErr.Error())
		return
	}
	hash := idempotencyHash(bodyBytes)

	var body DriveImportRequest
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if body.DriveFileID == "" {
		writeError(w, http.StatusUnprocessableEntity, "drive_file_id is required")
		return
	}
	if body.DriveAccountID == 0 {
		writeError(w, http.StatusUnprocessableEntity, "drive_account_id is required")
		return
	}
	if body.WorkspaceID == 0 {
		writeError(w, http.StatusUnprocessableEntity, "workspace_id is required")
		return
	}
	if len(body.Targets) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "at least one target is required")
		return
	}
	for i, t := range body.Targets {
		if t.PlatformAccountID == 0 {
			writeError(w, http.StatusUnprocessableEntity,
				fmt.Sprintf("targets[%d].platform_account_id is required", i))
			return
		}
	}

	// Workspace ownership check.
	ws, err := r.workspaceStore.FindByID(body.WorkspaceID)
	if err != nil {
		code, msg := mapWorkspaceError(err)
		writeError(w, code, "workspace lookup: "+msg)
		return
	}
	if ws == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if ws.OwnerID != userID {
		writeError(w, http.StatusForbidden, "workspace not owned by this user")
		return
	}

	// Idempotency-Key lookup (level 1). The drive-import endpoint
	// creates a post as its primary resource, so we cache on
	// resource_type="drive_import". On replay the post is returned
	// wrapped in a DriveImportResponse so the response shape matches
	// the first-request contract.
	idemKey := strings.TrimSpace(req.Header.Get("Idempotency-Key"))
	idemOutcome, idemRec, idemErr := idempotencyLookup(r, ws.ID, idemKey, hash, "drive_import")
	if idemErr != nil {
		if strings.Contains(idemErr.Error(), "exceeds") {
			writeError(w, http.StatusBadRequest, idemErr.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "idempotency lookup: "+idemErr.Error())
		return
	}
	switch idemOutcome {
	case idempotencyConflict:
		writeError(w, http.StatusConflict, "idempotency_key_conflict")
		return
	case idempotencyReplay:
		if replayErr := replayIdempotentResource(r, w, idemRec, idemRec.ResponseStatus); replayErr != nil {
			writeError(w, http.StatusInternalServerError, "idempotency replay: "+replayErr.Error())
		}
		return
	case idempotencyContinue:
		// Fall through to the rest of the handler.
	}

	// Verify the Drive account belongs to the user and is a google-drive account.
	driveAccount, err := r.userRepo.FindPlatformAccountByID(body.DriveAccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to find drive account: "+err.Error())
		return
	}
	if driveAccount == nil || driveAccount.UserID != userID || driveAccount.Platform != "google-drive" {
		writeError(w, http.StatusNotFound, "google drive account not found")
		return
	}

	// Verify every publish target belongs to the user.
	accounts, err := r.userRepo.ListPlatformAccountsByUser(userID, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list user accounts: "+err.Error())
		return
	}
	owned := make(map[int64]bool, len(accounts))
	for _, a := range accounts {
		owned[a.ID] = true
	}
	for i, tgt := range body.Targets {
		if !owned[tgt.PlatformAccountID] {
			writeError(w, http.StatusForbidden,
				fmt.Sprintf("targets[%d].platform_account_id does not belong to this user", i))
			return
		}
	}

	// Resolve the Google Drive provider.
	provider, ok := r.capabilities.Get("google-drive")
	if !ok {
		writeError(w, http.StatusNotImplemented, "google drive provider not configured")
		return
	}
	driveSvc, ok := provider.(services.DriveImporter)
	if !ok {
		writeError(w, http.StatusInternalServerError, "google drive provider misconfigured")
		return
	}

	// Get a fresh access token from the vault.
	oauthToken, err := r.vault.Renew(req.Context(), driveAccount.ID, models.TokenTypeBearer,
		func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
			return driveSvc.RefreshOAuthToken(ctx, refreshToken)
		})
	if err != nil {
		writeError(w, http.StatusUnauthorized, "failed to refresh google drive token: "+err.Error())
		return
	}

	// Fetch Drive file metadata.
	fileMeta, err := driveSvc.GetFileMetadata(req.Context(), oauthToken.AccessToken, body.DriveFileID)
	if err != nil {
		// Note: ErrDriveDownloadTooLarge is only returned by the
		// limitReadCloser wrapping DownloadFile's body, NOT by
		// GetFileMetadata (which uses io.ReadAll on the small JSON
		// metadata payload). We still keep this defensive check in
		// case a future refactor extends the limit to metadata too.
		if errors.Is(err, services.ErrDriveDownloadTooLarge) {
			writeError(w, http.StatusUnprocessableEntity,
				"drive file exceeds the 10 GiB download cap; split the file or contact support")
			return
		}
		writeError(w, http.StatusBadRequest, "failed to fetch drive file metadata: "+err.Error())
		return
	}
	if !isDriveVideoMimeType(fileMeta.MimeType, fileMeta.Name) {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("drive file is not a supported video type (got %s)", fileMeta.MimeType))
		return
	}
	// P0 hardening refactor: fail-fast on Drive's capabilities
	// block when it explicitly says canDownload=false (the file
	// is shared read-only OR is a shortcut whose target isn't
	// downloadable, etc.). ABSENT capabilities field is NOT a
	// rejection — legacy Drive files omit the field entirely
	// and we don't want to break those imports.
	if fileMeta.Capabilities != nil && !fileMeta.Capabilities.CanDownload {
		writeError(w, http.StatusUnprocessableEntity,
			"drive file is not downloadable (capabilities.canDownload=false); check the file's sharing settings")
		return
	}

	// Parse size; Drive may return an empty size for some formats.
	var sizeBytes int64
	if fileMeta.Size != "" {
		if n, err := strconv.ParseInt(fileMeta.Size, 10, 64); err == nil {
			sizeBytes = n
		}
	}
	if sizeBytes <= 0 {
		writeError(w, http.StatusUnprocessableEntity, "drive file size is unknown or zero; cannot import")
		return
	}

	maxBytes := r.maxUploadBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxUploadBytes
	}
	if sizeBytes > maxBytes {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("drive file size %d exceeds upload limit %d", sizeBytes, maxBytes))
		return
	}

	// Build the S3 key and create a pending media asset.
	key := services.BuildUploadKey(userID, fileMeta.Name)
	asset := &models.MediaAsset{
		UserID:      userID,
		UploadKey:   key,
		ContentType: fileMeta.MimeType,
		SizeBytes:   sizeBytes,
		Status:      models.MediaAssetStatusPending,
		ExpiresAt:   time.Now().Add(mediaAssetLifetime),
	}
	if err := r.mediaStore.Create(asset); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create media asset: "+err.Error())
		return
	}

	// Presign an S3 PUT for the key.
	grant, err := r.storageProvider.SignUpload(req.Context(), userID, key, fileMeta.MimeType, sizeBytes, mediaPresignTTL)
	if err != nil {
		_ = r.mediaStore.MarkFailedWithReason(asset.ID, err.Error(), err)
		writeError(w, http.StatusInternalServerError, "failed to sign s3 upload: "+err.Error())
		return
	}

	// Stream the file from Drive to S3.
	downloadResp, err := driveSvc.DownloadFile(req.Context(), oauthToken.AccessToken, body.DriveFileID)
	if err != nil {
		_ = r.mediaStore.MarkFailedWithReason(asset.ID, err.Error(), err)
		// P0 hardening refactor: ErrDriveDownloadTooLarge is the
		// reader-layer cap firing on a > 10 GiB file. Map it to
		// 422 (operator can split the file) instead of the generic
		// 502 (which would suggest a transient upstream outage).
		if errors.Is(err, services.ErrDriveDownloadTooLarge) {
			writeError(w, http.StatusUnprocessableEntity,
				"drive file exceeds the 10 GiB download cap; split the file or contact support")
			return
		}
		writeError(w, http.StatusBadGateway, "failed to download drive file: "+err.Error())
		return
	}
	defer downloadResp.Body.Close()

	uploadReq, err := http.NewRequestWithContext(req.Context(), http.MethodPut, grant.UploadURL, downloadResp.Body)
	if err != nil {
		_ = r.mediaStore.MarkFailedWithReason(asset.ID, err.Error(), err)
		writeError(w, http.StatusInternalServerError, "failed to build s3 upload request: "+err.Error())
		return
	}
	uploadReq.Header.Set("Content-Type", fileMeta.MimeType)
	if sizeBytes > 0 {
		uploadReq.ContentLength = sizeBytes
	}

	s3Client := &http.Client{Timeout: driveImportS3UploadTimeout}
	uploadResp, err := s3Client.Do(uploadReq)
	if err != nil {
		_ = r.mediaStore.MarkFailedWithReason(asset.ID, err.Error(), err)
		writeError(w, http.StatusBadGateway, "failed to upload to s3: "+err.Error())
		return
	}
	uploadResp.Body.Close()
	if uploadResp.StatusCode >= 300 {
		reason := fmt.Sprintf("s3 upload returned %d", uploadResp.StatusCode)
		_ = r.mediaStore.MarkFailedWithReason(asset.ID, reason, errors.New(reason))
		writeError(w, http.StatusBadGateway, reason)
		return
	}

	// Verify the upload and mark the asset ready.
	verifiedContentType, verifiedSize, err := r.storageProvider.VerifyUpload(req.Context(), key)
	if err != nil {
		_ = r.mediaStore.MarkFailedWithReason(asset.ID, err.Error(), err)
		writeError(w, http.StatusBadGateway, "failed to verify s3 upload: "+err.Error())
		return
	}
	if err := r.mediaStore.MarkReady(asset.ID, "", verifiedSize, verifiedContentType); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark media asset ready: "+err.Error())
		return
	}
	asset.Status = models.MediaAssetStatusReady
	asset.SizeBytes = verifiedSize
	asset.ContentType = verifiedContentType

	// Create the post with the internal S3 URL.
	mediaURL := r.storageProvider.AssetURL(key)
	post := &models.Post{
		WorkspaceID: body.WorkspaceID,
		Title:       body.Title,
		Caption:     body.Caption,
		MediaURL:    mediaURL,
		Status:      models.PostStatusQueued,
	}
	targets := make([]*models.PostTarget, 0, len(body.Targets))
	for _, t := range body.Targets {
		targets = append(targets, &models.PostTarget{
			PlatformAccountID: t.PlatformAccountID,
			Status:            models.PostStatusQueued,
		})
	}
	if err := r.postStore.Create(post, targets); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create post: "+err.Error())
		return
	}

	// Cache the post for Idempotency-Key replays as soon as the post
	// exists. This prevents retries from re-importing the Drive file
	// if the subsequent PublishPost call fails.
	insertIdempotentRecord(r, ws.ID, idemKey, "drive_import", post.ID, hash, http.StatusCreated)

	// Trigger immediate publishing via the existing worker path.
	if err := r.postStore.PublishPost(post.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "post created but failed to trigger publish: "+err.Error())
		return
	}
	post.Status = models.PostStatusPublishing

	slog.Info("drive import completed",
		"user_id", userID,
		"drive_file_id", body.DriveFileID,
		"post_id", post.ID,
		"asset_id", asset.ID,
	)

	// Replay responses preserve the top-level DriveImportResponse shape
	// but omit the asset; clients can fetch the asset separately if
	// they need it.
	writeJSON(w, http.StatusCreated, DriveImportResponse{Post: post, Asset: asset})
}

// isDriveVideoMimeType returns true for the video MIME types we accept.
// If Drive reports a generic MIME type, we also accept files whose name
// ends with a known video extension.
func isDriveVideoMimeType(mime, filename string) bool {
	switch mime {
	case "video/mp4", "video/quicktime", "video/webm", "video/x-msvideo", "video/mpeg":
		return true
	}
	if mime == "application/octet-stream" || mime == "" {
		lower := strings.ToLower(filename)
		for _, ext := range []string{".mp4", ".mov", ".webm", ".avi", ".mpeg", ".mkv"} {
			if strings.HasSuffix(lower, ext) {
				return true
			}
		}
	}
	return false
}
