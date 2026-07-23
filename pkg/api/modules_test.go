package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/channelimport"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	veloxapi "github.com/Marcuss-ops/InstaeditLogin/pkg/api/velox"
)

// ---------------------------------------------------------------------------
// BillingModule tests
// ---------------------------------------------------------------------------

type fakeBillingService struct {
	plans       []models.Plan
	checkoutURL string
	portalURL   string
	webhookErr  error
}

func (f *fakeBillingService) CreateCheckoutSession(workspaceID, userID int64, planID int64, billingCycle, customerEmail string) (string, error) {
	return f.checkoutURL, nil
}

func (f *fakeBillingService) HandleWebhook(payload []byte, signature string) error {
	return f.webhookErr
}

func (f *fakeBillingService) CreatePortalSession(workspaceID int64, returnURL string) (string, error) {
	return f.portalURL, nil
}

func (f *fakeBillingService) GetPlans() ([]models.Plan, error) {
	return f.plans, nil
}

func fakeIdentityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithIdentity(r.Context(), auth.NewUserIdentity(1, 1, 1))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func TestBillingModule_MountsRoutes(t *testing.T) {
	mux := chi.NewRouter()
	mod := NewBillingModule(BillingModuleDeps{
		BillingSvc:     &fakeBillingService{plans: []models.Plan{}},
		AuthMiddleware: fakeIdentityMiddleware,
		FrontendURL:    "http://localhost:5173",
	})
	mod.Register(mux)

	routes := mux.Routes()
	if len(routes) == 0 {
		t.Fatal("expected billing routes to be mounted")
	}
}

func TestBillingModule_SkipsWhenBillingSvcNil(t *testing.T) {
	mux := chi.NewRouter()
	mod := NewBillingModule(BillingModuleDeps{BillingSvc: nil})
	mod.Register(mux)

	if len(mux.Routes()) != 0 {
		t.Fatalf("expected no routes when BillingSvc is nil, got %d", len(mux.Routes()))
	}
}

func TestBillingModule_GetPlans(t *testing.T) {
	mux := chi.NewRouter()
	plans := []models.Plan{{ID: 1, Name: "Pro"}}
	mod := NewBillingModule(BillingModuleDeps{
		BillingSvc:     &fakeBillingService{plans: plans},
		AuthMiddleware: fakeIdentityMiddleware,
		FrontendURL:    "http://localhost:5173",
	})
	mod.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/billing/plans", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string][]models.Plan
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp["plans"]) != 1 || resp["plans"][0].Name != "Pro" {
		t.Fatalf("unexpected plans response: %+v", resp)
	}
}

func TestBillingModule_Webhook(t *testing.T) {
	mux := chi.NewRouter()
	mod := NewBillingModule(BillingModuleDeps{
		BillingSvc:     &fakeBillingService{},
		AuthMiddleware: fakeIdentityMiddleware,
		FrontendURL:    "",
	})
	mod.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/billing/webhook", bytes.NewReader([]byte("{}")))
	req.Header.Set("Stripe-Signature", "sig")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// AdminModule tests
// ---------------------------------------------------------------------------

type fakeAdminStore struct {
	counts   repository.AdminChannelCounts
	channels []repository.AdminChannelRow
	queue    repository.AdminQueueCounts
}

func (f *fakeAdminStore) ChannelCounts(ctx context.Context) (repository.AdminChannelCounts, error) {
	return f.counts, nil
}

func (f *fakeAdminStore) ListChannelsForOps(ctx context.Context, statusFilter, platformFilter string, limit int) ([]repository.AdminChannelRow, error) {
	return f.channels, nil
}

func (f *fakeAdminStore) QueueCounts(ctx context.Context) (repository.AdminQueueCounts, error) {
	return f.queue, nil
}

func (f *fakeAdminStore) InFlightPerWorker(ctx context.Context) ([]repository.AdminInFlightRow, error) {
	return nil, nil
}

func (f *fakeAdminStore) ListStuckJobs(ctx context.Context, limit int) ([]repository.AdminStuckJobRow, error) {
	return nil, nil
}

func (f *fakeAdminStore) ListDeadLetterJobs(ctx context.Context, limit int) ([]repository.AdminDeadLetterJobRow, error) {
	return nil, nil
}

func (f *fakeAdminStore) ErrorRatePerChannel(ctx context.Context, windowInterval, windowLabel string, limit int) ([]repository.AdminErrorRateRow, error) {
	return nil, nil
}

func (f *fakeAdminStore) YouTubeQuotaApproximation(ctx context.Context, window time.Duration, dailyBudgetUnits, costPerUploadUnits int64) (repository.AdminYouTubeQuota, error) {
	return repository.AdminYouTubeQuota{}, nil
}

func (f *fakeAdminStore) UpsertPendingChannel(ctx context.Context, ownerUserID int64, rows []channelimport.ImportRow) (channelimport.Result, error) {
	return channelimport.Result{}, nil
}

func (f *fakeAdminStore) CreateFleetReadinessSnapshot(ctx context.Context, adminUserID int64) (repository.FleetReadinessSnapshotResponse, error) {
	return repository.FleetReadinessSnapshotResponse{}, nil
}

type testConnectLinkNonceStore struct{}

func (f *testConnectLinkNonceStore) Create(jti, expectedChannelID string, expiresAt time.Time) error {
	return nil
}
func (f *testConnectLinkNonceStore) Consume(jti string) error { return nil }

func newAdminTestModule(adminStore AdminStore) (*AdminModule, *auth.Manager) {
	mgr := auth.NewManager("admin-test-secret-must-be-long-enough", 15)
	mod := NewAdminModule(AdminModuleDeps{
		AdminStore:            adminStore,
		AuthManager:           mgr,
		UserStore:             &mockUserStore{},
		WorkspaceStore:        &mockWorkspaceStore{},
		Capabilities:          services.NewCapabilityRouter(),
		ConnectLinkNonceStore: &testConnectLinkNonceStore{},
	})
	return mod.(*AdminModule), mgr
}

func TestAdminModule_MountsRoutes(t *testing.T) {
	mod, _ := newAdminTestModule(&fakeAdminStore{})
	mux := chi.NewRouter()
	mod.Register(mux)

	if len(mux.Routes()) == 0 {
		t.Fatal("expected admin routes to be mounted")
	}
}

func TestAdminModule_SkipsWhenAdminStoreNil(t *testing.T) {
	mod, _ := newAdminTestModule(nil)
	mux := chi.NewRouter()
	mod.Register(mux)

	if len(mux.Routes()) != 0 {
		t.Fatalf("expected no routes when AdminStore is nil, got %d", len(mux.Routes()))
	}
}

func TestAdminModule_ChannelsRequiresAdmin(t *testing.T) {
	mod, mgr := newAdminTestModule(&fakeAdminStore{})
	mux := chi.NewRouter()
	mod.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/admin/channels", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without identity, got %d: %s", w.Code, w.Body.String())
	}

	tok, _, _, err := mgr.IssueAccessAdmin(1, 1, 1, true)
	if err != nil {
		t.Fatalf("issue admin token: %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/channels", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for admin, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminModule_NonAdminIsForbidden(t *testing.T) {
	mod, mgr := newAdminTestModule(&fakeAdminStore{})
	mux := chi.NewRouter()
	mod.Register(mux)

	tok, _, _, err := mgr.IssueAccessAdmin(1, 1, 1, false)
	if err != nil {
		t.Fatalf("issue non-admin token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/channels", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// VeloxBFFModule tests
// ---------------------------------------------------------------------------

type fakeVeloxClient struct{}

func (f *fakeVeloxClient) ListJobs(ctx context.Context, workspaceID, userID int64, filter veloxapi.ListJobsFilter) ([]veloxapi.Job, error) {
	return nil, nil
}

func (f *fakeVeloxClient) CreateJob(ctx context.Context, workspaceID, userID int64, req veloxapi.CreateJobRequest) (*veloxapi.Job, error) {
	return nil, nil
}

func (f *fakeVeloxClient) GetJob(ctx context.Context, workspaceID, userID int64, jobID string) (*veloxapi.JobDetail, error) {
	return nil, nil
}

func (f *fakeVeloxClient) CancelJob(ctx context.Context, workspaceID, userID int64, jobID string) error {
	return nil
}

func (f *fakeVeloxClient) ListJobDeliveries(ctx context.Context, workspaceID, userID int64, jobID string) ([]veloxapi.Delivery, error) {
	return nil, nil
}

func (f *fakeVeloxClient) ListWorkers(ctx context.Context, workspaceID, userID int64) ([]veloxapi.Worker, error) {
	return nil, nil
}

func (f *fakeVeloxClient) GetWorker(ctx context.Context, workspaceID, userID int64, workerID string) (*veloxapi.Worker, error) {
	return nil, nil
}

func (f *fakeVeloxClient) GetAsset(ctx context.Context, workspaceID, userID int64, assetID string) (*veloxapi.Asset, error) {
	return nil, nil
}

func TestVeloxBFFModule_MountsRoutes(t *testing.T) {
	mux := chi.NewRouter()
	mod := NewVeloxBFFModule(VeloxBFFModuleDeps{
		Client:         &fakeVeloxClient{},
		AuthMiddleware: fakeIdentityMiddleware,
		CSRFMiddleware: func(next http.Handler) http.Handler { return next },
	})
	mod.Register(mux)

	if len(mux.Routes()) == 0 {
		t.Fatal("expected velox BFF routes to be mounted")
	}
}

func TestVeloxBFFModule_SkipsWhenClientNil(t *testing.T) {
	mux := chi.NewRouter()
	mod := NewVeloxBFFModule(VeloxBFFModuleDeps{Client: nil})
	mod.Register(mux)

	if len(mux.Routes()) != 0 {
		t.Fatalf("expected no routes when Client is nil, got %d", len(mux.Routes()))
	}
}
