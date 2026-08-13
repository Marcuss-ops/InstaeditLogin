import { describe, expect, it } from "vitest";
import type { GroupYouTubeVideo } from "./groupYouTubeVideosTypes";
import { videoAvailability } from "./groupYouTubeVideosVisual";

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
