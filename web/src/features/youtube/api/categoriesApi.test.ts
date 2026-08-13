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

  it("returns an empty list when the response has no categories", async () => {
    apiClientMock.mockResolvedValue({});
    await expect(getVideoCategories("IT")).resolves.toEqual([]);
  });

  it("propagates failures (including 404 — the endpoint is live)", async () => {
    apiClientMock.mockRejectedValue(new ApiClientError("boom", 500));
    await expect(getVideoCategories("IT")).rejects.toThrow("boom");

    apiClientMock.mockRejectedValue(new ApiClientError("request failed (status 404)", 404));
    await expect(getVideoCategories("IT")).rejects.toThrow("request failed (status 404)");
  });
});
