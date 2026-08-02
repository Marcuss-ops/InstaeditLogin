/**
 * Vitest coverage for usePostTargetStatus.
 *
 * Mocks `../api/postTargetsApi`. Drives the polling loop with
 * `vi.useFakeTimers()` + `vi.advanceTimersByTimeAsync`. The
 * `document.hidden` guard test explicitly toggles the property
 * via `Object.defineProperty` (jsdom's default value is `false`).
 */
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, AuthError } from "../../../lib/auth";
import { usePostTargetStatus } from "./usePostTargetStatus";

const { getPostTargetsMock } = vi.hoisted(() => ({
  getPostTargetsMock: vi.fn(),
}));

vi.mock("../api/postTargetsApi", () => ({
  getPostTargets: getPostTargetsMock,
  retryPostTarget: vi.fn(),
}));

// Helper: an `act` block that flushes a slice of fake timers AND
// awaits any microtasks the timer triggered.
const advance = async (ms: number) => {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
};

afterEach(() => {
  vi.useRealTimers();
  // Reset the document.hidden override in case a test changed it.
  Object.defineProperty(document, "hidden", { configurable: true, value: false });
});

describe("usePostTargetStatus", () => {
  beforeEach(() => {
    getPostTargetsMock.mockReset();
    vi.useFakeTimers();
  });

  it("stays idle when postId is null and fires no fetches", () => {
    const { result } = renderHook(() => usePostTargetStatus(null));
    expect(result.current.status).toBe("idle");
    expect(result.current.targets).toEqual([]);
    expect(getPostTargetsMock).not.toHaveBeenCalled();
  });

  it("fires the initial fetch and transitions loading → polling", async () => {
    getPostTargetsMock.mockResolvedValueOnce([
      { id: 1, post_id: 1, platform_account_id: 9, status: "publishing" },
    ]);
    const { result } = renderHook(() => usePostTargetStatus(1));
    expect(result.current.status).toBe("loading");

    await advance(0); // flush the await-mount microtasks

    expect(result.current.status).toBe("polling");
    expect(result.current.targets).toHaveLength(1);
    expect(getPostTargetsMock).toHaveBeenCalledWith(1);
  });

  it("transitions to terminal when every target is in a terminal state", async () => {
    getPostTargetsMock.mockResolvedValueOnce([
      { id: 1, post_id: 1, platform_account_id: 9, status: "published" },
      { id: 2, post_id: 1, platform_account_id: 9, status: "failed" },
    ]);
    const { result } = renderHook(() => usePostTargetStatus(1));
    await advance(0);

    expect(result.current.status).toBe("terminal");
    expect(result.current.error).toBeNull();
  });

  it("stays in polling when at least one target is still active", async () => {
    getPostTargetsMock.mockResolvedValueOnce([
      { id: 1, post_id: 1, platform_account_id: 9, status: "published" },
      { id: 2, post_id: 1, platform_account_id: 9, status: "publishing" },
    ]);
    const { result } = renderHook(() => usePostTargetStatus(1));
    await advance(0);

    expect(result.current.status).toBe("polling");
  });

  it("treats an empty parent-response as polling (worker hasn't fanned out)", async () => {
    getPostTargetsMock.mockResolvedValueOnce([]);
    const { result } = renderHook(() => usePostTargetStatus(1));
    await advance(0);

    expect(result.current.status).toBe("polling");
  });

  it("captures ApiError and preserves last-known-good targets", async () => {
    getPostTargetsMock.mockResolvedValueOnce([
      { id: 1, post_id: 1, platform_account_id: 9, status: "publishing" },
    ]);
    const { result } = renderHook(() => usePostTargetStatus(1));
    await advance(0);
    expect(result.current.targets).toHaveLength(1);

    getPostTargetsMock.mockRejectedValueOnce(new ApiError(500, "boom"));
    await advance(3000); // next interval tick

    expect(result.current.status).toBe("error");
    expect(result.current.error).toBe("boom");
    // targets[] is sticky across the network blip.
    expect(result.current.targets).toHaveLength(1);
  });

  it("recovers from a transient error and returns to polling", async () => {
    getPostTargetsMock.mockResolvedValueOnce([
      { id: 1, post_id: 1, platform_account_id: 9, status: "publishing" },
    ]);
    const { result } = renderHook(() => usePostTargetStatus(1));
    await advance(0);

    getPostTargetsMock.mockRejectedValueOnce(new ApiError(503, "down"));
    await advance(3000);
    expect(result.current.status).toBe("error");

    getPostTargetsMock.mockResolvedValueOnce([
      { id: 1, post_id: 1, platform_account_id: 9, status: "published" },
    ]);
    await advance(3000);
    expect(result.current.status).toBe("terminal");
    expect(result.current.error).toBeNull();
  });

  it("re-throws AuthError so the caller can navigate to /login", async () => {
    getPostTargetsMock.mockResolvedValueOnce([
      { id: 1, post_id: 1, platform_account_id: 9, status: "queued" },
    ]);
    const { result } = renderHook(() => usePostTargetStatus(1));
    await advance(0);

    getPostTargetsMock.mockRejectedValueOnce(new AuthError());
    let caught: unknown;
    await act(async () => {
      try {
        await result.current.refetch();
      } catch (e) {
        caught = e;
      }
    });
    expect(caught).toBeInstanceOf(AuthError);
  });

  it("skips setInterval ticks while document.hidden=true", async () => {
    getPostTargetsMock.mockResolvedValue([
      { id: 1, post_id: 1, platform_account_id: 9, status: "publishing" },
    ]);
    Object.defineProperty(document, "hidden", { configurable: true, value: false });
    renderHook(() => usePostTargetStatus(1));
    await advance(0);
    expect(getPostTargetsMock).toHaveBeenCalledTimes(1); // initial fetch only

    // Hide the tab; 6 seconds of interval ticks (2 ticks) should produce ZERO extra fetches.
    Object.defineProperty(document, "hidden", { configurable: true, value: true });
    await advance(6_000);
    expect(getPostTargetsMock).toHaveBeenCalledTimes(1);

    // Unhide; the interval is still armed, so it keeps firing.
    Object.defineProperty(document, "hidden", { configurable: true, value: false });
    await advance(6_000);
    // 2 ticks from the unhidden interval. (Initial 1 + 2 = 3.)
    expect(getPostTargetsMock).toHaveBeenCalledTimes(3);
  });

  it("refetch() is exposed and bypasses the interval", async () => {
    getPostTargetsMock.mockResolvedValueOnce([
      { id: 1, post_id: 1, platform_account_id: 9, status: "queued" },
    ]);
    const { result } = renderHook(() => usePostTargetStatus(1));
    await advance(0);

    getPostTargetsMock.mockResolvedValueOnce([
      { id: 1, post_id: 1, platform_account_id: 9, status: "published" },
    ]);
    await act(async () => {
      await result.current.refetch();
    });

    expect(result.current.targets[0].status).toBe("published");
    expect(result.current.status).toBe("terminal");
  });

  it("coalesces overlapping fetches via the in-flight ref", async () => {
    // Never-resolving fetch: the in-flight ref blocks subsequent ticks.
    let resolveFirst: (v: unknown) => void = () => {};
    getPostTargetsMock.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveFirst = resolve;
        }),
    );
    renderHook(() => usePostTargetStatus(1));
    await advance(0); // mount fires the fetch
    expect(getPostTargetsMock).toHaveBeenCalledTimes(1);

    // Advance 3 ticks — the in-flight ref blocks all subsequent ticks
    // because the first fetch never settled.
    await advance(9_000);
    expect(getPostTargetsMock).toHaveBeenCalledTimes(1);

    // Resolve the hung promise.
    await act(async () => {
      resolveFirst([{ id: 1, status: "published" }]);
    });
    // Now the next tick is allowed through.
    getPostTargetsMock.mockResolvedValueOnce([
      { id: 1, post_id: 1, platform_account_id: 9, status: "published" },
    ]);
    await advance(3_000);
    expect(getPostTargetsMock).toHaveBeenCalledTimes(2);
  });

  it("silently swallows AuthError thrown by an interval tick (no unhandled rejection)", async () => {
    // Prime: initial fetch succeeds with one active target so the
    // hook transitions loading → polling.
    getPostTargetsMock.mockResolvedValueOnce([
      { id: 1, post_id: 1, platform_account_id: 9, status: "publishing" },
    ]);
    const { result } = renderHook(() => usePostTargetStatus(1));
    await advance(0);
    expect(result.current.status).toBe("polling");

    // Capture any unhandledrejection that might escape the next
    // tick's AuthError swallow. If the .catch wrapper ever stops
    // working, this listener will record the event and the
    // assertion below will fail.
    const rejections: PromiseRejectionEvent[] = [];
    const handler = (event: PromiseRejectionEvent) => {
      rejections.push(event);
    };
    window.addEventListener("unhandledrejection", handler);

    try {
      // Tick 1 — session-expiry rejection: the .catch must swallow
      // AuthError, leaving hook state untouched (no setError call,
      // no setStatus('error') call).
      getPostTargetsMock.mockRejectedValueOnce(new AuthError());
      await advance(3_000);
      expect(result.current.status).toBe("polling");
      expect(result.current.error).toBeNull();
      expect(result.current.targets[0].status).toBe("publishing");
      expect(rejections).toEqual([]);

      // Tick 2 — session renewed: the interval timer must still
      // be armed. If the swallow had accidentally clearInterval'd,
      // the next fetch would never fire. Use a relative call count
      // so the test stays robust against future refetches (e.g.
      // window-focus refetch, retry button, telemetry) — only the
      // "interval survives the swallow" contract is what's pinned.
      const callsBeforeTick2 = getPostTargetsMock.mock.calls.length;
      getPostTargetsMock.mockResolvedValueOnce([
        { id: 1, post_id: 1, platform_account_id: 9, status: "published" },
      ]);
      await advance(3_000);
      expect(result.current.status).toBe("terminal");
      expect(
        getPostTargetsMock.mock.calls.length,
      ).toBe(callsBeforeTick2 + 1);
      expect(rejections).toEqual([]); // still no leaks
    } finally {
      window.removeEventListener("unhandledrejection", handler);
    }
  });
});
