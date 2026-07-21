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
func (r *Router) registerBillingRoutes() {
	// Public webhook (Stripe calls this, no auth).
	r.mux.Method(http.MethodPost, "/api/v1/billing/webhook", http.HandlerFunc(r.handleBillingWebhook))

	// Authenticated billing routes.
	r.mux.Route("/api/v1/billing", func(sr chi.Router) {
		sr.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				r.auth.Middleware(next).ServeHTTP(w, req)
			})
		})
		sr.Get("/plans", r.handleGetPlans)
		sr.Post("/checkout", r.handleCreateCheckout)
		sr.Post("/portal", r.handleCreatePortal)
	})
}

// handleGetPlans handles GET /api/v1/billing/plans.
func (r *Router) handleGetPlans(w http.ResponseWriter, req *http.Request) {
	plans, err := r.billingSvc.GetPlans()
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
func (r *Router) handleCreateCheckout(w http.ResponseWriter, req *http.Request) {
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

	url, err := r.billingSvc.CreateCheckoutSession(body.WorkspaceID, id.UserID(), body.PlanID, body.BillingCycle, body.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create checkout: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// handleCreatePortal handles POST /api/v1/billing/portal.
func (r *Router) handleCreatePortal(w http.ResponseWriter, req *http.Request) {
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

	returnURL := r.frontendURL + "/dashboard/billing"
	if returnURL == "" {
		returnURL = "http://localhost:5173/dashboard/billing"
	}

	url, err := r.billingSvc.CreatePortalSession(body.WorkspaceID, returnURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create portal: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// handleBillingWebhook handles POST /api/v1/billing/webhook.
func (r *Router) handleBillingWebhook(w http.ResponseWriter, req *http.Request) {
	payload, err := io.ReadAll(req.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body: "+err.Error())
		return
	}

	signature := req.Header.Get("Stripe-Signature")
	if err := r.billingSvc.HandleWebhook(payload, signature); err != nil {
		writeError(w, http.StatusBadRequest, "webhook error: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
}
