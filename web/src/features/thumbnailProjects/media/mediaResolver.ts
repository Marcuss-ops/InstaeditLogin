/**
 * Thumbnail media resolver — the editor's authoritative media source.
 *
 * On project open the editor collects every media_id referenced by the
 * snapshot and resolves them through the server (presigned GET URLs).
 * Local blobs are never authoritative: reopening the same snapshot in
 * another browser must load its images from the server, and unresolved
 * ids (missing, foreign, not-ready, expired) simply stay absent from
 * the returned map so the canvas renders them as placeholders. No
 * YouTube/OAuth dependency exists on this path.
 */

import { resolveThumbnailProjectMedia } from "../api/thumbnailProjectsApi";
import type { ResolvedProjectMedia, ThumbnailCanvasSnapshot } from "../types";

/**
 * Collects the distinct media_id references of a snapshot, preserving
 * first-occurrence order. Every object carrying a media_id is collected
 * (image objects today; font/overlay objects in future schema versions),
 * so the resolver is schema-forward-compatible.
 */
export function collectSnapshotMediaIds(
  snapshot: ThumbnailCanvasSnapshot | undefined | null,
): string[] {
  if (!snapshot?.objects) return [];
  const seen = new Set<string>();
  const ids: string[] = [];
  for (const obj of snapshot.objects) {
    const mediaId = obj?.media_id?.trim();
    if (mediaId && !seen.has(mediaId)) {
      seen.add(mediaId);
      ids.push(mediaId);
    }
  }
  return ids;
}

/**
 * Resolves every media_id referenced by the snapshot into a map of
 * media_id → resolved media (presigned URL + metadata). The server owns
 * the workspace guard: cross-workspace / not-ready / expired assets
 * never appear in the result. Returns an empty map when the snapshot
 * references no media or when the server resolves none.
 *
 * Errors propagate (ApiError/AuthError from authedFetch) so callers can
 * surface "Modifiche non salvate — riprova"-style states; a resolved
 * asset missing from the result is NOT an error — it is blocked.
 */
export async function resolveProjectMedia(
  workspaceId: number,
  projectId: string,
  snapshot: ThumbnailCanvasSnapshot | undefined | null,
  init: RequestInit = {},
): Promise<Map<string, ResolvedProjectMedia>> {
  const mediaIds = collectSnapshotMediaIds(snapshot);
  if (mediaIds.length === 0) return new Map();
  const items = await resolveThumbnailProjectMedia(
    workspaceId,
    projectId,
    mediaIds,
    init,
  );
  const resolved = new Map<string, ResolvedProjectMedia>();
  for (const item of items) {
    resolved.set(item.media_id, item);
  }
  return resolved;
}
