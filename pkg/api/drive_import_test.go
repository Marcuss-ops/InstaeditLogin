package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// mockIdempotencyStore is a minimal in-memory idempotency store for
// drive-import + drive-batch tests.
//
// Tracks parent records by (workspace_id, idempotency_key) AND side
// rows (BatchReplay) by idempotency_record_id, mirroring the real
// Postgres FK relationship (`idempotency_batch_replays` PK is the
// parent row's id, migration 039). Assigns monotonic IDs to Insert
// calls so the side row's PK lookup works in replay tests.
type mockIdempotencyStore struct {
	records    map[string]*models.IdempotencyRecord
	batchPlays map[int64]*models.BatchReplay
	nextID     int64
}

func newMockIdempotencyStore() *mockIdempotencyStore {
	return &mockIdempotencyStore{
		records:    make(map[string]*models.IdempotencyRecord),
		batchPlays: make(map[int64]*models.BatchReplay),
	}
}

func (m *mockIdempotencyStore) FindActiveByKey(workspaceID int64, key string, now time.Time) (*models.IdempotencyRecord, error) {
	rec, ok := m.records[idempotencyCompositeKey(workspaceID, key)]
	if !ok || now.After(rec.ExpiresAt) {
		return nil, nil
	}
	return rec, nil
}

// Insert mirrors the production repo: assigns a monotonic ID via
// RETURNING-equivalent semantics so the BatchReplay side row can be
// looked up by parent ID on replay.
func (m *mockIdempotencyStore) Insert(rec *models.IdempotencyRecord) error {
	if rec.ID == 0 {
		m.nextID++
		rec.ID = m.nextID
	}
	rec.CreatedAt = time.Now()
	m.records[idempotencyCompositeKey(rec.WorkspaceID, rec.IdempotencyKey)] = rec
	return nil
}

func (m *mockIdempotencyStore) FindBatchReplay(idempotencyRecordID int64) (*models.BatchReplay, error) {
	rec, ok := m.batchPlays[idempotencyRecordID]
	if !ok {
		return nil, nil
	}
	return rec, nil
}

func (m *mockIdempotencyStore) InsertBatchReplay(rec *models.BatchReplay) error {
	if rec == nil {
		return fmt.Errorf("nil batch replay record")
	}
	if rec.IdempotencyRecordID <= 0 {
		return fmt.Errorf("idempotency_record_id is required")
	}
	if len(rec.ResponsePayload) == 0 {
		return fmt.Errorf("response_payload is required")
	}
	rec.CreatedAt = time.Now()
	m.batchPlays[rec.IdempotencyRecordID] = rec
	return nil
}

func idempotencyCompositeKey(workspaceID int64, key string) string {
	return fmt.Sprintf("%d:%s", workspaceID, key)
}

// mockDriveImporter is a minimal services.DriveImporter for testing
// the drive-import handler end-to-end without real Google calls.
type mockDriveImporter struct {
	metadata *services.GoogleDriveFile
	body     []byte
}

func (m *mockDriveImporter) Name() string                    { return "google-drive" }
func (m *mockDriveImporter) GetLoginURL(state string) string { return "" }
func (m *mockDriveImporter) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	return nil, nil, fmt.Errorf("not implemented")
}
func (m *mockDriveImporter) RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error) {
	return &models.TokenData{AccessToken: "test-access-token", TokenType: models.TokenTypeBearer}, nil
}
func (m *mockDriveImporter) GetFileMetadata(ctx context.Context, accessToken, fileID string) (*services.GoogleDriveFile, error) {
	return m.metadata, nil
}
func (m *mockDriveImporter) DownloadFile(ctx context.Context, accessToken, fileID string) (*http.Response, error) {
	resp := httptest.NewRecorder()
	resp.WriteHeader(http.StatusOK)
	resp.Body.Write(m.body)
	return resp.Result(), nil
}
func (m *mockDriveImporter) DownloadPublicFile(ctx context.Context, fileID string) (*http.Response, error) {
	resp := httptest.NewRecorder()
	resp.WriteHeader(http.StatusOK)
	resp.Body.Write(m.body)
	return resp.Result(), nil
}

func TestDriveImport_Happy(t *testing.T) {
	capRouter := services.NewCapabilityRouter()
	capRouter.Register("google-drive", &mockDriveImporter{
		metadata: &services.GoogleDriveFile{
			ID:       "file-1",
			Name:     "clip.mp4",
			MimeType: "video/mp4",
			Size:     "1024",
		},
		body: make([]byte, 1024),
	})

	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Mine", OwnerID: 1}, nil
		},
	}
	postStore := &mockPostStore{
		createFn: func(p *models.Post, tgts []*models.PostTarget) error {
			p.ID = 123
			p.CreatedAt = time.Now()
			for i, t := range tgts {
				t.ID = int64(200 + i)
				t.PostID = p.ID
			}
			return nil
		},
	}
	userStore := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: id, UserID: 1, Platform: "google-drive"}, nil
		},
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return []*models.PlatformAccount{
				{ID: 2, UserID: 1, Platform: "instagram"},
			}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "test-access-token"}, nil
		},
	}

	// Local S3 stand-in so the handler's PUT to the presigned URL succeeds.
	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer s3Server.Close()

	storage := newMockStorageProvider()
	storage.signFn = func(key string) *services.UploadGrant {
		return &services.UploadGrant{
			UploadURL: s3Server.URL + "/" + key,
			MediaURL:  s3Server.URL + "/" + key,
			ExpiresAt: time.Now().Add(15 * time.Minute),
		}
	}
	storage.verifyFn = func(key string) (string, int64, error) {
		return "video/mp4", 1024, nil
	}
	storage.assetURLFn = func(key string) string {
		return s3Server.URL + "/" + key
	}

	r := NewRouter(
		capRouter,
		userStore,
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithMediaStore(newMockMediaStore()),
		WithStorageProvider(storage),
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
		WithCredentialVault(vault),
		WithIdempotencyStore(newMockIdempotencyStore()),
	)

	body := `{"drive_file_id":"file-1","drive_account_id":1,"workspace_id":1,"title":"t","caption":"c","targets":[{"platform_account_id":2}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp DriveImportResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Post == nil || resp.Post.ID != 123 {
		t.Fatalf("expected post id 123, got %+v", resp.Post)
	}
	if resp.Asset == nil {
		t.Fatal("expected asset in response")
	}
}

func newDriveImportTestRouter() *Router {
	userStore := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: id, UserID: 1, Platform: "google-drive"}, nil
		},
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return []*models.PlatformAccount{{ID: 2, UserID: 1, Platform: "instagram"}}, nil
		},
	}
	return NewRouter(
		services.NewCapabilityRouter(),
		userStore,
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithMediaStore(newMockMediaStore()),
		WithStorageProvider(newMockStorageProvider()),
		WithWorkspaceStore(&mockWorkspaceStore{}),
		WithPostStore(&mockPostStore{}),
		WithCredentialVault(&mockCredentialVault{}),
		WithIdempotencyStore(newMockIdempotencyStore()),
	)
}

func TestDriveImport_NoJWT_401(t *testing.T) {
	r := newDriveImportTestRouter()
	body := `{"drive_file_id":"x","drive_account_id":1,"workspace_id":1,"title":"t","caption":"c","targets":[{"platform_account_id":2}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestDriveImport_MissingFields_422(t *testing.T) {
	r := newDriveImportTestRouter()
	body := `{"drive_file_id":"x","drive_account_id":1,"workspace_id":1,"title":"t","caption":"c"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDriveImport_NotConfigured_501(t *testing.T) {
	r := NewRouter(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
	)
	body := `{"drive_file_id":"x","drive_account_id":1,"workspace_id":1,"title":"t","caption":"c","targets":[{"platform_account_id":2}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("want 501, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDriveImport_IdempotencyReplay(t *testing.T) {
	idemStore := newMockIdempotencyStore()
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Mine", OwnerID: 1}, nil
		},
	}
	postStore := &mockPostStore{
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{
				ID:          id,
				WorkspaceID: 1,
				Title:       "replayed",
				Status:      models.PostStatusPublishing,
				CreatedAt:   time.Now(),
			}, nil
		},
	}
	r := NewRouter(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithMediaStore(newMockMediaStore()),
		WithStorageProvider(newMockStorageProvider()),
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
		WithCredentialVault(&mockCredentialVault{}),
		WithIdempotencyStore(idemStore),
	)

	body := `{"drive_file_id":"x","drive_account_id":1,"workspace_id":1,"title":"t","caption":"c","targets":[{"platform_account_id":2}]}`
	hash := idempotencyHash([]byte(body))
	idemStore.records[idempotencyCompositeKey(1, "idem-key")] = &models.IdempotencyRecord{
		WorkspaceID:    1,
		IdempotencyKey: "idem-key",
		ResourceType:   "drive_import",
		ResourceID:     42,
		RequestHash:    hash,
		ResponseStatus: http.StatusCreated,
		ExpiresAt:      time.Now().Add(time.Hour),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idem-key")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201 replay, got %d: %s", w.Code, w.Body.String())
	}
	var resp DriveImportResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Post == nil || resp.Post.ID != 42 {
		t.Fatalf("replay post id: want 42, got %+v", resp.Post)
	}
}

func TestDriveImport_IdempotencyConflict(t *testing.T) {
	idemStore := newMockIdempotencyStore()
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Mine", OwnerID: 1}, nil
		},
	}
	r := NewRouter(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithMediaStore(newMockMediaStore()),
		WithStorageProvider(newMockStorageProvider()),
		WithWorkspaceStore(wsStore),
		WithPostStore(&mockPostStore{}),
		WithCredentialVault(&mockCredentialVault{}),
		WithIdempotencyStore(idemStore),
	)

	body := `{"drive_file_id":"x","drive_account_id":1,"workspace_id":1,"title":"t","caption":"c","targets":[{"platform_account_id":2}]}`
	idemStore.records[idempotencyCompositeKey(1, "idem-key")] = &models.IdempotencyRecord{
		WorkspaceID:    1,
		IdempotencyKey: "idem-key",
		ResourceType:   "drive_import",
		ResourceID:     42,
		RequestHash:    []byte("different-hash"),
		ResponseStatus: http.StatusCreated,
		ExpiresAt:      time.Now().Add(time.Hour),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idem-key")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("want 409 conflict, got %d: %s", w.Code, w.Body.String())
	}
}
