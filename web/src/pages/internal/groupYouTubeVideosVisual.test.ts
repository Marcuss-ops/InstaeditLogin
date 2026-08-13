import { describe, expect, it } from "vitest";
import type { GroupYouTubeVideo } from "./groupYouTubeVideosTypes";
import { categoryLabel, categoryOptions, videoAvailability } from "./groupYouTubeVideosVisual";

const base: GroupYouTubeVideo = {
  youtube_video_id: "v1",
  title: "Video",
  platform_account_id: 1,
};

describe("videoAvailability", () => {
  it("is available for a normal video", () => {
    expect(videoAvailability(base)).toEqual({ status: "available" });
  });

  it("maps phantom rows to deleted_or_missing", () => {
    expect(videoAvailability({ ...base, phantom: true })).toMatchObject({
      status: "deleted_or_missing",
    });
    expect(videoAvailability({ ...base, phantom: true }).reason).toBeTruthy();
  });

  it("maps drift sync to privacy_changed", () => {
    expect(videoAvailability({ ...base, youtube_sync_status: "drift" })).toMatchObject({
      status: "privacy_changed",
    });
  });

  it("maps failed sync to unavailable", () => {
    expect(videoAvailability({ ...base, youtube_sync_status: "failed" })).toMatchObject({
      status: "unavailable",
    });
  });

  it("keeps a stamped availability untouched", () => {
    const stamped = { status: "unknown", reason: "cache legacy" } as const;
    expect(videoAvailability({ ...base, availability: stamped })).toEqual(stamped);
  });
});

describe("categoryLabel", () => {
  it("prefers the row's own category_title", () => {
    expect(categoryLabel({ ...base, category_id: "17", category_title: "Sport" })).toBe("Sport");
  });

  it("resolves category_id through the canonical snapshot", () => {
    expect(categoryLabel({ ...base, category_id: "24" })).toBe("Intrattenimento");
  });

  it("falls back to the raw id when the snapshot has no match", () => {
    expect(categoryLabel({ ...base, category_id: "999" })).toBe("999");
  });

  it("is undefined when the row has no category at all", () => {
    expect(categoryLabel(base)).toBeUndefined();
  });
});

describe("categoryOptions", () => {
  it("derives distinct, alphabetically-sorted options from the rows", () => {
    const videos: GroupYouTubeVideo[] = [
      { ...base, youtube_video_id: "a", category_id: "20", category_title: "Gaming" },
      { ...base, youtube_video_id: "b", category_id: "24" },
      { ...base, youtube_video_id: "c", category_id: "20", category_title: "Gaming" },
      { ...base, youtube_video_id: "d" },
    ];
    expect(categoryOptions(videos)).toEqual([
      { key: "20", label: "Gaming" },
      { key: "24", label: "Intrattenimento" },
    ]);
  });

  it("returns an empty list when no video carries a category", () => {
    expect(categoryOptions([base])).toEqual([]);
    expect(categoryOptions([])).toEqual([]);
  });
});
