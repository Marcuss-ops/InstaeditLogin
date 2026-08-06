package api

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type adminMultipartPayload struct {
	body        *bytes.Buffer
	contentType string
}

func adminImportCSVBody(t *testing.T, csvData string) adminMultipartPayload {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "channels.csv")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := io.WriteString(part, csvData); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return adminMultipartPayload{body: &body, contentType: mw.FormDataContentType()}
}

func newAdminImportTestModule(workspaces func(int64) ([]models.Workspace, error), adminStore AdminStore) *AdminModule {
	return &AdminModule{deps: AdminModuleDeps{
		AdminStore: adminStore,
		WorkspaceStore: &mockWorkspaceStore{
			listByOwnerFn: workspaces,
		},
	}}
}

func withAdminImportIdentity(req *http.Request) *http.Request {
	return req.WithContext(auth.WithIdentity(req.Context(), staffIdentity{uid: 1, isAdmin: true}))
}

func validAdminImportCSV(extra string) string {
	return "channel_id,channel_name,manager_email_hint,workspace,group,language,timezone,expected_upload_frequency,extra\n" +
		"UC123,Channel,manager@example.com,alpha,group,en-US,UTC,1/week," + extra + "\n"
}

func TestAdminImportChannelsCSV_RejectsBodyOverExplicitLimit(t *testing.T) {
	module := newAdminImportTestModule(func(int64) ([]models.Workspace, error) {
		return []models.Workspace{{ID: 1, Name: "alpha"}}, nil
	}, &stubAdminStore{})
	payload := adminImportCSVBody(t, validAdminImportCSV(strings.Repeat("x", int(adminCSVMaxUploadBytes))))

	req := httptest.NewRequest(http.MethodPost, "/admin/channels/import-csv", payload.body)
	req.Header.Set("Content-Type", payload.contentType)
	req = withAdminImportIdentity(req)
	w := httptest.NewRecorder()

	module.handleAdminImportChannelsCSV(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: want 413, got %d (body=%q)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "multipart upload exceeds") {
		t.Errorf("error: want explicit upload-limit message, got %q", w.Body.String())
	}
}

func TestAdminImportChannelsCSV_SpoolsLargePartAndCleansTemporaryFiles(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)

	module := newAdminImportTestModule(func(int64) ([]models.Workspace, error) {
		return []models.Workspace{{ID: 1, Name: "alpha"}}, nil
	}, &stubAdminStore{})
	payload := adminImportCSVBody(t, validAdminImportCSV(strings.Repeat("x", 2<<20)))

	req := httptest.NewRequest(http.MethodPost, "/admin/channels/import-csv", payload.body)
	req.Header.Set("Content-Type", payload.contentType)
	req = withAdminImportIdentity(req)
	w := httptest.NewRecorder()

	module.handleAdminImportChannelsCSV(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%q)", w.Code, w.Body.String())
	}
	assertAdminImportTempDirEmpty(t, tmpDir)
}

func TestAdminImportChannelsCSV_CleansTemporaryFilesOnValidationError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)
	workspaceErr := fmt.Errorf("workspace store unavailable")

	module := newAdminImportTestModule(func(int64) ([]models.Workspace, error) {
		return nil, workspaceErr
	}, &stubAdminStore{})
	payload := adminImportCSVBody(t, validAdminImportCSV(strings.Repeat("x", 2<<20)))

	req := httptest.NewRequest(http.MethodPost, "/admin/channels/import-csv", payload.body)
	req.Header.Set("Content-Type", payload.contentType)
	req = withAdminImportIdentity(req)
	w := httptest.NewRecorder()

	module.handleAdminImportChannelsCSV(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: want 422, got %d (body=%q)", w.Code, w.Body.String())
	}
	assertAdminImportTempDirEmpty(t, tmpDir)
}

func TestAdminImportChannelsCSV_CleansTemporaryFilesOnMalformedMultipart(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)

	module := newAdminImportTestModule(func(int64) ([]models.Workspace, error) {
		return []models.Workspace{{ID: 1, Name: "alpha"}}, nil
	}, &stubAdminStore{})
	payload := adminImportCSVBody(t, validAdminImportCSV(strings.Repeat("x", 2<<20)))
	truncated := payload.body.Bytes()[:payload.body.Len()/2]

	req := httptest.NewRequest(http.MethodPost, "/admin/channels/import-csv", bytes.NewReader(truncated))
	req.Header.Set("Content-Type", payload.contentType)
	req = withAdminImportIdentity(req)
	w := httptest.NewRecorder()

	module.handleAdminImportChannelsCSV(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d (body=%q)", w.Code, w.Body.String())
	}
	assertAdminImportTempDirEmpty(t, tmpDir)
}

func assertAdminImportTempDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read temporary directory: %v", err)
	}
	if len(entries) == 0 {
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, filepath.Base(entry.Name()))
	}
	t.Fatalf("multipart temporary files were not cleaned up: %v", names)
}
