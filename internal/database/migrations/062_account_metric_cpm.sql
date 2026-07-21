-- 062: add cpm_cents to account_metric_history
-- CPM is a monetary analytics metric returned by YouTube Analytics
-- for monetized channels. Stored in cents for consistency with
-- revenue_cents and rpm_cents.

ALTER TABLE account_metric_history
    ADD COLUMN IF NOT EXISTS cpm_cents BIGINT;
