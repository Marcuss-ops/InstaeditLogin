// Shared types and constants for the Drive folder import / uploads page.
// Mirrors pkg/api/uploads_batch.go → UploadsBatchByFolderResponse on the SPA side.

export type Workspace = {
  id: number;
  name: string;
};

export type PlatformAccount = {
  id: number;
  platform: string;
  username: string;
  created_at: string;
};

export type LoadState =
  | { kind: "loading" }
  | {
      kind: "ready";
      workspaces: Workspace[];
      pages: PlatformAccount[];
      drives: PlatformAccount[];
    }
  | { kind: "error"; message: string };

export type BatchResponse = {
  folder_id: string;
  scheduled_count: number;
  page_count: number;
  total_runtime_estimate_seconds?: number;
  first_publish_at: string;
  last_scheduled_at: string;
  entries?: Array<{
    index: number;
    drive_file_id: string;
    name: string;
    job_id: number;
    scheduled_at: string;
    relative_hours_from_now: number;
  }>;
  partial_failure?: boolean;
  failed_at_page?: number;
  failed_at_page_token?: string;
  note?: string;
  needs_google_drive_api_key?: boolean;
  needs_drive_account?: boolean;
  error?: string;
};

export type SuccessPayload = {
  folderId: string;
  scheduledCount: number;
  pageCount: number;
  firstPublishAt: string;
  lastScheduledAt: string;
  entries: NonNullable<BatchResponse["entries"]>;
};

export type PartialPayload = {
  folderId: string;
  scheduledCount: number;
  pageCount: number;
  entries: NonNullable<BatchResponse["entries"]>;
  failedAtPage: number;
  failedAtPageToken: string;
  note: string;
  firstPublishAt: string;
  lastScheduledAt: string;
};

export type SubmitState =
  | { kind: "idle" }
  | { kind: "submitting" }
  | { kind: "success"; payload: SuccessPayload }
  | { kind: "partial"; payload: PartialPayload }
  | { kind: "guidance"; note: string }
  | { kind: "error"; message: string };

export type FormValues = {
  workspaceId: number | "";
  facebookAccountId: number | "";
  driveAccountId: number | "";
  folderId: string;
  advanced: boolean;
  title: string;
  captionPrefix: string;
  minJitterSeconds: number;
  maxJitterSeconds: number;
};

// Folder IDs in Google Drive are URL-safe base64ish — the suffix of
// https://drive.google.com/drive/folders/<ID>. Server enforces
// `^[A-Za-z0-9_-]{1,100}$`; mirror it here for inline feedback.
export const FOLDER_ID_PATTERN = /^[A-Za-z0-9_-]{1,100}$/;

// Jitter: 60 s floor (server also enforces) — anything below collapses
// into "publish back-to-back" anti-pattern detection.
export const MIN_JITTER_SEC = 60;
export const MAX_JITTER_SEC = 7 * 24 * 60 * 60;

// Defaults mirror the CLI: 4 h ± 30 min (centre-anchored).
export const DEFAULT_MIN_JITTER_SEC = 4 * 60 * 60 - 30 * 60;
export const DEFAULT_MAX_JITTER_SEC = 4 * 60 * 60 + 30 * 60;
