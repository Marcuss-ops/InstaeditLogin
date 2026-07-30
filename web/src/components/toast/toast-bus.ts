/**
 * Toast bus — module-level singleton that holds the queue of in-flight
 * toasts and notifies subscribers on every change.
 *
 * Why a module-level singleton + a `useSyncExternalStore`-subscribed
 * `ToastViewport` instead of a React-Context-only design: non-React
 * modules (notably `web/src/lib/auth.ts` and the cross-route OAuth
 * callback in `AuthCallback.tsx`) need to push toasts without holding
 * a React component context. Context cannot cross the module boundary
 * — only a top-level singleton can. The shape (`subscribe` +
 * `getSnapshot`) is deliberately the same that React's
 * `useSyncExternalStore` expects, so the viewport is one line of
 * subscription code.
 *
 * Flood-guard: identical (kind, message) pairs pushed within 1500ms
 * collapse into a single entry, so parallel `authedFetch` failures
 * (e.g. dashboard + posts + workspaces all 500ing on mount) do not
 * stack up on the user's screen.
 *
 * Snapshot stability: `useSyncExternalStore` requires `getSnapshot()`
 * to return the same reference when nothing has changed (else React
 * would re-render forever). We cache the snapshot in `cachedSnapshot`
 * and only refresh it from `notify()`, so unchanged state yields the
 * same reference AND the snapshot returned is a defensive copy that
 * callers can mutate without affecting internal state.
 *
 * TEST-ONLY: `__resetForTests` and `__sizeForTests` are exported with
 * a `__` prefix to signal "no production caller". Production code
 * touches `push`, `dismiss`, `dismissAll`, `subscribe`, `getSnapshot`.
 */
import type { ToastEntry, ToastKind } from "./types";

type Listener = (snapshot: ToastEntry[]) => void;

const DEFAULT_DURATION_MS = 5000;
const DEDUPE_WINDOW_MS = 1500;

let entries: ToastEntry[] = [];
let cachedSnapshot: ToastEntry[] = [];
const listeners = new Set<Listener>();

function refreshSnapshot(): ToastEntry[] {
  cachedSnapshot = entries.slice();
  return cachedSnapshot;
}

function notify(): void {
  const snapshot = refreshSnapshot();
  for (const listener of listeners) listener(snapshot);
}

function genId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `t_${Date.now()}_${Math.random().toString(36).slice(2, 9)}`;
}

function push(
  kind: ToastKind,
  message: string,
  durationMs: number = DEFAULT_DURATION_MS,
): string {
  const now = Date.now();
  // Flood-guard: if the very last entry has the same (kind, message)
  // and was added within the dedupe window, drop the new push and
  // return the existing entry's id. Prevents a cascade of identical
  // failure toasts when several parallel calls reject at once.
  const tail = entries[entries.length - 1];
  if (
    tail &&
    tail.kind === kind &&
    tail.message === message &&
    now - tail.createdAt < DEDUPE_WINDOW_MS
  ) {
    return tail.id;
  }
  const id = genId();
  entries = [...entries, { id, kind, message, createdAt: now }];
  notify();
  if (durationMs > 0 && typeof setTimeout === "function") {
    setTimeout(() => dismiss(id), durationMs);
  }
  return id;
}

function dismiss(id: string): void {
  const before = entries.length;
  entries = entries.filter((entry) => entry.id !== id);
  if (entries.length !== before) notify();
}

function dismissAll(): void {
  if (entries.length === 0) return; // silent no-op on already-empty queue
  entries = [];
  notify();
}

export const toastBus = {
  subscribe(listener: Listener): () => void {
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  },
  getSnapshot(): ToastEntry[] {
    // Same reference until the next `notify()` — required for
    // useSyncExternalStore's referential-equality comparison to
    // avoid spurious re-renders.
    return cachedSnapshot;
  },
  push,
  dismiss,
  dismissAll,
  // test-only helpers — never call from production code
  __resetForTests(): void {
    entries = [];
    cachedSnapshot = [];
    listeners.clear();
  },
  __sizeForTests(): number {
    return entries.length;
  },
};
