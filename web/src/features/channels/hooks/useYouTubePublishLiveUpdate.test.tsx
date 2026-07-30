/**
 * Tests for useYouTubePublishLiveUpdate — the cross-tab BroadcastChannel
 * listener hook.
 *
 * jsdom (vitest's default browser shim) does NOT implement
 * `BroadcastChannel` natively. We stub it via `vi.stubGlobal` with a
 * deterministic double so each test controls exactly what onmessage
 * fires. We also stub the BroadcastChannel as throwing on construction
 * in one test to verify the degradation path (SecurityError fallback).
 *
 * The CustomEvent + window listener path runs in real jsdom — we use
 * `window.dispatchEvent(new CustomEvent(...))` directly and let
 * jsdom's MUI of native addEventListener collect the calls.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import { StrictMode } from "react";
import {
  __test,
  dispatchYouTubePublishChanged,
  useChannelContentLiveUpdate,
  useGroupVideosLiveUpdate,
} from "./useYouTubePublishLiveUpdate";

// ─── Stub BroadcastChannel ────────────────────────────────────────────
//
// Tracks every instance so each test can:
//   • assert the singleton is created exactly once across mounts
//   • peek at postMessage history without replaying
//   • simulate a cross-tab message with `emit(data)`
//
// `throwOnConstruction = true` toggles the degraded-degradation
// path so SecurityError-style failures fall back to CustomEvent-only.
//
// We intentionally keep this stub separate from `globalThis` — it's
// bound via `vi.stubGlobal("BroadcastChannel", StubBC)` in beforeEach
// so the production module's `instanceof BroadcastChannel` checks
// still resolve to the stub during tests.

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

beforeEach(() => {
  // Reset module singletons BEFORE each test so registries + the BC
  // singleton are reproducible across tests. Without this, the first
  // test's BC leaks into the second.
  __test.reset();
  vi.stubGlobal("BroadcastChannel", StubBroadcastChannel);
  StubBroadcastChannel.instances = [];
  StubBroadcastChannel.throwOnConstruction = false;
});

afterEach(() => {
  vi.unstubAllGlobals();
});

// ─── Helpers ──────────────────────────────────────────────────────────

function mountChannelContent(
  accountId: number | null,
  onChange: () => void,
): void {
  renderHook(
    ({ id, fn }: { id: number | null; fn: () => void }) =>
      useChannelContentLiveUpdate(id, () => fn()),
    {
      initialProps: { id: accountId, fn: onChange },
    },
  );
}

function mountGroupVideos(
  accountId: number | null,
  onChange: () => void,
): void {
  renderHook(
    ({ id, fn }: { id: number | null; fn: () => void }) =>
      useGroupVideosLiveUpdate(id, () => fn()),
    {
      initialProps: { id: accountId, fn: onChange },
    },
  );
}

// ─── Tests ────────────────────────────────────────────────────────────

describe("useYouTubePublishLiveUpdate — singleton creation", () => {
  it("creates exactly ONE BroadcastChannel across many hook mounts", () => {
    mountChannelContent(1, () => {});
    mountChannelContent(1, () => {});
    mountChannelContent(2, () => {});
    mountGroupVideos(1, () => {});
    mountChannelContent(3, () => {});
    expect(StubBroadcastChannel.instances.length).toBe(1);
    expect(StubBroadcastChannel.instances[0]?.name).toBe(
      __test.channelName(),
    );
    expect(__test.channelName()).toBe("instaedit-publish");
  });

  it("installs the same-tab window listener exactly once", () => {
    const addSpy = vi.spyOn(window, "addEventListener");
    mountChannelContent(1, () => {});
    mountGroupVideos(1, () => {});
    const installs = addSpy.mock.calls.filter(
      ([name]) => name === __test.windowEventName(),
    );
    expect(installs.length).toBe(1);
    addSpy.mockRestore();
  });

  it("idempotent under StrictMode (one BC across double-mount)", () => {
    function Probe() {
      useChannelContentLiveUpdate(1, () => {});
      useGroupVideosLiveUpdate(1, () => {});
      return null;
    }
    renderHook(() => Probe(), { wrapper: StrictMode });
    expect(StubBroadcastChannel.instances.length).toBe(1);
  });

  it("degrades gracefully when BroadcastChannel throws on construction", () => {
    StubBroadcastChannel.throwOnConstruction = true;
    // Should NOT throw — CustomEvent-only path still works.
    mountChannelContent(1, () => {});
    mountGroupVideos(1, () => {});
    expect(StubBroadcastChannel.instances.length).toBe(0);
  });

  it("degrades gracefully when BroadcastChannel is undefined entirely", () => {
    // Simulate Safari < 15.4: BroadcastChannel not on the window at all.
    // Casting via unknown because deleting globals is not allowed.
    vi.stubGlobal("BroadcastChannel", undefined);
    mountChannelContent(1, () => {});
    mountGroupVideos(1, () => {});
    expect(StubBroadcastChannel.instances.length).toBe(0);
  });
});

describe("useYouTubePublishLiveUpdate — cross-tab fan-out", () => {
  it("fans out a cross-tab message to the channel-content subscriber", () => {
    const onChange = vi.fn();
    mountChannelContent(42, () => onChange());
    DispatchCrossTab(42, "published", "unlisted");
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  it("BroadcastChannel.onmessage fires for the OTHER tab; CustomEvent for SAME", () => {
    const onChange = vi.fn();
    mountChannelContent(7, () => onChange());
    // 1) Cross-tab path: simulate the OTHER tab's BC message.
    DispatchCrossTab(7, "published", "public");
    expect(onChange).toHaveBeenCalledTimes(1);
    // 2) Same-tab path: dispatch via dispatchYouTubePublishChanged.
    //    This fires CustomEvent on window. The BC postMessage is also
    //    fired (we'll see it in `posted`) but jsdom's BC doesn't echo
    //    back to the sender, so onChange stays at 1.
    dispatchYouTubePublishChanged({
      type: "youtube-publish-changed",
      account_id: 7,
      status: "published",
      actual_privacy: "public",
    });
    expect(onChange).toHaveBeenCalledTimes(2);
    const lastCall = StubBroadcastChannel.instances[0]?.posted.at(-1);
    expect(lastCall).toMatchObject({
      type: "youtube-publish-changed",
      account_id: 7,
      status: "published",
      actual_privacy: "public",
    });
  });

  it("fans out to BOTH registries (channelContent + groupVideos) for one event", () => {
    const onContent = vi.fn();
    const onGroups = vi.fn();
    mountChannelContent(13, () => onContent());
    mountGroupVideos(13, () => onGroups());
    DispatchCrossTab(13, "published", "private");
    expect(onContent).toHaveBeenCalledTimes(1);
    expect(onGroups).toHaveBeenCalledTimes(1);
  });
});

describe("useYouTubePublishLiveUpdate — same-tab fan-out", () => {
  it("fires onChange when dispatchYouTubePublishChanged is called", () => {
    const onChange = vi.fn();
    mountChannelContent(99, () => onChange());
    dispatchYouTubePublishChanged({
      type: "youtube-publish-changed",
      account_id: 99,
      status: "published",
    });
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  it("CustomEvent + onmessage BOTH fire on a same-tab dispatch (CustomEvent in jsdom; BC records postMessage)", () => {
    const onChange = vi.fn();
    mountChannelContent(11, () => onChange());
    dispatchYouTubePublishChanged({
      type: "youtube-publish-changed",
      account_id: 11,
      status: "published",
    });
    // CustomEvent fired → onChange called once. BC.postMessage is
    // recorded but does NOT trigger onmessage on the same tab.
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(StubBroadcastChannel.instances[0]?.posted).toHaveLength(1);
  });
});

describe("useYouTubePublishLiveUpdate — multi-account isolation", () => {
  it("does NOT fan out across different account ids", () => {
    const onA = vi.fn();
    const onB = vi.fn();
    mountChannelContent(100, () => onA());
    mountChannelContent(200, () => onB());
    DispatchCrossTab(100, "published", "public");
    expect(onA).toHaveBeenCalledTimes(1);
    expect(onB).not.toHaveBeenCalled();
  });
});

describe("useYouTubePublishLiveUpdate — payload validation", () => {
  it("ignores payloads with the wrong type tag", () => {
    const onChange = vi.fn();
    mountChannelContent(5, () => onChange());
    DispatchCrossTab(5, "published", "public"); // right type
    dispatchYouTubePublishChanged({
      type: "different-event",
      account_id: 5,
      status: "published",
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any); // wrong type tag
    dispatchYouTubePublishChanged({
      type: "youtube-publish-changed",
      account_id: 5,
      status: "published",
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any); // valid
    expect(onChange).toHaveBeenCalledTimes(2);
  });

  it("ignores payloads with non-numeric / negative / zero account_id", () => {
    const onChange = vi.fn();
    mountChannelContent(5, () => onChange());
    // Cross-tab malformed variants — jsdom MessageEvent.data ignores
    // type checks but we validate at the message handler.
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    DispatchRaw({ type: "youtube-publish-changed", account_id: 0 });
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    DispatchRaw({ type: "youtube-publish-changed", account_id: -3 });
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    DispatchRaw({ type: "youtube-publish-changed", account_id: "5" });
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    DispatchRaw({ type: "youtube-publish-changed", account_id: null });
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    DispatchRaw({ type: "youtube-publish-changed" });
    expect(onChange).not.toHaveBeenCalled();
  });
});

describe("useYouTubePublishLiveUpdate — lifecycle", () => {
  it("accountId=null keeps the subscriber out of the registry", () => {
    mountChannelContent(null, () => {});
    expect(__test.channels().content.size).toBe(0);
  });

  it("unmount removes the subscriber from the registry", () => {
    const { unmount } = renderHook(
      ({ id, fn }: { id: number; fn: () => void }) =>
        useChannelContentLiveUpdate(id, () => fn()),
      { initialProps: { id: 50, fn: () => {} } },
    );
    expect(__test.channels().content.get(50)?.size).toBe(1);
    unmount();
    expect(__test.channels().content.has(50)).toBe(false);
  });

  it("re-registering on accountId change does not stack subscribers", () => {
    const { rerender } = renderHook(
      ({ id }: { id: number | null }) =>
        useChannelContentLiveUpdate(id, () => {}),
      { initialProps: { id: 10 as number | null } },
    );
    rerender({ id: 20 });
    expect(__test.channels().content.get(10)).toBeUndefined();
    expect(__test.channels().content.get(20)?.size).toBe(1);
  });
});

describe("useYouTubePublishLiveUpdate — degraded paths (no BC)", () => {
  it("CustomEvent path still delivers when BC is missing", () => {
    vi.stubGlobal("BroadcastChannel", undefined);
    const onChange = vi.fn();
    mountChannelContent(77, () => onChange());
    dispatchYouTubePublishChanged({
      type: "youtube-publish-changed",
      account_id: 77,
      status: "published",
      actual_privacy: "unlisted",
    });
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  it("CustomEvent path still delivers when BC throws during construction", () => {
    StubBroadcastChannel.throwOnConstruction = true;
    const onChange = vi.fn();
    mountChannelContent(78, () => onChange());
    dispatchYouTubePublishChanged({
      type: "youtube-publish-changed",
      account_id: 78,
      status: "published",
    });
    expect(onChange).toHaveBeenCalledTimes(1);
  });
});

// ─── Test-local helpers ───────────────────────────────────────────────
//
function DispatchCrossTab(
  accountId: number,
  status: string,
  actualPrivacy?: string,
): void {
  DispatchRaw({
    type: "youtube-publish-changed",
    account_id: accountId,
    status,
    ...(actualPrivacy != null ? { actual_privacy: actualPrivacy } : {}),
  });
}

function DispatchRaw(payload: unknown): void {
  const bc = StubBroadcastChannel.instances[0];
  if (bc != null) {
    bc.emit(payload);
  }
}
