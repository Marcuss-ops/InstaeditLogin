// Package services provides BillingService for Stripe subscription management.
//
// FASE 3.1: Stripe billing integration.
package services

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/stripe/stripe-go/v84"
	"github.com/stripe/stripe-go/v84/checkout/session"
	portallink "github.com/stripe/stripe-go/v84/billingportal/session"
	"github.com/stripe/stripe-go/v84/webhook"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// BillingStore is the subset of BillingRepository consumed by BillingService.
type BillingStore interface {
	ListPlans() ([]models.Plan, error)
	FindPlanByID(id int64) (*models.Plan, error)
	FindSubscriptionByWorkspace(workspaceID int64) (*models.Subscription, error)
	FindSubscriptionByStripeID(stripeSubID string) (*models.Subscription, error)
	UpsertSubscription(s *models.Subscription) error
	UpdateSubscriptionStatus(stripeSubID, status string, periodEnd *time.Time) error
}

// BillingService handles Stripe Checkout, Customer Portal, and webhooks.
type BillingService struct {
	repo          BillingStore
	stripeKey     string
	webhookSecret string
	successURL    string
	cancelURL     string
}

// NewBillingService creates a BillingService.
func NewBillingService(repo BillingStore, stripeKey, webhookSecret, successURL, cancelURL string) *BillingService {
	stripe.Key = stripeKey
	return &BillingService{
		repo:          repo,
		stripeKey:     stripeKey,
		webhookSecret: webhookSecret,
		successURL:    successURL,
		cancelURL:     cancelURL,
	}
}

// CreateCheckoutSession creates a Stripe Checkout Session for a plan.
func (s *BillingService) CreateCheckoutSession(workspaceID, userID int64, planID int64, billingCycle string, customerEmail string) (string, error) {
	plan, err := s.repo.FindPlanByID(planID)
	if err != nil {
		return "", fmt.Errorf("checkout: find plan: %w", err)
	}
	if plan == nil {
		return "", fmt.Errorf("plan not found: %d", planID)
	}

	priceID := plan.StripePriceMonthlyID
	if billingCycle == "yearly" && plan.StripePriceYearlyID != "" {
		priceID = plan.StripePriceYearlyID
	}
	if priceID == "" {
		return "", fmt.Errorf("no Stripe price ID configured for plan %q (%s)", plan.Name, billingCycle)
	}

	// Check existing subscription for Stripe customer ID.
	var customerID string
	if sub, _ := s.repo.FindSubscriptionByWorkspace(workspaceID); sub != nil && sub.StripeCustomerID != "" {
		customerID = sub.StripeCustomerID
	}

	params := &stripe.CheckoutSessionParams{
		Mode:          stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		SuccessURL:    stripe.String(s.successURL),
		CancelURL:     stripe.String(s.cancelURL),
		CustomerEmail: stripe.String(customerEmail),
		Metadata: map[string]string{
			"workspace_id": fmt.Sprintf("%d", workspaceID),
			"user_id":      fmt.Sprintf("%d", userID),
			"plan_id":      fmt.Sprintf("%d", planID),
		},
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Price:    stripe.String(priceID),
				Quantity: stripe.Int64(1),
			},
		},
	}
	if customerID != "" {
		params.Customer = stripe.String(customerID)
	}

	sess, err := session.New(params)
	if err != nil {
		return "", fmt.Errorf("stripe checkout: %w", err)
	}

	return sess.URL, nil
}

// HandleWebhook processes a Stripe webhook event.
func (s *BillingService) HandleWebhook(payload []byte, signature string) error {
	event, err := webhook.ConstructEvent(payload, signature, s.webhookSecret)
	if err != nil {
		return fmt.Errorf("webhook signature verification: %w", err)
	}

	switch event.Type {
	case "checkout.session.completed":
		return s.handleCheckoutCompleted(event)
	case "customer.subscription.updated":
		return s.handleSubscriptionUpdated(event)
	case "customer.subscription.deleted":
		return s.handleSubscriptionDeleted(event)
	default:
		slog.Debug("unhandled stripe webhook event", "type", event.Type)
		return nil
	}
}

func (s *BillingService) handleCheckoutCompleted(event stripe.Event) error {
	var sess stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &sess); err != nil {
		return fmt.Errorf("parse checkout.session.completed: %w", err)
	}

	workspaceID, _ := parseInt64FromMeta(sess.Metadata, "workspace_id")
	userID, _ := parseInt64FromMeta(sess.Metadata, "user_id")
	planID, _ := parseInt64FromMeta(sess.Metadata, "plan_id")

	if workspaceID == 0 || userID == 0 || planID == 0 {
		return fmt.Errorf("missing metadata in checkout session: %v", sess.Metadata)
	}

	sub := &models.Subscription{
		UserID:               userID,
		WorkspaceID:          workspaceID,
		PlanID:               planID,
		StripeSubscriptionID: sess.Subscription.ID,
		StripeCustomerID:     sess.Customer.ID,
		Status:               models.SubStatusActive,
	}
	return s.repo.UpsertSubscription(sub)
}

func (s *BillingService) handleSubscriptionUpdated(event stripe.Event) error {
	var sub struct {
		ID                string `json:"id"`
		Status            string `json:"status"`
		CurrentPeriodEnd  int64  `json:"current_period_end"`
	}
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		return fmt.Errorf("parse subscription.updated: %w", err)
	}

	var periodEnd *time.Time
	if sub.CurrentPeriodEnd > 0 {
		t := time.Unix(sub.CurrentPeriodEnd, 0)
		periodEnd = &t
	}

	return s.repo.UpdateSubscriptionStatus(sub.ID, sub.Status, periodEnd)
}

func (s *BillingService) handleSubscriptionDeleted(event stripe.Event) error {
	var sub struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		return fmt.Errorf("parse subscription.deleted: %w", err)
	}

	return s.repo.UpdateSubscriptionStatus(sub.ID, models.SubStatusCanceled, nil)
}

// CreatePortalSession creates a Stripe Customer Portal session.
func (s *BillingService) CreatePortalSession(workspaceID int64, returnURL string) (string, error) {
	sub, err := s.repo.FindSubscriptionByWorkspace(workspaceID)
	if err != nil {
		return "", fmt.Errorf("portal: find subscription: %w", err)
	}
	if sub == nil || sub.StripeCustomerID == "" {
		return "", fmt.Errorf("no stripe customer for workspace %d", workspaceID)
	}

	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(sub.StripeCustomerID),
		ReturnURL: stripe.String(returnURL),
	}

	portalSession, err := portallink.New(params)
	if err != nil {
		return "", fmt.Errorf("stripe portal: %w", err)
	}

	return portalSession.URL, nil
}

// GetPlans returns all available plans.
func (s *BillingService) GetPlans() ([]models.Plan, error) {
	return s.repo.ListPlans()
}

func parseInt64FromMeta(meta map[string]string, key string) (int64, error) {
	var v int64
	_, err := fmt.Sscanf(meta[key], "%d", &v)
	return v, err
}
