import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";

const { useSharedQueryMock, getVideoCategoriesMock } = vi.hoisted(() => ({
  useSharedQueryMock: vi.fn(),
  getVideoCategoriesMock: vi.fn(),
}));

vi.mock("../../../lib/queryRegistry", () => ({
  useSharedQuery: useSharedQueryMock,
}));

vi.mock("../api/categoriesApi", () => ({
  getVideoCategories: getVideoCategoriesMock,
  YOUTUBE_CATEGORIES: [],
}));

import { useYouTubeCategories, youtubeCategoriesQueryKey } from "./useYouTubeCategories";

const snapshot = {
  data: undefined,
  error: null,
  isLoading: true,
  isFetching: false,
  updatedAt: 0,
  refetch: vi.fn(),
};

beforeEach(() => {
  useSharedQueryMock.mockReset();
  getVideoCategoriesMock.mockReset();
  useSharedQueryMock.mockReturnValue(snapshot);
});

describe("youtubeCategoriesQueryKey", () => {
  it("flattens the react-query style ['youtube','categories',regionCode] key", () => {
    expect(youtubeCategoriesQueryKey("IT")).toBe("youtube:categories:IT");
    expect(youtubeCategoriesQueryKey("US")).toBe("youtube:categories:US");
  });
});

describe("useYouTubeCategories", () => {
  it("uses the shared cache key and a long staleTime", () => {
    renderHook(() => useYouTubeCategories("IT"));
    expect(useSharedQueryMock).toHaveBeenCalledWith(
      "youtube:categories:IT",
      expect.objectContaining({ staleTime: expect.any(Number) }),
    );
  });

  it("defaults the region to IT", () => {
    renderHook(() => useYouTubeCategories());
    expect(useSharedQueryMock).toHaveBeenCalledWith(
      "youtube:categories:IT",
      expect.anything(),
    );
  });

  it("forwards the region and signal through the fetcher", async () => {
    getVideoCategoriesMock.mockResolvedValue([{ id: "24", label: "Intrattenimento" }]);
    renderHook(() => useYouTubeCategories("US"));

    const options = useSharedQueryMock.mock.calls[0][1] as { fetcher: (signal: AbortSignal) => Promise<unknown> };
    const controller = new AbortController();
    await expect(options.fetcher(controller.signal)).resolves.toEqual([
      { id: "24", label: "Intrattenimento" },
    ]);
    expect(getVideoCategoriesMock).toHaveBeenCalledWith("US", { signal: controller.signal });
  });
});
