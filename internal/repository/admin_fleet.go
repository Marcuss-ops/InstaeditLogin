package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// AdminYouTubeQuota is the /admin/health "quota used / remaining"
// estimate (D2.b — derived approximation, not the live YouTube
// quota API). CostPerUploadUnits defaults to 1 (1 unit per videos.insert — YouTube 2026 bucket model).
// (YouTube Data API v3 — pre-2026-06-01 was 1600 units per call.)
// An operator may lower it if their traffic is dominantly cheaper
// endpoints. DailyBudgetUnits defaults to 10000.
type AdminYouTubeQuota struct {
	WindowHours        int
	EstimatedUnits     int64
	SuccessCount       int
	QuotaFailures      int
	DailyBudgetUnits   int64
	RemainingEstimate  int64
	CostPerUploadUnits int64
}

// YouTubeQuotaApproximation (D2.b) reads the existing
// publish_success_total / publish_error_total{outcome=quota}
// counters indirectly via the post_targets audit trail (success
// row per publish; failure rows have error_code='quota_exceeded').
// Single SQL query; no Prometheus scrape needed.
//
// NOTE: this is a proxy for actual YouTube quota usage. Real
// per-endpoint costs vary (videos.insert = 1 unit under 2026 bucket model, channels.list ~1,
// search.list ~100). Operators can override CostPerUploadUnits via
// cfg.YoutubeQuotaCostPerUpload if their traffic profile diverges
// significantly from the assumed all-uploads shape.
func (r *AdminRepository) YouTubeQuotaApproximation(ctx context.Context, window time.Duration, dailyBudgetUnits, costPerUploadUnits int64) (AdminYouTubeQuota, error) {
	if window <= 0 {
		window = 24 * time.Hour
	}
	if dailyBudgetUnits <= 0 {
		dailyBudgetUnits = 10000
	}
	if costPerUploadUnits <= 0 {
		// 2026 bucket model: 1 videos.insert = 1 unit. (Was 1600 pre-2026-06-01).
		costPerUploadUnits = 1
	}

	var q AdminYouTubeQuota
	q.WindowHours = int(window / time.Hour)
	q.DailyBudgetUnits = dailyBudgetUnits
	q.CostPerUploadUnits = costPerUploadUnits

	err := r.db.QueryRowContext(ctx,
		`SELECT
		   COUNT(*) FILTER (WHERE status = 'published')  AS success_count,
		   COUNT(*) FILTER (WHERE last_error_code = 'quota_exceeded' OR last_error_code LIKE 'quota%') AS quota_failures
		 FROM post_targets
		 WHERE updated_at > NOW() - $1::interval
		   AND platform_account_id IN (SELECT id FROM platform_accounts WHERE platform = 'youtube')`,
		fmt.Sprintf("%d hours", q.WindowHours),
	).Scan(&q.SuccessCount, &q.QuotaFailures)
	if err != nil {
		return q, fmt.Errorf("admin: youtube quota query: %w", err)
	}

	totalBillable := int64(q.SuccessCount) + int64(q.QuotaFailures)
	q.EstimatedUnits = totalBillable * costPerUploadUnits
	if q.EstimatedUnits > dailyBudgetUnits {
		q.RemainingEstimate = 0
	} else {
		q.RemainingEstimate = dailyBudgetUnits - q.EstimatedUnits
	}
	return q, nil
}

// FleetReadinessCounts is the 12-field Definition-of-Done breakdown
// the docs/OAUTH-PRODUCTION.md Step 10 readiness checklist calls out
// for the 200-channel rollout. The JSON tags exactly match the field
// names the operator dashboard reads on every render; do NOT
// rename without a coordinated dashboard re-skin.
//
// Sourcing:
//
//   - the 6 status counters (Active / Pending / Reauth / Revoked /
//     Error / Total) come from COUNT(*) FILTER (WHERE status = ...)
//     on platform_accounts WHERE platform = 'youtube'.
//
//   - the 6 DoD-OK counters (refresh_test_ok / scope_youtube_upload_ok /
//     scope_youtube_readonly_ok / channel_binding_ok /
//     private_canary_ok / canary_channel_match_ok) derive from
//     platform_accounts state columns + the metadata JSONB:
//
//     refresh_test_ok         -> last_refresh_at IS NOT NULL AND status='active'
//     scope_youtube_upload_ok -> metadata->>'granted_scopes' LIKE '%youtube.upload%'
//     scope_youtube_readonly_ok -> metadata->>'granted_scopes' LIKE '%youtube.readonly%'
//     channel_binding_ok      -> last_validated_at IS NOT NULL
//     private_canary_ok       -> metadata->>'canary_result' = 'ok'
//     canary_channel_match_ok -> metadata->>'canary_channel_match' = 'true'
//
// All 12 counts come from a SINGLE round-trip: one platform_accounts
// SELECT with 12 FILTER clauses. Mirrors the existing ChannelCounts
// pattern at the top of this file.
type FleetReadinessCounts struct {
	Total                  int `json:"youtube_channels_total"`
	Active                 int `json:"active"`
	PendingAuthorization   int `json:"pending_authorization"`
	ReauthRequired         int `json:"reauth_required"`
	Revoked                int `json:"revoked"`
	Error                  int `json:"error"`
	RefreshTestOK          int `json:"refresh_test_ok"`
	ScopeYoutubeUploadOK   int `json:"scope_youtube_upload_ok"`
	ScopeYoutubeReadonlyOK int `json:"scope_youtube_readonly_ok"`
	ChannelBindingOK       int `json:"channel_binding_ok"`
	PrivateCanaryOK        int `json:"private_canary_ok"`
	CanaryChannelMatchOK   int `json:"canary_channel_match_ok"`
}

// FleetReadinessSnapshotResponse is the JSON envelope for
// GET /admin/youtube/fleet_readiness. The DoD counters live under
// fleet_readiness (matching the field names in docs/OAUTH-PRODUCTION.md
// Step 10 verbatim) and the snapshot_id lets the operator correlate
// this single response with the per-channel rows persisted to
// fleet_readiness_snapshot_channels.
type FleetReadinessSnapshotResponse struct {
	FleetReadiness FleetReadinessCounts `json:"fleet_readiness"`
	SnapshotID     string               `json:"snapshot_id"`
	TakenAt        time.Time            `json:"taken_at"`
}

// CountFleetReadiness is the read-only aggregate query behind the
// platform_accounts side of the Definition-of-Done readiness
// readout (the 12-count breakdown per docs/OAUTH-PRODUCTION.md Step
// 10). It returns the current counts WITHOUT writing a snapshot row
// -- ideal for ad-hoc dashboards / monitoring scrapes that want
// the latest numbers but should not consume a snapshot_id.
//
// NOT USED inside the tx in CreateFleetReadinessSnapshot: the
// snapshot legitimately needs tx-pinned REPEATABLE READ so the
// aggregate counts and the per-channel INSERT...SELECT match.
// This function is the canonical single-roundtrip FILTER aggregate
// for any caller that does not need snapshot-time consistency.
//
// Single round-trip: 12 COUNT(*) FILTER clauses on platform_accounts
// WHERE platform='youtube'. The 6 status counters come from the
// status column directly; the 6 DoD-OK counters derive from the
// state columns (last_refresh_at, last_validated_at) and the
// metadata JSONB keys (granted_scopes, canary_result,
// canary_channel_match). JSON field names are byte-equal to
// docs/OAUTH-PRODUCTION.md Step 10.
func (r *AdminRepository) CountFleetReadiness(ctx context.Context) (FleetReadinessCounts, error) {
	var c FleetReadinessCounts
	err := r.db.QueryRowContext(ctx, `
		SELECT
		    COUNT(*)                                              AS total,
		    COUNT(*) FILTER (WHERE status = 'active')             AS active,
		    COUNT(*) FILTER (WHERE status = 'pending_authorization') AS pending_authorization,
		    COUNT(*) FILTER (WHERE status = 'reauth_required')    AS reauth_required,
		    COUNT(*) FILTER (WHERE status = 'revoked')             AS revoked,
		    COUNT(*) FILTER (WHERE status = 'error')               AS error,
		    COUNT(*) FILTER (WHERE last_refresh_at  IS NOT NULL
		                       AND status = 'active')               AS refresh_test_ok,
		    COUNT(*) FILTER (WHERE COALESCE(metadata->>'granted_scopes', '') LIKE '%youtube.upload%')    AS scope_youtube_upload_ok,
		    COUNT(*) FILTER (WHERE COALESCE(metadata->>'granted_scopes', '') LIKE '%youtube.readonly%') AS scope_youtube_readonly_ok,
		    COUNT(*) FILTER (WHERE last_validated_at IS NOT NULL)  AS channel_binding_ok,
		    COUNT(*) FILTER (WHERE COALESCE(metadata->>'canary_result', '') = 'ok') AS private_canary_ok,
		    COUNT(*) FILTER (WHERE COALESCE(metadata->>'canary_channel_match', 'false') = 'true')  AS canary_channel_match_ok
		FROM platform_accounts
		WHERE platform = 'youtube'`,
	).Scan(
		&c.Total,
		&c.Active,
		&c.PendingAuthorization,
		&c.ReauthRequired,
		&c.Revoked,
		&c.Error,
		&c.RefreshTestOK,
		&c.ScopeYoutubeUploadOK,
		&c.ScopeYoutubeReadonlyOK,
		&c.ChannelBindingOK,
		&c.PrivateCanaryOK,
		&c.CanaryChannelMatchOK,
	)
	if err != nil {
		return c, fmt.Errorf("admin: count fleet readiness: %w", err)
	}
	return c, nil
}

// CreateFleetReadinessSnapshot is the single round-trip service
// behind GET /admin/youtube/fleet_readiness. In ONE transaction
// (RepeatableRead isolation) it:
//
//  1. Aggregates the 12 DoD counters via a COUNT(*) FILTER query on
//     platform_accounts (one roundtrip; mirrors the ChannelCounts
//     pattern at the top of this file).
//  2. Inserts a fleet_readiness_snapshots parent row carrying
//     adminUserID + the JSON-marshalled counts as summary_json.
//  3. INSERT…SELECTs every YouTube platform_account row into
//     fleet_readiness_snapshot_channels with the 12 per-channel
//     fields docs/OAUTH-PRODUCTION.md Step 10 names verbatim.
//
// Transaction guarantees: callers see consistent aggregate + row
// data within a single snapshot. A concurrent platform_accounts
// UPDATE during the snapshot either:
//   - happens BEFORE the snapshot started -> invisible (RepeatableRead
//     snapshots the row state at tx-start time);
//   - happens AFTER the snapshot committed -> visible in the next
//     snapshot, NOT this one.
//
// Without the tx + RepeatableRead, the 3 queries (aggregate / parent
// insert / per-channel insert...select) would race against any
// concurrent bind / disconnect / status-change, and the JSON
// counters would not be guaranteed to match the per-channel rows.
// REPEATABLE READ in Postgres is the dedicated snapshot-isolation
// mode (vs SERIALIZABLE which adds Serializable Snapshot Isolation
// on top + aborts on detected conflicts); REPEATABLE READ here is
// the minimum needed for the audit-consistency contract.
//
// The granted_scopes column is sourced from
// platform_accounts.metadata->'granted_scopes' (a JSONB array
// written by the OAuth callback chain post-bind). Empty array when
// the metadata key is missing. The TEXT[] type matches
// oauth_connections.scopes (migration 043) so a future follow-up
// can JOIN instead without a column-type migration.
//
// Returns the snapshot response (counts + snapshot UUID + taken_at)
// for the operator dashboard to render.
func (r *AdminRepository) CreateFleetReadinessSnapshot(ctx context.Context, adminUserID int64) (FleetReadinessSnapshotResponse, error) {
	var resp FleetReadinessSnapshotResponse

	// Open a single tx at REPEATABLE READ. The RepeatableRead
	// isolation level guarantees the per-statement snapshots are
	// pinned to tx-start so the aggregate + per-channel reads see
	// the same row state. Rollback on any non-commit path (the
	// closed flag guards against double-Rollback if Commit fires
	// first).
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return resp, fmt.Errorf("admin: begin fleet readiness tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Single round-trip on the aggregates. Mirrors the ChannelCounts
	// shape (this package's other COUNT(*) FILTER pattern).
	err = tx.QueryRowContext(ctx, `
		SELECT
		    COUNT(*)                                              AS total,
		    COUNT(*) FILTER (WHERE status = 'active')             AS active,
		    COUNT(*) FILTER (WHERE status = 'pending_authorization') AS pending_authorization,
		    COUNT(*) FILTER (WHERE status = 'reauth_required')    AS reauth_required,
		    COUNT(*) FILTER (WHERE status = 'revoked')             AS revoked,
		    COUNT(*) FILTER (WHERE status = 'error')               AS error,
		    COUNT(*) FILTER (WHERE last_refresh_at  IS NOT NULL
		                       AND status = 'active')               AS refresh_test_ok,
		    COUNT(*) FILTER (WHERE COALESCE(metadata->>'granted_scopes', '') LIKE '%youtube.upload%')    AS scope_youtube_upload_ok,
		    COUNT(*) FILTER (WHERE COALESCE(metadata->>'granted_scopes', '') LIKE '%youtube.readonly%') AS scope_youtube_readonly_ok,
		    COUNT(*) FILTER (WHERE last_validated_at IS NOT NULL)  AS channel_binding_ok,
		    COUNT(*) FILTER (WHERE COALESCE(metadata->>'canary_result', '') = 'ok') AS private_canary_ok,
		    COUNT(*) FILTER (WHERE COALESCE(metadata->>'canary_channel_match', 'false') = 'true')  AS canary_channel_match_ok
		FROM platform_accounts
		WHERE platform = 'youtube'`,
	).Scan(
		&resp.FleetReadiness.Total,
		&resp.FleetReadiness.Active,
		&resp.FleetReadiness.PendingAuthorization,
		&resp.FleetReadiness.ReauthRequired,
		&resp.FleetReadiness.Revoked,
		&resp.FleetReadiness.Error,
		&resp.FleetReadiness.RefreshTestOK,
		&resp.FleetReadiness.ScopeYoutubeUploadOK,
		&resp.FleetReadiness.ScopeYoutubeReadonlyOK,
		&resp.FleetReadiness.ChannelBindingOK,
		&resp.FleetReadiness.PrivateCanaryOK,
		&resp.FleetReadiness.CanaryChannelMatchOK,
	)
	if err != nil {
		return resp, fmt.Errorf("admin: fleet readiness aggregate counts: %w", err)
	}

	// Marshal the (now-populated) counts for the parent row's
	// summary_json mirror field.
	summaryJSON, _ := json.Marshal(resp.FleetReadiness)

	// Insert parent row. RETURNING gives us the generated UUID
	// (gen_random_uuid()) AND taken_at (server time, NOT the
	// wall-clock before the tx started); both round-trip in the
	// same network message.
	var snapshotID string
	var takenAt time.Time
	err = tx.QueryRowContext(ctx, `
		INSERT INTO fleet_readiness_snapshots (operator_user_id, summary_json)
		VALUES ($1, $2)
		RETURNING id, taken_at`,
		adminUserID, summaryJSON,
	).Scan(&snapshotID, &takenAt)
	if err != nil {
		return resp, fmt.Errorf("admin: insert fleet readiness parent snapshot: %w", err)
	}

	// Per-channel dump. INSERT…SELECT keeps the round-trip count
	// to 1 for the entire snapshot (aggregate + parent + child =
	// 3 query-messages inside ONE tx). For a 200-channel fleet this
	// is 200 rows; bounded by the platform_accounts WHERE
	// platform = 'youtube' predicate (we never include Drive/IG/etc.).
	_, err = tx.ExecContext(ctx, `
		INSERT INTO fleet_readiness_snapshot_channels (
		    snapshot_id, platform_account_id, channel_id, channel_name,
		    manager_email, oauth_connection_id, granted_scopes,
		    last_refresh_at, last_binding_check_at,
		    canary_video_id, canary_result, last_error_code
		)
		SELECT
		    $1::uuid,
		    pa.id,
		    COALESCE(pa.platform_user_id, ''),
		    COALESCE(pa.username, ''),
		    COALESCE(pa.metadata->>'manager_email_hint', ''),
		    pa.oauth_connection_id,
		    COALESCE(
		        ARRAY(
		            SELECT jsonb_array_elements_text(
		                COALESCE(pa.metadata->'granted_scopes', '[]'::jsonb)
		            )
		        ),
		        ARRAY[]::TEXT[]
		    ),
		    pa.last_refresh_at,
		    pa.last_validated_at,
		    COALESCE(pa.metadata->>'canary_video_id', ''),
		    COALESCE(pa.metadata->>'canary_result', ''),
		    COALESCE(pa.last_error_code, '')
		FROM platform_accounts pa
		WHERE pa.platform = 'youtube'`,
		snapshotID,
	)
	if err != nil {
		return resp, fmt.Errorf("admin: insert fleet readiness per-channel rows: %w", err)
	}

	// Final commit. The defer'd Rollback sees `committed=true` and
	// skips. If we never reach this line (a panic in BETWEEN), the
	// defer fires Rollback and Postgres releases the row-lock set
	// by the tx.
	if err := tx.Commit(); err != nil {
		return resp, fmt.Errorf("admin: commit fleet readiness tx: %w", err)
	}
	committed = true

	resp.SnapshotID = snapshotID
	resp.TakenAt = takenAt
	return resp, nil
}
