import { useCallback, useEffect, useRef, useSyncExternalStore } from "react";

/**
 * useQuery — deps-free, store-aware data-fetching hook for the SPA.
 *
 *   • Moudle-level state: each `key` has a single `QueryEntry` (status,
 *     data, error, fetchedAt) so two components hitting the same path
 *     see the same data without a second network roundtrip.
 *   • In-flight dedupe: a module Map of `<key, {ac, promise, refCount}>`
 *     means the second subscriber of an in-flight query attaches to the
 *     existing promise rather than issuing a parallel request.
 *   • AbortController composition: a single ac per active fetch. The
 *     signal is forwarded to the fetcher so a cancelled fetcher rejects
 *     with AbortError and we stop writing to the cache.
 *   • Ref-counted unmount cleanup: abort fires only after the LAST
 *     subscriber unmounts. Deferred with `setTimeout(0)` to survive
 *     React 18+ StrictMode's tighten/remount cycle (which would
 *     otherwise abort+refetch in dev to no purpose).
 *   • TTL: `isStale` reports `true` once `now - fetchedAt >= ttl`;
 *     mount within the TTL window does NOT issue a re-fetch (cached).
 *   • refetch(): explicit re-fetch regardless of TTL. Returns the data
 *     after the promise resolves (or rejects with the cause on error).
 *   • refetchOnFocus: listens for `window.focus` and restarts the fetch.
 *
 * No new npm deps. Uses React 18 `useSyncExternalStore` for cache
 * subscription so the React tree reacts to in-flight completion without
 * polling.
 *
 * Taglio: built on top of `authedFetch` in lib/auth.ts. The post-refetch
 * migration will replace per-page useState+AbortController dance.
 */
export type QueryKey = string;

export type QueryStatus = "idle" | "loading" | "ready" | "error";

export type QueryEntry<T> = {
  status: QueryStatus;
  data: T | undefined;
  error: Error | undefined;
  fetchedAt: number | undefined;
};

export type QueryFetcher<T> = (signal: AbortSignal) => Promise<T>;

export type UseQueryOptions = {
  /** Milliseconds before resolved data is considered stale. */
  ttl?: number;
  /** Re-fetch on window focus. */
  refetchOnFocus?: boolean;
  /** If false, do not auto-fetch on mount (return cached state only). */
  enabled?: boolean;
};

export type UseQueryResult<T> = {
  data: T | undefined;
  loading: boolean;
  error: Error | undefined;
  /** Force a re-fetch and await its result. Throws on error or abort. */
  refetch: () => Promise<T>;
  /** True if TTL has expired and the data should be considered stale. */
  isStale: boolean;
};

type InflightEntry = {
  ac: AbortController;
  promise: Promise<void>;
  refCount: number;
};

// Single source of truth, shared by every subscriber across components.
const cache = new Map<QueryKey, QueryEntry<unknown>>();
const inflight = new Map<QueryKey, InflightEntry>();
const listeners = new Map<QueryKey, Set<() => void>>();

/**
 * Build an empty entry. Type-inferred (not annotated as QueryEntry<unknown>)
 * so spreading it into a `QueryEntry<T>` typed slot preserves the literal
 * `data: undefined` and avoids the `unknown`-propagates-through-spread
 * trap that TS would otherwise flag.
 */
function emptyEntry(): {
  status: QueryStatus;
  data: undefined;
  error: undefined;
  fetchedAt: undefined;
} {
  return { status: "idle", data: undefined, error: undefined, fetchedAt: undefined };
}

/**
 * Drop all module state. Test-only — exported with a leading underscore
 * to make the intent obvious at call sites.
 */
export function _resetQueryCacheForTesting(): void {
  for (const entry of inflight.values()) entry.ac.abort();
  cache.clear();
  inflight.clear();
  listeners.clear();
}

function getEntry<T>(key: QueryKey): QueryEntry<T> {
  const existing = cache.get(key) as QueryEntry<T> | undefined;
  if (existing) return existing;
  const fresh: QueryEntry<T> = emptyEntry() as QueryEntry<T>;
  cache.set(key, fresh as QueryEntry<unknown>);
  return fresh;
}

function setEntry<T>(key: QueryKey, partial: Partial<QueryEntry<T>>): void {
  const old = getEntry<T>(key);
  const next: QueryEntry<T> = { ...old, ...partial };
  cache.set(key, next as QueryEntry<unknown>);
  notify(key);
}

function notify(key: QueryKey): void {
  const ls = listeners.get(key);
  if (!ls) return;
  // Copy to a snapshot so listeners that themselves subscribe/unsubscribe
  // during the iteration don't perturb the loop.
  for (const cb of Array.from(ls)) cb();
}

function subscribe(key: QueryKey, cb: () => void): () => void {
  let ls = listeners.get(key);
  if (!ls) {
    ls = new Set();
    listeners.set(key, ls);
  }
  ls.add(cb);
  return () => {
    ls!.delete(cb);
    if (ls!.size === 0) listeners.delete(key);
  };
}

function startFetch<T>(key: QueryKey, fetcher: QueryFetcher<T>): InflightEntry {
  // Cancel any prior inflight for this key — explicit refetch always wins.
  const prior = inflight.get(key);
  if (prior) prior.ac.abort();

  const ac = new AbortController();
  setEntry<T>(key, { status: "loading", error: undefined });

  const promise = (async () => {
    try {
      const data = await fetcher(ac.signal);
      if (ac.signal.aborted) return;
      setEntry<T>(key, {
        status: "ready",
        data,
        error: undefined,
        fetchedAt: Date.now(),
      });
    } catch (err) {
      // An aborted fetch rejects with AbortError AND the controller's
      // signal is aborted, so a single guard fully covers both surfaces.
      if (ac.signal.aborted) return;
      setEntry<T>(key, {
        status: "error",
        error: err instanceof Error ? err : new Error(String(err)),
      });
    }
  })();

  const entry: InflightEntry = { ac, promise, refCount: 0 };
  inflight.set(key, entry);

  void promise.finally(() => {
    // Only drop OUR inflight record; a refetch released in the meantime
    // is a different entry and owns the slot now.
    if (inflight.get(key)?.ac === ac) inflight.delete(key);
  });

  return entry;
}

function acquire<T>(key: QueryKey, fetcher: QueryFetcher<T>): InflightEntry {
  const existing = inflight.get(key);
  if (existing) {
    existing.refCount += 1;
    return existing;
  }
  const fresh = startFetch<T>(key, fetcher);
  fresh.refCount = 1;
  return fresh;
}

/**
 * Decrement refcount AFTER the React unmount cleanup settles. StrictMode
 * does mount → unmount → remount on the same renderer tick; without the
 * defer, the unmount would zero the refcount, aborting the in-flight
 * request, and the remount would fire a fresh one (visible as a noisy
 * aborted-then-retry in browser devtools).
 */
function release(key: QueryKey): void {
  // Defer so StrictMode's tighten/remount cycle keeps the in-flight
  // request alive (see header docblock). The idempotency guard prevents
  // a stale deferred release from driving refCount negative if a fresh
  // acquire has already re-armed the entry.
  setTimeout(() => {
    const entry = inflight.get(key);
    if (!entry) return;
    if (entry.refCount <= 0) return;
    entry.refCount -= 1;
    if (entry.refCount <= 0) {
      entry.ac.abort();
      if (inflight.get(key)?.ac === entry.ac) inflight.delete(key);
    }
  }, 0);
}

export function useQuery<T>(
  key: QueryKey,
  fetcher: QueryFetcher<T>,
  opts: UseQueryOptions = {},
): UseQueryResult<T> {
  const enabled = opts.enabled ?? true;
  const ttl = opts.ttl;
  const refetchOnFocus = opts.refetchOnFocus ?? false;

  const fetcherRef = useRef(fetcher);
  fetcherRef.current = fetcher;

  const subscribeFn = useCallback((cb: () => void) => subscribe(key, cb), [key]);
  const getSnapshot = useCallback(() => getEntry<T>(key), [key]);

  // useSyncExternalStore gives React tearing-free access to the cache.
  // Server snapshot == client snapshot (jsdom is always "client").
  const entry = useSyncExternalStore(subscribeFn, getSnapshot, getSnapshot);

  useEffect(() => {
    if (!enabled) return;

    const cur = getEntry<T>(key);
    const isFresh =
      cur.status === "ready" &&
      cur.fetchedAt !== undefined &&
      ttl !== undefined &&
      Date.now() - cur.fetchedAt < ttl;
    if (isFresh) return;

    acquire<T>(key, fetcherRef.current);
    return () => release(key);
  }, [key, enabled, ttl]);

  useEffect(() => {
    if (!refetchOnFocus) return;
    const onFocus = () => {
      startFetch<T>(key, fetcherRef.current);
    };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [key, refetchOnFocus]);

  const refetch = useCallback(async (): Promise<T> => {
    const entry = startFetch<T>(key, fetcherRef.current);
    await entry.promise;
    if (entry.ac.signal.aborted) {
      throw new DOMException("aborted", "AbortError");
    }
    const result = getEntry<T>(key);
    if (result.status === "error" && result.error) throw result.error;
    return result.data as T;
  }, [key]);

  const isStale = Boolean(
    entry.fetchedAt !== undefined &&
      ttl !== undefined &&
      Date.now() - entry.fetchedAt >= ttl,
  );

  return {
    data: entry.data,
    loading: entry.status === "loading" || entry.status === "idle",
    error: entry.error,
    refetch,
    isStale,
  };
}
