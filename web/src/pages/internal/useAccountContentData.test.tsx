import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";

const { authedFetchMock } = vi.hoisted(() => ({
  authedFetchMock: vi.fn(),
}));

vi.mock("../../lib/auth", () => ({
  authedFetch: authedFetchMock,
}));

import { useAccountContentData } from "./useAccountContentData";

const FIRST_PAGE = {
  items: [{ external_id: "video-1", title: "First video" }],
  next_cursor: "cursor-2",
};
const SECOND_PAGE = {
  items: [{ external_id: "video-2", title: "Second video" }],
};

function jsonResponse(body: unknown): Response {
  return { json: async () => body } as Response;
}

describe("useAccountContentData", () => {
  beforeEach(() => {
    authedFetchMock.mockReset();
    vi.spyOn(Date, "now").mockReturnValue(123456);
  });

  it("loads YouTube content with the private filter and updates cacheBust", async () => {
    authedFetchMock.mockResolvedValueOnce(jsonResponse(FIRST_PAGE));

    const { result } = renderHook(() =>
      useAccountContentData("7", "youtube"),
    );

    await act(async () => {
      await result.current.loadContent();
    });

    expect(authedFetchMock).toHaveBeenCalledWith(
      "/api/v1/accounts/7/content?limit=20&privacy=private",
    );
    expect(result.current.contentState).toEqual({
      kind: "ready",
      items: FIRST_PAGE.items,
      nextCursor: "cursor-2",
      isLoadingMore: false,
      loadMoreError: undefined,
    });
    expect(result.current.contentCacheBust).toBe(123456);
  });

  it("appends the next page using the cursor and preserves existing items", async () => {
    authedFetchMock
      .mockResolvedValueOnce(jsonResponse(FIRST_PAGE))
      .mockResolvedValueOnce(jsonResponse(SECOND_PAGE));

    const { result } = renderHook(() =>
      useAccountContentData("7", "instagram"),
    );

    await act(async () => {
      await result.current.loadContent();
      await result.current.loadContent("cursor-2");
    });

    expect(authedFetchMock.mock.calls.map(([url]) => url)).toEqual([
      "/api/v1/accounts/7/content?limit=20",
      "/api/v1/accounts/7/content?limit=20&cursor=cursor-2",
    ]);
    expect(result.current.contentState).toMatchObject({
      kind: "ready",
      items: [...FIRST_PAGE.items, ...SECOND_PAGE.items],
    });
  });

  it("keeps ready items and exposes an append error", async () => {
    authedFetchMock
      .mockResolvedValueOnce(jsonResponse(FIRST_PAGE))
      .mockRejectedValueOnce(new Error("temporary content failure"));

    const { result } = renderHook(() =>
      useAccountContentData("7", "youtube"),
    );

    await act(async () => {
      await result.current.loadContent();
      await result.current.loadContent("cursor-2");
    });

    expect(result.current.contentState).toEqual({
      kind: "ready",
      items: FIRST_PAGE.items,
      nextCursor: "cursor-2",
      isLoadingMore: false,
      loadMoreError: "temporary content failure",
    });
  });

  it("surfaces an initial loading failure", async () => {
    authedFetchMock.mockRejectedValueOnce(new Error("content unavailable"));

    const { result } = renderHook(() =>
      useAccountContentData("7", "youtube"),
    );

    await act(async () => {
      await result.current.loadContent();
    });
    await waitFor(() => expect(result.current.contentState.kind).toBe("error"));
    expect(result.current.contentState).toEqual({
      kind: "error",
      message: "content unavailable",
    });
  });
});
