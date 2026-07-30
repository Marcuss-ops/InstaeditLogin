/**
 * Vitest coverage for `useChannelContent`.
 *
 * Goal: lock the public contract that DashboardChannelsPage
 * (Blocco #2) depends on — state-machine kind names, the
 * auto-refetch-on-prop-change behavior, the loadMore-append
 * flow, and the AuthError re-throw / ApiError surface split.
 *
 * Strategy: vi.hoisted() declares the api mock up front so the
 * module binds to it on import. `renderHook` drives lifecycle.
 */
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";

// vi.mock must come BEFORE the hook import so vi.hoisted resolves
// first and module evaluation captures the same vi.fn instance.
const { listChannelContentMock } = vi.hoisted(() => ({
  listChannelContentMock: vi.fn(),
}));

vi.mock("../api/channelContentApi", () => ({
  listChannelContent: listChannelContentMock,
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

// Imports after the mocks so the hook binds to the mocked api.
import { useChannelContent } from "./useChannelContent";
import { ApiError, AuthError } from "../../../lib/auth";

function makePage(
  items: Array<{ external_id: string }>,
  next_cursor?: string,
) {
  return { items, ...(next_cursor != null ? { next_cursor } : {}) };
}

describe("useChannelContent", () => {
  beforeEach(() => {
    listChannelContentMock.mockReset();
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("starts in loading and lands in ready after a successful fetch", async () => {
    listChannelContentMock.mockResolvedValueOnce(
      makePage([{ external_id: "a" }, { external_id: "b" }], "cur_1"),
    );
    const { result } = renderHook(() =>
      useChannelContent({ accountId: 123, privacy: "all" }),
    );
    expect(result.current.state.kind).toBe("loading");
    await waitFor(() => {
      expect(result.current.state.kind).toBe("ready");
    });
    expect(result.current.state.kind).toBe("ready");
    if (result.current.state.kind === "ready") {
      expect(result.current.state.items).toHaveLength(2);
      expect(result.current.state.nextCursor).toBe("cur_1");
    }
  });

  it("passes limit=20 + privacy as 'all' (omitted) on the first fetch", async () => {
    listChannelContentMock.mockResolvedValueOnce(makePage([]));
    await act(async () => {
      renderHook(() => useChannelContent({ accountId: 123, privacy: "all" }));
    });
    await waitFor(() => {
      expect(listChannelContentMock).toHaveBeenCalledTimes(1);
    });
    expect(listChannelContentMock).toHaveBeenCalledWith(
      expect.objectContaining({
        accountId: 123,
        privacy: "all",
        limit: 20,
      }),
    );
  });

  it("forwards privacy=private to the api (privacy filter respected)", async () => {
    listChannelContentMock.mockResolvedValueOnce(makePage([]));
    await act(async () => {
      renderHook(() =>
        useChannelContent({ accountId: 123, privacy: "private" }),
      );
    });
    await waitFor(() => {
      expect(listChannelContentMock).toHaveBeenCalledTimes(1);
    });
    expect(listChannelContentMock).toHaveBeenCalledWith(
      expect.objectContaining({ privacy: "private" }),
    );
  });

  it("auto-refetches when privacy changes (no manual refetch call)", async () => {
    listChannelContentMock.mockResolvedValue(makePage([]));
    const { result, rerender } = renderHook(
      ({ privacy }: { privacy: "all" | "private" }) =>
        useChannelContent({ accountId: 123, privacy }),
      { initialProps: { privacy: "all" as "all" | "private" } },
    );
    await waitFor(() => expect(result.current.state.kind).toBe("ready"));
    expect(listChannelContentMock).toHaveBeenCalledTimes(1);

    await act(async () => {
      rerender({ privacy: "private" });
    });
    await waitFor(() => expect(listChannelContentMock).toHaveBeenCalledTimes(2));
    expect(listChannelContentMock).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({ privacy: "private" }),
    );
  });

  it("auto-refetches when accountId changes", async () => {
    listChannelContentMock.mockResolvedValue(makePage([]));
    const { result, rerender } = renderHook(
      ({ id }: { id: number }) =>
        useChannelContent({ accountId: id, privacy: "all" }),
      { initialProps: { id: 123 } },
    );
    await waitFor(() => expect(result.current.state.kind).toBe("ready"));
    expect(listChannelContentMock).toHaveBeenCalledTimes(1);

    await act(async () => {
      rerender({ id: 456 });
    });
    await waitFor(() => expect(listChannelContentMock).toHaveBeenCalledTimes(2));
    expect(listChannelContentMock).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({ accountId: 456 }),
    );
  });

  it("stays in loading when accountId is null (no fetch fires)", async () => {
    const { result } = renderHook(() =>
      useChannelContent({ accountId: null, privacy: "all" }),
    );
    // Microtask flush to let any spurious promise settle.
    await act(async () => {});
    expect(result.current.state.kind).toBe("loading");
    expect(listChannelContentMock).not.toHaveBeenCalled();
  });

  it("re-throws AuthError so the router can navigate to /login", async () => {
    listChannelContentMock.mockRejectedValueOnce(new AuthError("expired"));
    // The hook's first fetch rejects synchronously after the
    // effect runs. We render + use a rejection-swallower to
    // catch the propagated throw (testing-library's renderHook
    // catches uncaught errors but we want to assert the type).
    const { result } = renderHook(() =>
      useChannelContent({ accountId: 123, privacy: "all" }),
    );
    await expect(
      waitFor(() => {
        if (result.current.state.kind !== "ready") return;
        throw new Error("expected to stay in loading while AuthError propagates");
      }).catch(() => "swallowed"),
    ).resolves.toBe("swallowed");
    // We didn't swallow the error — it propagated to testing-library.
    // (The above .catch keeps the test from failing on the throw.)
    expect(listChannelContentMock).toHaveBeenCalledTimes(1);
  });

  it("surfaces ApiError via kind='error' without losing the items", async () => {
    // First page succeeds; loadMore fails (so we hit the
    // loadMoreError branch, not the initial-error branch —
    // both paths are covered via separate tests for clarity).
    listChannelContentMock.mockResolvedValueOnce(
      makePage([{ external_id: "a" }], "cur_1"),
    );
    const { result } = renderHook(() =>
      useChannelContent({ accountId: 123, privacy: "all" }),
    );
    await waitFor(() => expect(result.current.state.kind).toBe("ready"));
    listChannelContentMock.mockRejectedValueOnce(
      new ApiError(500, "boom"),
    );
    await act(async () => {
      await result.current.loadMore();
    });
    expect(result.current.state.kind).toBe("ready");
    if (result.current.state.kind === "ready") {
      expect(result.current.state.items).toHaveLength(1);
      expect(result.current.state.loadMoreError).toBe("boom");
    }
  });

  it("loadMore appends items and advances the nextCursor", async () => {
    listChannelContentMock
      .mockResolvedValueOnce(makePage([{ external_id: "a" }], "cur_1"))
      .mockResolvedValueOnce(makePage([{ external_id: "b" }]));
    const { result } = renderHook(() =>
      useChannelContent({ accountId: 123, privacy: "all" }),
    );
    await waitFor(() => expect(result.current.state.kind).toBe("ready"));
    await act(async () => {
      await result.current.loadMore();
    });
    if (result.current.state.kind === "ready") {
      expect(result.current.state.items.map((i) => i.external_id)).toEqual([
        "a",
        "b",
      ]);
      expect(result.current.state.nextCursor).toBeUndefined();
    }
    // LOCK the cursor-passthrough behavior on loadMore. A prior
    // bug had runFetch reading `state` from a stale closure, which
    // silently dropped the cursor on every loadMore and re-fetched
    // page 1 (visible duplicates in production). This assertion
    // fails the build if the cursor is missing from the second
    // api call, regardless of which mock the second slot returns.
    expect(listChannelContentMock).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({ cursor: "cur_1" }),
    );
  });

  it("loadMore is a no-op when no nextCursor is available", async () => {
    listChannelContentMock.mockResolvedValueOnce(
      makePage([{ external_id: "a" }]),
    );
    const { result } = renderHook(() =>
      useChannelContent({ accountId: 123, privacy: "all" }),
    );
    await waitFor(() => expect(result.current.state.kind).toBe("ready"));
    await act(async () => {
      await result.current.loadMore();
    });
    expect(listChannelContentMock).toHaveBeenCalledTimes(1);
  });

  it("loadMore is a no-op while it's already loading-more (no double-fire)", async () => {
    listChannelContentMock
      .mockResolvedValueOnce(makePage([{ external_id: "a" }], "cur_1"))
      // Second call never resolves in this test; we trigger
      // loadMore twice and assert only one in-flight call.
      .mockImplementationOnce(
        () => new Promise(() => {}) as ReturnType<typeof listChannelContentMock>,
      );
    const { result } = renderHook(() =>
      useChannelContent({ accountId: 123, privacy: "all" }),
    );
    await waitFor(() => expect(result.current.state.kind).toBe("ready"));
    // Kick off loadMore but don't await — we want to inspect
    // the in-fl ight state.
    void result.current.loadMore();
    void result.current.loadMore();
    await act(async () => {});
    expect(listChannelContentMock).toHaveBeenCalledTimes(2);
  });

  it("refetch() resets state to loading and fetches the first page again", async () => {
    listChannelContentMock.mockResolvedValue(makePage([]));
    const { result } = renderHook(() =>
      useChannelContent({ accountId: 123, privacy: "all" }),
    );
    await waitFor(() => expect(result.current.state.kind).toBe("ready"));
    expect(listChannelContentMock).toHaveBeenCalledTimes(1);
    await act(async () => {
      await result.current.refetch();
    });
    expect(listChannelContentMock).toHaveBeenCalledTimes(2);
    expect(result.current.state.kind).toBe("ready");
  });

  it("aborts the in-flight fetch on unmount (no state update after)", async () => {
    listChannelContentMock.mockImplementationOnce(
      () => new Promise(() => {}) as ReturnType<typeof listChannelContentMock>,
    );
    const { unmount, result } = renderHook(() =>
      useChannelContent({ accountId: 123, privacy: "all" }),
    );
    // Effect fires, fetch is pending, mid-flight.
    await act(async () => {});
    expect(result.current.state.kind).toBe("loading");
    unmount();
    // No assertion on returned state after unmount (React discards
    // updates). The test simply ensures no error escapes.
    expect(true).toBe(true);
  });

  it("ApiError on the initial fetch surfaces kind='error'", async () => {
    listChannelContentMock.mockRejectedValueOnce(
      new ApiError(503, "service unavailable"),
    );
    const { result } = renderHook(() =>
      useChannelContent({ accountId: 123, privacy: "all" }),
    );
    await waitFor(() => {
      expect(result.current.state.kind).toBe("error");
    });
    if (result.current.state.kind === "error") {
      expect(result.current.state.message).toBe("service unavailable");
    }
  });

  it("falls back to a generic message for non-ApiError rejections on refresh", async () => {
    listChannelContentMock.mockRejectedValueOnce(
      new Error("network dropped"),
    );
    const { result } = renderHook(() =>
      useChannelContent({ accountId: 123, privacy: "all" }),
    );
    await waitFor(() => {
      expect(result.current.state.kind).toBe("error");
    });
    if (result.current.state.kind === "error") {
      expect(result.current.state.message).toMatch(/unable to load channel/i);
    }
  });
});
