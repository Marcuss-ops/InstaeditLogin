import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { clearSharedQueryCache, useSharedQuery } from "./queryRegistry";

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
