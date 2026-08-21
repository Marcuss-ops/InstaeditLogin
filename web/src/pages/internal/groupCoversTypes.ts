/**
 * One cover project row returned by GET /api/v1/groups/{group_id}/covers.
 *
 * The backend join (thumbnail_projects → velox_project_bridges →
 * youtube_video_edits) attributes each cover to the group whose
 * accounts own the underlying editor session, so the SPA can render a
 * per-group covers grid (current + archived history) in one request.
 */
export interface GroupCover {
  project_id: string;
  workspace_id: number;
  session_id: string;
  /** InstaEditor project handle — the /editor/{velox_project_id} URL segment. */
  velox_project_id: string;
  /** Server-built InstaEditor URL; empty when INSTAEDITOR_URL is unconfigured. */
  editor_url: string;
  /** thumbnail_projects.name — "YouTube cover" for session-minted covers. */
  name: string;
  /** thumbnail_projects.status: draft | ready | archived (deleted excluded). */
  project_status: "draft" | "ready" | "archived" | string;
  /** youtube_video_edits.status: editing | failed | publishing | published. */
  edit_status: string;
  /** Rendered cover preview (media_assets UUID) — resolve via GET /api/v1/media/{id}. */
  preview_media_id?: string | null;
  /** Attached thumbnail asset, when the session linked one. */
  thumbnail_media_id?: string | null;
  /** Original YouTube video thumbnail — stable fallback for un-rendered covers. */
  source_thumbnail_url?: string;
  youtube_video_id: string;
  platform_account_id: number;
  channel_name?: string;
  language?: string;
  /** YouTube video category stamped on the editor session (e.g. "24" → Intrattenimento). */
  category_id?: string;
  /** Live YouTube visibility: actual read-back when published, else the desired one. */
  privacy_status?: string;
	draft_title?: string | null;
	draft_description?: string | null;
  project_version: number;
  created_at: string;
  updated_at: string;
}

export interface GroupCoversResponse {
  covers?: GroupCover[];
}

export interface GroupDraft {
  id: string;
  workspace_id: number;
  name: string;
  description?: string;
  status: string;
  preview_media_id?: string | null;
  latest_export_id?: string | null;
  updated_at: string;
}

export type CoversLoadState =
  | { kind: "loading" }
  | {
      kind: "ready";
      covers: GroupCover[];
      /** resolved preview URLs keyed by preview_media_id */
      previewUrls: Record<string, string | undefined>;
    }
  | { kind: "error"; message: string };
