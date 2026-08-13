import { beforeEach, describe, expect, it, vi } from "vitest";

const { authedFetchMock, invalidateGroupVideosMock } = vi.hoisted(() => ({
  authedFetchMock: vi.fn(),
  invalidateGroupVideosMock: vi.fn(),
}));

vi.mock("../../../lib/auth", () => ({
  authedFetch: authedFetchMock,
  AuthError: class AuthError extends Error {
    override name = "AuthError";
  },
  ApiError: class ApiError extends Error {
    override name = "ApiError";
    constructor(public readonly status: number, message: string) {
      super(message);
    }
  },
}));

vi.mock("../hooks/useGroupVideosInvalidation", () => ({
  invalidateGroupVideos: invalidateGroupVideosMock,
}));

import { ApiError, AuthError } from "../../../lib/auth";
import { patchGroupVideoMetadata } from "./videosApi";

function jsonResponse(data: unknown) {
  return { json: async () => data };
}

const patch = {
  platform_account_id: 42,
  title: "Nuovo titolo",
  description: "Nuova descrizione",
  category_id: "24",
};

beforeEach(() => {
  authedFetchMock.mockReset();
  invalidateGroupVideosMock.mockReset();
});

describe("patchGroupVideoMetadata", () => {
  it("PATCHes the group metadata endpoint with the partial body and returns the merged echo", async () => {
    authedFetchMock.mockResolvedValue(
      jsonResponse({
        youtube_video_id: "video-1",
        title: "Nuovo titolo",
        description: "Nuova descrizione",
        category_id: "24",
      }),
    );

    const result = await patchGroupVideoMetadata(7, "video-1", patch);

    expect(authedFetchMock).toHaveBeenCalledWith(
      "/api/v1/groups/7/youtube/videos/video-1",
      expect.objectContaining({
        method: "PATCH",
        body: JSON.stringify(patch),
      }),
    );
    expect(result).toEqual({
      youtube_video_id: "video-1",
      title: "Nuovo titolo",
      description: "Nuova descrizione",
      category_id: "24",
    });
  });

  it("encodes the video id in the path", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({}));

    await patchGroupVideoMetadata(7, "id/with-slash", patch);

    expect(authedFetchMock).toHaveBeenCalledWith(
      "/api/v1/groups/7/youtube/videos/id%2Fwith-slash",
      expect.anything(),
    );
  });

  it("invalidates ONLY the group-videos cache after a successful save", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({}));

    await patchGroupVideoMetadata(7, "video-1", patch);

    expect(invalidateGroupVideosMock).toHaveBeenCalledWith(7);
    expect(invalidateGroupVideosMock).toHaveBeenCalledTimes(1);
  });

  it("propagates AuthError without invalidating (caller routes to login)", async () => {
    authedFetchMock.mockRejectedValue(new AuthError());

    await expect(patchGroupVideoMetadata(7, "video-1", patch)).rejects.toBeInstanceOf(AuthError);
    expect(invalidateGroupVideosMock).not.toHaveBeenCalled();
  });

  it("propagates ApiError without invalidating", async () => {
    authedFetchMock.mockRejectedValue(new ApiError(502, "YouTube non risponde temporaneamente. Riprova tra poco."));

    await expect(patchGroupVideoMetadata(7, "video-1", patch)).rejects.toBeInstanceOf(ApiError);
    expect(invalidateGroupVideosMock).not.toHaveBeenCalled();
  });
});
