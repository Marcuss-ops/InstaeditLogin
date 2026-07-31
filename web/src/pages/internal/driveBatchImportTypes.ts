export type Workspace = { id: number; name: string };
export type PlatformAccount = { id: number; platform: string; username: string };

export type LoadState =
  | { kind: "loading" }
  | { kind: "ready"; workspaces: Workspace[]; pages: PlatformAccount[] }
  | { kind: "error"; message: string };

export type BatchEntry = {
  index: number;
  drive_file_id: string;
  name: string;
  job_id: number;
  scheduled_at: string;
  relative_hours_from_now: number;
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

export type SuccessPayload = {
  folderId: string;
  scheduledCount: number;
  firstPublishAt: string;
  lastScheduledAt: string;
  entries: BatchEntry[];
  cursorClampedToNow: boolean;
};

export type SubmitState =
  | { kind: "idle" }
  | { kind: "submitting" }
  | { kind: "success"; payload: SuccessPayload; nextPageToken: string }
  | { kind: "guidance"; note: string }
  | { kind: "error"; message: string };

export type FormValues = {
  workspaceId: number | "";
  facebookAccountId: number | "";
  folderId: string;
  advanced: boolean;
  title: string;
  captionPrefix: string;
  minJitterMinutes: number;
  maxJitterMinutes: number;
};

export const FOLDER_ID_PATTERN = /^[A-Za-z0-9_-]{1,100}$/;
export const MIN_JITTER_MIN = 30;
export const MAX_JITTER_MIN = 7 * 24 * 60;
export const DEFAULT_MIN_JITTER_MIN = 180;
export const DEFAULT_MAX_JITTER_MIN = 270;
