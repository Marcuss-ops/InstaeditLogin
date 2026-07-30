import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { useToast } from "./useToast";
import { toastBus } from "./toast-bus";

describe("useToast", () => {
  beforeEach(() => {
    // The bus is a module-level singleton, owned by `vitest.setup.ts`'s
    // `afterEach` (which calls `toastBus.__resetForTests()`). The per-file
    // setup here only needs to flip to fake timers; the bus starts clean.
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("exposes success / error / info / warning methods", () => {
    const { result } = renderHook(() => useToast());
    expect(typeof result.current.success).toBe("function");
    expect(typeof result.current.error).toBe("function");
    expect(typeof result.current.info).toBe("function");
    expect(typeof result.current.warning).toBe("function");
  });

  it("success pushes a 'success'-kind toast onto the bus", () => {
    const { result } = renderHook(() => useToast());
    act(() => {
      result.current.success("Saved!");
    });
    const snap = toastBus.getSnapshot();
    expect(snap).toHaveLength(1);
    expect(snap[0]).toMatchObject({ kind: "success", message: "Saved!" });
  });

  it("error pushes an 'error'-kind toast with the default 5s duration", () => {
    const { result } = renderHook(() => useToast());
    act(() => {
      result.current.error("Boom");
    });
    expect(toastBus.__sizeForTests()).toBe(1);
    act(() => {
      vi.advanceTimersByTime(5000);
    });
    expect(toastBus.__sizeForTests()).toBe(0);
  });

  it("info accepts a duration override", () => {
    const { result } = renderHook(() => useToast());
    act(() => {
      result.current.info("Quick", { duration: 1000 });
    });
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(toastBus.__sizeForTests()).toBe(0);
  });

  it("warning pushes a 'warning'-kind toast", () => {
    const { result } = renderHook(() => useToast());
    act(() => {
      result.current.warning("Watch out");
    });
    expect(toastBus.getSnapshot()[0]).toMatchObject({
      kind: "warning",
      message: "Watch out",
    });
  });

  it("returns a stable reference across renders (no churn in deps)", () => {
    const { result, rerender } = renderHook(() => useToast());
    const first = result.current;
    rerender();
    const second = result.current;
    // The bus-bound methods are stable; the returned object identity
    // is preserved so useCallback dep arrays don't change every render.
    expect(first).toBe(second);
  });
});
