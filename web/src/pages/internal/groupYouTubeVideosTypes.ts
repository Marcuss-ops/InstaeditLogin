export interface GroupYouTubeVideo {
  youtube_video_id: string;
  title: string;
  description?: string;
  draft_description?: string;
  thumbnail_url?: string;
  privacy_status?: string;
  processing_status?: string;
  platform_account_id: number;
  channel_name?: string;
  language?: string;
  editor_session_id?: string;
  velox_project_id?: string;
  editor_url?: string;
  editor_status?: string;
  desired_privacy?: string;
  publish_at?: string;
  actual_privacy?: string;
  youtube_sync_status?: string;
  phantom?: boolean;
}

export type VideoPreview = {
  video: GroupYouTubeVideo;
};

export interface GroupYouTubeVideosResponse {
  videos?: GroupYouTubeVideo[];
  warnings?: string[];
  has_more?: boolean;
  next_offset?: number;
}

export type LoadState =
  | { kind: "loading" }
  | {
      kind: "ready";
      videos: GroupYouTubeVideo[];
      warnings: string[];
      hasMore: boolean;
      nextOffset: number | null;
      isLoadingMore: boolean;
    }
  | { kind: "error"; message: string; upstream: boolean };

export const DEFAULT_PAGE_SIZE = 50;
export const RECENCY_OPTIONS = [7, 14, 28, 90] as const;
