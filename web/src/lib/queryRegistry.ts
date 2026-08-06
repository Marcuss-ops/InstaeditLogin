import { useCallback, useEffect, useRef, useSyncExternalStore } from "react";

export type PollingInterval<T> = number | null | ((data: T | undefined) => number | null);

export interface QuerySnapshot<T> {
  data: T | undefined;
  error: unknown | null;
  isLoading: boolean;
  isFetching: boolean;
  updatedAt: number;
}

export interface QueryOptions<T> {
  enabled?: boolean;
  /** Set false for task pollers that are already triggered by their owner. */
  fetchOnMount?: boolean;
  staleTime?: number;
  pollingInterval?: PollingInterval<T>;
  fetcher: (signal: AbortSignal) => Promise<T>;
}

export interface UseSharedQueryResult<T> extends QuerySnapshot<T> {
  refetch: () => Promise<T | undefined>;
}

type Listener = () => void;

type Entry<T> = {
  key: string;
  options: QueryOptions<T>;
  snapshot: QuerySnapshot<T>;
  listeners: Set<Listener>;
  inFlight: Promise<T | undefined> | null;
  controller: AbortController | null;
  timer: ReturnType<typeof setTimeout> | null;
  cleanupTimer: ReturnType<typeof setTimeout> | null;
  scheduledInterval: number | null;
  failureCount: number;
};

const CACHE_TIME_MS = 5 * 60_000;
const DEFAULT_STALE_TIME_MS = 60_000;
const MAX_ERROR_RETRY_MS = 60_000;
const entries = new Map<string, Entry<unknown>>();
const activeEntries = new Set<Entry<unknown>>();
let visibilityListenerInstalled = false;

function isHidden(): boolean {
  return typeof document !== "undefined" &&
    (document.hidden || document.visibilityState === "hidden");
}

function notify<T>(entry: Entry<T>): void {
  for (const listener of entry.listeners) listener();
}

function clearTimer<T>(entry: Entry<T>): void {
  if (entry.timer !== null) {
    clearTimeout(entry.timer);
    entry.timer = null;
  }
}

function installVisibilityListener(): void {
  if (visibilityListenerInstalled || typeof document === "undefined") return;
  visibilityListenerInstalled = true;
  document.addEventListener("visibilitychange", () => {
    if (document.hidden || document.visibilityState === "hidden") return;
    for (const entry of activeEntries) {
      if (intervalFor(entry) == null) continue;
      clearTimer(entry);
      void fetchEntry(entry, true).catch(() => {
        // The error is retained in the query snapshot. Consumers decide
        // whether to render it; visibility must never create an unhandled
        // rejection.
      });
    }
  });
  if (typeof window !== "undefined") {
    window.addEventListener("instaedit:session-cleared", () => registry.clear());
  }
}

function intervalFor<T>(entry: Entry<T>): number | null {
  const configured = entry.options.pollingInterval;
  if (configured == null) return null;
  const interval = typeof configured === "function"
    ? configured(entry.snapshot.data)
    : configured;
  return typeof interval === "number" && Number.isFinite(interval) && interval > 0
    ? interval
    : null;
}

function schedule<T>(entry: Entry<T>): void {
  clearTimer(entry);
  if (entry.listeners.size === 0 || entry.options.enabled === false) return;
  const interval = intervalFor(entry);
  if (interval == null || isHidden()) return;

  // Recursive timeout avoids overlapping requests and lets a predicate
  // adapt the cadence after every successful response.
  const retryMultiplier = entry.snapshot.error
    ? Math.min(2 ** Math.min(entry.failureCount, 4), 16)
    : 1;
  const retryDelay = Math.min(interval * retryMultiplier, MAX_ERROR_RETRY_MS);
  entry.scheduledInterval = interval;
  entry.timer = setTimeout(() => {
    entry.timer = null;
    if (isHidden()) {
      schedule(entry);
      return;
    }
    void fetchEntry(entry, true).catch(() => {
      // Polling errors are represented in the snapshot and retried later.
    });
  }, retryDelay);
}

async function fetchEntry<T>(entry: Entry<T>, force: boolean): Promise<T | undefined> {
  if (entry.options.enabled === false || entry.listeners.size === 0) return entry.snapshot.data;
  if (isHidden() && !force) return entry.snapshot.data;
  if (entry.inFlight) return entry.inFlight;

  const staleTime = entry.options.staleTime ?? DEFAULT_STALE_TIME_MS;
  const isFresh = entry.snapshot.updatedAt > 0 && Date.now() - entry.snapshot.updatedAt < staleTime;
  if (!force && isFresh) {
    schedule(entry);
    return entry.snapshot.data;
  }

  const controller = new AbortController();
  entry.controller = controller;
  entry.snapshot = {
    ...entry.snapshot,
    isLoading: entry.snapshot.data === undefined,
    isFetching: true,
    error: null,
  };
  notify(entry);

  const request = entry.options.fetcher(controller.signal)
    .then((data) => {
      if (controller.signal.aborted) return data;
      entry.failureCount = 0;
      entry.snapshot = {
        data,
        error: null,
        isLoading: false,
        isFetching: false,
        updatedAt: Date.now(),
      };
      notify(entry);
      return data;
    })
    .catch((error: unknown) => {
      if (controller.signal.aborted || (error instanceof DOMException && error.name === "AbortError")) {
        return entry.snapshot.data;
      }
      entry.failureCount += 1;
      entry.snapshot = {
        ...entry.snapshot,
        isLoading: entry.snapshot.data === undefined,
        isFetching: false,
        error,
      };
      notify(entry);
      throw error;
    })
    .finally(() => {
      entry.inFlight = null;
      entry.controller = null;
      schedule(entry);
    });

  entry.inFlight = request;
  return request;
}

function getOrCreate<T>(key: string, options: QueryOptions<T>): Entry<T> {
  const existing = entries.get(key) as Entry<T> | undefined;
  if (existing) return existing;
  const entry: Entry<T> = {
    key,
    options,
    snapshot: {
      data: undefined,
      error: null,
      isLoading: options.enabled !== false,
      isFetching: false,
      updatedAt: 0,
    },
    listeners: new Set(),
    inFlight: null,
    controller: null,
    timer: null,
    cleanupTimer: null,
    scheduledInterval: null,
    failureCount: 0,
  };
  entries.set(key, entry as Entry<unknown>);
  installVisibilityListener();
  return entry;
}

const registry = {
  subscribe<T>(key: string, listener: Listener, options: QueryOptions<T>): () => void {
    const entry = getOrCreate(key, options);
    if (entry.cleanupTimer !== null) {
      clearTimeout(entry.cleanupTimer);
      entry.cleanupTimer = null;
    }
    entry.listeners.add(listener);
    activeEntries.add(entry as Entry<unknown>);
    if (entry.options.fetchOnMount !== false) {
      void fetchEntry(entry, false).catch(() => {
        // Consumers observe the error through getSnapshot.
      });
    } else {
      schedule(entry);
    }
    return () => {
      entry.listeners.delete(listener);
      if (entry.listeners.size > 0) return;
      activeEntries.delete(entry as Entry<unknown>);
      clearTimer(entry);
      entry.controller?.abort();
      entry.cleanupTimer = setTimeout(() => {
        if (entry.listeners.size === 0) entries.delete(key);
      }, CACHE_TIME_MS);
    };
  },

  update<T>(key: string, options: QueryOptions<T>, wasEnabled = false): void {
    const entry = entries.get(key) as Entry<T> | undefined;
    if (!entry) return;
    const wasInterval = intervalFor(entry);
    entry.options = options;
    const isEnabled = options.enabled !== false;
    const nextInterval = intervalFor(entry);
    if (!isEnabled) {
      clearTimer(entry);
      entry.controller?.abort();
      return;
    }
    if (!wasEnabled && isEnabled) {
      void fetchEntry(entry, false).catch(() => {});
      return;
    }
    // Only reschedule when the effective cadence changed. Function-valued
    // options are recreated by React renders, but their result usually is
    // not; resetting the timer on every render would starve polling.
    if (wasInterval !== nextInterval) schedule(entry);
  },

  getSnapshot<T>(key: string, options: QueryOptions<T>): QuerySnapshot<T> {
    return getOrCreate(key, options).snapshot;
  },

  refetch<T>(key: string, options: QueryOptions<T>): Promise<T | undefined> {
    const entry = getOrCreate(key, options);
    if (entry.listeners.size === 0) activeEntries.add(entry as Entry<unknown>);
    return fetchEntry(entry, true);
  },

  clear(): void {
    for (const entry of entries.values()) {
      clearTimer(entry);
      entry.controller?.abort();
    }
    entries.clear();
    activeEntries.clear();
  },
};

export function useSharedQuery<T>(key: string, options: QueryOptions<T>): UseSharedQueryResult<T> {
  const optionsRef = useRef(options);
  optionsRef.current = options;
  const previousKeyRef = useRef(key);
  const previousEnabledRef = useRef(options.enabled !== false);

  const subscribe = useCallback((listener: Listener) => {
    return registry.subscribe(key, listener, optionsRef.current);
  }, [key]);
  const getSnapshot = useCallback(() => registry.getSnapshot<T>(key, optionsRef.current), [key]);

  useEffect(() => {
    const sameKey = previousKeyRef.current === key;
    registry.update(key, optionsRef.current, sameKey && previousEnabledRef.current);
    previousKeyRef.current = key;
    previousEnabledRef.current = optionsRef.current.enabled !== false;
  }, [key, options.enabled, options.fetcher, options.pollingInterval, options.staleTime]);

  const snapshot = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  const refetch = useCallback(() => registry.refetch(key, optionsRef.current), [key]);
  return { ...snapshot, refetch };
}

export interface SharedPollingOptions {
  enabled?: boolean;
  interval: number | null;
  task: (signal: AbortSignal) => Promise<void>;
}

/**
 * Shared scheduler for imperative polling tasks (batch status, paginated
 * views). It uses the same deduplication, visibility, abort, and recursive
 * timeout machinery as data queries without forcing the task owner to move
 * its local state into the registry.
 */
export function useSharedPolling(key: string, options: SharedPollingOptions): () => Promise<void> {
  const query = useSharedQuery<number>(`poll:${key}`, {
    enabled: options.enabled !== false && options.interval != null,
    fetchOnMount: false,
    staleTime: 0,
    pollingInterval: options.interval,
    fetcher: async (signal) => {
      await options.task(signal);
      return Date.now();
    },
  });
  return useCallback(async () => {
    await query.refetch();
  }, [query.refetch]);
}

/** Test-only reset hook. It is harmless in production and avoids cache leakage between isolated tests. */
export function clearSharedQueryCache(): void {
  registry.clear();
}
