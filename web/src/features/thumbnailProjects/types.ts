/**
 * Shared wire types for the autonomous Thumbnail Project domain.
 *
 * These mirror the Go JSON contract of `/api/v1/thumbnail-projects` and
 * `/api/v1/thumbnail-exports` (see pkg/api/thumbnail_projects_handlers.go
 * and pkg/api/thumbnail_exports_handlers.go). A project deliberately
 * carries no YouTube channel/video/account: destinations enter only via
 * `ThumbnailProjectAssignment` after an export exists.
 */

export type ThumbnailProjectStatus = "draft" | "ready" | "archived" | "deleted";

/** A workspace-scoped editable graphic project (ThumbnailProject). */
export interface ThumbnailProject {
  id: string;
  workspace_id: number;
  created_by: number;
  name: string;
  description: string;
  canvas_width: number;
  canvas_height: number;
  status: ThumbnailProjectStatus;
  current_revision_id?: string | null;
  preview_media_id?: string | null;
  latest_export_id?: string | null;
  /** Optimistic-concurrency token; every saved snapshot increments it. */
  version: number;
  created_at: string;
  updated_at: string;
}

/** Editor canvas snapshot: canvas config + ordered drawable objects. */
export interface ThumbnailCanvasSnapshot {
  canvas?: {
    width?: number;
    height?: number;
    background?: string;
  };
  objects?: ThumbnailSnapshotObject[];
}

/**
 * One drawable object (snapshot schema_version 1). The renderer accepts
 * `text`, `rect` and `image`; unknown future types are ignored. Extra
 * editor fields are tolerated via the index signature.
 */
export interface ThumbnailSnapshotObject {
  id: string;
  type: string;
  x?: number;
  y?: number;
  width?: number;
  height?: number;
  scale_x?: number;
  scale_y?: number;
  /** Degrees, clockwise. */
  rotation?: number;
  visible?: boolean;
  fill?: string;
  radius?: number;
  text?: string;
  font_family?: string;
  font_size?: number;
  font_weight?: number;
  text_align?: string;
  media_id?: string;
  [key: string]: unknown;
}

/** Immutable canvas snapshot (ThumbnailProjectRevision). */
export interface ThumbnailProjectRevision {
  id: string;
  project_id: string;
  revision_number: number;
  schema_version: number;
  snapshot_json: ThumbnailCanvasSnapshot;
  /** base64 of the 32-byte snapshot SHA-256 (Go []byte JSON encoding). */
  snapshot_sha256: string;
  renderer_version: string;
  created_by: number;
  created_at: string;
}

/** Response of PUT .../snapshot and POST .../restore/{revision_id}. */
export interface ThumbnailProjectSnapshotResult {
  project_id: string;
  revision_id: string;
  revision_number: number;
  version: number;
  saved_at: string;
  /** hex-encoded SHA-256 of the canonicalized snapshot. */
  snapshot_sha256: string;
  /** true when the exact snapshot already existed (no new revision). */
  deduplicated?: boolean;
}

export type ThumbnailExportStatus = "rendering" | "ready" | "failed";
export type ThumbnailExportContentType = "image/png" | "image/jpeg";

/** A rendered, verifiable file persisted in the Media Library. */
export interface ThumbnailExport {
  id: string;
  project_id: string;
  revision_id: string;
  media_id: string;
  content_type: ThumbnailExportContentType;
  width: number;
  height: number;
  file_size: number;
  /** base64 of the 32-byte file SHA-256 (Go []byte JSON encoding). */
  sha256: string;
  renderer_version: string;
  status: ThumbnailExportStatus;
  last_error: string;
  created_at: string;
}

export type ThumbnailAssignmentStatus =
  | "draft"
  | "pending"
  | "applied"
  | "failed"
  | "cancelled";

/** Optional YouTube destination for an export. */
export interface ThumbnailProjectAssignment {
  id: string;
  workspace_id: number;
  project_id: string;
  export_id: string;
  platform_account_id: number;
  platform: "youtube";
  youtube_video_id: string;
  target_language?: string | null;
  status: ThumbnailAssignmentStatus;
  created_at: string;
  updated_at: string;
}

/** Error body the server returns on optimistic-concurrency conflicts. */
export interface ProjectVersionConflict {
  code: "PROJECT_VERSION_CONFLICT";
  /**
   * Live project version when the server could determine it (snapshot
   * and restore races). Omitted on lifecycle CAS paths that only know
   * the expected version — callers should fall back to reloading the
   * project in that case.
   */
  current_version?: number;
}
