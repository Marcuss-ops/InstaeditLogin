/**
 * useGroupVideosInvalidation — targeted invalidation for the group
 * video-list cache, keyed exactly as `['groups', groupId, 'youtube',
 * 'videos']` (flattened to `groups:{groupId}:youtube:videos`, the
 * queryRegistry key convention used across the app).
 *
 * The group video list is a single canonical resource: after ANY save
 * in the domain (video metadata, cover title/description, thumbnail
 * publish), the cards must update WITHOUT reloading all of InstaEdit —
 * so the save flow calls `invalidateGroupVideos(groupId)` and ONLY the
 * group's video-list cache is refreshed:
 *
 *   1. Registry-backed shared queries with the same key prefix are
 *      force-refetched via `invalidateSharedQueries`.
 *   2. The group's mounted local-state video lists (the manual state
 *      machine in useGroupYouTubeVideos) are notified through this bus.
 *
 * Same-tab vs cross-tab:
 *
 *   - BroadcastChannel does NOT fire `onmessage` on the same tab that
 *     called `postMessage`. Same-tab delivery MUST go through a
 *     parallel channel (a `CustomEvent` on `window`). The dispatcher
 *     (`invalidateGroupVideos`) fires BOTH paths so a single
 *     invalidation reaches the same tab AND every other tab in the
 *     same origin (e.g. the InstaEditor tab where a cover gets
 *     published, or a second InstaEdit tab).
 *
 * Group-keyed registries:
 *
 *   - Each `useGroupVideosInvalidation(groupId, fn)` call registers
 *     `fn` under that `groupId` on mount and unregisters it on unmount.
 *     An invalidation for group A never calls a subscriber registered
 *     under group B.
 *
 * Browser fallbacks:
 *
 *   - BroadcastChannel is missing on Safari < 15.4, some Firefox
 *     private-mode tabs, and detached iframes. The module handles the
 *     `typeof BroadcastChannel === "undefined"` case AND any
 *     `SecurityError` thrown by the constructor; the same-tab
 *     CustomEvent still works, so the multi-tab UX degrades to
 *     single-tab instead of breaking.
 *
 *   - SSR defense: `typeof window === "undefined"` short-circuits all
 *     setup. The hook becomes a no-op on the server side.
 *
 * StrictMode:
 *
 *   - React 18 StrictMode runs effects twice in dev (mount →
 *     immediately-unmount → mount-again). The registries and the
 *     BroadcastChannel/window listeners are module-scoped singletons,
 *     so the double-mount never creates duplicate listeners.
 */
import { useEffect, useRef } from "react";
import { invalidateSharedQueries } from "../../../lib/queryRegistry";

/** BroadcastChannel name. Hardcoded once so any rename is a single find-and-replace. */
const CHANNEL_NAME = "instaedit-group-videos";

/** Same-tab delivery event name. Distinct from the BC name. */
const WINDOW_EVENT_NAME = "instaedit:group-videos-invalidated";

/** Subscriber function the video-list hook wires into the registry. */
type RefetchFn = () => void | Promise<void>;

/** The shape of the dispatch payload. group_id is validated strictly. */
export interface GroupVideosInvalidationMessage {
  readonly type: "group-videos-invalidated";
  readonly group_id: number;
}

/**
 * Canonical query key for the group YouTube videos list, exactly as
 * specified by the cache contract: `['groups', groupId, 'youtube',
 * 'videos']`. Any future registry-backed query for this resource must
 * use this key (or a sub-key deriving from it) so invalidation reaches it.
 */
export function groupVideosQueryKey(groupId: number): string[] {
  return ["groups", String(groupId), "youtube", "videos"];
}

/** Flattened queryRegistry key: `groups:{groupId}:youtube:videos`. */
export function groupVideosQueryKeyString(groupId: number): string {
  return `groups:${groupId}:youtube:videos`;
}

// ─── Per-group registry (module-scoped) ───────────────────────────────
const listeners: Map<number, Set<RefetchFn>> = new Map();

// ─── Singleton state (module-scoped) ──────────────────────────────────
// These survive across imports/StrictMode remounts so the
// application's first useEffect installs the listeners exactly once.
let bcSingleton: BroadcastChannel | null = null;
let listenerInstalled = false;
let bcUnsupported = false;
let windowMessageHandler: ((event: Event) => void) | null = null;

function getChannel(): BroadcastChannel | null {
  if (bcUnsupported) return null;
  if (typeof window === "undefined") return null;
  if (typeof BroadcastChannel === "undefined") {
    bcUnsupported = true;
    return null;
  }
  if (bcSingleton == null) {
    try {
      bcSingleton = new BroadcastChannel(CHANNEL_NAME);
    } catch {
      // SecurityError in some sandboxed contexts. Mark as unsupported
      // so we don't retry on every mount.
      bcUnsupported = true;
      bcSingleton = null;
    }
  }
  return bcSingleton;
}

function isGroupVideosMessage(value: unknown): value is GroupVideosInvalidationMessage {
  if (typeof value !== "object" || value == null) return false;
  const m = value as Record<string, unknown>;
  if (m["type"] !== "group-videos-invalidated") return false;
  if (typeof m["group_id"] !== "number") return false;
  if (!Number.isFinite(m["group_id"])) return false;
  if (m["group_id"] <= 0) return false;
  return true;
}

function fanOut(groupId: number): void {
  const subs = listeners.get(groupId);
  if (!subs) return;
  // Copy to an array so a subscriber that unmounts during dispatch
  // (removing itself from the set) doesn't shift the iteration.
  for (const fn of Array.from(subs)) {
    try {
      void fn();
    } catch (err) {
      // Refetch errors are owned by the caller's state machine (it
      // catches ApiError / AuthError). Log so a single buggy listener
      // doesn't break the fan-out silently.
      if (typeof console !== "undefined") {
        console.error("[useGroupVideosInvalidation] listener threw:", err);
      }
    }
  }
}

function register(groupId: number, fn: RefetchFn): () => void {
  let set = listeners.get(groupId);
  if (!set) {
    set = new Set();
    listeners.set(groupId, set);
  }
  set.add(fn);
  return () => {
    const s = listeners.get(groupId);
    if (!s) return;
    s.delete(fn);
    if (s.size === 0) listeners.delete(groupId);
  };
}

function installListenersOnce(): boolean {
  if (listenerInstalled) return true;
  if (typeof window === "undefined") return false;

  // 1. Same-tab forwarder (CustomEvent on window). Always available
  //    when `window` is; runs even when BC is unavailable.
  windowMessageHandler = (evt) => {
    const detail = (evt as CustomEvent<GroupVideosInvalidationMessage>).detail;
    if (!isGroupVideosMessage(detail)) return;
    fanOut(detail.group_id);
  };
  window.addEventListener(WINDOW_EVENT_NAME, windowMessageHandler);

  // 2. Cross-tab forwarder (BroadcastChannel). Optional — if BC
  //    refuses, the app still works for the single-tab case.
  const bc = getChannel();
  if (bc != null) {
    bc.onmessage = (event: MessageEvent<GroupVideosInvalidationMessage>) => {
      if (!isGroupVideosMessage(event.data)) return;
      fanOut(event.data.group_id);
    };
  }

  listenerInstalled = true;
  return true;
}

// ─── Public API ───────────────────────────────────────────────────────

/**
 * Subscribe the group YouTube videos cache (`['groups', groupId,
 * 'youtube', 'videos']`) to invalidation events. `handler` is called
 * when `invalidateGroupVideos(groupId)` fires (same tab or another tab
 * in the same origin). The handler is read from a ref, so it can be a
 * fresh closure every render without re-registering the subscription —
 * only a groupId change re-registers.
 *
 * Typical wiring:
 *
 *   useGroupVideosInvalidation(groupId, useCallback(() => {
 *     void refreshVideos(false, true);
 *   }, [refreshVideos]));
 */
export function useGroupVideosInvalidation(
  groupId: number | null,
  handler: RefetchFn,
): void {
  const handlerRef = useRef(handler);
  handlerRef.current = handler;

  useEffect(() => {
    if (groupId == null || groupId <= 0) return;
    if (!installListenersOnce()) return;
    const id = groupId;
    return register(id, () => {
      void handlerRef.current();
    });
    // listeners is module-scoped and stable; only groupId matters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [groupId]);
}

/**
 * Invalidate exactly the group YouTube videos cache for `groupId`:
 *
 *   1. Force-refetches registry-backed shared queries with the key
 *      prefix `groups:{groupId}:youtube:videos`.
 *   2. Notifies mounted local-state video lists — same tab (CustomEvent)
 *      AND every other tab in the origin (BroadcastChannel).
 *
 * Returns `false` only in detached SSR-style environments without
 * `window`; the registry invalidation still runs there.
 */
export function invalidateGroupVideos(groupId: number): boolean {
  // Registry-backed queries — works in any environment.
  invalidateSharedQueries(groupVideosQueryKeyString(groupId));
  if (typeof window === "undefined") return false;

  const payload: GroupVideosInvalidationMessage = {
    type: "group-videos-invalidated",
    group_id: groupId,
  };

  // Same-tab — CustomEvent. The BroadcastChannel onmessage never fires
  // on the sender's own tab, so this is the ONLY way to reach same-tab
  // listeners.
  window.dispatchEvent(
    new CustomEvent<GroupVideosInvalidationMessage>(WINDOW_EVENT_NAME, {
      detail: payload,
    }),
  );

  // Cross-tab — BroadcastChannel. Silently no-op if unsupported.
  const bc = getChannel();
  if (bc != null) {
    try {
      bc.postMessage(payload);
    } catch {
      // SecurityError in some browser contexts. The same-tab path
      // already delivered the event; cross-tab loss is non-fatal.
    }
  }
  return true;
}

// ─── Test helpers ─────────────────────────────────────────────────────
// Exported under a namespaced object so callers won't grab them by
// accident — only the vitest suite imports `__test`. Keeping the
// helpers in the production file ensures tests cover the real paths.
export const __test = {
  reset(): void {
    listeners.clear();
    bcSingleton?.close();
    bcSingleton = null;
    if (windowMessageHandler && typeof window !== "undefined") {
      window.removeEventListener(WINDOW_EVENT_NAME, windowMessageHandler);
    }
    windowMessageHandler = null;
    listenerInstalled = false;
    bcUnsupported = false;
  },
  listeners(): Map<number, Set<RefetchFn>> {
    return listeners;
  },
  channelName(): string {
    return CHANNEL_NAME;
  },
  windowEventName(): string {
    return WINDOW_EVENT_NAME;
  },
};
