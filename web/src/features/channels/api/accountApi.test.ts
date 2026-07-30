/**
 * Vitest coverage for `accountApi`.
 *
 * Locks the wire contract so future renames of either the path
 * (e.g. `/accounts/{id}` → `/api/v2/accounts/{id}`) or the
 * response type break the test before runtime.
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
import { getChannelAccount } from "./accountApi";

const READY = {
  json: async () => ({
    id: 123,
    platform: "youtube",
    platform_user_id: "yt_abc",
    username: "demo-channel",
    status: "active",
    created_at: "2026-01-01T00:00:00.000Z",
    resource: {
      display_name: "Demo Channel",
      handle: "@demo",
      avatar_url: "https://cdn.example.test/avatar.png",
    },
  }),
} as unknown as Response;

beforeEach(() => {
  authedFetchMock.mockReset();
});
afterEach(() => {
  vi.restoreAllMocks();
});

describe("getChannelAccount", () => {
  it("calls GET /api/v1/accounts/{accountId} with numeric accountId", async () => {
    authedFetchMock.mockResolvedValueOnce(READY);
    await getChannelAccount({ accountId: 123 });
    expect(authedFetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = authedFetchMock.mock.calls[0]!;
    expect(url).toBe("/api/v1/accounts/123");
    expect(init).toEqual({});
  });

  it("passes through the AbortSignal when provided", async () => {
    const ctrl = new AbortController();
    authedFetchMock.mockResolvedValueOnce(READY);
    await getChannelAccount({ accountId: 123, signal: ctrl.signal });
    expect(authedFetchMock.mock.calls[0]![1]).toEqual({ signal: ctrl.signal });
  });

  it("returns the parsed JSON as a ChannelAccount", async () => {
    authedFetchMock.mockResolvedValueOnce(READY);
    const result = await getChannelAccount({ accountId: 123 });
    expect(result.id).toBe(123);
    expect(result.platform).toBe("youtube");
    expect(result.resource?.display_name).toBe("Demo Channel");
  });

  it("re-throws AuthError so router can navigate to /login", async () => {
    authedFetchMock.mockRejectedValueOnce(new AuthError("expired"));
    await expect(getChannelAccount({ accountId: 123 })).rejects.toBeInstanceOf(
      AuthError,
    );
  });

  it("passes ApiError through for the hook to surface", async () => {
    authedFetchMock.mockRejectedValueOnce(new ApiError(404, "not found"));
    await expect(getChannelAccount({ accountId: 123 })).rejects.toBeInstanceOf(
      ApiError,
    );
  });
});
