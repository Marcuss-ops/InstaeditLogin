/**
 * Vitest coverage for the thumbnail media resolver: snapshot media_id
 * collection (dedupe, schema-forward-compat) and server-side resolution
 * (workspace-guarded, no local-blob fallback).
 */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { resolveMediaMock } = vi.hoisted(() => ({
  resolveMediaMock: vi.fn(),
}));

vi.mock("../api/thumbnailProjectsApi", async (orig) => {
  const actual = await orig();
  return {
    ...actual,
    resolveThumbnailProjectMedia: (...args: unknown[]) => resolveMediaMock(...args),
  };
});

import {
  collectSnapshotMediaIds,
  resolveProjectMedia,
} from "./mediaResolver";
import type { ResolvedProjectMedia, ThumbnailCanvasSnapshot } from "../types";

const MEDIA_A = "00000000-0000-4000-8000-000000000001";
const MEDIA_B = "00000000-0000-4000-8000-000000000002";

function resolvedMedia(mediaId: string): ResolvedProjectMedia {
  return {
    media_id: mediaId,
    url: `https://cdn.example/${mediaId}?X-Amz-Signature=abc`,
    content_type: "image/jpeg",
    size_bytes: 2048,
    created_at: "2026-08-03T00:00:00Z",
  };
}

beforeEach(() => {
  resolveMediaMock.mockReset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("collectSnapshotMediaIds", () => {
  it("collects media_id from every object, deduplicated in order", () => {
    const snapshot: ThumbnailCanvasSnapshot = {
      objects: [
        { id: "img-1", type: "image", media_id: MEDIA_A },
        { id: "txt-1", type: "text", text: "Hi" },
        { id: "img-2", type: "image", media_id: MEDIA_B },
        { id: "img-3", type: "image", media_id: MEDIA_A }, // duplicate
        { id: "ovl-1", type: "overlay", media_id: ` ${MEDIA_B} ` }, // trimmed dup
      ],
    };
    expect(collectSnapshotMediaIds(snapshot)).toEqual([MEDIA_A, MEDIA_B]);
  });

  it("returns [] for snapshots without objects or with no media_ids", () => {
    expect(collectSnapshotMediaIds(undefined)).toEqual([]);
    expect(collectSnapshotMediaIds(null)).toEqual([]);
    expect(collectSnapshotMediaIds({})).toEqual([]);
    expect(
      collectSnapshotMediaIds({ objects: [{ id: "t", type: "text", text: "x" }] }),
    ).toEqual([]);
  });

  it("ignores empty/whitespace-only media_ids", () => {
    const snapshot: ThumbnailCanvasSnapshot = {
      objects: [
        { id: "a", type: "image", media_id: "  " },
        { id: "b", type: "image", media_id: "" },
      ],
    };
    expect(collectSnapshotMediaIds(snapshot)).toEqual([]);
  });
});

describe("resolveProjectMedia", () => {
  it("resolves all referenced media_ids through the server", async () => {
    resolveMediaMock.mockResolvedValue([resolvedMedia(MEDIA_A), resolvedMedia(MEDIA_B)]);
    const snapshot: ThumbnailCanvasSnapshot = {
      objects: [{ id: "img-1", type: "image", media_id: MEDIA_A }],
    };
    const map = await resolveProjectMedia(7, "thumbproj_1", snapshot);
    const [workspaceId, projectId, mediaIds] = resolveMediaMock.mock.calls[0] as [
      number,
      string,
      string[],
    ];
    expect(workspaceId).toBe(7);
    expect(projectId).toBe("thumbproj_1");
    expect(mediaIds).toEqual([MEDIA_A]);
    expect(map.size).toBe(2);
    expect(map.get(MEDIA_A)?.url).toContain("X-Amz-Signature=abc");
  });

  it("never falls back to a local blob: blocked assets are simply absent", async () => {
    // Server blocks a foreign/not-ready asset → only MEDIA_A resolves.
    resolveMediaMock.mockResolvedValue([resolvedMedia(MEDIA_A)]);
    const snapshot: ThumbnailCanvasSnapshot = {
      objects: [
        { id: "img-1", type: "image", media_id: MEDIA_A },
        { id: "img-2", type: "image", media_id: "00000000-0000-4000-8000-000000000099" },
      ],
    };
    const map = await resolveProjectMedia(7, "thumbproj_1", snapshot);
    expect(map.has(MEDIA_A)).toBe(true);
    expect(map.has("00000000-0000-4000-8000-000000000099")).toBe(false);
  });

  it("does not call the server when the snapshot references no media", async () => {
    const map = await resolveProjectMedia(7, "thumbproj_1", {
      objects: [{ id: "t", type: "text", text: "x" }],
    });
    expect(resolveMediaMock).not.toHaveBeenCalled();
    expect(map.size).toBe(0);
  });
});
