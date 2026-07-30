/**
 * useYouTubePublishLiveUpdate — single BroadcastChannel listener that
 * fans out YouTube publish-update events to TWO cache consumers in
 * one place:
 *
 *   1. The channel-content cache, keyed by
 *      `['channel-content', accountId, privacyFilter]` (all privacy
 *      filters mounted for the same account are invalidated; see the
 *      reasoning in the docs comment below).
 *   2. The group-videos cache, keyed by `groupYouTubeVideosQueryKey`
 *      (the legacy group/folder video list — AccountDetails.tsx tab
 *      "videos", Calendar.tsx Linked Channels panel, etc.).
 *
 * Why ONE BroadcastChannel (and not two):
 *
 *   - The publish flow surface emits a single event per "video &
 *     account" transition (`queued → publishing → published`, or
 *     a thumbnail swap, or a privacy flip). Each consumer cares
 *     about a DIFFERENT cache, but they care about the SAME event.
 *     Splitting the BC proliferates wiring (each page has to know
 *     which BC to listen on) and forces the dispatch site to fire
 *     N BCs in lockstep. One BC + one fan-out is the natural
 *     mapping for "one event, N independent queries invalidated".
 *
 *   - The audit doc (docs/PUBLISH-FLOW-AUDIT.md §"Order of work,
 *     step 8") explicitly profiled this as "Cross-tab BroadcastChannel
 *     unificato: 1 solo listener che invalida SIA channel content
 *     cache SIA groupYouTubeVideosQueryKey" before naming a
 *     non-existent `useYouTubePublishLiveUpdate` — this file is
 *     that hook.
 *
 * Same-tab vs cross-tab:
 *
 *   - BroadcastChannel does NOT fire `onmessage` on the same tab that
 *     called `postMessage`. Same-tab delivery MUST go through a
 *     parallel channel (we use a `CustomEvent` on `window`). The
 *     dispatcher in this file (`dispatchYouTubePublishChanged`)
 *     fires BOTH paths so a single dispatch reaches the same tab
 *     AND every other tab in the same origin.
 *
 * Account-keyed registries:
 *
 *   - Each hook call `useChannelContentLiveUpdate(accountId, fn)`
 *     registers `fn` under that `accountId` on mount and unregisters
 *     it on unmount. A dispatch for account A never calls a
 *     subscriber registered under account B. The fn the page wires
 *     in is typically a wrapper of the hook's `refetch()` plus
 *     optional privacy-filter bookkeeping.
 *
 *   - We key by accountId ONLY (not by accountId+privacyFilter).
 *     Reasoning: a single Dark Editor publish can flip
 *     `private → public`. Three cache entries exist per account
 *     (`['channel-content', accountId, 'all']`,
 *     `['channel-content', accountId, 'private']`,
 *     `['channel-content', accountId, 'public']`). When the flip
 *     lands, the 'private' cache row disappears (correct) and the
 *     'public' / 'all' caches gain a row (also correct). Invalidating
 *     ONLY the user's currently-visible privacy filter would leave
 *     the other two caches stale and the tab-switch UX inconsistent
 *     (e.g. user is on "Private", video flips public, they don't see
 *     the change until manual refresh). Fanning out across all
 *     privacy filters for the account is the correct semantics.
 *
 * Browser fallbacks:
 *
 *   - BroadcastChannel is missing on Safari < 15.4, on some Firefox
 *     private-mode tabs, and in detached iframes. The hook handles
 *     the `typeof BroadcastChannel === "undefined"` case AND any
 *     `SecurityError` thrown by the constructor (`new BroadcastChannel`
 *     can throw in some sandboxed contexts). When BC is unavailable,
 *     the same-tab CustomEvent still works — the multi-tab UX simply
 *     degrades to single-tab, which is the best we can do without
 *     a WebSocket.
 *
 *   - SSR defense: `typeof window === "undefined"` short-circuits all
 *     setup. The hook becomes a no-op on the server side; React 18 +
 *     Vite ssr-load warrants this guard everywhere we touch `window`,
 *     `BroadcastChannel`, or `CustomEvent`.
 *
 * StrictMode:
 *
 *   - React 18 StrictMode runs effects twice in dev (mount →
 *     immediately-unmount → mount-again). Each pair of mounts/unmounts
 *     adds and removes the subscriber via the useEffect cleanup
 *     function returned to React. The registries themselves are
 *     module-scoped singletons — the BroadcastChannel singleton and
 *     the window listener are ALSO module-scoped, so StrictMode
 *     mounting twice never creates two BC instances (the second
 *     `ensureInstall()` call is a no-op).
 */

import { useEffect, useRef } from "react";

/**
 * BroadcastChannel name. Hardcoded once here so any future rename
 * goes through a single find-and-replace. Matches the test
 * comments in `pkg/api/youtube_publish_pipeline_test.go:99` that
 * imply a single shared channel for the SPA's Publish path.
 */
const CHANNEL_NAME = "instaedit-publish";

/**
 * Same-tab delivery event name. Distinct from the BC name so an
 * embedder that later wires a third tab-delivery mechanism can
 * displace it without overlapping the BC event namespace.
 */
const WINDOW_EVENT_NAME = "instaedit:youtube-publish-changed";

/** Subscriber function the page wires into the registry. */
type RefetchFn = () => void | Promise<void>;

/**
 * The shape of the dispatch payload. `status` and `actual_privacy`
 * are intentionally typed as `string` (not a strict enum) so the
 * client doesn't crash on a server-side lexicon change — the audit
 * doc tracks `draft/queued/publishing/published/failed/retrying/
 * waiting_provider/dlq/partially_published` plus a couple more in
 * the YouTube branch. The listener never BRANCHES on these values,
 * only forwards them — so the relaxed type is fine.
 */
export interface PublishUpdateMessage {
  readonly type: "youtube-publish-changed";
  readonly account_id: number;
  readonly status: string;
  readonly actual_privacy?: string;
}

// ─── Per-account registries (module-scoped) ────────────────────────────
// One registry per cache surface. The fan-out walks BOTH on each
// broadcast so a single dispatch invalidates both queries.
const channelContentListeners: Map<number, Set<RefetchFn>> = new Map();
const groupVideosListeners: Map<number, Set<RefetchFn>> = new Map();

// ─── Singleton state (module-scoped) ──────────────────────────────────
// These survive across imports/StrictMode remounts so the
// application's first useEffect installs the listeners exactly once.
let bcSingleton: BroadcastChannel | null = null;
let listenerInstalled = false;
let bcUnsupported = false;

// ─── Helpers ──────────────────────────────────────────────────────────

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
      // SecurityError in some sandboxed contexts. Mark as
      // unsupported so we don't retry on every mount.
      bcUnsupported = true;
      bcSingleton = null;
    }
  }
  return bcSingleton;
}

function isPublishMessage(value: unknown): value is PublishUpdateMessage {
  if (typeof value !== "object" || value == null) return false;
  const m = value as Record<string, unknown>;
  if (m["type"] !== "youtube-publish-changed") return false;
  if (typeof m["account_id"] !== "number") return false;
  if (!Number.isFinite(m["account_id"])) return false;
  if (m["account_id"] <= 0) return false;
  return true;
}

function fanOut(
  registry: Map<number, Set<RefetchFn>>,
  accountId: number,
): void {
  const subs = registry.get(accountId);
  if (!subs) return;
  // Copy to an array so a subscriber that unmounts during dispatch
  // (and so removes ITSELF from the set) doesn't shift the iteration.
  const snapshot = Array.from(subs);
  for (const fn of snapshot) {
    try {
      void fn();
    } catch (err) {
      // Refetch errors are owned by the page's state machine (it
      // catches ApiError / AuthError). Here we only log so a single
      // buggy listener doesn't break the broadcast fan-out.
      if (typeof console !== "undefined") {
        console.error("[useYouTubePublishLiveUpdate] refetch threw:", err);
      }
    }
  }
}

function register(
  registry: Map<number, Set<RefetchFn>>,
  accountId: number,
  fn: RefetchFn,
): () => void {
  let set = registry.get(accountId);
  if (!set) {
    set = new Set();
    registry.set(accountId, set);
  }
  set.add(fn);
  return () => {
    const s = registry.get(accountId);
    if (!s) return;
    s.delete(fn);
    if (s.size === 0) registry.delete(accountId);
  };
}

function installListenersOnce(): boolean {
  if (listenerInstalled) return true;
  if (typeof window === "undefined") return false;

  // 1. Same-tab forwarder (CustomEvent on window). Always available
  //    when `window` is; runs even when BC is unavailable so we
  //    never lose the same-tab case.
  window.addEventListener(WINDOW_EVENT_NAME, (evt) => {
    const detail = (evt as CustomEvent<PublishUpdateMessage>).detail;
    if (!isPublishMessage(detail)) return;
    fanOut(channelContentListeners, detail.account_id);
    fanOut(groupVideosListeners, detail.account_id);
  });

  // 2. Cross-tab forwarder (BroadcastChannel). Optional — if BC
  //    refuses, the app still works for the single-tab case.
  const bc = getChannel();
  if (bc != null) {
    bc.onmessage = (event: MessageEvent<PublishUpdateMessage>) => {
      if (!isPublishMessage(event.data)) return;
      fanOut(channelContentListeners, event.data.account_id);
      fanOut(groupVideosListeners, event.data.account_id);
    };
  }

  listenerInstalled = true;
  return true;
}

function useAccountLiveUpdate(
  registry: Map<number, Set<RefetchFn>>,
  accountId: number | null,
  handleMessage: (msg: PublishUpdateMessage) => void,
): void {
  // propsRef lets us read the LATEST handleMessage without making
  // it a useEffect dep — the deps of the effect below are so
  // minimal we only need register/unregister on accountId changes.
  const handlerRef = useRef(handleMessage);
  handlerRef.current = handleMessage;

  useEffect(() => {
    if (accountId == null) return;
    if (!installListenersOnce()) return;
    // Pin the accountId into the closure so the dispatch wrapper
    // sends the original accountId, not whatever the latest props
    // were when the dispatch ran (which would be the same value,
    // but typing it explicitly makes the invariant unmistakable).
    const id = accountId;
    return register(registry, id, () => {
      handlerRef.current({
        type: "youtube-publish-changed",
        account_id: id,
        status: "published",
      });
    });
    // registry is module-scoped and stable; only accountId matters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountId, registry]);
}

// ─── Public API ───────────────────────────────────────────────────────

/**
 * Subscribe the channel-content cache
 * (`['channel-content', accountId, privacyFilter]`) to cross-tab
 * publish-update events. Pass `accountId == null` to unsubscribe.
 *
 * The page wires `handleMessage` to its own state machine's
 * refetch; typically:
 *
 *   useChannelContentLiveUpdate(accountId, useCallback(() => {
 *     void contentState.refetch();
 *   }, [contentState]));
 *
 * (A `useCallback` wrapper around `contentState.refetch` keeps the
 * handler reference stable so the inner registry add/remove is a
 * no-op when only an unrelated re-render happens.)
 */
export function useChannelContentLiveUpdate(
  accountId: number | null,
  handleMessage: (msg: PublishUpdateMessage) => void,
): void {
  useAccountLiveUpdate(channelContentListeners, accountId, handleMessage);
}

/**
 * Subscribe the group-videos cache (`groupYouTubeVideosQueryKey`)
 * to cross-tab publish-update events. Same contract as
 * {@link useChannelContentLiveUpdate}.
 */
export function useGroupVideosLiveUpdate(
  accountId: number | null,
  handleMessage: (msg: PublishUpdateMessage) => void,
): void {
  useAccountLiveUpdate(groupVideosListeners, accountId, handleMessage);
}

/**
 * Dispatch a publish-update event. Fires BOTH same-tab (CustomEvent
 * on `window`) and cross-tab (BroadcastChannel `postMessage`). At
 * least one path always succeeds when `window` is defined, so we
 * return `true` in that case and `false` only in detached SSR-style
 * environments without `window`.
 *
 * Called from the publish-status page (ContentPublish.tsx) when a
 * target reaches `published`, AND can be triggered from any future
 * caller (the Dark Editor publish endpoint, a manual re-sync, etc.).
 * The dispatcher is idempotent at the receiver level — receiving
 * multiple identical events just calls `refetch` multiple times,
 * which is harmless because `refetch` already aborts a stale fetch.
 */
export function dispatchYouTubePublishChanged(
  payload: PublishUpdateMessage,
): boolean {
  if (typeof window === "undefined") return false;
  // Same-tab — CustomEvent. The BroadcastChannel onmessage never
  // fires on the sender's own tab, so this is the ONLY way to
  // reach same-tab listeners.
  window.dispatchEvent(
    new CustomEvent<PublishUpdateMessage>(WINDOW_EVENT_NAME, {
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
// helpers in the production file ensures tests cover the real
// paths (registry map, BroadcastChannel singleton) rather than a
// parallel fake that drifts.

export const __test = {
  reset(): void {
    channelContentListeners.clear();
    groupVideosListeners.clear();
    bcSingleton?.close();
    bcSingleton = null;
    listenerInstalled = false;
    bcUnsupported = false;
  },
  channels(): {
    content: Map<number, Set<RefetchFn>>;
    groups: Map<number, Set<RefetchFn>>;
  } {
    return {
      content: channelContentListeners,
      groups: groupVideosListeners,
    };
  },
  channelName(): string {
    return CHANNEL_NAME;
  },
  windowEventName(): string {
    return WINDOW_EVENT_NAME;
  },
};
