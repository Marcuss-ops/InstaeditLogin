import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";

const { authedFetchMock, navigateMock } = vi.hoisted(() => ({
  authedFetchMock: vi.fn(),
  navigateMock: vi.fn(),
}));

vi.mock("../../lib/auth", () => ({
  authedFetch: authedFetchMock,
  AuthError: class AuthError extends Error {
    override name = "AuthError";
  },
}));

vi.mock("react-router-dom", async (importOriginal) => ({
  ...(await importOriginal<typeof import("react-router-dom")>()),
  useNavigate: () => navigateMock,
}));

import { AuthError } from "../../lib/auth";
import { useAccountDetailsData } from "./useAccountDetailsData";

const ACCOUNT = {
  id: 7,
  platform: "youtube" as const,
  platform_user_id: "channel-7",
  username: "demo-channel",
  status: "active",
  created_at: "2026-01-01T00:00:00.000Z",
};

function jsonResponse(body: unknown): Response {
  return { json: async () => body } as Response;
}

describe("useAccountDetailsData", () => {
  beforeEach(() => {
    authedFetchMock.mockReset();
    navigateMock.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("loads the account and exposes ready state", async () => {
    authedFetchMock.mockResolvedValueOnce(jsonResponse(ACCOUNT));

    const { result } = renderHook(() => useAccountDetailsData("7"));

    expect(result.current.state.kind).toBe("loading");
    await waitFor(() => expect(result.current.state.kind).toBe("ready"));
    expect(authedFetchMock).toHaveBeenCalledWith(
      "/api/v1/accounts/7",
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
    if (result.current.state.kind === "ready") {
      expect(result.current.state.account).toEqual(ACCOUNT);
    }
  });

  it("redirects AuthError failures to login", async () => {
    authedFetchMock.mockRejectedValueOnce(new AuthError("expired"));

    renderHook(() => useAccountDetailsData("7"));

    await waitFor(() => {
      expect(navigateMock).toHaveBeenCalledWith("/login", { replace: true });
    });
  });

  it("surfaces ordinary account failures and synchronizes through the same loader", async () => {
    authedFetchMock
      .mockResolvedValueOnce(jsonResponse(ACCOUNT))
      .mockResolvedValueOnce(jsonResponse({ ok: true }))
      .mockResolvedValueOnce(jsonResponse({ ...ACCOUNT, username: "synced-channel" }));

    const { result } = renderHook(() => useAccountDetailsData("7"));
    await waitFor(() => expect(result.current.state.kind).toBe("ready"));

    await act(async () => {
      await result.current.handleSync();
    });

    expect(authedFetchMock.mock.calls.map(([path]) => path)).toEqual([
      "/api/v1/accounts/7",
      "/api/v1/accounts/7/sync",
      "/api/v1/accounts/7",
    ]);
    await waitFor(() => {
      expect(result.current.state).toMatchObject({
        kind: "ready",
        account: { username: "synced-channel" },
      });
    });
    expect(result.current.syncing).toBe(false);
  });

  it("aborts the account request when unmounted", async () => {
    let capturedSignal: AbortSignal | undefined;
    authedFetchMock.mockImplementationOnce(
      (_path: string, init: RequestInit) => {
        capturedSignal = init.signal as AbortSignal;
        return new Promise<Response>(() => {});
      },
    );

    const { unmount } = renderHook(() => useAccountDetailsData("7"));
    await act(async () => {});
    expect(capturedSignal?.aborted).toBe(false);

    unmount();

    expect(capturedSignal?.aborted).toBe(true);
  });

  it("uses a generic message for non-Error failures", async () => {
    authedFetchMock.mockRejectedValueOnce("network failure");

    const { result } = renderHook(() => useAccountDetailsData("7"));

    await waitFor(() => expect(result.current.state.kind).toBe("error"));
    expect(result.current.state).toEqual({
      kind: "error",
      message: "Unable to load account.",
    });
  });
});
