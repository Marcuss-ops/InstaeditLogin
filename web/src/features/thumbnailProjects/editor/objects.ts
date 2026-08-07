/**
 * Object factories for the Cover editor canvas.
 *
 * Each factory returns a fully-formed ThumbnailSnapshotObject with the
 * canonical defaults the canvas renders (position, size, scale, fill,
 * typography) so the toolbar can add a new layer with one call.
 */
import type { MediaPickerDetail } from "../components/MediaPickerDialog";
import type { ThumbnailSnapshotObject } from "../types";
import { MAX_CANVAS_DIMENSION } from "./snapshot";

export function makeId(prefix: string): string {
  return `${prefix}-${crypto.randomUUID?.() ?? Math.random().toString(36).slice(2)}`;
}

export function newTextObject(): ThumbnailSnapshotObject {
  return {
    id: makeId("text"),
    type: "text",
    text: "Testo",
    x: 120,
    y: 140,
    width: 720,
    height: 180,
    scale_x: 1,
    scale_y: 1,
    rotation: 0,
    visible: true,
    fill: "#ffffff",
    font_family: "Inter",
    font_size: 96,
    font_weight: 700,
    text_align: "center",
  };
}

export function newRectObject(): ThumbnailSnapshotObject {
  return {
    id: makeId("rect"),
    type: "rect",
    x: 240,
    y: 240,
    width: 480,
    height: 260,
    scale_x: 1,
    scale_y: 1,
    rotation: 0,
    visible: true,
    fill: "#0a84ff",
    radius: 16,
  };
}

export function newImageObject(item: MediaPickerDetail): ThumbnailSnapshotObject {
  const width =
    item.width && item.width > 0 ? Math.min(item.width, MAX_CANVAS_DIMENSION) : 480;
  const height =
    item.height && item.height > 0 ? Math.min(item.height, MAX_CANVAS_DIMENSION) : 270;
  return {
    id: makeId("img"),
    type: "image",
    media_id: item.id,
    x: 0,
    y: 0,
    width,
    height,
    scale_x: 1,
    scale_y: 1,
    rotation: 0,
    visible: true,
  };
}
