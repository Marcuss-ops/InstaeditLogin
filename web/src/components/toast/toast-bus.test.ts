import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { toastBus } from "./toast-bus";

describe("toastBus", () => {
  beforeEach(() => {
    // The bus is a module-level singleton, owned by `vitest.setup.ts`'s
    // `afterEach` (which calls `toastBus.__resetForTests()`). The per-file
    // setup here only needs to flip to fake timers; the bus starts clean.
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("starts empty and reports zero size", () => {
    expect(toastBus.__sizeForTests()).toBe(0);
    expect(toastBus.getSnapshot()).toEqual([]);
  });

  it("push creates an entry and notifies subscribers", () => {
    const listener = vi.fn();
    const unsub = toastBus.subscribe(listener);

    const id = toastBus.push("success", "Hello", 5000);
    expect(typeof id).toBe("string");
    expect(id.length).toBeGreaterThan(0);

    const snapshot = toastBus.getSnapshot();
    expect(snapshot).toHaveLength(1);
    expect(snapshot[0]).toMatchObject({
      kind: "success",
      message: "Hello",
    });
    expect(typeof snapshot[0].createdAt).toBe("number");
    expect(listener).toHaveBeenCalledTimes(1);
    unsub();
  });

  it("auto-dismisses after the configured duration", () => {
    toastBus.push("info", "transient", 5000);
    expect(toastBus.__sizeForTests()).toBe(1);
    vi.advanceTimersByTime(4999);
    expect(toastBus.__sizeForTests()).toBe(1);
    vi.advanceTimersByTime(1);
    expect(toastBus.__sizeForTests()).toBe(0);
  });

  it("respects an explicit override duration", () => {
    toastBus.push("warning", "quick", 2000);
    vi.advanceTimersByTime(1999);
    expect(toastBus.__sizeForTests()).toBe(1);
    vi.advanceTimersByTime(1);
    expect(toastBus.__sizeForTests()).toBe(0);
  });

  it("uses 5000ms as the default duration when none is supplied", () => {
    toastBus.push("info", "default-5s");
    vi.advanceTimersByTime(5000);
    expect(toastBus.__sizeForTests()).toBe(0);
  });

  it("dedupes an identical (kind, message) push within the flood window", () => {
    // The two pushes are ~0ms apart in fake-timer world; DEDUPE_WINDOW_MS
    // is 1500ms so the second one collapses onto the first.
    toastBus.push("error", "Network unreachable");
    const secondId = toastBus.push("error", "Network unreachable");
    expect(toastBus.__sizeForTests()).toBe(1);
    // Dedupe returns the *existing* entry's id so callers don't get
    // a phantom id for an entry that was never created.
    const existing = toastBus.getSnapshot()[0];
    expect(secondId).toBe(existing.id);
  });

  it("does NOT dedupe different variants with identical messages", () => {
    toastBus.push("info", "Same text");
    toastBus.push("warning", "Same text");
    expect(toastBus.__sizeForTests()).toBe(2);
  });

  it("explicit dismiss by id removes a single entry", () => {
    const id = toastBus.push("success", "Hello");
    toastBus.push("success", "World");
    toastBus.dismiss(id);
    expect(toastBus.__sizeForTests()).toBe(1);
    expect(toastBus.getSnapshot()[0].message).toBe("World");
  });

  it("dismiss by unknown id is a silent no-op", () => {
    toastBus.push("success", "Hello");
    toastBus.dismiss("not-a-real-id");
    expect(toastBus.__sizeForTests()).toBe(1);
  });

  it("dismissAll empties the queue", () => {
    toastBus.push("success", "a");
    toastBus.push("error", "b");
    toastBus.push("info", "c");
    toastBus.dismissAll();
    expect(toastBus.__sizeForTests()).toBe(0);
  });

  it("subscribed listener fires on push AND on non-empty dismissAll", () => {
    const listener = vi.fn();
    toastBus.subscribe(listener);
    // 3 notifs: push (queue 0→1), dismissAll (queue 1→0), push (queue 0→1).
    toastBus.push("info", "1");
    toastBus.dismissAll();
    toastBus.push("info", "2");
    expect(listener).toHaveBeenCalledTimes(3);
  });

  it("dismissAll on an already-empty queue is silent (no listener notification)", () => {
    const listener = vi.fn();
    toastBus.subscribe(listener);
    // Queue is empty after the beforeEach reset; a defensive optimization
    // skips the notify() call to avoid spurious subscriber work.
    toastBus.dismissAll();
    expect(listener).not.toHaveBeenCalled();
    // Sanity: a fresh push afterward still notifies normally.
    toastBus.push("info", "after-empty-dismiss");
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it("subscribe returns an unsubscribe that stops notifications", () => {
    const listener = vi.fn();
    const unsub = toastBus.subscribe(listener);
    toastBus.push("success", "1");
    expect(listener).toHaveBeenCalledTimes(1);
    unsub();
    toastBus.push("success", "2");
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it("getSnapshot callers can mutate the returned array without breaking bus operations", () => {
    // `useSyncExternalStore` requires the snapshot to be referentially
    // stable across unchanged reads (else React re-renders forever).
    // We serve this contract by holding a single `cachedSnapshot`
    // reference (see toast-bus.ts) and rebuilding it from
    // `entries.slice()` on every notify. Callers who treat the
    // snapshot as read-only is the IDEAL; this test pins the SAFETY
    // guarantee (subsequent operations work and the corruption is
    // overwritten on the next notify).
    toastBus.push("info", "x");
    const a = toastBus.getSnapshot();
    expect(a).toHaveLength(1);
    expect(a[0].message).toBe("x");

    // Caller mutates freely:
    a.length = 0;
    a.push({
      id: "fake",
      kind: "success",
      message: "bogus",
      createdAt: 0,
    });

    // The bus's source-of-truth `entries` array is unchanged.
    expect(toastBus.__sizeForTests()).toBe(1);

    // The next notify rebuilds `cachedSnapshot` from `entries.slice()`,
    // discarding the caller's local mutation.
    toastBus.push("info", "y");
    expect(toastBus.__sizeForTests()).toBe(2);
    expect(toastBus.getSnapshot().map((e) => e.message)).toEqual(["x", "y"]);
  });
});
