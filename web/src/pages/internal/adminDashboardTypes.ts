// Admin Dashboard API response types (extracted from AdminDashboard.tsx).

export type FleetReadiness = {
  youtube_channels_total: number;
  active: number;
  pending_authorization: number;
  reauth_required: number;
  revoked: number;
  error: number;
  refresh_test_ok: number;
  scope_youtube_upload_ok: number;
  scope_youtube_readonly_ok: number;
  channel_binding_ok: number;
  private_canary_ok: number;
  canary_channel_match_ok: number;
};

export type FleetReadinessData = {
  fleet_readiness: FleetReadiness;
  snapshot_id: string;
  taken_at: string;
};

export type AdminErrorRate = {
  platform_account_id: number;
  platform: string;
  username: string;
  window_label: string;
  total_count: number;
  failed_count: number;
  error_rate: number;
};

export type YouTubeQuota = {
  window_hours: number;
  estimated_units: number;
  success_count: number;
  quota_failures: number;
  daily_budget_units: number;
  remaining_estimate: number;
  cost_per_upload_units: number;
};

export type QueueCounts = {
  pending_count: number;
  leased_count: number;
  processing_count: number;
  ingest_completed: number;
  publish_completed: number;
  failed_count: number;
  dead_letter_count: number;
  cancelled_count: number;
  retry_wait_count: number;
  total: number;
  stuck_count: number;
};

export type AdminHealth = {
  youtube_quota_estimate: YouTubeQuota;
  error_rate_1h: AdminErrorRate[];
  error_rate_24h: AdminErrorRate[];
  queue_counts: QueueCounts;
  generated_at_unix: number;
};

export type YouTubeOAuthPoolState = {
  oauth_client_key: string;
  active_refresh_tokens: number;
  recommended_capacity: number;
  provider_limit: number;
  remaining_capacity: number;
  health: string;
};

export type YouTubeOAuthPoolChannel = {
  platform_account_id: number;
  platform_user_id: string;
  username: string;
  status: string;
  oauth_client_key: string;
  grant_status: string;
};

export type YouTubeOAuthPoolManager = {
  provider_subject_id: string;
  oauth_client_key: string;
  grant_status: string;
  channels_total: number;
  channels_reauth_required: number;
  channels: YouTubeOAuthPoolChannel[];
};

export type YouTubeOAuthPoolCapacityResponse = {
  pools: YouTubeOAuthPoolState[];
  managers: YouTubeOAuthPoolManager[];
  totals: {
    managers_total: number;
    channels_total: number;
    channels_reauth_required: number;
  };
  generated_at_unix: number;
};

export type FetchState<T> =
  | { kind: "loading" }
  | { kind: "ready"; data: T }
  | { kind: "error"; message: string };
