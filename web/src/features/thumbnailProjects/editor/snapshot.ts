/**
 * Editor snapshot normalization for the Cover editor canvas.
 *
 * The editor always works on a normalized snapshot with both keys set:
 * canvas (width/height/background) and objects — exactly the snapshot
 * schema_version 1 the canonical renderer rasterizes.
 */
import type {
  ThumbnailCanvasSnapshot,
  ThumbnailSnapshotObject,
} from "../types";

export const DEFAULT_BACKGROUND = "#30305a";
export const MAX_CANVAS_DIMENSION = 16384;

/** The editor always works on a normalized snapshot with both keys set. */
export interface EditorSnapshot {
  canvas: { width: number; height: number; background: string };
  objects: ThumbnailSnapshotObject[];
}

export function normalizeSnapshot(
  snapshot: ThumbnailCanvasSnapshot | undefined | null,
): EditorSnapshot {
  return {
    canvas: {
      width: snapshot?.canvas?.width ?? 1920,
      height: snapshot?.canvas?.height ?? 1080,
      background: snapshot?.canvas?.background ?? DEFAULT_BACKGROUND,
    },
    objects: Array.isArray(snapshot?.objects) ? snapshot!.objects! : [],
  };
}
