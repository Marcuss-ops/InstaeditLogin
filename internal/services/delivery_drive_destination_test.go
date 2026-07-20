package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// Test-group-design: this file covers the GoogleDriveDestination +
// GoogleDriveDeliveryAdapter surfaces. The shared fakeDriveServer
// simulates the v3 Drive API endpoints the destination talks to;
// the test cases below pin Name() matching, chunk-size minimum,
// nil-input rejection, and the app-property-dedupe happy path
// (no sqlmock iterations needed for the smoke tests because the
// destination fails BEFORE the row create when dest.Config is
// missing). The full chunk-loop + crash-recovery scenario is
// covered by Task 9/10's dedicated E2E suite (out of scope today).

// fakeDriveEncryptor is a deterministic in-memory encryptor for
// Drive tests. Round-trip: Decrypt(Encrypt(x)) == x. Prefixed
// "enc:" so an empty decrypt round-trips with the empty string.
// (Named differently from the existing youtube_oauth_resume_test.go
// fakeEncryptor to avoid the package-level redeclaration that
// broke the prior build.)
type fakeDriveEncryptor struct{}

func (fakeDriveEncryptor) Encrypt(plaintext string) ([]byte, error) {
	return []byte("enc:" + plaintext), nil
}
func (fakeDriveEncryptor) Decrypt(ciphertext []byte) (string, error) {
	s := string(ciphertext)
	if !strings.HasPrefix(s, "enc:") {
		return "", errors.New("fakeDriveEncryptor: bad ciphertext prefix")
	}
	return strings.TrimPrefix(s, "enc:"), nil
}

// fakeDriveAccessTokenProvider returns a fixed bearer token.
type fakeDriveAccessTokenProvider struct {
	fixed string
}

func (p *fakeDriveAccessTokenProvider) GetAccessToken(_ context.Context, accountID int64) (string, error) {
	if accountID <= 0 {
		return "", errors.New("fakeDriveAccessTokenProvider: accountID <= 0")
	}
	return p.fixed, nil
}

// fakeDriveServer is a stateful httptest.NewServer handler that
// emulates the Google Drive v3 endpoints GoogleDriveDestination
// talks to in tests:
//
//   - GET /drive/v3/files?q=appProperties has{...} (app-property
//     dedupe lookup; with ?filesByIdempotencyKey pre-populated)
//   - POST /upload/drive/v3/files (initiate — mocked to no-op
//     because the destination's Deliver short-circuits before
//     POST when the dedupe hits)
//   - GET /drive/v3/files/<id> (post-upload verify)
type fakeDriveServer struct {
	mu sync.Mutex
	// filesByAppProperty: idempotency_key → {id, webViewLink}.
	filesByAppProperty map[string]*fakeDriveFile
	listRequests       int64
}

type fakeDriveFile struct {
	id          string
	webViewLink string
}

func newFakeDriveServer() (*fakeDriveServer, http.Handler) {
	f := &fakeDriveServer{
		filesByAppProperty: make(map[string]*fakeDriveFile),
	}
	return f, http.HandlerFunc(f.handle)
}

func (f *fakeDriveServer) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/drive/v3/files":
		f.handleList(w, r)
	default:
		http.Error(w, "fake drive: not implemented", http.StatusNotImplemented)
	}
}

func (f *fakeDriveServer) handleList(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&f.listRequests, 1)
	q := r.URL.Query().Get("q")
	expected := "appProperties has { key='instaedit_delivery_id' and value='"
	if !strings.HasPrefix(q, expected) {
		http.Error(w, "fake drive: unsupported q shape", http.StatusBadRequest)
		return
	}
	tail := strings.TrimPrefix(q, expected)
	// Drive's q-format wraps the value in single-quotes:
	// `... value='<key>' }`. The destination builds `q` with a
	// trailing apostrophe + space + `}; after the URL-encoded
	// outer wrapper lands at the server, the trailing fragment is
	// `<key>'}`. Strip the trailing apostrophe so the lookup key
	// matches what addFile() stored in the map.
	tail = strings.TrimSuffix(tail, " }")
	tail = strings.TrimSuffix(tail, "'")
	unescaped, _ := url.QueryUnescape(tail)

	f.mu.Lock()
	file, ok := f.filesByAppProperty[unescaped]
	f.mu.Unlock()

	if !ok {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"files":[]}`))
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"files": []map[string]string{{
			"id":          file.id,
			"webViewLink": file.webViewLink,
		}},
	})
}

func (f *fakeDriveServer) addFile(idempotencyKey, fileID, webViewLink string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filesByAppProperty[idempotencyKey] = &fakeDriveFile{
		id:          fileID,
		webViewLink: webViewLink,
	}
}

// makeDestinationServer wires httptest.NewServer around fakeDriveServer.
// Returns the live URL + the server record (for hit-counting) and
// a teardown.
func makeDestinationServer(t *testing.T) (*httptest.Server, *fakeDriveServer) {
	t.Helper()
	f, h := newFakeDriveServer()
	srv := httptest.NewServer(h)
	return srv, f
}

// newTestDB returns a sqlmock-backed *sql.DB. The destination's
// repository methods will see these expectations; tests use
// sqlmock.AnyArg() to tolerate varying parameters.
func newTestDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	return db, mock
}

// rebuildDriveDestinationURL rewrites the destination's Drive
// API endpoints to point at the test fake server. The destination
// uses hard-coded https://www.googleapis.com URLs, but tests
// reproduce that behaviour by injecting the test server's URL
// via a transport-rewriting http.Client. Simpler: each test that
// needs the fake server constructs destination with a dedicated
// transport that reroutes *.googleapis.com to driveSrv.URL.
func rewriteTransportForFake(t *testing.T, driveSrvURL string) *http.Client {
	t.Helper()
	return &http.Client{
		Transport: rewriteTransport(driveSrvURL),
	}
}

type rewriteTripper struct {
	base  http.RoundTripper
	drive string
}

func (rt *rewriteTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	newURL := req.URL.String()
	newURL = strings.Replace(newURL, "https://www.googleapis.com/", rt.drive+"/", 1)
	parsed, err := url.Parse(newURL)
	if err != nil {
		return nil, err
	}
	req2 := req.Clone(req.Context())
	req2.URL = parsed
	return rt.base.RoundTrip(req2)
}

func rewriteTransport(driveSrvURL string) http.RoundTripper {
	parsedDrive, _ := url.Parse(driveSrvURL)
	driveBase := parsedDrive.Scheme + "://" + parsedDrive.Host
	return &rewriteTripper{base: http.DefaultTransport, drive: driveBase}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestGoogleDriveDestination_Name_MatchesPlatformGoogleDrive(t *testing.T) {
	db, _ := newTestDB(t)
	defer db.Close()
	tp := &fakeDriveAccessTokenProvider{fixed: "fake-bearer"}
	dst, err := NewGoogleDriveDestination(
		repository.NewDeliverySessionRepository(db),
		tp, fakeDriveEncryptor{}, &http.Client{}, 256*1024,
	)
	if err != nil {
		t.Fatalf("NewGoogleDriveDestination: %v", err)
	}
	if dst.Name() != models.PlatformGoogleDrive {
		t.Errorf("dst.Name() = %q, want %q", dst.Name(), models.PlatformGoogleDrive)
	}
	// Adapter relays the same.
	adapter, err := NewGoogleDriveDeliveryAdapter(dst)
	if err != nil {
		t.Fatalf("NewGoogleDriveDeliveryAdapter: %v", err)
	}
	if adapter.Name() != models.PlatformGoogleDrive {
		t.Errorf("adapter.Name() = %q, want %q", adapter.Name(), models.PlatformGoogleDrive)
	}
}

func TestGoogleDriveDestination_NewGoogleDriveDestination_ChunkSizeMinimum(t *testing.T) {
	db, _ := newTestDB(t)
	defer db.Close()
	tp := &fakeDriveAccessTokenProvider{fixed: "fake-bearer"}
	_, err := NewGoogleDriveDestination(
		repository.NewDeliverySessionRepository(db),
		tp, fakeDriveEncryptor{}, &http.Client{}, 1024, // too small
	)
	if err == nil {
		t.Fatalf("expected error for chunkSizeBytes < 262144, got nil")
	}
	if !strings.Contains(err.Error(), "262144") {
		t.Errorf("error should mention Drive's 256 KiB minimum; got %q", err.Error())
	}
}

func TestGoogleDriveDestination_NewGoogleDriveDestination_NilDependenciesRefused(t *testing.T) {
	db, _ := newTestDB(t)
	defer db.Close()
	tp := &fakeDriveAccessTokenProvider{fixed: "fake-bearer"}
	cases := []struct {
		name string
		fn   func() error
	}{
		{"nil sessionStore", func() error {
			_, err := NewGoogleDriveDestination(nil, tp, fakeDriveEncryptor{}, &http.Client{}, 256*1024)
			return err
		}},
		{"nil tokenProvider", func() error {
			_, err := NewGoogleDriveDestination(repository.NewDeliverySessionRepository(db), nil, fakeDriveEncryptor{}, &http.Client{}, 256*1024)
			return err
		}},
		{"nil encryptor", func() error {
			_, err := NewGoogleDriveDestination(repository.NewDeliverySessionRepository(db), tp, nil, &http.Client{}, 256*1024)
			return err
		}},
		{"nil httpClient", func() error {
			_, err := NewGoogleDriveDestination(repository.NewDeliverySessionRepository(db), tp, fakeDriveEncryptor{}, nil, 256*1024)
			return err
		}},
	}
	for _, tc := range cases {
		if err := tc.fn(); err == nil {
			t.Errorf("%s expected error, got nil", tc.name)
		}
	}
}

func TestGoogleDriveDeliveryAdapter_NewGoogleDriveDeliveryAdapter_NilDestinationRefused(t *testing.T) {
	if _, err := NewGoogleDriveDeliveryAdapter(nil); err == nil {
		t.Errorf("expected nil-destination error, got nil")
	}
}

func TestGoogleDriveDestination_Deliver_NilInputsRejected(t *testing.T) {
	driveSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer driveSrv.Close()

	db, _ := newTestDB(t)
	defer db.Close()
	dst, err := NewGoogleDriveDestination(
		repository.NewDeliverySessionRepository(db),
		&fakeDriveAccessTokenProvider{fixed: "tk"},
		fakeDriveEncryptor{},
		driveSrv.Client(),
		256*1024,
	)
	if err != nil {
		t.Fatalf("NewGoogleDriveDestination: %v", err)
	}

	asset := &models.MediaAsset{ID: "a", SizeBytes: 1024}
	dest := &models.DeliveryDestination{
		Provider: "google-drive",
		Config:   map[string]string{"drive_account_id": "1"},
	}

	if _, err := dst.Deliver(context.Background(), nil, dest, "k"); err == nil {
		t.Errorf("nil asset must error")
	}
	if _, err := dst.Deliver(context.Background(), asset, nil, "k"); err == nil {
		t.Errorf("nil dest must error")
	}
	if _, err := dst.Deliver(context.Background(), asset, dest, ""); err == nil {
		t.Errorf("empty idempotencyKey must error")
	}
	if _, err := dst.Deliver(context.Background(),
		&models.MediaAsset{ID: "a", SizeBytes: 0}, dest, "k"); err == nil {
		t.Errorf("zero SizeBytes must error")
	}
}

func TestGoogleDriveDestination_Deliver_AppPropertyDedupeHitSkipsUpload(t *testing.T) {
	driveSrv, fake := makeDestinationServer(t)
	defer driveSrv.Close()
	fake.addFile("post_target_333", "drive-file-pre-existing",
		"https://drive.google.com/file/d/drive-file-pre-existing/view")

	db, mock := newTestDB(t)
	defer db.Close()

	dst, err := NewGoogleDriveDestination(
		repository.NewDeliverySessionRepository(db),
		&fakeDriveAccessTokenProvider{fixed: "fake-bearer"},
		fakeDriveEncryptor{},
		rewriteTransportForFake(t, driveSrv.URL),
		256*1024,
	)
	if err != nil {
		t.Fatalf("NewGoogleDriveDestination: %v", err)
	}

	res, err := dst.Deliver(context.Background(),
		&models.MediaAsset{ID: "asset-333", SizeBytes: 1024, ContentType: "video/mp4"},
		&models.DeliveryDestination{
			Provider: "google-drive",
			Config: map[string]string{
				"drive_account_id":  "1",
				"folder_id":         "1AbcFolder",
				"filename_template": "{title}.mp4",
			},
		}, "post_target_333")
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if res.Status != "published" {
		t.Errorf("res.Status = %q, want published", res.Status)
	}
	if res.RemoteID != "drive-file-pre-existing" {
		t.Errorf("res.RemoteID = %q, want drive-file-pre-existing", res.RemoteID)
	}
	if res.Metadata["dedupe_source"] != "app_property" {
		t.Errorf("res.Metadata[dedupe_source] = %q, want app_property", res.Metadata["dedupe_source"])
	}
	if atomic.LoadInt64(&fake.listRequests) != 1 {
		t.Errorf("expected 1 list request, got %d", fake.listRequests)
	}
	// Dedupe hit short-circuits BEFORE any DB row create; sqlmock
	// has zero expectations and must see them met.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

// helpers below must import `models` (used by test assertions) Deliver with a missing drive_account_id returns
// ErrDriveConfig (failure-shape: nil result, wrapped err). Pinned
// via errors.Is so dispatchPostCompletion's error-classification
// branch can map it deterministically.
func TestGoogleDriveDestination_Deliver_MissingDriveAccountID_ErrDriveConfig(t *testing.T) {
	driveSrv, _ := makeDestinationServer(t)
	defer driveSrv.Close()

	db, _ := newTestDB(t)
	defer db.Close()

	dst, err := NewGoogleDriveDestination(
		repository.NewDeliverySessionRepository(db),
		&fakeDriveAccessTokenProvider{fixed: "fake-bearer"},
		fakeDriveEncryptor{},
		rewriteTransportForFake(t, driveSrv.URL),
		256*1024,
	)
	if err != nil {
		t.Fatalf("NewGoogleDriveDestination: %v", err)
	}

	res, deliverErr := dst.Deliver(context.Background(),
		&models.MediaAsset{ID: "asset-config", SizeBytes: 1024, ContentType: "video/mp4"},
		&models.DeliveryDestination{Provider: "google-drive"}, // no drive_account_id
		"post_target_no_config")

	if deliverErr == nil {
		t.Fatalf("Deliver: expected ErrDriveConfig, got nil (res=%+v)", res)
	}
	if !errors.Is(deliverErr, ErrDriveConfig) {
		t.Errorf("deliverErr should wrap ErrDriveConfig; got %v", deliverErr)
	}
	if res != nil {
		t.Errorf("res should be nil for config errors; got %+v", res)
	}
}

// Smoke: parseFinalMetadata correctly extracts (id, webViewLink).
func TestParseDriveFinalMetadata_Extracts_ID_and_WebViewLink(t *testing.T) {
	body := []byte(`{"id":"abc123","webViewLink":"https://drive.google.com/file/d/abc123/view","size":"1024"}`)
	id, urlStr, err := parseDriveFinalMetadata(body)
	if err != nil {
		t.Fatalf("parseDriveFinalMetadata: %v", err)
	}
	if id != "abc123" {
		t.Errorf("id = %q, want abc123", id)
	}
	if urlStr != "https://drive.google.com/file/d/abc123/view" {
		t.Errorf("url = %q", urlStr)
	}
}

func TestParseDriveFinalMetadata_EmptyIDErrors(t *testing.T) {
	body := []byte(`{"id":"","webViewLink":"https://example.com"}`)
	if _, _, err := parseDriveFinalMetadata(body); err == nil {
		t.Errorf("expected error for empty id, got nil")
	}
}

func TestDriveResolveFilename(t *testing.T) {
	cases := []struct {
		name     string
		template string
		id       string
		want     string
		wantErr  bool
	}{
		{"empty template", "", "asset-1", "asset-1.mp4", false},
		{"title token", "{title}.mp4", "asset-2", "asset-2.mp4", false},
		{"title + date tokens", "{title}_{date}.mp4", "asset-3", "", false}, // pulls live time.Now — only asserts non-empty
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asset := &models.MediaAsset{ID: tc.id}
			got, err := driveResolveFilename(tc.template, asset)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && tc.name != "title + date tokens" && got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if tc.name == "title + date tokens" && got == "" {
				t.Errorf("title + date should render to non-empty")
			}
		})
	}
	// Date-only template: empty template + nil asset → error.
	if _, err := driveResolveFilename("", nil); err == nil {
		t.Errorf("empty template + nil asset must error")
	}
	if _, err := driveResolveFilename("{title}.mp4", nil); err == nil {
		t.Errorf("non-empty template + nil asset must error")
	}
}

// Reserved for future test expansions. Kept (rather than deleted)
// so adding a new test doesn't risk triggering the unused-import
// gate; these anchors are a deliberate trade-off.
var (
	_ = fmt.Sprintf
	_ = sql.ErrNoRows
	_ = time.Now
)
