package repository

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// AccountMetricPoint represents a single daily metric row for an account.
type AccountMetricPoint struct {
	Date          time.Time `json:"date"`
	UpdatedAt     time.Time `json:"-"`
	Subscribers   int64     `json:"subscribers"`
	Views         int64     `json:"views"`
	Videos        int64     `json:"videos"`
	WatchTimeMins *int64    `json:"watch_time_minutes,omitempty"`
	Impressions   *int64    `json:"impressions,omitempty"`
	CTR           *float64  `json:"ctr,omitempty"`
	RevenueCents  *int64    `json:"revenue_cents,omitempty"`
	RPMCents      *int64    `json:"rpm_cents,omitempty"`
	CPMCents      *int64    `json:"cpm_cents,omitempty"`
}

// utcDay normalizes provider timestamps to the UTC calendar day.
func utcDay(value time.Time) time.Time {
	if value.IsZero() {
		value = time.Now().UTC()
	}
	value = value.UTC()
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// AccountMetricsRepository persists daily metric snapshots for
// platform accounts. It is intentionally separate from
// SnapshotRepository, which caches the raw remote JSON; this table
// stores a time-series of extracted numeric metrics.
type AccountMetricsRepository struct {
	db *sql.DB
}

// NewAccountMetricsRepository creates a new AccountMetricsRepository.
func NewAccountMetricsRepository(db *sql.DB) *AccountMetricsRepository {
	return &AccountMetricsRepository{db: db}
}

// UpsertMonetary updates only the monetary columns for a single daily
// metric row. It is used when earnings data arrives separately from the
// public channel statistics (for example, from the YouTube Analytics
// API) so that existing subscriber/view/video values are preserved.
func (r *AccountMetricsRepository) UpsertMonetary(
	platformAccountID int64,
	date time.Time,
	point AccountMetricPoint,
) error {
	_, err := r.db.Exec(
		`INSERT INTO account_metric_history
		    (platform_account_id, metric_date, subscribers, views, videos,
		     watch_time_minutes, impressions, ctr, revenue_cents, rpm_cents,
		     cpm_cents, updated_at)
		 VALUES ($1, $2, 0, 0, 0, NULL, NULL, NULL, $3, $4, $5, NOW())
		 ON CONFLICT (platform_account_id, metric_date) DO UPDATE SET
		    revenue_cents = EXCLUDED.revenue_cents,
		    rpm_cents       = EXCLUDED.rpm_cents,
		    cpm_cents       = EXCLUDED.cpm_cents,
		    updated_at      = NOW()`,
		platformAccountID,
		utcDay(date),
		point.RevenueCents,
		point.RPMCents,
		point.CPMCents,
	)
	if err != nil {
		return fmt.Errorf("upsert monetary metrics: %w", err)
	}
	return nil
}

// UpsertDaily inserts or updates a single daily metric row for an
// account. The caller typically invokes this immediately after a fresh
// snapshot is fetched from the provider.
func (r *AccountMetricsRepository) UpsertDaily(
	platformAccountID int64,
	date time.Time,
	point AccountMetricPoint,
) error {
	_, err := r.db.Exec(
		`INSERT INTO account_metric_history
		    (platform_account_id, metric_date, subscribers, views, videos,
		     watch_time_minutes, impressions, ctr, revenue_cents, rpm_cents,
		     cpm_cents, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
		 ON CONFLICT (platform_account_id, metric_date) DO UPDATE SET
		    subscribers        = EXCLUDED.subscribers,
		    views              = EXCLUDED.views,
		    videos             = EXCLUDED.videos,
		    watch_time_minutes = EXCLUDED.watch_time_minutes,
		    impressions        = EXCLUDED.impressions,
		    ctr                = EXCLUDED.ctr,
		    revenue_cents      = EXCLUDED.revenue_cents,
		    rpm_cents          = EXCLUDED.rpm_cents,
		    cpm_cents          = EXCLUDED.cpm_cents,
		    updated_at         = NOW()`,
		platformAccountID,
		utcDay(date),
		point.Subscribers,
		point.Views,
		point.Videos,
		point.WatchTimeMins,
		point.Impressions,
		point.CTR,
		point.RevenueCents,
		point.RPMCents,
		point.CPMCents,
	)
	if err != nil {
		return fmt.Errorf("upsert daily metrics: %w", err)
	}
	return nil
}

// GetHistory returns the daily metric rows for an account within the
// requested inclusive date range, ordered oldest to newest.
func (r *AccountMetricsRepository) GetHistory(
	platformAccountID int64,
	from time.Time,
	to time.Time,
) ([]AccountMetricPoint, error) {
	rows, err := r.db.Query(
		`SELECT metric_date, updated_at, subscribers, views, videos,
		        watch_time_minutes, impressions, ctr, revenue_cents, rpm_cents, cpm_cents
		 FROM account_metric_history
		 WHERE platform_account_id = $1
		   AND metric_date >= $2
		   AND metric_date <= $3
		 ORDER BY metric_date ASC`,
		platformAccountID, utcDay(from), utcDay(to),
	)
	if err != nil {
		return nil, fmt.Errorf("query metric history: %w", err)
	}
	defer rows.Close()

	var points []AccountMetricPoint
	for rows.Next() {
		var p AccountMetricPoint
		if err := rows.Scan(
			&p.Date,
			&p.UpdatedAt,
			&p.Subscribers,
			&p.Views,
			&p.Videos,
			&p.WatchTimeMins,
			&p.Impressions,
			&p.CTR,
			&p.RevenueCents,
			&p.RPMCents,
			&p.CPMCents,
		); err != nil {
			return nil, fmt.Errorf("scan metric history: %w", err)
		}
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate metric history: %w", err)
	}
	return points, nil
}

// GetHistoryBatch returns the daily metric history for all supplied
// accounts in one SQL round-trip. The result is grouped by account and
// ordered by metric_date ASC within each group so callers can apply the
// same growth calculations as GetHistory without an N+1 query fan-out.
//
// The account/date index created by migration 061 covers the equality
// predicate on platform_account_id and the metric_date range/order.
func (r *AccountMetricsRepository) GetHistoryBatch(
	platformAccountIDs []int64,
	from time.Time,
	to time.Time,
) (map[int64][]AccountMetricPoint, error) {
	out := make(map[int64][]AccountMetricPoint, len(platformAccountIDs))
	if len(platformAccountIDs) == 0 {
		return out, nil
	}

	rows, err := r.db.Query(
		`SELECT platform_account_id, metric_date, updated_at, subscribers, views, videos,
		        watch_time_minutes, impressions, ctr, revenue_cents, rpm_cents, cpm_cents
		 FROM account_metric_history
		 WHERE platform_account_id = ANY($1::bigint[])
		   AND metric_date >= $2
		   AND metric_date <= $3
		 ORDER BY platform_account_id ASC, metric_date DESC`,
		pq.Array(platformAccountIDs), utcDay(from), utcDay(to),
	)
	if err != nil {
		return nil, fmt.Errorf("query metric history for accounts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var accountID int64
		var point AccountMetricPoint
		if err := rows.Scan(
			&accountID,
			&point.Date,
			&point.UpdatedAt,
			&point.Subscribers,
			&point.Views,
			&point.Videos,
			&point.WatchTimeMins,
			&point.Impressions,
			&point.CTR,
			&point.RevenueCents,
			&point.RPMCents,
			&point.CPMCents,
		); err != nil {
			return nil, fmt.Errorf("scan metric history for account %d: %w", accountID, err)
		}
		out[accountID] = append(out[accountID], point)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate metric history for accounts: %w", err)
	}
	// The composite index is ordered by account ASC, metric_date DESC,
	// which avoids a global sort in PostgreSQL. Restore the historical
	// GetHistory contract (oldest to newest) independently per account
	// before returning to callers.
	for accountID, history := range out {
		for left, right := 0, len(history)-1; left < right; left, right = left+1, right-1 {
			history[left], history[right] = history[right], history[left]
		}
		out[accountID] = history
	}
	return out, nil
}

// LatestForAccount returns the most recent metric point for an
// account, or nil if no history exists.
func (r *AccountMetricsRepository) LatestForAccount(platformAccountID int64) (*AccountMetricPoint, error) {
	row := r.db.QueryRow(
		`SELECT metric_date, updated_at, subscribers, views, videos,
		        watch_time_minutes, impressions, ctr, revenue_cents, rpm_cents, cpm_cents
		 FROM account_metric_history
		 WHERE platform_account_id = $1
		 ORDER BY metric_date DESC
		 LIMIT 1`,
		platformAccountID,
	)
	var p AccountMetricPoint
	err := row.Scan(
		&p.Date,
		&p.UpdatedAt,
		&p.Subscribers,
		&p.Views,
		&p.Videos,
		&p.WatchTimeMins,
		&p.Impressions,
		&p.CTR,
		&p.RevenueCents,
		&p.RPMCents,
		&p.CPMCents,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query latest metrics: %w", err)
	}
	return &p, nil
}

// LatestForAccounts returns the most recent metric point for each of
// the supplied account IDs. The returned map keys are the account IDs;
// accounts with no history are omitted from the map.
func (r *AccountMetricsRepository) LatestForAccounts(platformAccountIDs []int64) (map[int64]AccountMetricPoint, error) {
	if len(platformAccountIDs) == 0 {
		return map[int64]AccountMetricPoint{}, nil
	}

	// Build a positional IN clause ($1, $2, ..., $N).
	args := make([]interface{}, 0, len(platformAccountIDs))
	placeholders := make([]string, 0, len(platformAccountIDs))
	for i, id := range platformAccountIDs {
		args = append(args, id)
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
	}

	query := fmt.Sprintf(
		`SELECT DISTINCT ON (platform_account_id)
		        platform_account_id,
		        metric_date, updated_at, subscribers, views, videos,
		        watch_time_minutes, impressions, ctr, revenue_cents, rpm_cents, cpm_cents
		 FROM account_metric_history
		 WHERE platform_account_id IN (%s)
		 ORDER BY platform_account_id, metric_date DESC`,
		strings.Join(placeholders, ", "),
	)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query latest metrics for accounts: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]AccountMetricPoint)
	for rows.Next() {
		var accountID int64
		var p AccountMetricPoint
		if err := rows.Scan(
			&accountID,
			&p.Date,
			&p.UpdatedAt,
			&p.Subscribers,
			&p.Views,
			&p.Videos,
			&p.WatchTimeMins,
			&p.Impressions,
			&p.CTR,
			&p.RevenueCents,
			&p.RPMCents,
			&p.CPMCents,
		); err != nil {
			return nil, fmt.Errorf("scan latest metrics for accounts: %w", err)
		}
		out[accountID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest metrics for accounts: %w", err)
	}
	return out, nil
}
