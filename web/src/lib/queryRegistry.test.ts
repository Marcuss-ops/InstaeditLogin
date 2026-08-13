import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { clearSharedQueryCache, invalidateSharedQueries, useSharedQuery } from "./queryRegistry";

async function flush(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

afterEach(() => {
  clearSharedQueryCache();
  vi.useRealTimers();
  Object.defineProperty(document, "hidden", { configurable: true, value: false });
  Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
});

describe("useSharedQuery", () => {
  it("deduplicates in-flight requests and shares cached data", async () => {
    const fetcher = vi.fn().mockResolvedValue({ value: 1 });
    const first = renderHook(() => useSharedQuery("accounts", {
      staleTime: 60_000,
      fetcher,
    }));
    const second = renderHook(() => useSharedQuery("accounts", {
      staleTime: 60_000,
      fetcher,
    }));

    await flush();
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(first.result.current.data).toEqual({ value: 1 });
    expect(second.result.current.data).toEqual({ value: 1 });
    first.unmount();
    second.unmount();
  });

  it("does not refetch fresh data until staleTime expires", async () => {
    vi.useFakeTimers();
    const fetcher = vi.fn().mockResolvedValue(1);
    const { result } = renderHook(() => useSharedQuery("workspace", {
      staleTime: 1_000,
      fetcher,
    }));
    await flush();
    expect(result.current.data).toBe(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(900); });
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("polls adaptively and stops when the predicate returns null", async () => {
    vi.useFakeTimers();
    const fetcher = vi.fn()
      .mockResolvedValueOnce({ active: true })
      .mockResolvedValueOnce({ active: false });
    const { result } = renderHook(() => useSharedQuery("job", {
      staleTime: 0,
      pollingInterval: (data) => data?.active ? 100 : null,
      fetcher,
    }));
    await flush();
    expect(result.current.data).toEqual({ active: true });
    await act(async () => { await vi.advanceTimersByTimeAsync(100); });
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(result.current.data).toEqual({ active: false });
    await act(async () => { await vi.advanceTimersByTimeAsync(500); });
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("refetches on focus only when explicitly enabled", async () => {
    const focusedFetcher = vi.fn().mockResolvedValue(1);
    const defaultFetcher = vi.fn().mockResolvedValue(1);
    renderHook(() => useSharedQuery("focus-enabled", {
      staleTime: 60_000,
      refetchOnWindowFocus: true,
      fetcher: focusedFetcher,
    }));
    renderHook(() => useSharedQuery("focus-default", {
      staleTime: 60_000,
      fetcher: defaultFetcher,
    }));
    await flush();
    expect(focusedFetcher).toHaveBeenCalledTimes(1);
    expect(defaultFetcher).toHaveBeenCalledTimes(1);

    await act(async () => {
      window.dispatchEvent(new Event("focus"));
      await Promise.resolve();
    });
    expect(focusedFetcher).toHaveBeenCalledTimes(2);
    expect(defaultFetcher).toHaveBeenCalledTimes(1);
  });

  it("skips hidden-tab polling and refetches on visibility restore", async () => {
    vi.useFakeTimers();
    const fetcher = vi.fn().mockResolvedValue(1);
    renderHook(() => useSharedQuery("hidden", {
      staleTime: 0,
      pollingInterval: 100,
      fetcher,
    }));
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(1);

    Object.defineProperty(document, "hidden", { configurable: true, value: true });
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
    await act(async () => { await vi.advanceTimersByTimeAsync(500); });
    expect(fetcher).toHaveBeenCalledTimes(1);

    Object.defineProperty(document, "hidden", { configurable: true, value: false });
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
      await Promise.resolve();
    });
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
});

describe("invalidateSharedQueries", () => {
  it("force-refetches the exact key and any sub-key prefix, bypassing staleTime", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce("first")
      .mockResolvedValueOnce("first")
      .mockResolvedValue("refetched");
    const exact = renderHook(() => useSharedQuery("groups:7:youtube:videos", {
      staleTime: 60_000,
      fetcher,
    }));
    const child = renderHook(() => useSharedQuery("groups:7:youtube:videos:extra", {
      staleTime: 60_000,
      fetcher,
    }));
    await flush();
    expect(exact.result.current.data).toBe("first");
    expect(child.result.current.data).toBe("first");
    expect(fetcher).toHaveBeenCalledTimes(2);

    await act(async () => {
      invalidateSharedQueries("groups:7:youtube:videos");
      await Promise.resolve();
    });
    // Both entries refetch even though their staleTime (60s) is far
    // from expiring — invalidation is a forced refresh.
    expect(fetcher).toHaveBeenCalledTimes(4);
    expect(exact.result.current.data).toBe("refetched");
    expect(child.result.current.data).toBe("refetched");
    exact.unmount();
    child.unmount();
  });

  it("does NOT touch unrelated cache keys", async () => {
    const targetFetcher = vi.fn().mockResolvedValue(1);
    const otherFetcher = vi.fn().mockResolvedValue(2);
    const target = renderHook(() => useSharedQuery("groups:7:youtube:videos", {
      staleTime: 60_000,
      fetcher: targetFetcher,
    }));
    const unrelated = renderHook(() => useSharedQuery("groups:8:youtube:videos", {
      staleTime: 60_000,
      fetcher: otherFetcher,
    }));
    const different = renderHook(() => useSharedQuery("accounts", {
      staleTime: 60_000,
      fetcher: otherFetcher,
    }));
    await flush();
    expect(targetFetcher).toHaveBeenCalledTimes(1);
    expect(otherFetcher).toHaveBeenCalledTimes(2);

    await act(async () => {
      invalidateSharedQueries("groups:7:youtube:videos");
      await Promise.resolve();
    });
    expect(targetFetcher).toHaveBeenCalledTimes(2);
    expect(otherFetcher).toHaveBeenCalledTimes(2);
    target.unmount();
    unrelated.unmount();
    different.unmount();
  });

  it("is a no-op for unknown keys and expires data-only entries for the next subscriber", async () => {
    const fetcher = vi.fn().mockResolvedValue("cached");
    const first = renderHook(() => useSharedQuery("groups:7:youtube:videos", {
      staleTime: 60_000,
      fetcher,
    }));
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(1);
    first.unmount();

    // No listeners left — the entry still holds cached data.
    await act(async () => {
      invalidateSharedQueries("unknown:key");
      await Promise.resolve();
    });
    expect(fetcher).toHaveBeenCalledTimes(1);

    // Invalidating the real key while no one is subscribed expires the
    // snapshot, so the next mount re-fetches instead of serving the
    // still-fresh cached value.
    await act(async () => {
      invalidateSharedQueries("groups:7:youtube:videos");
      await Promise.resolve();
    });
    const second = renderHook(() => useSharedQuery("groups:7:youtube:videos", {
      staleTime: 60_000,
      fetcher,
    }));
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(2);
    second.unmount();
  });
});
