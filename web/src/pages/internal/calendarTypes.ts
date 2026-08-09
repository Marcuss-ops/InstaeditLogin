export type Post = {
  id: number;
  workspace_id: number;
  title?: string;
  caption?: string;
  scheduled_at?: string | null;
  status: string;
  created_at: string;
  source?: "post" | "upload";
  targets?: number[];
  source_type?: string;
  copyright_alerts?: YouTubeCopyrightAlert[];
};

export type YouTubeCopyrightAlert = {
  id: number;
  post_id: number;
  upload_job_id: number;
  post_target_id: number;
  platform_account_id: number;
  youtube_video_id: string;
  status: "claim" | "blocked" | "error";
  message: string;
  rejection_reason?: string;
  failure_reason?: string;
  processing_status?: string;
  licensed_content?: boolean;
  blocked_regions?: string[];
  allowed_regions?: string[];
  checked_at?: string;
};

export type Workspace = { id: number; name: string };
export type CalendarGroup = { id: number; name: string; workspace_id: number };

export type ContentMetric = {
  key: string;
  label: string;
  value: number;
  display_value: string;
};

export type ContentItem = {
  account_id?: number;
  account_name?: string;
  external_id: string;
  title?: string;
  description?: string;
  thumbnail_url?: string;
  public_url?: string;
  privacy?: string;
  status?: string;
  published_at?: string;
  duration?: string;
  metrics?: ContentMetric[];
  properties?: Record<string, unknown>;
};

export type ContentPage = { items: ContentItem[]; next_cursor?: string };

export type CalendarTab = "calendar" | "videos";

export type FetchState =
  | { kind: "loading" }
  | { kind: "ready"; posts: Post[]; workspaces: Workspace[]; groups: CalendarGroup[] }
  | { kind: "error"; message: string };

export type VideoState =
  | { kind: "idle" }
  | { kind: "loading" }
  | {
      kind: "ready";
      items: ContentItem[];
      nextCursor?: string;
      isLoadingMore?: boolean;
      loadMoreError?: string;
    }
  | { kind: "error"; message: string };
