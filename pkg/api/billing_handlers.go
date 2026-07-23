package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// BillingServiceAPI is the subset of BillingService consumed by handlers.
type BillingServiceAPI interface {
	CreateCheckoutSession(workspaceID, userID int64, planID int64, billingCycle string, customerEmail string) (string, error)
	HandleWebhook(payload []byte, signature string) error
	CreatePortalSession(workspaceID int64, returnURL string) (string, error)
	GetPlans() ([]models.Plan, error)
}

// registerBillingRoutes adds billing endpoints to the chi mux.
func (m *BillingModule) registerBillingRoutes(mux chi.Router) {
	// Public webhook (Stripe calls this, no auth).
	mux.Method(http.MethodPost, "/api/v1/billing/webhook", http.HandlerFunc(m.handleBillingWebhook))

	// Authenticated billing routes.
	mux.Route("/api/v1/billing", func(sr chi.Router) {
		if m.deps.AuthMiddleware != nil {
			sr.Use(m.deps.AuthMiddleware)
		}
		sr.Get("/plans", m.handleGetPlans)
		sr.Post("/checkout", m.handleCreateCheckout)
		sr.Post("/portal", m.handleCreatePortal)
	})
}

// handleGetPlans handles GET /api/v1/billing/plans.
func (m *BillingModule) handleGetPlans(w http.ResponseWriter, req *http.Request) {
	plans, err := m.deps.BillingSvc.GetPlans()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list plans: "+err.Error())
		return
	}
	if plans == nil {
		plans = []models.Plan{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"plans": plans})
}

// handleCreateCheckout handles POST /api/v1/billing/checkout.
func (m *BillingModule) handleCreateCheckout(w http.ResponseWriter, req *http.Request) {
	id := auth.IdentityFromContext(req.Context())
	if id == nil {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}

	var body struct {
		WorkspaceID  int64  `json:"workspace_id"`
		PlanID       int64  `json:"plan_id"`
		BillingCycle string `json:"billing_cycle"`
		Email        string `json:"email"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.WorkspaceID <= 0 || body.PlanID <= 0 {
		writeError(w, http.StatusBadRequest, "workspace_id and plan_id are required")
		return
	}
	if body.BillingCycle == "" {
		body.BillingCycle = "monthly"
	}

	url, err := m.deps.BillingSvc.CreateCheckoutSession(body.WorkspaceID, id.UserID(), body.PlanID, body.BillingCycle, body.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create checkout: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// handleCreatePortal handles POST /api/v1/billing/portal.
func (m *BillingModule) handleCreatePortal(w http.ResponseWriter, req *http.Request) {
	id := auth.IdentityFromContext(req.Context())
	if id == nil {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}

	var body struct {
		WorkspaceID int64 `json:"workspace_id"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.WorkspaceID <= 0 {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}

	returnURL := "http://localhost:5173/dashboard/billing"
	if m.deps.FrontendURL != "" {
		returnURL = m.deps.FrontendURL + "/dashboard/billing"
	}

	url, err := m.deps.BillingSvc.CreatePortalSession(body.WorkspaceID, returnURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create portal: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// handleBillingWebhook handles POST /api/v1/billing/webhook.
func (m *BillingModule) handleBillingWebhook(w http.ResponseWriter, req *http.Request) {
	payload, err := io.ReadAll(req.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body: "+err.Error())
		return
	}

	signature := req.Header.Get("Stripe-Signature")
	if err := m.deps.BillingSvc.HandleWebhook(payload, signature); err != nil {
		writeError(w, http.StatusBadRequest, "webhook error: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
}
