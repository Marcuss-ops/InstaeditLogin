export type Workspace = {
  id: number;
  name: string;
};

export type PlatformAccount = {
  id: number;
  platform: string;
  platform_user_id: string;
  username: string;
  status: string;
  created_at: string;
};

export type LoadState =
  | { kind: "loading" }
  | {
      kind: "ready";
      workspaces: Workspace[];
      youtubeChannels: PlatformAccount[];
      driveAccounts: PlatformAccount[];
    }
  | { kind: "error"; message: string };

export type AsyncBatchResponse = {
  batch_id: string;
  status: string;
  schedule_clamped: boolean;
  schedule_clamp_reason?: string;
};

export type BatchStatusResponse = {
  id: string;
  user_id: number;
  workspace_id: number;
  source_provider: string;
  source_drive_account_id: number | null;
  source_folder_id: string;
  target_account_ids: number[];
  target_group_name: string | null;
  publish_schedule_start_at: string;
  publish_schedule_min_gap: number;
  publish_schedule_max_gap: number;
  default_privacy_level: string;
  status: string;
  file_count: number | null;
  processed_count: number;
  created_at: string;
  updated_at: string;
  completed_at: string | null;
};

export type EditorSession = {
  id: string;
  youtube_video_id: string;
  velox_project_id: string;
  editor_url: string;
  status: string;
  thumbnail_media_id: string | null;
  desired_privacy: string;
  publish_at: string | null;
};

export type SubmitState =
  | { kind: "idle" }
  | { kind: "submitting" }
  | { kind: "queued"; batchId: string }
  | { kind: "polling"; batchId: string }
  | { kind: "success"; payload: { scheduledCount: number } }
  | { kind: "partial"; payload: { scheduledCount: number } }
  | { kind: "guidance" }
  | { kind: "error"; message: string };

// BatchEntry / BatchResponse re-homed from
// src/pages/internal/DriveBatchImportDialog.tsx (where they remain as a
// duplicate local type for now) so that UploadsTable.tsx can import the
// shared `NonNullable<BatchResponse["entries"]>` shape and `preview.map((e) => ...)`
// narrows `e` to BatchEntry automatically (clearing the TS7006 implicit-any
// error on the map callback). Consolidating DriveBatchImportDialog to
// import these from here is a separate cleanup.
export type BatchEntry = {
  job_id: string;
  name: string;
  scheduled_at?: string | null;
  relative_hours_from_now: number;
  video_id?: string;
  status?: string;
};

export type BatchResponse = {
  folder_id: string;
  scheduled_count: number;
  first_publish_at: string;
  last_scheduled_at: string;
  entries: BatchEntry[];
  next_page_token: string;
  note: string;
  cursor_clamped_to_now: boolean;
  needs_google_drive_api_key: boolean;
  needs_drive_account: boolean;
  error: string;
};

export type FormValues = {
  workspaceId: number | "";
  youtubeAccountId: number | "";
  driveAccountId: number | "";
  folderId: string;
  privacyLevel: "private" | "unlisted" | "public";
  startAt: string;
  advanced: boolean;
  title: string;
  descriptionPrefix: string;
  minJitterSeconds: number;
  maxJitterSeconds: number;
};

export const FOLDER_ID_PATTERN = /^[A-Za-z0-9_-]{1,100}$/;

export const MIN_JITTER_SEC = 60;
export const MAX_JITTER_SEC = 7 * 24 * 60 * 60;

export const DEFAULT_MIN_JITTER_SEC = 4 * 60 * 60 - 30 * 60;
export const DEFAULT_MAX_JITTER_SEC = 4 * 60 * 60 + 30 * 60;
