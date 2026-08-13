// privacy_status mirrors YouTube's status.privacyStatus — exactly
// three legal values. Anything unexpected on a legacy/edge row is
// normalized to undefined (unknown) rather than inventing a value.
export type YouTubePrivacyStatus = "public" | "private" | "unlisted";

export function isYouTubePrivacyStatus(value: unknown): value is YouTubePrivacyStatus {
  return value === "public" || value === "private" || value === "unlisted";
}

// Availability is orthogonal to YouTubePrivacyStatus: privacy answers
// "what visibility does YouTube report", availability answers "can
// InstaEdit still see/manage this video". A video can be public
// (privacy) yet absent from the editable listing (deleted_or_missing);
// a private video can drift from the requested privacy
// (privacy_changed). The wire API does not emit this object yet: the
// hook derives it from the raw fields (phantom, youtube_sync_status,
// editor_status) and stamps it on every row.
export type VideoAvailabilityStatus =
  | "available"
  | "privacy_changed"
  | "deleted_or_missing"
  | "unavailable"
  | "unknown";

export interface VideoAvailability {
  status: VideoAvailabilityStatus;
  reason?: string;
}

export interface GroupYouTubeVideo {
  youtube_video_id: string;
  title: string;
  description?: string;
  draft_description?: string;
  thumbnail_url?: string;
  privacy_status?: YouTubePrivacyStatus;
  // YouTube snippet category, when the platform provides it. Optional
  // for now: the backend emits it once the metadata work lands.
  category_id?: string;
  category_title?: string;
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
  // Derived projection computed by the hook (see videoAvailability in
  // groupYouTubeVideosVisual.ts). Kept separate from privacy_status on
  // purpose: the two answer different questions.
  availability?: VideoAvailability;
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
