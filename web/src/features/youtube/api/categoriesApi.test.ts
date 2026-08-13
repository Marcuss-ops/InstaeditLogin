import { afterEach, describe, expect, it, vi } from "vitest";

const { apiClientMock, ApiClientErrorMock } = vi.hoisted(() => {
  class ApiClientErrorMock extends Error {
    status: number | undefined;
    constructor(message: string, status?: number) {
      super(message);
      this.name = "ApiClientError";
      this.status = status;
    }
  }
  return { apiClientMock: vi.fn(), ApiClientErrorMock };
});

vi.mock("../../../lib/api-client", () => ({
  apiClient: apiClientMock,
  ApiClientError: ApiClientErrorMock,
}));

import { ApiClientError } from "../../../lib/api-client";
import {
  getVideoCategories,
  YOUTUBE_CATEGORIES,
  YOUTUBE_CATEGORIES_PATH,
} from "./categoriesApi";

afterEach(() => {
  apiClientMock.mockReset();
});

describe("getVideoCategories", () => {
  it("requests the endpoint with the region_code and returns the categories", async () => {
    const categories = [{ id: "24", label: "Intrattenimento" }];
    apiClientMock.mockResolvedValue({ categories });
    const controller = new AbortController();

    await expect(getVideoCategories("IT", { signal: controller.signal })).resolves.toEqual(categories);
    expect(apiClientMock).toHaveBeenCalledWith(
      `${YOUTUBE_CATEGORIES_PATH}?region_code=IT`,
      { signal: controller.signal },
    );
  });

  it("falls back to the canonical snapshot on 404 (endpoint not deployed)", async () => {
    apiClientMock.mockRejectedValue(new ApiClientError("request failed (status 404)", 404));
    await expect(getVideoCategories("US")).resolves.toEqual(YOUTUBE_CATEGORIES);
  });

  it("falls back to the canonical snapshot when the response has no categories", async () => {
    apiClientMock.mockResolvedValue({});
    await expect(getVideoCategories("IT")).resolves.toEqual(YOUTUBE_CATEGORIES);
  });

  it("propagates non-404 failures", async () => {
    apiClientMock.mockRejectedValue(new ApiClientError("boom", 500));
    await expect(getVideoCategories("IT")).rejects.toThrow("boom");
  });
});
