/**
 * Typed contracts for the publishing API layer.
 *
 * Aligns with the Go handlers in:
 *   - pkg/api/media_handlers.go       (PresignMediaRequest/Response, MediaStore)
 *   - pkg/api/posts_handlers.go       (CreatePostRequest, Post, PostTarget)
 *   - pkg/api/youtube_*.go            (youTube target settings discriminator)
 *   - openapi.yaml → /posts + /post_targets/{id}
 *
 * Every enum-like value is a `string` union (NOT a TS enum) because
 * tsconfig.app.json has `erasableSyntaxOnly: true`, which forbids
 * runtime-emitting constructs. Server-side enum semantics are
 * unchanged.
 */

// ───────────────────────────────────────────────────────────────────
// Media assets (presign → PUT → complete)
// ───────────────────────────────────────────────────────────────────

/** Allowlist on the server (pkg/api/media_handlers.go:presign allow-list). */
export type MediaAssetContentType =
  | "video/mp4"
  | "video/quicktime"
  | "image/jpeg"
  | "image/png"
  | "image/webp";

/** Reference that `postsApi.createPost` accepts inside `content.media[]`. */
export interface MediaAssetRef {
  asset_id: string;
}

/** Body of POST /api/v1/media/presign. */
export interface PresignMediaRequest {
  filename: string;
  content_type: MediaAssetContentType;
  size_bytes: number;
  /**
   * Client-computed SHA-256 hex digest (Task 6/10 enforcement). The
   * server rejects /complete with empty SHA. Optional at presign
   * time, but recommended — committing the hash up-front lets the
   * server cross-check the S3 object hash later.
   */
  sha256?: string;
  /**
   * Optional RFC3339 timestamp. When set, the server derives the
   * asset's `expires_at` as `publish_at + VIDEO_RETENTION_BUFFER_DAYS`
   * Leave undefined for publish-now
   * flows; the server falls through to `now + PUBLISH_HORIZON_DAYS`.
   */
  publish_at?: string;
}

/** Response from POST /api/v1/media/presign. */
export interface PresignMediaResponse {
  asset_id: string;
  upload_url: string;
  upload_method: "PUT";
  upload_headers: Record<string, string>;
  expires_at: string;
  content_type: MediaAssetContentType;
  max_size_bytes: number;
}

/** Asset lifecycle status (matches internal/models/asset.go MediaAssetStatus). */
export type MediaAssetStatus = "pending" | "ready" | "failed" | "expired";

/** Canonical MediaAsset record returned by POST /complete and /media/{id}. */
export interface MediaAsset {
  id: string;
  upload_key?: string;
  content_type: string;
  size_bytes: number;
  sha256: string | null;
  status: MediaAssetStatus;
  expires_at: string;
  user_id?: number;
  created_at?: string;
  updated_at?: string;
}

// ───────────────────────────────────────────────────────────────────
// Posts (universal publish payload)
// ───────────────────────────────────────────────────────────────────

/**
 * Full async state machine for both Post and PostTarget. Kept as one
 * union because the post-level status set is a strict subset of the
 * target-level set (`draft`, `queued`, `publishing`, `published`,
 * `failed`, `partially_published`) augmented by worker-only values
 * (`retrying`, `waiting_provider`, `dlq`).
 *
 * See pkg/metrics/collector.go:knownTargetStatuses for the canonical
 * list the server enforces.
 */
export type PostStatus =
  | "draft"
  | "queued"
  | "publishing"
  | "published"
  | "failed"
  | "retrying"
  | "waiting_provider"
  | "partially_published"
  | "dlq";

export interface PostContent {
  title?: string;
  caption?: string;
  media?: MediaAssetRef[];
}

/**
 * YouTube-specific target settings. The wizard constructs this object
 * and the server's
 * `resolveFirstMediaURL` + per-platform settings engine translates
 * it into the right YouTube Data API update call.
 *
 * Other platforms will get their own settings discriminators later
 * (instagram, tiktok, threads, …). Adding them does NOT change the
 * `PostTargetInput.settings` shape (still `{ youtube?: … }` keyed by
 * platform name).
 */
export interface YouTubeTargetSettings {
  title: string;
  description?: string;
  privacy_status: "public" | "unlisted" | "private";
  made_for_kids?: boolean;
  tags?: string[];
}

/**
 * A single entry in `targets[]`. The field is explicitly typed as
 * `youtube`-only for now so tsc blocks accidental cross-platform
 * additions without an extension point.
 */
export interface PostTargetInput {
  platform_account_id: number;
  settings: {
    youtube: YouTubeTargetSettings;
  };
}

/** Body of POST /api/v1/posts. */
export interface CreatePostRequest {
  workspace_id: number;
  content: PostContent;
  /** Canonical publish cursor (RFC3339). Wins over `scheduled_at`. */
  publish_at?: string;
  /** Legacy alias for `publish_at`. Kept for one minor version. */
  scheduled_at?: string;
  /** Initial post-level status. Server forces `queued` when publish_at is set. */
  status?: Extract<PostStatus, "draft" | "queued">;
  targets: PostTargetInput[];
}

/** Per-target payload returned alongside POST /posts and GET /posts/{id}/targets. */
export interface PostTarget {
  id: number;
  post_id: number;
  platform_account_id: number;
  status: PostStatus;
  /** YouTube video_id once the worker publishes; null until then. */
  external_id?: string | null;
  public_url?: string | null;
  error_message?: string | null;
  published_at?: string | null;
  attempt_count?: number;
  next_attempt_at?: string | null;
  /** YouTube-specific: privacy command from the wizard plus the worker's snapshot. */
  privacy_status?: "public" | "unlisted" | "private" | null;
  made_for_kids?: boolean | null;
  /** Drift marker when the worker's actual_privacy differs from desired privacy. */
  youtube_sync_status?: "pending" | "confirmed" | "drift" | "failed" | null;
  /** Server-stamped actual privacy from the most recent worker reconcile. */
  actual_privacy?: "public" | "unlisted" | "private" | null;
}

/** Canonical PostResponse. */
export interface Post {
  id: number;
  workspace_id: number;
  title?: string;
  caption?: string;
  /** Server-resolved internal S3 URL. Live as soon as the first asset lands. */
  media_url?: string | null;
  status: PostStatus;
  publish_at?: string | null;
  scheduled_at?: string | null;
  created_at?: string;
  updated_at?: string;
  /** Populated when the response shape includes embedded targets (rare). */
  targets?: PostTarget[];
}

/**
 * Detailed single-target state, returned by
 * GET /api/v1/post-targets/{id}.
 */
export interface PostTargetDetail extends PostTarget {
  /** Lock bookkeeping for the worker's claim runtime (migrations 035). */
  locked_by?: string | null;
  locked_at?: string | null;
  lease_expires_at?: string | null;
  /** Free-form provider-side state (YouTube resumable uploads, IG carriage, …). */
  provider_state?: Record<string, unknown> | null;
  /** Optimistic-concurrency counter (post_targets.version, migration 018). */
  version?: number;
}
