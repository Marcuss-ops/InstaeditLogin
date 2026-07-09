/**
 * SessionStorage-backed cache of the last successful backend probe.
 *
 * Why this exists
 * ---------------
 * `/login` and `/status` both run `probeBackend()` (and `/status` also
 * `probeAuthBoundary()`) on mount. Every mount of `/login` previously
 * flashed a "Checking…" pill while the probe was in flight, and every
 * mount issued a fresh GET /api/v1/health. For a user who bounces between
 * `/` and `/login` a few times in a session, that's a lot of redundant
 * requests and visual noise.
 *
 * This module stores the *last successful* probe result under the key
 * `probes.lastOkAt` in `sessionStorage`, with a 5-minute TTL. While the
 * entry is fresh, both pages hydrate their initial state from it
 * (zero "Checking…" flash, zero /health calls).
 *
 * Cache invariants
 * ----------------
 * 1. We only WRITE on `result.ok === true`. A failed probe must never
 *    overwrite a previously-good cached state — failures are transient
 *    and the cached "known good" snapshot remains the most trustworthy
 *    signal we have until a new probe succeeds.
 * 2. We READ strictly: an entry whose `lastOkAt` is older than TTL_MS is
 *    treated as missing. The session continues with a fresh probe.
 * 3. `sessionStorage` is per-tab, so closing the tab invalidates the
 *    cache automatically. This matches the "ad-hoc self-debug" use
 *    case of these pages — we never want stale probe results bleeding
 *    into a fresh tab the user opens tomorrow.
 * 4. Every storage call is wrapped in try/catch because `sessionStorage`
 *    throws in private-browsing modes in some browsers and when the
 *    quota is exhausted. A failed cache call must never break the page.
 */

import type { AuthBoundaryResult, ProbeResult } from "./auth";

export const PROBE_CACHE_KEY = "probes.lastOkAt";

/** 5 minutes — long enough to survive typical nav bounces, short enough
 *  that a user opening the app tomorrow sees fresh state. */
export const PROBE_CACHE_TTL_MS = 5 * 60 * 1000;

export type CachedProbe = {
  /** Epoch ms of the last successful probe that produced this entry. */
  lastOkAt: number;
  /** Result of the /api/v1/health probe. Always `ok: true` (we never
   *  cache failures — see invariants above). */
  backend: Extract<ProbeResult, { ok: true }>;
  /** Optional: result of the unauthenticated /api/v1/accounts probe
   *  (Status page only). Caching this lets the Status page render its
   *  "Auth boundary ✓ 401" stat on first paint, not after a second
   *  fetch. */
  authBoundary?: AuthBoundaryResult;
  /** Optional: parsed JSON body of /api/v1/health, so the Status page
   *  can show the {status, service, version, platforms} block on first
   *  paint instead of waiting for an extra round-trip. */
  healthBody?: Record<string, unknown> | null;
  /** Optional: latency of the /api/v1/health round-trip in ms. */
  latencyMs?: number;
};

/**
 * In-memory counters that tell the /status page how often the cache is
 * actually saving a /health round-trip. Reset on page reload (these are
 * deliberately NOT persisted to sessionStorage — they're per-tab
 * observability, not durable state).
 */
export type ProbeCacheStats = {
  /** readProbeCache returned a valid, fresh entry. */
  hits: number;
  /** readProbeCache returned null for any reason (missing, expired,
   *  corrupt, schema-broken, private mode, SSR). */
  misses: number;
  /** clearProbeCache was called (right-click / long-press dev affordance
   *  on the status pill). Tracked separately from misses because it's
   *  a user-initiated bypass, not a cache miss. */
  forceClears: number;
};

const probeCacheStats: ProbeCacheStats = { hits: 0, misses: 0, forceClears: 0 };

/** Returns a snapshot of the current counters. */
export function getProbeCacheStats(): ProbeCacheStats {
  return { ...probeCacheStats };
}

/** Reset all counters to zero. Used by tests and by future "reset" UI. */
export function resetProbeCacheStats(): void {
  probeCacheStats.hits = 0;
  probeCacheStats.misses = 0;
  probeCacheStats.forceClears = 0;
}

/**
 * Read the cache. Returns `null` if the entry is missing, corrupt,
 * schema-broken, or older than `maxAgeMs` (default = PROBE_CACHE_TTL_MS).
 *
 * Reads never throw — a corrupt entry is treated the same as a missing
 * one (a `JSON.parse` failure means the cache is poisoned and we want
 * to start fresh).
 *
 * Side effect: increments `probeCacheStats.hits` on a successful read
 * and `probeCacheStats.misses` on any null-return path. See
 * `getProbeCacheStats()` for the read API.
 */
export function readProbeCache(
  maxAgeMs: number = PROBE_CACHE_TTL_MS,
): CachedProbe | null {
  const result = readProbeCacheInner(maxAgeMs);
  if (result === null) {
    probeCacheStats.misses += 1;
  } else {
    probeCacheStats.hits += 1;
  }
  return result;
}

function readProbeCacheInner(
  maxAgeMs: number,
): CachedProbe | null {
  if (typeof window === "undefined") {
    return null;
  }
  let raw: string | null;
  try {
    raw = window.sessionStorage.getItem(PROBE_CACHE_KEY);
  } catch {
    // sessionStorage may be unavailable (private mode, sandboxed iframe).
    return null;
  }
  if (raw === null || raw === "") {
    return null;
  }

  let parsed: CachedProbe;
  try {
    parsed = JSON.parse(raw) as CachedProbe;
  } catch {
    // Corrupt entry — clear and fall through to "missing".
    try {
      window.sessionStorage.removeItem(PROBE_CACHE_KEY);
    } catch {
      /* ignore */
    }
    return null;
  }

  if (
    typeof parsed.lastOkAt !== "number" ||
    typeof parsed.backend !== "object" ||
    parsed.backend === null ||
    parsed.backend.ok !== true
  ) {
    // Schema drift or hand-tampered entry — discard.
    try {
      window.sessionStorage.removeItem(PROBE_CACHE_KEY);
    } catch {
      /* ignore */
    }
    return null;
  }

  if (Date.now() - parsed.lastOkAt > maxAgeMs) {
    return null;
  }

  return parsed;
}

/**
 * Persist a successful probe result to the cache. Silent no-op on
 * storage failure (private mode / quota).
 *
 * Callers MUST pass an `entry.backend.ok === true` result. We do not
 * re-check here so this stays a thin write — the caller's contract is
 * "only call this with a known-good result".
 */
export function writeProbeCache(entry: CachedProbe): void {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.sessionStorage.setItem(
      PROBE_CACHE_KEY,
      JSON.stringify(entry),
    );
  } catch {
    /* private mode / quota — the page still works without caching. */
  }
}

/**
 * Drop the cache entry. Used by the dev "force re-probe" affordance
 * (right-click / long-press on the /login status pill and the
 * /status green/red card). Increments `probeCacheStats.forceClears`.
 */
export function clearProbeCache(): void {
  probeCacheStats.forceClears += 1;
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.sessionStorage.removeItem(PROBE_CACHE_KEY);
  } catch {
    /* ignore */
  }
}

/** Convenience: is this entry still within the freshness window? */
export function isProbeCacheFresh(
  entry: CachedProbe | null,
  maxAgeMs: number = PROBE_CACHE_TTL_MS,
): boolean {
  if (entry === null) {
    return false;
  }
  return Date.now() - entry.lastOkAt < maxAgeMs;
}
