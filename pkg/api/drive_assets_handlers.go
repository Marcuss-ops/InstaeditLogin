package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/go-chi/chi/v5"
)

type driveAssetItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	MimeType     string `json:"mime_type"`
	Size         string `json:"size,omitempty"`
	ModifiedTime string `json:"modified_time,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	ContentURL   string `json:"content_url"`
}

func (r *Router) resolveDriveAssetAccount(w http.ResponseWriter, userID int64, rawID string) (*models.PlatformAccount, bool) {
	if strings.TrimSpace(rawID) != "" {
		accountID, err := strconv.ParseInt(strings.TrimSpace(rawID), 10, 64)
		if err != nil || accountID <= 0 {
			writeError(w, http.StatusBadRequest, "drive_account_id must be a positive integer")
			return nil, false
		}
		account, err := r.userRepo.FindPlatformAccountByID(accountID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "find drive account: "+err.Error())
			return nil, false
		}
		if account == nil || account.UserID != userID || account.Platform != models.PlatformGoogleDrive || account.Status == models.AccountStatusDeleted {
			writeError(w, http.StatusNotFound, "google drive account not found")
			return nil, false
		}
		return account, true
	}
	accounts, err := r.userRepo.ListPlatformAccountsByUser(userID, models.PlatformGoogleDrive)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list drive accounts: "+err.Error())
		return nil, false
	}
	for _, account := range accounts {
		if account != nil && account.Status != models.AccountStatusDeleted {
			return account, true
		}
	}
	writeError(w, http.StatusPreconditionRequired, "connect a Google Drive account before loading editor assets")
	return nil, false
}

func (r *Router) handleListDriveAssets(w http.ResponseWriter, req *http.Request) {
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}
	folderID := strings.TrimSpace(req.URL.Query().Get("folder_id"))
	if folderID == "" || !driveFolderIDPatternRegex.MatchString(folderID) {
		writeError(w, http.StatusBadRequest, "folder_id must be a valid Google Drive folder id")
		return
	}
	account, ok := r.resolveDriveAssetAccount(w, userID, req.URL.Query().Get("drive_account_id"))
	if !ok {
		return
	}
	if r.capabilities == nil {
		writeError(w, http.StatusNotImplemented, "google-drive provider not configured")
		return
	}
	providerRaw, exists := r.capabilities.Get(models.PlatformGoogleDrive)
	if !exists {
		writeError(w, http.StatusNotImplemented, "google-drive provider not configured")
		return
	}
	lister, ok := providerRaw.(services.DriveImageFolderLister)
	if !ok {
		writeError(w, http.StatusNotImplemented, "google-drive image listing not configured")
		return
	}
	accessToken, ok := r.resolveDriveListingToken(w, req, userID, account.ID, providerRaw)
	if !ok {
		return
	}
	driveID := resolveSharedDriveID(req.Context(), providerRaw, folderID, accessToken, userID, "drive asset listing")
	files, next, err := lister.ListImageFolder(req.Context(), folderID, driveID, accessToken, req.URL.Query().Get("page_token"))
	if err != nil {
		if errors.Is(err, services.ErrDriveListRequiresAPIKey) {
			writeError(w, http.StatusServiceUnavailable, "Google Drive access is not configured")
			return
		}
		writeError(w, http.StatusBadGateway, "Drive asset listing failed")
		return
	}
	items := make([]driveAssetItem, 0, len(files))
	for _, file := range files {
		items = append(items, driveAssetItem{
			ID: file.ID, Name: file.Name, MimeType: file.MimeType, Size: file.Size, ModifiedTime: file.ModifiedTime,
			ThumbnailURL: file.ThumbnailLink,
			ContentURL:   "/api/v1/drive/assets/" + file.ID + "/content?drive_account_id=" + strconv.FormatInt(account.ID, 10),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"drive_account_id": account.ID, "folder_id": folderID, "items": items, "next_page_token": next})
}

func (r *Router) handleGetDriveAsset(w http.ResponseWriter, req *http.Request) {
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}
	account, ok := r.resolveDriveAssetAccount(w, userID, req.URL.Query().Get("drive_account_id"))
	if !ok {
		return
	}
	if r.capabilities == nil {
		writeError(w, http.StatusNotImplemented, "google-drive provider not configured")
		return
	}
	providerRaw, exists := r.capabilities.Get(models.PlatformGoogleDrive)
	if !exists {
		writeError(w, http.StatusNotImplemented, "google-drive provider not configured")
		return
	}
	provider, ok := providerRaw.(services.DriveImporter)
	if !ok {
		writeError(w, http.StatusNotImplemented, "google-drive provider not configured")
		return
	}
	accessToken, ok := r.resolveDriveListingToken(w, req, userID, account.ID, providerRaw)
	if !ok {
		return
	}
	fileID := strings.TrimSpace(chi.URLParam(req, "id"))
	if fileID == "" {
		writeError(w, http.StatusBadRequest, "asset id is required")
		return
	}
	meta, err := provider.GetFileMetadata(req.Context(), accessToken, fileID)
	if err != nil || meta == nil || !strings.EqualFold(meta.MimeType, "image/png") {
		writeError(w, http.StatusNotFound, "PNG asset not found")
		return
	}
	resp, err := provider.DownloadFile(req.Context(), accessToken, fileID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Drive asset download failed")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=300")
	if resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	_, _ = io.Copy(w, resp.Body)
}
