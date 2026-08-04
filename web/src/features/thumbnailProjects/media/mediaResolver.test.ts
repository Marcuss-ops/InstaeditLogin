/**
 * Vitest coverage for the thumbnail open resolver (mediaResolver.ts).
 *
 * Certifies the "open" contract of the autonomous Dark Editor: reopening a
 * project must resolve every media_id referenced by the snapshot through
 * the server (presigned GET URLs), never from local blobs — so a browser
 * cache clear, a different device, or a full service restart (API /
 * worker / MinIO / PostgreSQL) cannot leave images broken as long as the
 * media rows still exist. Unresolvable ids (missing, foreign, not-ready,
 * expired) are simply absent from the returned map and render as
 * placeholders.
 */
import { describe, expect, it, vi } from "vitest";
import {
  collectSnapshotMediaIds,
  resolveProjectMedia,
} from "./mediaResolver";
import type { ResolvedProjectMedia } from "../types";

const { resolveThumbnailProjectMediaMock } = vi.hoisted(() => ({
  resolveThumbnailProjectMediaMock: vi.fn(),
}));

vi.mock("../api/thumbnailProjectsApi", () => ({
  resolveThumbnailProjectMedia: resolveThumbnailProjectMediaMock,
}));

const MEDIA = "00000000-0000-4000-8000-000000000001";

describe("collectSnapshotMediaIds", () => {
  it("collects distinct media_id references preserving order", () => {
    const ids = collectSnapshotMediaIds({
      objects: [
        { id: "a", type: "image", media_id: MEDIA },
        { id: "b", type: "text", text: "no media" },
        { id: "c", type: "image", media_id: "00000000-0000-4000-8000-000000000002" },
        { id: "d", type: "image", media_id: ` ${MEDIA} ` }, // trimmed, deduped
      ],
    });
    expect(ids).toEqual([
      MEDIA,
      "00000000-0000-4000-8000-000000000002",
    ]);
  });

  it("ignores objects without a media_id and empty snapshots", () => {
    expect(collectSnapshotMediaIds({ objects: [{ id: "t", type: "text", text: "x" }] })).toEqual([]);
    expect(collectSnapshotMediaIds({ canvas: {}, objects: [] })).toEqual([]);
    expect(collectSnapshotMediaIds(null)).toEqual([]);
    expect(collectSnapshotMediaIds(undefined)).toEqual([]);
  });

  it("is schema-forward: collects media_id from future object types (font)", () => {
    expect(
      collectSnapshotMediaIds({ objects: [{ id: "f", type: "font", media_id: MEDIA }] }),
    ).toEqual([MEDIA]);
  });
});

describe("resolveProjectMedia", () => {
  it("returns an empty map when the snapshot references no media (no API call)", async () => {
    resolveThumbnailProjectMediaMock.mockReset();
    const map = await resolveProjectMedia(7, "thumbproj_1", { objects: [] });
    expect(map.size).toBe(0);
    expect(resolveThumbnailProjectMediaMock).not.toHaveBeenCalled();
  });

  it("resolves every referenced media_id into a map keyed by media_id", async () => {
    const items: ResolvedProjectMedia[] = [
      {
        media_id: MEDIA,
        url: "https://cdn.example/presigned/1?X-Amz-Signature=abc",
        content_type: "image/png",
        size_bytes: 2048,
        created_at: "2026-08-04T10:00:00Z",
      },
    ];
    resolveThumbnailProjectMediaMock.mockResolvedValue(items);

    const map = await resolveProjectMedia(7, "thumbproj_1", {
      objects: [{ id: "a", type: "image", media_id: MEDIA }],
    });

    expect(resolveThumbnailProjectMediaMock).toHaveBeenCalledWith(
      7,
      "thumbproj_1",
      [MEDIA],
      expect.anything(),
    );
    expect(map.get(MEDIA)?.url).toBe("https://cdn.example/presigned/1?X-Amz-Signature=abc");
    expect(map.get(MEDIA)?.content_type).toBe("image/png");
  });

  it("omits server-blocked assets (foreign/not-ready/expired) — never a local blob fallback", async () => {
    // The server simply does not return the blocked media_id.
    resolveThumbnailProjectMediaMock.mockResolvedValue([]);

    const map = await resolveProjectMedia(7, "thumbproj_1", {
      objects: [{ id: "a", type: "image", media_id: MEDIA }],
    });
    expect(map.size).toBe(0);
    expect(map.has(MEDIA)).toBe(false);
  });

  it("propagates API errors so the editor can surface a retry state", async () => {
    resolveThumbnailProjectMediaMock.mockRejectedValue(new Error("network down"));
    await expect(
      resolveProjectMedia(7, "thumbproj_1", {
        objects: [{ id: "a", type: "image", media_id: MEDIA }],
      }),
    ).rejects.toThrow("network down");
  });
});
