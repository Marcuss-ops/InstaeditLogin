package models

import "time"

// Plan is a subscription tier available for purchase.
type Plan struct {
	ID                   int64     `json:"id"`
	Name                 string    `json:"name"`
	PriceMonthly         int64     `json:"price_monthly"`
	PriceYearly          int64     `json:"price_yearly"`
	Features             []string  `json:"features"`
	StripePriceMonthlyID string    `json:"stripe_price_monthly_id,omitempty"`
	StripePriceYearlyID  string    `json:"stripe_price_yearly_id,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

// Subscription represents a workspace's active Stripe subscription.
type Subscription struct {
	ID                   int64      `json:"id"`
	UserID               int64      `json:"user_id"`
	WorkspaceID          int64      `json:"workspace_id"`
	PlanID               int64      `json:"plan_id"`
	StripeSubscriptionID string     `json:"stripe_subscription_id,omitempty"`
	StripeCustomerID     string     `json:"stripe_customer_id,omitempty"`
	Status               string     `json:"status"`
	CurrentPeriodEnd     *time.Time `json:"current_period_end,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// Subscription status constants.
const (
	SubStatusIncomplete = "incomplete"
	SubStatusActive     = "active"
	SubStatusPastDue    = "past_due"
	SubStatusCanceled   = "canceled"
	SubStatusUnpaid     = "unpaid"
)
