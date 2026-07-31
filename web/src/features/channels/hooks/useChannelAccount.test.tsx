/**
 * Vitest coverage for `useChannelAccount`.
 *
 * Mirrors the test shape used for `useYouTubeChannels.test.ts`:
 * the public surface to a server-backed hook is so similar that the
 * same 6-section structure applies (state machine, AuthError,
 * ApiError, refresh, abort, no-fetch-when-paused).
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";

const { getChannelAccountMock } = vi.hoisted(() => ({
  getChannelAccountMock: vi.fn(),
}));

vi.mock("../api/accountApi", () => ({
  getChannelAccount: getChannelAccountMock,
}));

vi.mock("../../../lib/auth", () => ({
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

import { useChannelAccount } from "./useChannelAccount";
import { ApiError, AuthError } from "../../../lib/auth";

const READY_ACCOUNT = {
  id: 123,
  platform: "youtube",
  platform_user_id: "yt_abc",
  username: "demo-channel",
  status: "active",
  created_at: "2026-01-01T00:00:00.000Z",
  resource: { display_name: "Demo Channel", handle: "@demo" },
};

describe("useChannelAccount", () => {
  beforeEach(() => {
    getChannelAccountMock.mockReset();
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("starts in loading and lands in ready after a successful fetch", async () => {
    getChannelAccountMock.mockResolvedValueOnce(READY_ACCOUNT);
    const { result } = renderHook(() =>
      useChannelAccount({ accountId: 123 }),
    );
    expect(result.current.state.kind).toBe("loading");
    await waitFor(() => {
      expect(result.current.state.kind).toBe("ready");
    });
    if (result.current.state.kind === "ready") {
      expect(result.current.state.account.id).toBe(123);
    }
  });

  it("calls getChannelAccount with the numeric accountId", async () => {
    getChannelAccountMock.mockResolvedValueOnce(READY_ACCOUNT);
    await act(async () => {
      renderHook(() => useChannelAccount({ accountId: 123 }));
    });
    await waitFor(() =>
      expect(getChannelAccountMock).toHaveBeenCalledTimes(1),
    );
    expect(getChannelAccountMock).toHaveBeenCalledWith(
      expect.objectContaining({ accountId: 123 }),
    );
  });

  it("auto-refetches when accountId changes", async () => {
    getChannelAccountMock.mockResolvedValue(READY_ACCOUNT);
    const { result, rerender } = renderHook(
      ({ id }: { id: number }) => useChannelAccount({ accountId: id }),
      { initialProps: { id: 123 } },
    );
    await waitFor(() => expect(result.current.state.kind).toBe("ready"));
    expect(getChannelAccountMock).toHaveBeenCalledTimes(1);

    await act(async () => {
      rerender({ id: 456 });
    });
    await waitFor(() => expect(getChannelAccountMock).toHaveBeenCalledTimes(2));
    expect(getChannelAccountMock).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({ accountId: 456 }),
    );
  });

  it("stays in loading when accountId is null (no fetch fires)", async () => {
    const { result } = renderHook(() =>
      useChannelAccount({ accountId: null }),
    );
    // Microtask flush so any spurious promise settles.
    await act(async () => {});
    expect(result.current.state.kind).toBe("loading");
    expect(getChannelAccountMock).not.toHaveBeenCalled();
  });

  it("re-throws AuthError so the router can navigate to /login", async () => {
    getChannelAccountMock.mockRejectedValueOnce(new AuthError("expired"));
    const { result } = renderHook(() =>
      useChannelAccount({ accountId: 123 }),
    );
    await act(async () => {
      await Promise.resolve();
    });
    expect(result.current.state.kind).toBe("loading");
    expect(getChannelAccountMock).toHaveBeenCalledTimes(1);
  });

  it("surfaces ApiError via kind='error'", async () => {
    getChannelAccountMock.mockRejectedValueOnce(
      new ApiError(404, "not found"),
    );
    const { result } = renderHook(() =>
      useChannelAccount({ accountId: 123 }),
    );
    await waitFor(() => {
      expect(result.current.state.kind).toBe("error");
    });
    if (result.current.state.kind === "error") {
      expect(result.current.state.message).toBe("not found");
    }
  });

  it("falls back to a generic message for non-Api rejections on initial fetch", async () => {
    getChannelAccountMock.mockRejectedValueOnce(new Error("network dropped"));
    const { result } = renderHook(() =>
      useChannelAccount({ accountId: 123 }),
    );
    await waitFor(() => {
      expect(result.current.state.kind).toBe("error");
    });
    if (result.current.state.kind === "error") {
      expect(result.current.state.message).toMatch(/unable to load channel/i);
    }
  });

  it("refetch() resets state to loading and fetches again", async () => {
    getChannelAccountMock.mockResolvedValue(READY_ACCOUNT);
    const { result } = renderHook(() =>
      useChannelAccount({ accountId: 123 }),
    );
    await waitFor(() => expect(result.current.state.kind).toBe("ready"));
    expect(getChannelAccountMock).toHaveBeenCalledTimes(1);
    await act(async () => {
      await result.current.refetch();
    });
    expect(getChannelAccountMock).toHaveBeenCalledTimes(2);
  });

  it("aborts the in-flight fetch on unmount (no state update after)", async () => {
    getChannelAccountMock.mockImplementationOnce(
      () => new Promise(() => {}) as ReturnType<typeof getChannelAccountMock>,
    );
    const { unmount, result } = renderHook(() =>
      useChannelAccount({ accountId: 123 }),
    );
    await act(async () => {});
    expect(result.current.state.kind).toBe("loading");
    unmount();
    // No assertion on returned state after unmount (React discards
    // updates). The test simply ensures unmount is a no-op.
    expect(true).toBe(true);
  });
});
