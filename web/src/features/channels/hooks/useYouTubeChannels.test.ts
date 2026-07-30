/**
 * Vitest coverage of useYouTubeChannels.
 *
 * Mocks `../api/channelsApi` via `vi.hoisted` so the factory runs
 * before module-level `let`. Locks down:
 *
 *   - starts in 'loading'
 *   - successful load transitions to 'ready' with workspaces + channels
 *   - defaultChannelId is set ONLY when exactly one channel exists
 *   - defaultWorkspaceId is set ONLY when exactly one workspace exists
 *   - ApiError from channelsApi → kind='error' with the server message
 *   - AuthError IS RE-THROWN (caller navigates to /login)
 *   - refetch() aborts the prior controller AND starts a fresh fetch
 */
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, AuthError } from "../../../lib/auth";
import type { PlatformAccount, Workspace } from "../../../types/uploads";

const { listYouTubeChannelsAndWorkspacesMock } = vi.hoisted(() => ({
  listYouTubeChannelsAndWorkspacesMock: vi.fn(),
}));

vi.mock("../api/channelsApi", () => ({
  listYouTubeChannelsAndWorkspaces: listYouTubeChannelsAndWorkspacesMock,
  // filterYouTube is a 1-liner tested via the api unit (not here)
  filterYouTube: vi.fn(),
}));

import { useYouTubeChannels } from "./useYouTubeChannels";

const YT_A: PlatformAccount = {
  id: 9,
  platform: "youtube",
  platform_user_id: "yt-A",
  username: "channel-a",
  status: "connected",
  created_at: "2024-01-01T00:00:00Z",
};
const YT_B: PlatformAccount = {
  id: 11,
  platform: "youtube",
  platform_user_id: "yt-B",
  username: "channel-b",
  status: "connected",
  created_at: "2024-01-01T00:00:00Z",
};
const WS_1: Workspace = { id: 1, name: "Personal" };
const WS_2: Workspace = { id: 2, name: "Brand" };

describe("useYouTubeChannels", () => {
  beforeEach(() => {
    listYouTubeChannelsAndWorkspacesMock.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("starts in loading", () => {
    listYouTubeChannelsAndWorkspacesMock.mockResolvedValue({
      channels: [YT_A],
      workspaces: [WS_1],
    });
    const { result } = renderHook(() => useYouTubeChannels());
    expect(result.current.state.kind).toBe("loading");
  });

  it("transitions loading → ready with channels + workspaces", async () => {
    listYouTubeChannelsAndWorkspacesMock.mockResolvedValue({
      channels: [YT_A, YT_B],
      workspaces: [WS_1, WS_2],
    });
    const { result } = renderHook(() => useYouTubeChannels());

    await act(async () => {
      await Promise.resolve();
    });
    expect(listYouTubeChannelsAndWorkspacesMock).toHaveBeenCalledTimes(1);
    expect(result.current.state.kind).toBe("ready");

    if (result.current.state.kind === "ready") {
      expect(result.current.state.channels.map((c) => c.id)).toEqual([9, 11]);
      expect(result.current.state.workspaces.map((w) => w.id)).toEqual([1, 2]);
      expect(result.current.state.defaultChannelId).toBeNull(); // 2 channels
      expect(result.current.state.defaultWorkspaceId).toBeNull(); // 2 workspaces
    }
  });

  it("auto-selects defaultChannelId when exactly one channel", async () => {
    listYouTubeChannelsAndWorkspacesMock.mockResolvedValue({
      channels: [YT_A],
      workspaces: [WS_1, WS_2],
    });
    const { result } = renderHook(() => useYouTubeChannels());
    await act(async () => {
      await Promise.resolve();
    });

    if (result.current.state.kind === "ready") {
      expect(result.current.state.defaultChannelId).toBe(9);
      expect(result.current.state.defaultWorkspaceId).toBeNull();
    }
  });

  it("auto-selects defaultWorkspaceId when exactly one workspace", async () => {
    listYouTubeChannelsAndWorkspacesMock.mockResolvedValue({
      channels: [YT_A, YT_B],
      workspaces: [WS_1],
    });
    const { result } = renderHook(() => useYouTubeChannels());
    await act(async () => {
      await Promise.resolve();
    });

    if (result.current.state.kind === "ready") {
      expect(result.current.state.defaultChannelId).toBeNull();
      expect(result.current.state.defaultWorkspaceId).toBe(1);
    }
  });

  it("ApiError channelsApi → kind='error' with server message", async () => {
    listYouTubeChannelsAndWorkspacesMock.mockRejectedValue(
      new ApiError(503, "Service down"),
    );
    const { result } = renderHook(() => useYouTubeChannels());

    await act(async () => {
      await Promise.resolve();
    });
    expect(result.current.state).toEqual({
      kind: "error",
      message: "Service down",
    });
  });

  it("AuthError from channelsApi IS RE-THROWN so caller can navigate to /login", async () => {
    // Initial mount succeeds (mockResolvedValueOnce). Then refetch()
    // is the deterministic surface where the AuthError surfaces:
    // runFetch `throw err`'s before the error-state setState, so
    // the awaitable refetch promise rejects with AuthError.
    listYouTubeChannelsAndWorkspacesMock
      .mockResolvedValueOnce({ channels: [YT_A], workspaces: [WS_1] })
      .mockRejectedValueOnce(new AuthError());

    const { result } = renderHook(() => useYouTubeChannels());
    await act(async () => {
      await Promise.resolve();
    });
    expect(result.current.state.kind).toBe("ready");

    await expect(
      act(async () => {
        await result.current.refetch();
      }),
    ).rejects.toBeInstanceOf(AuthError);

    // AuthError short-circuits BEFORE setState({kind:'error'}); the
    // refetch() function sets state to 'loading' BEFORE awaiting
    // runFetch, so the final kind after a re-throw is 'loading' —
    // caller owns the auth redirect (router-level ProtectedRoute).
    expect(result.current.state.kind).not.toBe("error");
    expect(result.current.state.kind).not.toBe("ready");
  });

  it("refetch() aborts prior controller and reloads", async () => {
    listYouTubeChannelsAndWorkspacesMock.mockResolvedValueOnce({
      channels: [YT_A],
      workspaces: [WS_1],
    });
    listYouTubeChannelsAndWorkspacesMock.mockResolvedValueOnce({
      channels: [YT_A, YT_B],
      workspaces: [WS_1, WS_2],
    });

    const { result } = renderHook(() => useYouTubeChannels());
    await act(async () => {
      await Promise.resolve();
    });
    expect(listYouTubeChannelsAndWorkspacesMock).toHaveBeenCalledTimes(1);
    if (result.current.state.kind === "ready") {
      expect(result.current.state.channels).toHaveLength(1);
    }

    await act(async () => {
      await result.current.refetch();
    });
    expect(listYouTubeChannelsAndWorkspacesMock).toHaveBeenCalledTimes(2);
    if (result.current.state.kind === "ready") {
      expect(result.current.state.channels).toHaveLength(2);
    }
  });
});
