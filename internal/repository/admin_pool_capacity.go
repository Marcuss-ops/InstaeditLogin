package repository

import (
	"context"
	"fmt"
	"sort"
)

// YouTubePoolUsageCount is one pool client's active-grant total across
// the whole fleet (the count Google's 100-refresh-token cap per
// (Google account, OAuth client) pair is measured against, summed over
// every Google subject). Legacy rows whose oauth_client_key is ”
// default to youtube_pool_a.
type YouTubePoolUsageCount struct {
	OAuthClientKey      string `json:"oauth_client_key"`
	ActiveRefreshTokens int64  `json:"active_refresh_tokens"`
}

// YouTubePoolChannel is one YouTube platform_account with its grant's
// pool client + status, for the per-manager drill-down. The pool
// client and grant status come from the shared oauth_connections row
// (subject-keyed grant), so every channel under one manager reports
// the same pair.
type YouTubePoolChannel struct {
	PlatformAccountID int64  `json:"platform_account_id"`
	PlatformUserID    string `json:"platform_user_id"`
	Username          string `json:"username"`
	Status            string `json:"status"`
	OAuthClientKey    string `json:"oauth_client_key"`
	GrantStatus       string `json:"grant_status"`
}

// YouTubePoolManager is one Google manager account (a distinct
// provider_subject_id) with the pool client that issued its grant,
// the grant status, the channel totals under it and the per-channel
// drill-down.
type YouTubePoolManager struct {
	ProviderSubjectID      string               `json:"provider_subject_id"`
	OAuthClientKey         string               `json:"oauth_client_key"`
	GrantStatus            string               `json:"grant_status"`
	ChannelsTotal          int64                `json:"channels_total"`
	ChannelsReauthRequired int64                `json:"channels_reauth_required"`
	Channels               []YouTubePoolChannel `json:"channels"`
}

// YouTubePoolCapacityReport is the read-only aggregate behind the
// admin pool-capacity dashboard: fleet-wide per-client usage + the
// per-Google-manager breakdown.
type YouTubePoolCapacityReport struct {
	PoolUsage []YouTubePoolUsageCount `json:"pool_usage"`
	Managers  []YouTubePoolManager    `json:"managers"`
}

// youtubePoolUsageSQL counts active refresh grants per pool client
// across EVERY Google subject. Same grant definition as the
// per-subject capacity query in oauth_capacity.go (status='active'
// connection owning a bearer token with a non-empty encrypted refresh
// token) — this is the fleet-wide version the admin dashboard shows.
//
// Unlike the per-manager drill-down below, this fleet-wide count ALSO
// includes legacy connections with an empty provider_subject_id
// (defaulted to youtube_pool_a): it is the ground truth Google's 100
// per-(account, client) cap is measured against, so its per-pool
// totals may legitimately exceed the sum of the subject-keyed
// drill-down channels.
const youtubePoolUsageSQL = `SELECT oc.oauth_client_key,
       COUNT(DISTINCT oc.id) AS active_refresh_grants
  FROM oauth_connections oc
  JOIN tokens t ON t.oauth_connection_id = oc.id
 WHERE oc.provider = 'youtube'
   AND oc.status = 'active'
   AND t.token_type = 'bearer'
   AND t.encrypted_refresh_token IS NOT NULL
   AND octet_length(t.encrypted_refresh_token) > 0
 GROUP BY oc.oauth_client_key`

// youtubePoolManagersSQL lists every Google manager (subject-keyed
// oauth_connections row) with the pool client that issued its grant,
// its grant status and the channel totals under it. LEFT JOIN keeps
// managers whose channels were detached visible at zero.
const youtubePoolManagersSQL = `SELECT oc.provider_subject_id,
       oc.oauth_client_key,
       oc.status,
       COUNT(pa.id) AS channels_total,
       COUNT(pa.id) FILTER (WHERE pa.status = 'reauth_required') AS channels_reauth
  FROM oauth_connections oc
  LEFT JOIN platform_accounts pa ON pa.oauth_connection_id = oc.id
 WHERE oc.provider = 'youtube'
   AND oc.provider_subject_id <> ''
 GROUP BY oc.provider_subject_id, oc.oauth_client_key, oc.status
 ORDER BY oc.provider_subject_id, oc.oauth_client_key, oc.status`

// youtubePoolChannelsSQL lists every YouTube channel attached to a
// subject-keyed grant, with the owning subject + the grant's pool
// client + status. The subject is selected explicitly so the Go-side
// grouping into managers does not depend on sort stability.
const youtubePoolChannelsSQL = `SELECT oc.provider_subject_id,
       pa.id,
       pa.platform_user_id,
       COALESCE(pa.username, ''),
       pa.status,
       oc.oauth_client_key,
       oc.status
  FROM platform_accounts pa
  JOIN oauth_connections oc ON oc.id = pa.oauth_connection_id
 WHERE oc.provider = 'youtube'
   AND oc.provider_subject_id <> ''
 ORDER BY oc.provider_subject_id, pa.platform_user_id`

// YouTubePoolCapacity assembles the fleet-wide pool-capacity report in
// three indexed queries (per-client usage, per-manager aggregates,
// per-channel drill-down). The handler layer merges the registry's
// recommended capacities + health bands on top of the counts; this
// layer only ever touches subject IDs, client keys and statuses —
// never credential material.
func (r *AdminRepository) YouTubePoolCapacity(ctx context.Context) (YouTubePoolCapacityReport, error) {
	var report YouTubePoolCapacityReport

	// (1) Fleet-wide per-client active-grant counts.
	rows, err := r.db.QueryContext(ctx, youtubePoolUsageSQL)
	if err != nil {
		return report, fmt.Errorf("admin: pool usage query: %w", err)
	}
	for rows.Next() {
		var row YouTubePoolUsageCount
		if err := rows.Scan(&row.OAuthClientKey, &row.ActiveRefreshTokens); err != nil {
			rows.Close()
			return report, fmt.Errorf("admin: scan pool usage: %w", err)
		}
		if row.OAuthClientKey == "" {
			// Legacy single-client rows default to youtube_pool_a
			// (mirrors the pool-metrics collector + oauth_capacity.go).
			row.OAuthClientKey = "youtube_pool_a"
		}
		report.PoolUsage = append(report.PoolUsage, row)
	}
	if err := rows.Close(); err != nil {
		return report, fmt.Errorf("admin: close pool usage rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("admin: iterate pool usage: %w", err)
	}

	// (2) Per-manager aggregates.
	mgrRows, err := r.db.QueryContext(ctx, youtubePoolManagersSQL)
	if err != nil {
		return report, fmt.Errorf("admin: pool managers query: %w", err)
	}
	managersBySubject := make(map[string]*YouTubePoolManager)
	for mgrRows.Next() {
		var m YouTubePoolManager
		if err := mgrRows.Scan(
			&m.ProviderSubjectID,
			&m.OAuthClientKey,
			&m.GrantStatus,
			&m.ChannelsTotal,
			&m.ChannelsReauthRequired,
		); err != nil {
			mgrRows.Close()
			return report, fmt.Errorf("admin: scan pool manager: %w", err)
		}
		if m.OAuthClientKey == "" {
			m.OAuthClientKey = "youtube_pool_a"
		}
		// A Google subject can hold grants on more than one pool client
		// (the 50/50 two-pool fleet shape), so the same subject may span
		// several rows. Merge them into ONE manager entry: sum the
		// channel totals and keep the first (deterministic: the SQL
		// orders by oauth_client_key, status) pool client + grant status
		// for display. The channel drill-down groups every channel of
		// the subject under this single manager, each reporting its own
		// pool.
		if existing, ok := managersBySubject[m.ProviderSubjectID]; ok {
			existing.ChannelsTotal += m.ChannelsTotal
			existing.ChannelsReauthRequired += m.ChannelsReauthRequired
			continue
		}
		managersBySubject[m.ProviderSubjectID] = &m
	}
	if err := mgrRows.Close(); err != nil {
		return report, fmt.Errorf("admin: close pool manager rows: %w", err)
	}
	if err := mgrRows.Err(); err != nil {
		return report, fmt.Errorf("admin: iterate pool managers: %w", err)
	}

	// (3) Per-channel drill-down, grouped under its manager.
	chRows, err := r.db.QueryContext(ctx, youtubePoolChannelsSQL)
	if err != nil {
		return report, fmt.Errorf("admin: pool channels query: %w", err)
	}
	for chRows.Next() {
		var (
			ch         YouTubePoolChannel
			subject    string
			clientKey  string
			grantState string
		)
		if err := chRows.Scan(
			&subject,
			&ch.PlatformAccountID,
			&ch.PlatformUserID,
			&ch.Username,
			&ch.Status,
			&clientKey,
			&grantState,
		); err != nil {
			chRows.Close()
			return report, fmt.Errorf("admin: scan pool channel: %w", err)
		}
		if clientKey == "" {
			clientKey = "youtube_pool_a"
		}
		ch.OAuthClientKey = clientKey
		ch.GrantStatus = grantState
		if m, ok := managersBySubject[subject]; ok {
			m.Channels = append(m.Channels, ch)
		}
	}
	if err := chRows.Close(); err != nil {
		return report, fmt.Errorf("admin: close pool channel rows: %w", err)
	}
	if err := chRows.Err(); err != nil {
		return report, fmt.Errorf("admin: iterate pool channels: %w", err)
	}

	// Emit managers in a stable (subject-sorted) order.
	report.Managers = make([]YouTubePoolManager, 0, len(managersBySubject))
	for _, subject := range sortedSubjects(managersBySubject) {
		report.Managers = append(report.Managers, *managersBySubject[subject])
	}
	return report, nil
}

// sortedSubjects returns the manager-map keys in ascending lexical
// order so the managers slice is deterministic across calls.
func sortedSubjects(m map[string]*YouTubePoolManager) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
