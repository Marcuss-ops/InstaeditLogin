package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// BillingRepository handles plans and subscriptions tables.
type BillingRepository struct {
	db *sql.DB
}

// NewBillingRepository creates a BillingRepository.
func NewBillingRepository(db *sql.DB) *BillingRepository {
	return &BillingRepository{db: db}
}

// ListPlans returns all available subscription plans.
func (r *BillingRepository) ListPlans() ([]models.Plan, error) {
	rows, err := r.db.Query(
		`SELECT id, name, price_monthly, price_yearly, features,
		        stripe_price_monthly_id, stripe_price_yearly_id, created_at
		 FROM plans ORDER BY price_monthly ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	defer rows.Close()

	var plans []models.Plan
	for rows.Next() {
		p := models.Plan{}
		var featuresJSON []byte
		if err := rows.Scan(&p.ID, &p.Name, &p.PriceMonthly, &p.PriceYearly,
			&featuresJSON, &p.StripePriceMonthlyID, &p.StripePriceYearlyID,
			&p.CreatedAt); err != nil {
			return nil, fmt.Errorf("list plans: scan: %w", err)
		}
		json.Unmarshal(featuresJSON, &p.Features)
		plans = append(plans, p)
	}
	return plans, nil
}

// FindPlanByID returns a plan by id, or nil if not found.
func (r *BillingRepository) FindPlanByID(id int64) (*models.Plan, error) {
	p := &models.Plan{}
	var featuresJSON []byte
	err := r.db.QueryRow(
		`SELECT id, name, price_monthly, price_yearly, features,
		        stripe_price_monthly_id, stripe_price_yearly_id, created_at
		 FROM plans WHERE id = $1`, id,
	).Scan(&p.ID, &p.Name, &p.PriceMonthly, &p.PriceYearly,
		&featuresJSON, &p.StripePriceMonthlyID, &p.StripePriceYearlyID,
		&p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find plan: %w", err)
	}
	json.Unmarshal(featuresJSON, &p.Features)
	return p, nil
}

// FindSubscriptionByWorkspace returns the subscription for a workspace.
func (r *BillingRepository) FindSubscriptionByWorkspace(workspaceID int64) (*models.Subscription, error) {
	s := &models.Subscription{}
	err := r.db.QueryRow(
		`SELECT id, user_id, workspace_id, plan_id, stripe_subscription_id,
		        stripe_customer_id, status, current_period_end, created_at, updated_at
		 FROM subscriptions WHERE workspace_id = $1`, workspaceID,
	).Scan(&s.ID, &s.UserID, &s.WorkspaceID, &s.PlanID,
		&s.StripeSubscriptionID, &s.StripeCustomerID, &s.Status,
		&s.CurrentPeriodEnd, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find subscription: %w", err)
	}
	return s, nil
}

// FindSubscriptionByStripeID returns the subscription for a Stripe subscription ID.
func (r *BillingRepository) FindSubscriptionByStripeID(stripeSubID string) (*models.Subscription, error) {
	s := &models.Subscription{}
	err := r.db.QueryRow(
		`SELECT id, user_id, workspace_id, plan_id, stripe_subscription_id,
		        stripe_customer_id, status, current_period_end, created_at, updated_at
		 FROM subscriptions WHERE stripe_subscription_id = $1`, stripeSubID,
	).Scan(&s.ID, &s.UserID, &s.WorkspaceID, &s.PlanID,
		&s.StripeSubscriptionID, &s.StripeCustomerID, &s.Status,
		&s.CurrentPeriodEnd, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find subscription by stripe id: %w", err)
	}
	return s, nil
}

// UpsertSubscription creates or updates a subscription row.
func (r *BillingRepository) UpsertSubscription(s *models.Subscription) error {
	now := time.Now()
	err := r.db.QueryRow(
		`INSERT INTO subscriptions (user_id, workspace_id, plan_id, stripe_subscription_id,
		        stripe_customer_id, status, current_period_end, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (workspace_id) DO UPDATE SET
		     plan_id = EXCLUDED.plan_id,
		     stripe_subscription_id = EXCLUDED.stripe_subscription_id,
		     stripe_customer_id = EXCLUDED.stripe_customer_id,
		     status = EXCLUDED.status,
		     current_period_end = EXCLUDED.current_period_end,
		     updated_at = EXCLUDED.updated_at
		 RETURNING id, created_at`,
		s.UserID, s.WorkspaceID, s.PlanID, s.StripeSubscriptionID,
		s.StripeCustomerID, s.Status, s.CurrentPeriodEnd, now,
	).Scan(&s.ID, &s.CreatedAt)
	if err != nil {
		return fmt.Errorf("upsert subscription: %w", err)
	}
	s.UpdatedAt = now
	return nil
}

// UpdateSubscriptionStatus updates the status and period_end of a subscription.
func (r *BillingRepository) UpdateSubscriptionStatus(stripeSubID, status string, periodEnd *time.Time) error {
	_, err := r.db.Exec(
		`UPDATE subscriptions SET status = $1, current_period_end = $2, updated_at = $3
		 WHERE stripe_subscription_id = $4`,
		status, periodEnd, time.Now(), stripeSubID,
	)
	if err != nil {
		return fmt.Errorf("update subscription status: %w", err)
	}
	return nil
}
