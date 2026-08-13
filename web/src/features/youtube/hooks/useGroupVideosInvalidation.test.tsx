/**
 * Tests for useGroupVideosInvalidation — the group-scoped cache
 * invalidation bus for `['groups', groupId, 'youtube', 'videos']`.
 *
 * jsdom (vitest's default browser shim) does NOT implement
 * `BroadcastChannel` natively. We stub it via `vi.stubGlobal` with a
 * deterministic double so each test controls exactly what onmessage
 * fires, plus a throw-on-construction toggle for the degradation path.
 *
 * The CustomEvent + window listener path runs in real jsdom.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import { StrictMode } from "react";

const { invalidateSharedQueriesMock } = vi.hoisted(() => ({
  invalidateSharedQueriesMock: vi.fn(),
}));

vi.mock("../../../lib/queryRegistry", () => ({
  invalidateSharedQueries: invalidateSharedQueriesMock,
}));

import {
  __test,
  groupVideosQueryKey,
  groupVideosQueryKeyString,
  invalidateGroupVideos,
  useGroupVideosInvalidation,
} from "./useGroupVideosInvalidation";

// ─── Stub BroadcastChannel ────────────────────────────────────────────
class StubBroadcastChannel {
  static instances: StubBroadcastChannel[] = [];
  static throwOnConstruction = false;
  readonly name: string;
  onmessage: ((e: MessageEvent<unknown>) => void) | null = null;
  readonly posted: unknown[] = [];
  closed = false;
  constructor(name: string) {
    if (StubBroadcastChannel.throwOnConstruction) {
      throw new DOMException("BroadcastChannel denied", "SecurityError");
    }
    this.name = name;
    StubBroadcastChannel.instances.push(this);
  }
  postMessage = (data: unknown): void => {
    this.posted.push(data);
  };
  close = (): void => {
    this.closed = true;
  };
  // Test-only helper to simulate ANOTHER tab sending a message.
  emit = (data: unknown): void => {
    this.onmessage?.(new MessageEvent("message", { data }));
  };
}

function mount(groupId: number | null, onChange: () => void) {
  return renderHook(
    ({ id, fn }: { id: number | null; fn: () => void }) =>
      useGroupVideosInvalidation(id, () => fn()),
    {
      initialProps: { id: groupId, fn: onChange },
    },
  );
}

beforeEach(() => {
  __test.reset();
  invalidateSharedQueriesMock.mockReset();
  vi.stubGlobal("BroadcastChannel", StubBroadcastChannel);
  StubBroadcastChannel.instances = [];
  StubBroadcastChannel.throwOnConstruction = false;
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("query key contract", () => {
  it("exposes the canonical ['groups', groupId, 'youtube', 'videos'] key", () => {
    expect(groupVideosQueryKey(7)).toEqual(["groups", "7", "youtube", "videos"]);
    expect(groupVideosQueryKey(123)).toEqual(["groups", "123", "youtube", "videos"]);
  });

  it("flattens the key to groups:{groupId}:youtube:videos", () => {
    expect(groupVideosQueryKeyString(7)).toBe("groups:7:youtube:videos");
  });
});

describe("useGroupVideosInvalidation — singleton creation", () => {
  it("creates exactly ONE BroadcastChannel across many hook mounts", () => {
    mount(1, () => {});
    mount(1, () => {});
    mount(2, () => {});
    mount(3, () => {});
    expect(StubBroadcastChannel.instances.length).toBe(1);
    expect(StubBroadcastChannel.instances[0]?.name).toBe(__test.channelName());
    expect(__test.channelName()).toBe("instaedit-group-videos");
  });

  it("installs the same-tab window listener exactly once", () => {
    const addSpy = vi.spyOn(window, "addEventListener");
    mount(1, () => {});
    mount(2, () => {});
    const installs = addSpy.mock.calls.filter(
      ([name]) => name === __test.windowEventName(),
    );
    expect(installs.length).toBe(1);
    addSpy.mockRestore();
  });

  it("is idempotent under StrictMode (one BC across double-mount)", () => {
    function Probe() {
      useGroupVideosInvalidation(1, () => {});
      return null;
    }
    renderHook(() => Probe(), { wrapper: StrictMode });
    expect(StubBroadcastChannel.instances.length).toBe(1);
  });

  it("degrades gracefully when BroadcastChannel throws on construction", () => {
    StubBroadcastChannel.throwOnConstruction = true;
    // Should NOT throw — the CustomEvent-only path still works.
    mount(1, () => {});
    expect(StubBroadcastChannel.instances.length).toBe(0);
  });

  it("degrades gracefully when BroadcastChannel is undefined entirely", () => {
    vi.stubGlobal("BroadcastChannel", undefined);
    mount(1, () => {});
    expect(StubBroadcastChannel.instances.length).toBe(0);
  });
});

describe("useGroupVideosInvalidation — same-tab fan-out", () => {
  it("fires the handler when invalidateGroupVideos is called for the same group", () => {
    const onChange = vi.fn();
    mount(7, () => onChange());
    invalidateGroupVideos(7);
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  it("records the BC postMessage on same-tab dispatch (no echo to sender)", () => {
    const onChange = vi.fn();
    mount(11, () => onChange());
    invalidateGroupVideos(11);
    // CustomEvent fired → handler called once. BC.postMessage is
    // recorded but does NOT trigger onmessage on the same tab.
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(StubBroadcastChannel.instances[0]?.posted).toHaveLength(1);
    expect(StubBroadcastChannel.instances[0]?.posted[0]).toMatchObject({
      type: "group-videos-invalidated",
      group_id: 11,
    });
  });

  it("delivers via CustomEvent even when BC is missing", () => {
    vi.stubGlobal("BroadcastChannel", undefined);
    const onChange = vi.fn();
    mount(77, () => onChange());
    invalidateGroupVideos(77);
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  it("delivers via CustomEvent even when BC throws during construction", () => {
    StubBroadcastChannel.throwOnConstruction = true;
    const onChange = vi.fn();
    mount(78, () => onChange());
    invalidateGroupVideos(78);
    expect(onChange).toHaveBeenCalledTimes(1);
  });
});

describe("useGroupVideosInvalidation — cross-tab fan-out", () => {
  it("fans out a BroadcastChannel message (other tab) to the subscriber", () => {
    const onChange = vi.fn();
    mount(42, () => onChange());
    StubBroadcastChannel.instances[0]?.emit({
      type: "group-videos-invalidated",
      group_id: 42,
    });
    expect(onChange).toHaveBeenCalledTimes(1);
  });
});

describe("useGroupVideosInvalidation — multi-group isolation", () => {
  it("does NOT fan out across different group ids", () => {
    const onA = vi.fn();
    const onB = vi.fn();
    mount(100, () => onA());
    mount(200, () => onB());
    invalidateGroupVideos(100);
    expect(onA).toHaveBeenCalledTimes(1);
    expect(onB).not.toHaveBeenCalled();
  });
});

describe("useGroupVideosInvalidation — payload validation", () => {
  it("ignores payloads with the wrong type tag", () => {
    const onChange = vi.fn();
    mount(5, () => onChange());
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    StubBroadcastChannel.instances[0]?.emit({ type: "different-event", group_id: 5 } as any);
    expect(onChange).not.toHaveBeenCalled();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    StubBroadcastChannel.instances[0]?.emit({ type: "group-videos-invalidated", group_id: 5 } as any);
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  it("ignores payloads with non-numeric / negative / zero group_id", () => {
    const onChange = vi.fn();
    mount(5, () => onChange());
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    StubBroadcastChannel.instances[0]?.emit({ type: "group-videos-invalidated", group_id: 0 } as any);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    StubBroadcastChannel.instances[0]?.emit({ type: "group-videos-invalidated", group_id: -3 } as any);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    StubBroadcastChannel.instances[0]?.emit({ type: "group-videos-invalidated", group_id: "5" } as any);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    StubBroadcastChannel.instances[0]?.emit({ type: "group-videos-invalidated" } as any);
    expect(onChange).not.toHaveBeenCalled();
  });
});

describe("useGroupVideosInvalidation — lifecycle", () => {
  it("keeps subscribers out of the registry for null / non-positive ids", () => {
    mount(null, () => {});
    mount(0, () => {});
    mount(-1, () => {});
    expect(__test.listeners().size).toBe(0);
  });

  it("unmount removes the subscriber from the registry", () => {
    const { unmount } = mount(50, () => {});
    expect(__test.listeners().get(50)?.size).toBe(1);
    unmount();
    expect(__test.listeners().has(50)).toBe(false);
  });

  it("re-registering on groupId change does not stack subscribers", () => {
    const { rerender } = renderHook(
      ({ id }: { id: number | null }) => useGroupVideosInvalidation(id, () => {}),
      { initialProps: { id: 10 as number | null } },
    );
    rerender({ id: 20 });
    expect(__test.listeners().get(10)).toBeUndefined();
    expect(__test.listeners().get(20)?.size).toBe(1);
  });
});

describe("invalidateGroupVideos — registry integration", () => {
  it("invalidates registry queries with the flattened group key prefix", () => {
    invalidateGroupVideos(7);
    expect(invalidateSharedQueriesMock).toHaveBeenCalledWith("groups:7:youtube:videos");
    expect(invalidateSharedQueriesMock).toHaveBeenCalledTimes(1);
  });

  it("always invalidates the registry even when window is unavailable (SSR)", () => {
    vi.stubGlobal("window", undefined);
    const result = invalidateGroupVideos(7);
    expect(result).toBe(false);
    expect(invalidateSharedQueriesMock).toHaveBeenCalledWith("groups:7:youtube:videos");
  });
});
