package services

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
)

// redirectingRoundTripper redirects every Google API URL to a local
// httptest server. Used by the integration-shaped "real GetFileMetadata
// + httptest Drive" tests so the production JSON-parse path runs end
// to end without burning a real Drive API call.
type redirectingRoundTripper struct {
	target *url.URL
}

func (r *redirectingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rewrite the scheme + host to the test server; keep path + query
	// intact so the production code's parsing logic runs unchanged.
	req.URL.Scheme = r.target.Scheme
	req.URL.Host = r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

// newTestDriveService wires a *GoogleDriveOAuthService with a custom
// httpClient whose RoundTripper redirects to the supplied httptest
// server. The cfg is minimal — listing config keys aren't exercised
// by the resolve path.
func newTestDriveService(t *testing.T, srv *httptest.Server) *GoogleDriveOAuthService {
	t.Helper()
	srvURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse srv URL: %v", err)
	}
	return &GoogleDriveOAuthService{
		cfg: &config.Config{
			Auth: config.AuthConfig{
				GoogleDriveClientID:     "test-client",
				GoogleDriveClientSecret: "test-secret",
			},
		},
		httpClient: &http.Client{
			Transport: &redirectingRoundTripper{target: srvURL},
		},
	}
}

// TestResolveFolderDriveID_SharedDrive_ReturnsDriveID verifies the
// happy path: a fake Drive API server returns a file whose driveId
// is "0ABCshared-xxx" → the resolver surfaces that string faithfully.
// This is the FULL PRODUCTION CODE PATH — GetFileMetadata → JSON
// unmarshal → DriveID surfaced; not a mocked interface.
func TestResolveFolderDriveID_SharedDrive_ReturnsDriveID(t *testing.T) {
	const (
		folderID      = "folder-with-shared-drive"
		sharedDriveID = "0ABCshared-xxx"
		accessToken   = "ya29.fake-token"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !strings.HasSuffix(req.URL.Path, "/"+folderID) {
			http.Error(w, "wrong path "+req.URL.Path, http.StatusBadRequest)
			return
		}
		if req.Header.Get("Authorization") != "Bearer "+accessToken {
			http.Error(w, "bad bearer", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "` + folderID + `",
			"name": "shared/",
			"mimeType": "application/vnd.google-apps.folder",
			"driveId": "` + sharedDriveID + `"
		}`))
	}))
	defer srv.Close()

	svc := newTestDriveService(t, srv)
	got, err := ResolveFolderDriveID(context.Background(), svc, folderID, accessToken)
	if err != nil {
		t.Fatalf("RESOLVE err: %v", err)
	}
	if got != sharedDriveID {
		t.Errorf("RESOLVE driveID: want %q, got %q", sharedDriveID, got)
	}
}

// TestResolveFolderDriveID_PrivateFolder_ReturnsEmpty verifies the
// My-Drive corpus path: driveId is missing from the JSON response →
// the resolver returns "" (not the JSON field's null/unset zero
// value) so the caller threads empty into ListFolder → My Drive
// corpus (pre-T6/10 back-compat).
func TestResolveFolderDriveID_PrivateFolder_ReturnsEmpty(t *testing.T) {
	const folderID = "private-folder"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "` + folderID + `",
			"name": "private/",
			"mimeType": "application/vnd.google-apps.folder"
		}`))
	}))
	defer srv.Close()

	svc := newTestDriveService(t, srv)
	got, err := ResolveFolderDriveID(context.Background(), svc, folderID, "ya29.fake")
	if err != nil {
		t.Fatalf("RESOLVE err: %v", err)
	}
	if got != "" {
		t.Errorf("RESOLVE driveID: want empty string for My-Drive folder, got %q", got)
	}
}

// TestResolveFolderDriveID_404_WrapsTypedSentinel verifies the
// failure path: when GetFileMetadata returns 404 (or any non-200),
// the resolver surfaces a wrapped ErrDriveFolderMetadataFetchFailed
// so callers can errors.Is against it for structured remediation.
// This is the typed-sentinel contract that the handler's best-effort
// log line depends on.
func TestResolveFolderDriveID_404_WrapsTypedSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	svc := newTestDriveService(t, srv)
	_, err := ResolveFolderDriveID(context.Background(), svc, "missing-folder", "ya29.fake")
	if err == nil {
		t.Fatal("RESOLVE err: want non-nil on 404, got nil")
	}
	if !errors.Is(err, ErrDriveFolderMetadataFetchFailed) {
		t.Errorf("RESOLVE err: want wrapped ErrDriveFolderMetadataFetchFailed, got %v", err)
	}
}

// TestResolveFolderDriveID_EmptyArgsReturnsTypedSentinel verifies
// input-validation: nil inspector and empty folderID both surface
// the typed sentinel (NOT a panic). The handler's lister type-assert
// can return nil if the provider doesn't implement DriveFolderInspector,
// so a nil-guard here is critical for defensive operation.
func TestResolveFolderDriveID_EmptyArgsReturnsTypedSentinel(t *testing.T) {
	cases := []struct {
		name     string
		svc      DriveFolderInspector
		folderID string
		token    string
	}{
		{"nil inspector", nil, "any", "ya29.fake"},
		{"empty folderID", &fakeInspector{}, "", "ya29.fake"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			driveID, err := ResolveFolderDriveID(context.Background(), tc.svc, tc.folderID, tc.token)
			if !errors.Is(err, ErrDriveFolderMetadataFetchFailed) {
				t.Errorf("err: want ErrDriveFolderMetadataFetchFailed, got %v", err)
			}
			if driveID != "" {
				t.Errorf("driveID: want empty on input-validation failure, got %q", driveID)
			}
		})
	}
}

// fakeInspector is the minimal DriveFolderInspector for the input-
// validation test. Its GetFileMetadata is never called because the
// resolver's guards short-circuit first.
type fakeInspector struct{}

func (fakeInspector) GetFileMetadata(_ context.Context, _, _ string) (*GoogleDriveFile, error) {
	return nil, nil
}
