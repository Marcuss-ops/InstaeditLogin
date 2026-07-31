export type Post = {
  id: number;
  workspace_id: number;
  title?: string;
  caption?: string;
  scheduled_at?: string | null;
  status: string;
  created_at: string;
};

export type Workspace = { id: number; name: string };

export type ContentMetric = {
  key: string;
  label: string;
  value: number;
  display_value: string;
};

export type ContentItem = {
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
  | { kind: "ready"; posts: Post[]; workspaces: Workspace[] }
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
