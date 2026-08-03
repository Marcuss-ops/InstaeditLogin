/**
 * Types for the Live streaming control center
 * (GET /api/v1/livestreams?workspace_id=N).
 *
 * Mirrors the backend `livestreamResponse` DTO
 * (pkg/api/livestreams_types.go). The YouTube resource references
 * (broadcast/stream ids) and the stream name/key are deliberately not
 * exposed by the API, so they never appear here either.
 */
export type LivestreamRow = {
  id: string;
  workspace_id: number;
  platform_account_id: number;
  /** Display name of the bound YouTube channel; empty when unknown. */
  channel_name?: string;
  title: string;
  description: string;
  privacy_status: string; // private | unlisted | public
  playback_mode: string; // loop_continuous | play_once
  schedule_type: string; // manual | now | scheduled | recurring
  scheduled_start_at?: string | null;
  desired_state: string;
  actual_state: string;
  resolution: string; // 720p30 | 1080p30
  frame_rate: number;
  auto_restart: boolean;
  created_at: string;
  updated_at: string;
};

export type LivestreamsResponse = {
  items: LivestreamRow[];
};

export type LivestreamTab =
  | "all"
  | "live"
  | "scheduled"
  | "drafts"
  | "ended"
  | "errors";

export type LivestreamSummary = {
  live: number;
  scheduled: number;
  reconnecting: number;
  errors: number;
};
