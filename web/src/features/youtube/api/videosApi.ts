/**
 * Group YouTube videos client — the single frontend gateway to the
 * group-videos endpoints under /api/v1/groups/{groupId}/youtube.
 *
 * patchGroupVideoMetadata is the "Modifica video" drawer save path: it
 * PATCHes the single metadata endpoint (title / description /
 * category_id) under the group's owning channel. On success it
 * invalidates ONLY the group-videos cache (`['groups', groupId,
 * 'youtube', 'videos']`) so the cards reflect the new metadata without
 * reloading the rest of InstaEdit. The backend merges the patch over
 * the canonical YouTube snippet (tags and omitted fields survive) and
 * invalidates its own per-account cache, so the next list fetch is
 * already fresh.
 *
 * Error semantics follow authedFetch: AuthError (401 → route to login)
 * and ApiError (with the backend `error` message) are thrown as-is;
 * the caller surfaces them with its own toast handling.
 */

import { authedFetch } from "../../../lib/auth";
import { invalidateGroupVideos } from "../hooks/useGroupVideosInvalidation";

/** The PATCH body — platform_account_id identifies the group channel
 * that owns the video (the card already carries it on every row). */
export interface GroupVideoMetadataPatch {
  platform_account_id: number;
  title: string;
  description: string;
  category_id: string;
}

/** The merged snippet projection the backend echoes after the update. */
export interface GroupVideoMetadataResult {
  youtube_video_id: string;
  title: string;
  description: string;
  category_id: string;
}

/**
 * PATCH /api/v1/groups/{groupId}/youtube/videos/{videoId} — update
 * title / description / category of a group YouTube video.
 *
 * After a successful save the group-videos cache is invalidated (the
 * targeted `['groups', groupId, 'youtube', 'videos']` surface only), so
 * mounted lists refetch in the background and cards update without any
 * full InstaEdit reload.
 */
export async function patchGroupVideoMetadata(
  groupId: number,
  videoId: string,
  patch: GroupVideoMetadataPatch,
): Promise<GroupVideoMetadataResult> {
  const response = await authedFetch(
    `/api/v1/groups/${groupId}/youtube/videos/${encodeURIComponent(videoId)}`,
    {
      method: "PATCH",
      body: JSON.stringify(patch),
    },
  );
  invalidateGroupVideos(groupId);
  return (await response.json()) as GroupVideoMetadataResult;
}
