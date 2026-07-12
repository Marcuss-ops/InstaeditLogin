-- 029_billing.sql — FASE 3.1: Stripe billing tables
--
-- plans: available subscription tiers with Stripe price IDs.
-- subscriptions: per-workspace Stripe subscription state.
--
-- Idempotent via IF NOT EXISTS.

CREATE TABLE IF NOT EXISTS plans (
    id            BIGSERIAL PRIMARY KEY,
    name          VARCHAR(128) NOT NULL,
    price_monthly BIGINT NOT NULL DEFAULT 0,
    price_yearly  BIGINT NOT NULL DEFAULT 0,
    features      JSONB NOT NULL DEFAULT '[]',
    stripe_price_monthly_id VARCHAR(128),
    stripe_price_yearly_id  VARCHAR(128),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS subscriptions (
    id                     BIGSERIAL PRIMARY KEY,
    user_id                BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id           BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    plan_id                BIGINT NOT NULL REFERENCES plans(id),
    stripe_subscription_id VARCHAR(128),
    stripe_customer_id     VARCHAR(128),
    status                 VARCHAR(32) NOT NULL DEFAULT 'incomplete',
    current_period_end     TIMESTAMPTZ,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(workspace_id)
);

CREATE INDEX IF NOT EXISTS idx_subscriptions_workspace_id ON subscriptions(workspace_id);
CREATE INDEX IF NOT EXISTS idx_subscriptions_user_id ON subscriptions(user_id);
CREATE INDEX IF NOT EXISTS idx_subscriptions_stripe_sub_id ON subscriptions(stripe_subscription_id);

-- Seed default plans.
INSERT INTO plans (name, price_monthly, price_yearly, features, stripe_price_monthly_id, stripe_price_yearly_id)
SELECT 'Free', 0, 0, '["1 workspace","3 social accounts","10 posts/month"]', NULL, NULL
WHERE NOT EXISTS (SELECT 1 FROM plans WHERE name = 'Free');

INSERT INTO plans (name, price_monthly, price_yearly, features, stripe_price_monthly_id, stripe_price_yearly_id)
SELECT 'Pro', 1900, 19000, '["Unlimited workspaces","Unlimited social accounts","Unlimited posts","Analytics","Priority support"]', NULL, NULL
WHERE NOT EXISTS (SELECT 1 FROM plans WHERE name = 'Pro');
