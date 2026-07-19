package worker

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// fakeImporter implements services.DriveImporter with mockable
// hooks. Records every call so the test can assert the source went
// through the OAuth refresh + DownloadFile pipeline.
//
// NOTE: GoogleDriveFile.Size is a string per the Drive API JSON
// shape; we wire the fake to return a stringly-typed size so the
// production parse path is exercised.
type fakeImporter struct {
	mu                 sync.Mutex
	refreshCalls       int
	downloadCalls      int
	metadataCalls      int
	refreshErr         error
	downloadErr        error
	metadataErr        error
	downloadResp       *http.Response
	metadataResp       *services.GoogleDriveFile
	refreshTokSeen     string
	downloadAccessSeen string
	downloadFileIDSeen string
}

func (f *fakeImporter) RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshCalls++
	f.refreshTokSeen = refreshToken
	if f.refreshErr != nil {
		return nil, f.refreshErr
	}
	return &models.TokenData{
		AccessToken:  "fake-access",
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
	}, nil
}

func (f *fakeImporter) GetFileMetadata(ctx context.Context, accessToken, fileID string) (*services.GoogleDriveFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metadataCalls++
	if f.metadataErr != nil {
		return nil, f.metadataErr
	}
	if f.metadataResp != nil {
		return f.metadataResp, nil
	}
	return &services.GoogleDriveFile{
		ID:             fileID,
		Name:           "test.mp4",
		Size:           "4096", // stringly-typed per Drive API JSON
		MimeType:       "video/mp4",
		SHA256Checksum: "fake-sha256-hex-padded-to-64-chars-aaaaaaaaaaaaaaaaaaaaaaaaaa",
	}, nil
}

func (f *fakeImporter) DownloadFile(ctx context.Context, accessToken, fileID string) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.downloadCalls++
	f.downloadAccessSeen = accessToken
	f.downloadFileIDSeen = fileID
	if f.downloadErr != nil {
		return nil, f.downloadErr
	}
	if f.downloadResp != nil {
		return f.downloadResp, nil
	}
	// Default: a tiny 200 OK with a synthetic body.
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader("fake-artifact-bytes")),
		Header:     http.Header{"Content-Type": []string{"video/mp4"}},
	}, nil
}

// fakeVault satisfies the full credentials.VaultAPI surface. Only
// Renew is exercised by AuthenticatedDriveSource; the rest exist
// to keep the compile-time assertion compile-clean.
type fakeVault struct {
	renewCalls   int
	renewErr     error
	renewAccess  string
	renewHandoff string
}

func (f *fakeVault) Save(_ context.Context, _ int64, _ *models.TokenData) error {
	return errors.New("not implemented in test fake")
}

func (f *fakeVault) Get(_ context.Context, _ int64, _ string) (*models.OAuthToken, error) {
	return nil, errors.New("not implemented in test fake")
}

func (f *fakeVault) Rotate(_ context.Context, _ int64, _ *models.TokenData) error {
	return errors.New("not implemented in test fake")
}

func (f *fakeVault) Renew(
	ctx context.Context,
	accountID int64,
	tokenType string,
	handoff credentials.TokenRefresher,
) (*models.OAuthToken, error) {
	f.renewCalls++
	if f.renewErr != nil {
		return nil, f.renewErr
	}
	// fakeVault simulates a stored refresh_token of "stored-refresh-{accountID}".
	refresh := "stored-refresh-" + strconv.FormatInt(accountID, 10)
	f.renewHandoff = refresh
	tok, err := handoff(ctx, refresh)
	if err != nil {
		return nil, err
	}
	f.renewAccess = tok.AccessToken
	return &models.OAuthToken{
		AccessToken: tok.AccessToken,
		TokenType:   tok.TokenType,
	}, nil
}

func (f *fakeVault) Revoke(_ context.Context, _ int64) error {
	return errors.New("not implemented in test fake")
}

var _ credentials.VaultAPI = (*fakeVault)(nil)

// TestNewAuthenticatedDriveSource_NilImporter — boot-time check.
func TestNewAuthenticatedDriveSource_NilImporter(t *testing.T) {
	_, err := NewAuthenticatedDriveSource(nil, &fakeVault{})
	if err == nil {
		t.Fatal("NewAuthenticatedDriveSource(nil importer) should fail")
	}
}

// TestNewAuthenticatedDriveSource_NilVault — boot-time check.
func TestNewAuthenticatedDriveSource_NilVault(t *testing.T) {
	imp := &fakeImporter{}
	_, err := NewAuthenticatedDriveSource(imp, nil)
	if err == nil {
		t.Fatal("NewAuthenticatedDriveSource(nil vault) should fail")
	}
}

// TestAuthenticatedDriveSource_Inspect_NilDriveAccountID —
// Actionable message rather than a confusing Drive 401.
func TestAuthenticatedDriveSource_Inspect_NilDriveAccountID(t *testing.T) {
	src, err := NewAuthenticatedDriveSource(&fakeImporter{}, &fakeVault{})
	if err != nil {
		t.Fatalf("NewAuthenticatedDriveSource: %v", err)
	}
	_, err = src.Inspect(context.Background(), &models.UploadJob{ID: 1, DriveAccountID: nil})
	if err == nil {
		t.Fatal("Inspect with nil DriveAccountID should fail")
	}
	if !strings.Contains(err.Error(), "drive_account_id") {
		t.Fatalf("expected drive_account_id in error; got %v", err)
	}
}

// TestAuthenticatedDriveSource_Inspect_Happy — refresh + GetFileMetadata
// pipeline returns SourceMetadata populated. Asserts the importer
// was called with the refreshed access token (round-tripped via vault).
func TestAuthenticatedDriveSource_Inspect_Happy(t *testing.T) {
	imp := &fakeImporter{}
	vault := &fakeVault{}
	src, err := NewAuthenticatedDriveSource(imp, vault)
	if err != nil {
		t.Fatalf("NewAuthenticatedDriveSource: %v", err)
	}
	driveID := int64(42)
	job := &models.UploadJob{ID: 7, DriveAccountID: &driveID, SourceID: "drive-file-id"}
	md, err := src.Inspect(context.Background(), job)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if md == nil {
		t.Fatal("Inspect returned nil metadata")
	}
	if md.SizeBytes != 4096 || md.MimeType != "video/mp4" {
		t.Errorf("metadata = %+v; want size=4096 mime=video/mp4", md)
	}
	imp.mu.Lock()
	defer imp.mu.Unlock()
	if imp.refreshCalls != 1 {
		t.Errorf("refreshCalls = %d; want 1", imp.refreshCalls)
	}
	if imp.metadataCalls != 1 {
		t.Errorf("metadataCalls = %d; want 1", imp.metadataCalls)
	}
	// Defense-in-depth: Drive-declared SHA is never trusted. The
	// worker hashes during Open + verifies against the post-Read
	// triple, so SourceMetadata.SHA256Hex must remain empty.
	if md.SHA256Hex != "" {
		t.Errorf("SHA256Hex = %q; want empty (defense-in-depth)", md.SHA256Hex)
	}
}

// TestAuthenticatedDriveSource_Open_NilDriveAccountID — mirror of
// the Inspect gate; the pre-flight invariants match.
func TestAuthenticatedDriveSource_Open_NilDriveAccountID(t *testing.T) {
	src, err := NewAuthenticatedDriveSource(&fakeImporter{}, &fakeVault{})
	if err != nil {
		t.Fatalf("NewAuthenticatedDriveSource: %v", err)
	}
	_, err = src.Open(context.Background(), &models.UploadJob{ID: 1})
	if err == nil {
		t.Fatal("Open with nil DriveAccountID should fail")
	}
}

// TestAuthenticatedDriveSource_Open_Happy — Refresh + DownloadFile
// pipeline returns a ReadCloser that yields the body. Most observable
// invariant: the importer saw the access token delivered from vault.
func TestAuthenticatedDriveSource_Open_Happy(t *testing.T) {
	imp := &fakeImporter{}
	vault := &fakeVault{}
	src, err := NewAuthenticatedDriveSource(imp, vault)
	if err != nil {
		t.Fatalf("NewAuthenticatedDriveSource: %v", err)
	}
	driveID := int64(42)
	job := &models.UploadJob{ID: 7, DriveAccountID: &driveID, SourceID: "drive-file-id"}
	rc, err := src.Open(context.Background(), job)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(body) != "fake-artifact-bytes" {
		t.Errorf("body = %q; want %q", string(body), "fake-artifact-bytes")
	}
	imp.mu.Lock()
	defer imp.mu.Unlock()
	if imp.downloadCalls != 1 {
		t.Errorf("downloadCalls = %d; want 1", imp.downloadCalls)
	}
	if imp.downloadAccessSeen != "fake-access" {
		t.Errorf("download saw access=%q; want fake-access", imp.downloadAccessSeen)
	}
	if imp.downloadFileIDSeen != "drive-file-id" {
		t.Errorf("download saw fileID=%q; want drive-file-id", imp.downloadFileIDSeen)
	}
}

// TestAuthenticatedDriveSource_Open_RefreshFails — refresh error
// bubbles up so classifyUploadError can route the row to retry +
// "auth_error" taxonomy.
func TestAuthenticatedDriveSource_Open_RefreshFails(t *testing.T) {
	imp := &fakeImporter{}
	vault := &fakeVault{renewErr: errors.New("refresh failed")}
	src, err := NewAuthenticatedDriveSource(imp, vault)
	if err != nil {
		t.Fatalf("NewAuthenticatedDriveSource: %v", err)
	}
	driveID := int64(42)
	_, err = src.Open(context.Background(), &models.UploadJob{ID: 7, DriveAccountID: &driveID, SourceID: "x"})
	if err == nil {
		t.Fatal("Open with refresh error should fail")
	}
	if !strings.Contains(err.Error(), "refresh") {
		t.Fatalf("expected 'refresh' in error; got %v", err)
	}
}

// TestAuthenticatedDriveSource_Open_DownloadFails — DownloadFile
// error bubbles up so classifyUploadError can route it.
func TestAuthenticatedDriveSource_Open_DownloadFails(t *testing.T) {
	imp := &fakeImporter{downloadErr: errors.New("drive 502")}
	vault := &fakeVault{}
	src, err := NewAuthenticatedDriveSource(imp, vault)
	if err != nil {
		t.Fatalf("NewAuthenticatedDriveSource: %v", err)
	}
	driveID := int64(42)
	_, err = src.Open(context.Background(), &models.UploadJob{ID: 7, DriveAccountID: &driveID, SourceID: "x"})
	if err == nil {
		t.Fatal("Open with download error should fail")
	}
	if !strings.Contains(err.Error(), "download") {
		t.Fatalf("expected 'download' in error; got %v", err)
	}
}
