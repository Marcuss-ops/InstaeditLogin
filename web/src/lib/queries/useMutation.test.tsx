import { afterEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { useMutation } from "./useMutation";

afterEach(() => {
  vi.useRealTimers();
});

describe("useMutation", () => {
  it("mutate resolves with data and transitions loading + data state", async () => {
    const fetcher = vi.fn(async () => ({ id: 1 }));
    const { result } = renderHook(() => useMutation<{ id: number }, void>(fetcher));
    expect(result.current.loading).toBe(false);

    const data = await act(async () => {
      return await result.current.mutate();
    });
    expect(data).toEqual({ id: 1 });
    expect(result.current.loading).toBe(false);
    expect(result.current.data).toEqual({ id: 1 });
    expect(result.current.error).toBeUndefined();
  });

  it("captures errors and surfaces them in error state", async () => {
    const fetcher = vi.fn(async () => {
      throw new Error("mut-fail");
    });
    const { result } = renderHook(() => useMutation<never, void>(fetcher));

    await act(async () => {
      await result.current.mutate().catch(() => undefined);
    });
    expect(result.current.error?.message).toBe("mut-fail");
    expect(result.current.loading).toBe(false);
  });

  it("aborts the prior mutate when a new mutate fires (rapid double-click)", async () => {
    let firstSignal: AbortSignal | undefined;
    let secondSignal: AbortSignal | undefined;
    const blockingFetcher = vi.fn(
      async (
        _vars: { tag: string },
        sig: AbortSignal,
      ): Promise<{ ok: boolean }> => {
        if (!firstSignal) firstSignal = sig;
        else secondSignal = sig;
        // Hang until the signal aborts; failing on cancel is intentional
        // and mirrors a slow network POST.
        return await new Promise<{ ok: boolean }>((_, reject) => {
          sig.addEventListener(
            "abort",
            () => reject(new DOMException("aborted", "AbortError")),
            { once: true },
          );
        });
      },
    );
    const { result } = renderHook(() =>
      useMutation<{ ok: boolean }, { tag: string }>(blockingFetcher),
    );

    // Fire first mutate (in-flight, intentionally never completes).
    void result.current.mutate({ tag: "first" }).catch(() => undefined);
    await act(async () => {
      await new Promise<void>((r) => setTimeout(r, 5));
    });

    expect(firstSignal).toBeDefined();
    expect(firstSignal!.aborted).toBe(false);

    // Fire second mutate — it will abort the first and create a new
    // controller. We do NOT await this: the second's fetcher also hangs
    // forever, so awaiting would deadlock. We assert the side-effect
    // (first signal aborted, fetcher called twice).
    void result.current.mutate({ tag: "second" }).catch(() => undefined);
    await act(async () => {
      await new Promise<void>((r) => setTimeout(r, 5));
    });

    expect(firstSignal!.aborted).toBe(true);
    expect(blockingFetcher).toHaveBeenCalledTimes(2);
    expect(secondSignal).toBeDefined();
    expect(secondSignal!.aborted).toBe(false);
  });

  it("reset() clears state and aborts any in-flight mutation", async () => {
    let captured: AbortSignal | undefined;
    const fetcher = vi.fn(
      async (_vars: { x: number }, sig: AbortSignal): Promise<{ ok: boolean }> => {
        captured = sig;
        return new Promise<{ ok: boolean }>(() => undefined);
      },
    );
    const { result } = renderHook(() =>
      useMutation<{ ok: boolean }, { x: number }>(fetcher),
    );

    void result.current.mutate({ x: 1 }).catch(() => undefined);
    await act(async () => {
      await new Promise<void>((r) => setTimeout(r, 5));
    });

    act(() => result.current.reset());
    expect(captured!.aborted).toBe(true);
    expect(result.current.loading).toBe(false);
    expect(result.current.data).toBeUndefined();
    expect(result.current.error).toBeUndefined();
  });

  it("aborts in-flight mutation on unmount", async () => {
    let captured: AbortSignal | undefined;
    const fetcher = vi.fn(
      async (_vars: { x: number }, sig: AbortSignal): Promise<{ ok: boolean }> => {
        captured = sig;
        return new Promise<{ ok: boolean }>(() => undefined);
      },
    );
    const { result, unmount } = renderHook(() =>
      useMutation<{ ok: boolean }, { x: number }>(fetcher),
    );

    void result.current.mutate({ x: 1 }).catch(() => undefined);
    await act(async () => {
      await new Promise<void>((r) => setTimeout(r, 5));
    });

    unmount();
    expect(captured!.aborted).toBe(true);
  });
});
