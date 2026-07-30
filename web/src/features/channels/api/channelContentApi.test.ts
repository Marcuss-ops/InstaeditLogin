/**
 * Vitest coverage for `channelContentApi`.
 *
 * Goal: lock the wire contract that the channel-page consumer
 * (the hook + the upcoming DashboardChannelsPage) depends on so a
 * future change to the URL shape, the response shape, or the
 * error classification breaks the test before runtime.
 *
 * Strategy: vi.hoisted() declares the authedFetch mock up front
 * so the api module binds to it on import.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { authedFetchMock } = vi.hoisted(() => ({
  authedFetchMock: vi.fn(),
}));

vi.mock("../../../lib/auth", () => ({
  authedFetch: authedFetchMock,
  AuthError: class AuthError extends Error {
    override name = "AuthError";
  },
  ApiError: class ApiError extends Error {
    override name = "ApiError";
    constructor(public readonly status: number, msg: string) {
      super(msg);
    }
  },
}));

import { ApiError, AuthError } from "../../../lib/auth";
import { listChannelContent } from "./channelContentApi";

const VIEW = {
  json: async () => ({
    items: [
      {
        external_id: "yt_abc",
        title: "Demo",
        privacy: "private",
        status: "live",
      },
    ],
    next_cursor: "cur_001",
  }),
} as unknown as Response;

const EMPTY = {
  json: async () => ({ items: [] }),
} as unknown as Response;

beforeEach(() => {
  authedFetchMock.mockReset();
});
afterEach(() => {
  vi.restoreAllMocks();
});

describe("listChannelContent", () => {
  it("calls GET /api/v1/accounts/{id}/content with limit=20 by default", async () => {
    authedFetchMock.mockResolvedValueOnce(VIEW);
    await listChannelContent({ accountId: 123 });
    expect(authedFetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = authedFetchMock.mock.calls[0]!;
    expect(url).toBe("/api/v1/accounts/123/content?limit=20");
    // Default RequestInit doesn't include a signal — the hook owns it.
    expect(init).toEqual({ signal: undefined });
  });

  it("overrides the limit when provided", async () => {
    authedFetchMock.mockResolvedValueOnce(VIEW);
    await listChannelContent({ accountId: 123, limit: 50 });
    expect(authedFetchMock.mock.calls[0]![0]).toBe(
      "/api/v1/accounts/123/content?limit=50",
    );
  });

  it("includes ?privacy=private|unlisted|public when non-'all'", async () => {
    authedFetchMock.mockResolvedValueOnce(VIEW);
    await listChannelContent({
      accountId: 123,
      privacy: "private",
    });
    expect(authedFetchMock.mock.calls[0]![0]).toBe(
      "/api/v1/accounts/123/content?limit=20&privacy=private",
    );
  });

  it("OMITS ?privacy entirely when the filter is 'all' (UI-only)", async () => {
    authedFetchMock.mockResolvedValueOnce(VIEW);
    await listChannelContent({ accountId: 123, privacy: "all" });
    expect(authedFetchMock.mock.calls[0]![0]).toBe(
      "/api/v1/accounts/123/content?limit=20",
    );
  });

  it("appends ?cursor= when provided", async () => {
    authedFetchMock.mockResolvedValueOnce(VIEW);
    await listChannelContent({
      accountId: 123,
      privacy: "unlisted",
      cursor: "cur_001",
    });
    expect(authedFetchMock.mock.calls[0]![0]).toBe(
      "/api/v1/accounts/123/content?limit=20&privacy=unlisted&cursor=cur_001",
    );
  });

  it("passes through the AbortSignal when provided", async () => {
    const ctrl = new AbortController();
    authedFetchMock.mockResolvedValueOnce(VIEW);
    await listChannelContent({ accountId: 123, signal: ctrl.signal });
    expect(authedFetchMock.mock.calls[0]![1]).toEqual({ signal: ctrl.signal });
  });

  it("returns the items array and a next_cursor when present", async () => {
    authedFetchMock.mockResolvedValueOnce(VIEW);
    const result = await listChannelContent({ accountId: 123 });
    expect(result.items).toHaveLength(1);
    expect(result.items[0]!.external_id).toBe("yt_abc");
    expect(result.next_cursor).toBe("cur_001");
  });

  it("strips next_cursor when the server omits it", async () => {
    authedFetchMock.mockResolvedValueOnce(EMPTY);
    const result = await listChannelContent({ accountId: 123 });
    expect(result.items).toEqual([]);
    expect(result.next_cursor).toBeUndefined();
  });

  it("treats a missing items key as an empty page (no crash)", async () => {
    authedFetchMock.mockResolvedValueOnce({
      json: async () => ({ next_cursor: "cur_002" }),
    } as unknown as Response);
    const result = await listChannelContent({ accountId: 123 });
    expect(result.items).toEqual([]);
    expect(result.next_cursor).toBe("cur_002");
  });

  it("re-throws AuthError so router can navigate to /login", async () => {
    authedFetchMock.mockRejectedValueOnce(new AuthError("expired"));
    await expect(listChannelContent({ accountId: 123 })).rejects.toBeInstanceOf(
      AuthError,
    );
  });

  it("passes ApiError through for the hook to surface", async () => {
    authedFetchMock.mockRejectedValueOnce(new ApiError(500, "boom"));
    await expect(listChannelContent({ accountId: 123 })).rejects.toBeInstanceOf(
      ApiError,
    );
  });

  it("supports a tri-privacy filter union without coercion failures", async () => {
    authedFetchMock.mockResolvedValueOnce(VIEW);
    await listChannelContent({ accountId: 123, privacy: "public" });
    await listChannelContent({ accountId: 123, privacy: "unlisted" });
    await listChannelContent({ accountId: 123, privacy: "private" });
    await listChannelContent({ accountId: 123, privacy: "all" });
    expect(authedFetchMock).toHaveBeenCalledTimes(4);
  });

  it("URL-escapes the accountId in the path is NOT done (numeric only)", async () => {
    authedFetchMock.mockResolvedValueOnce(VIEW);
    await listChannelContent({ accountId: 42 });
    expect(authedFetchMock.mock.calls[0]![0]).toBe(
      "/api/v1/accounts/42/content?limit=20",
    );
  });

  it("keeps limit under the server cap of 50 without complaint", async () => {
    authedFetchMock.mockResolvedValueOnce(VIEW);
    await listChannelContent({ accountId: 123, limit: 50 });
    expect(authedFetchMock.mock.calls[0]![0]).toContain("limit=50");
  });
});
