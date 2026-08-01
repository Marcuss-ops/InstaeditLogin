package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/go-chi/chi/v5"
	"net/http"
	"sync"
	"time"
)

type fakeExternalDestinationStore struct {
	mu sync.Mutex

	CreatedRow *models.ExternalDestination
	CreateErr  error

	ByIDRow *models.ExternalDestination
	ByIDErr error
	ByIDMap map[string]*models.ExternalDestination

	ListErr   error
	DeleteErr error

	// Settable per-test to simulate the repo returning
	// ErrExternalDestinationNotFound or to force a 500 path.
	// Defaults to nil ("happy repo path").
	UpdateEnabledAndDefaultsErr error

	// updateEnabledAndDefaultsCalls counts how many times the
	// combined verb was reached. The
	// TestUpdateIntegrationVeloxDestination_CombinedUpdate test
	// asserts this counter == 1 to prove the handler issues ONE
	// UPDATE per PATCH (not two). A counter > 1 means a future
	// refactor accidentally re-introduced the partial-write window.
	updateEnabledAndDefaultsCalls int
}

func (f *fakeExternalDestinationStore) Create(ctx context.Context, d *models.ExternalDestination) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CreateErr != nil {
		return f.CreateErr
	}
	f.CreatedRow = d
	if f.ByIDMap == nil {
		f.ByIDMap = map[string]*models.ExternalDestination{}
	}
	f.ByIDMap[d.ID] = d
	return nil
}

func (f *fakeExternalDestinationStore) GetByID(ctx context.Context, id string) (*models.ExternalDestination, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ByIDErr != nil {
		return nil, f.ByIDErr
	}
	if f.ByIDMap != nil {
		if r, ok := f.ByIDMap[id]; ok {
			return r, nil
		}
	}
	if f.ByIDRow != nil && f.ByIDRow.ID == id {
		return f.ByIDRow, nil
	}
	return nil, nil
}

func (f *fakeExternalDestinationStore) ListByWorkspace(ctx context.Context, workspaceID int64, enabledOnly bool) ([]models.ExternalDestination, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	out := make([]models.ExternalDestination, 0)
	for _, d := range f.ByIDMap {
		if d.WorkspaceID != workspaceID {
			continue
		}
		if enabledOnly && !d.Enabled {
			continue
		}
		out = append(out, *d)
	}
	return out, nil
}

func (f *fakeExternalDestinationStore) Delete(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	if f.ByIDMap == nil {
		return repository.ErrExternalDestinationNotFound
	}
	if _, ok := f.ByIDMap[id]; !ok {
		return repository.ErrExternalDestinationNotFound
	}
	delete(f.ByIDMap, id)
	return nil
}

func (f *fakeExternalDestinationStore) UpdateEnabledAndDefaults(_ context.Context, id string, enabled *bool, defaults json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateEnabledAndDefaultsCalls++
	if f.UpdateEnabledAndDefaultsErr != nil {
		return f.UpdateEnabledAndDefaultsErr
	}
	if f.ByIDMap == nil {
		return repository.ErrExternalDestinationNotFound
	}
	d, ok := f.ByIDMap[id]
	if !ok {
		return repository.ErrExternalDestinationNotFound
	}
	if enabled != nil {
		d.Enabled = *enabled
	}
	if len(defaults) > 0 {
		// Defensive copy so a caller reusing the slice after the call
		// cannot observe later mutations.
		defCopy := append(json.RawMessage(nil), defaults...)
		d.DefaultMetadata = defCopy
	}
	f.ByIDMap[id] = d
	return nil
}

type fakeWorkspaceStore struct {
	FindByIDResult *models.Workspace
	FindByIDErr    error
}

func (f *fakeWorkspaceStore) FindByID(id int64) (*models.Workspace, error) {
	return f.FindByIDResult, f.FindByIDErr
}

func (f *fakeWorkspaceStore) Create(w *models.Workspace) error {
	return errors.New("not implemented in fakeWorkspaceStore")
}

func (f *fakeWorkspaceStore) ListByOwner(ownerID int64) ([]models.Workspace, error) {
	return nil, errors.New("not implemented in fakeWorkspaceStore")
}

func (f *fakeWorkspaceStore) Delete(id int64) error {
	return errors.New("not implemented in fakeWorkspaceStore")
}

func (f *fakeWorkspaceStore) AttachChannel(ctx context.Context, workspaceID, platformAccountID int64, groupName string) (*models.WorkspaceChannel, error) {
	return nil, errors.New("not implemented in fakeWorkspaceStore")
}

func (f *fakeWorkspaceStore) ListChannels(ctx context.Context, workspaceID int64) ([]models.WorkspaceChannel, error) {
	return nil, errors.New("not implemented in fakeWorkspaceStore")
}

func (f *fakeWorkspaceStore) UpdateChannel(ctx context.Context, workspaceID, platformAccountID int64, groupName *string, enabled *bool) error {
	return errors.New("not implemented in fakeWorkspaceStore")
}

func (f *fakeWorkspaceStore) DetachChannel(ctx context.Context, workspaceID, platformAccountID int64) error {
	return errors.New("not implemented in fakeWorkspaceStore")
}

func (f *fakeWorkspaceStore) FindChannel(ctx context.Context, workspaceID, platformAccountID int64) (*models.WorkspaceChannel, error) {
	return &models.WorkspaceChannel{Enabled: true}, nil
}

type fakeUserStore struct {
	FindPlatformAccountByIDResult *models.PlatformAccount
	FindPlatformAccountByIDErr    error
}

func (f *fakeUserStore) FindPlatformAccountByID(id int64) (*models.PlatformAccount, error) {
	return f.FindPlatformAccountByIDResult, f.FindPlatformAccountByIDErr
}

func (f *fakeUserStore) AttachPlatformAccount(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
	return nil, errors.New("not implemented in fakeUserStore")
}

func (f *fakeUserStore) ListPlatformAccountsByUser(userID int64, platform string) ([]*models.PlatformAccount, error) {
	return nil, errors.New("not implemented in fakeUserStore")
}

func (f *fakeUserStore) FindPlatformAccount(platform, platformUserID string) (*models.PlatformAccount, error) {
	return nil, errors.New("not implemented in fakeUserStore")
}

func (f *fakeUserStore) UpdatePlatformAccount(account *models.PlatformAccount) error {
	return errors.New("not implemented in fakeUserStore")
}

func (f *fakeUserStore) DeletePlatformAccount(id int64) error {
	return errors.New("not implemented in fakeUserStore")
}

func (f *fakeUserStore) FindUserIDByEmail(ctx context.Context, email string) (int64, error) {
	return 0, errors.New("not implemented in fakeUserStore")
}

func (f *fakeUserStore) FinalizeAttach(ctx context.Context, accountID int64, scopes []string) (int64, error) {
	return 0, errors.New("not implemented in fakeUserStore")
}

func (f *fakeUserStore) MarkReauthRequired(ctx context.Context, accountID int64, code, message string) error {
	return nil
}

func (f *fakeUserStore) ListFilteredYouTubeAccounts(userID int64, workspaceID *int64, group, language, manager string) ([]*models.PlatformAccount, error) {
	return nil, nil
}

type fakeAuditLogStore struct {
	LogCalls     int
	LastEvent    string
	LastActorID  string
	LastResType  string
	LastResID    string
	LastMetadata map[string]interface{}
}

func (f *fakeAuditLogStore) Log(ctx context.Context, eventType, actorID, resourceType, resourceID string, metadata map[string]interface{}) error {
	f.LogCalls++
	f.LastEvent = eventType
	f.LastActorID = actorID
	f.LastResType = resourceType
	f.LastResID = resourceID
	f.LastMetadata = metadata
	return nil
}

func setupRouterForCreateDestination() (*Router, *fakeExternalDestinationStore, *fakeWorkspaceStore, *fakeUserStore, *fakeAuditLogStore) {
	destStore := &fakeExternalDestinationStore{}
	wsStore := &fakeWorkspaceStore{}
	userStore := &fakeUserStore{}
	auditStore := &fakeAuditLogStore{}
	r := &Router{
		mux:                  chi.NewRouter(),
		externalDestinations: destStore,
		workspaceStore:       wsStore,
		userRepo:             userStore,
		auditLogStore:        auditStore,
		auth:                 auth.NewManager("test-secret-32-chars-aaaaaaaaaa", 24),
		csrfMiddleware:       passthroughCSRF, // bypass CSRF for test
		authMiddleware:       passthroughAuth, // bypass JWT for test
	}
	r.registerUserVeloxDestinations(r.mux)
	return r, destStore, wsStore, userStore, auditStore
}

func passthroughCSRF(next http.Handler) http.Handler {
	return next
}

func passthroughAuth(next http.Handler) http.Handler {
	return next
}

func reqWithUser(req *http.Request, userID int64) *http.Request {
	id := auth.NewUserIdentity(int64(userID), 0, 0)
	return req.WithContext(auth.WithIdentity(req.Context(), id))
}

func ptrTime() *time.Time {
	t := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	return &t
}

type typedErr struct{ s string }

func (e *typedErr) Error() string { return e.s }

func errorsMatch(s string) error { return &typedErr{s: s} }

func setupRouterForDestinations() (*Router, *fakeExternalDestinationStore, *fakeWorkspaceStore, *fakeUserStore, *fakeAuditLogStore) {
	r, destStore, wsStore, userStore, auditStore := setupRouterForCreateDestination()
	return r, destStore, wsStore, userStore, auditStore
}

func seedDestination(destStore *fakeExternalDestinationStore, id string, wsID, paID int64, enabled bool) *models.ExternalDestination {
	if destStore.ByIDMap == nil {
		destStore.ByIDMap = map[string]*models.ExternalDestination{}
	}
	d := &models.ExternalDestination{
		ID:                id,
		SourceSystem:      "velox",
		WorkspaceID:       wsID,
		PlatformAccountID: paID,
		Enabled:           enabled,
		DefaultMetadata:   json.RawMessage(`{"privacy_status":"private"}`),
	}
	destStore.ByIDMap[id] = d
	return d
}
