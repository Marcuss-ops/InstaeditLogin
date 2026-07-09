/**
 * Unit tests for web/src/lib/probe-cache.ts.
 *
 * The cache module is pure-data with a sessionStorage side effect, so the
 * tests replace `window.sessionStorage` with a per-test in-memory mock.
 * That keeps us off the real sessionStorage (which would leak between
 * tests and across reloads) while still exercising the real try/catch
 * wrappers and JSON.parse / JSON.stringify paths.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  PROBE_CACHE_KEY,
  PROBE_CACHE_TTL_MS,
  clearProbeCache,
  getProbeCacheStats,
  isProbeCacheFresh,
  readProbeCache,
  resetProbeCacheStats,
  writeProbeCache,
  type CachedProbe,
  type ProbeCacheStats,
} from "./probe-cache";

/** Build a well-formed fresh entry, optionally overriding fields. */
function makeEntry(overrides: Partial<CachedProbe> = {}): CachedProbe {
  return {
    lastOkAt: Date.now(),
    backend: { ok: true, url: "https://api.example.com", status: 200 },
    ...overrides,
  };
}

interface MockSessionStorage {
  store: Map<string, string>;
  api: Storage;
}

function makeMockStorage(): MockSessionStorage {
  const store = new Map<string, string>();
  const api: Storage = {
    getItem: vi.fn((key: string) => store.get(key) ?? null),
    setItem: vi.fn((key: string, value: string) => {
      store.set(key, value);
    }),
    removeItem: vi.fn((key: string) => {
      store.delete(key);
    }),
    clear: vi.fn(() => {
      store.clear();
    }),
    key: vi.fn((i: number) => Array.from(store.keys())[i] ?? null),
    get length() {
      return store.size;
    },
  };
  return { store, api };
}

describe("probe-cache", () => {
  let mock: MockSessionStorage;
  let original: Storage;

  beforeEach(() => {
    mock = makeMockStorage();
    // Counters are module-level in-memory state; reset before each
    // test so hits/misses/forceClears don't leak between cases.
    resetProbeCacheStats();
    original = window.sessionStorage;
    Object.defineProperty(window, "sessionStorage", {
      value: mock.api,
      configurable: true,
      writable: true,
    });
  });

  afterEach(() => {
    Object.defineProperty(window, "sessionStorage", {
      value: original,
      configurable: true,
      writable: true,
    });
  });

  // --------------------------------------------------------------------------
  // readProbeCache
  // --------------------------------------------------------------------------

  describe("readProbeCache", () => {
    it("returns null when no entry exists", () => {
      expect(readProbeCache()).toBeNull();
    });

    it("returns null for an empty-string value (treated as missing)", () => {
      mock.store.set(PROBE_CACHE_KEY, "");
      expect(readProbeCache()).toBeNull();
    });

    it("returns the parsed entry when it is fresh", () => {
      const entry = makeEntry();
      mock.store.set(PROBE_CACHE_KEY, JSON.stringify(entry));
      expect(readProbeCache()).toEqual(entry);
    });

    it("returns null when the entry is older than the default TTL", () => {
      const entry = makeEntry({ lastOkAt: Date.now() - PROBE_CACHE_TTL_MS - 1 });
      mock.store.set(PROBE_CACHE_KEY, JSON.stringify(entry));
      expect(readProbeCache()).toBeNull();
    });

    it("returns null and removes the entry when JSON is corrupt", () => {
      mock.store.set(PROBE_CACHE_KEY, "{not valid json");
      expect(readProbeCache()).toBeNull();
      // Corrupt entry should be cleared so a future write starts clean.
      expect(mock.api.removeItem).toHaveBeenCalledWith(PROBE_CACHE_KEY);
    });

    it("returns null and removes the entry when backend.ok !== true (schema-broken)", () => {
      const broken = {
        lastOkAt: Date.now(),
        backend: {
          ok: false,
          url: "https://x",
          status: 500,
          reason: "http_error",
          message: "boom",
        },
      };
      mock.store.set(PROBE_CACHE_KEY, JSON.stringify(broken));
      expect(readProbeCache()).toBeNull();
      expect(mock.api.removeItem).toHaveBeenCalledWith(PROBE_CACHE_KEY);
    });

    it("returns null and removes the entry when lastOkAt is not a number", () => {
      mock.store.set(
        PROBE_CACHE_KEY,
        JSON.stringify({
          lastOkAt: "not a number",
          backend: { ok: true, url: "https://x", status: 200 },
        }),
      );
      expect(readProbeCache()).toBeNull();
      expect(mock.api.removeItem).toHaveBeenCalledWith(PROBE_CACHE_KEY);
    });

    it("returns null and removes the entry when backend is missing", () => {
      mock.store.set(
        PROBE_CACHE_KEY,
        JSON.stringify({ lastOkAt: Date.now() }),
      );
      expect(readProbeCache()).toBeNull();
      expect(mock.api.removeItem).toHaveBeenCalledWith(PROBE_CACHE_KEY);
    });

    it("returns null when sessionStorage.getItem throws (private mode)", () => {
      mock.api.getItem = vi.fn(() => {
        throw new Error("SecurityError: storage access denied");
      });
      expect(readProbeCache()).toBeNull();
    });

    it("respects a custom maxAgeMs argument", () => {
      const entry = makeEntry({ lastOkAt: Date.now() - 1_000 });
      mock.store.set(PROBE_CACHE_KEY, JSON.stringify(entry));
      expect(readProbeCache(2_000)).toEqual(entry);
      expect(readProbeCache(500)).toBeNull();
    });
  });

  // --------------------------------------------------------------------------
  // writeProbeCache
  // --------------------------------------------------------------------------

  describe("writeProbeCache", () => {
    it("persists a fresh entry as JSON under PROBE_CACHE_KEY", () => {
      const entry = makeEntry();
      writeProbeCache(entry);
      expect(mock.api.setItem).toHaveBeenCalledWith(
        PROBE_CACHE_KEY,
        JSON.stringify(entry),
      );
      expect(mock.store.get(PROBE_CACHE_KEY)).toBe(JSON.stringify(entry));
    });

    it("is a no-op (no throw) when sessionStorage.setItem throws — private mode", () => {
      mock.api.setItem = vi.fn(() => {
        throw new Error("SecurityError: storage disabled");
      });
      const entry = makeEntry();
      expect(() => writeProbeCache(entry)).not.toThrow();
    });

    it("is a no-op (no throw) when sessionStorage.setItem throws — quota exceeded", () => {
      mock.api.setItem = vi.fn(() => {
        const err = new Error("Quota exceeded");
        err.name = "QuotaExceededError";
        throw err;
      });
      const entry = makeEntry();
      expect(() => writeProbeCache(entry)).not.toThrow();
    });
  });

  // --------------------------------------------------------------------------
  // clearProbeCache
  // --------------------------------------------------------------------------

  describe("clearProbeCache", () => {
    it("removes the cache key from sessionStorage", () => {
      mock.store.set(PROBE_CACHE_KEY, JSON.stringify(makeEntry()));
      clearProbeCache();
      expect(mock.api.removeItem).toHaveBeenCalledWith(PROBE_CACHE_KEY);
    });

    it("is a no-op (no throw) when sessionStorage.removeItem throws", () => {
      mock.api.removeItem = vi.fn(() => {
        throw new Error("SecurityError");
      });
      expect(() => clearProbeCache()).not.toThrow();
    });
  });

  // --------------------------------------------------------------------------
  // isProbeCacheFresh
  // --------------------------------------------------------------------------

  describe("isProbeCacheFresh", () => {
    it("returns false for null", () => {
      expect(isProbeCacheFresh(null)).toBe(false);
    });

    it("returns true for a freshly-stamped entry", () => {
      expect(isProbeCacheFresh(makeEntry())).toBe(true);
    });

    it("returns false for an entry older than the default TTL", () => {
      const entry = makeEntry({ lastOkAt: Date.now() - PROBE_CACHE_TTL_MS - 1 });
      expect(isProbeCacheFresh(entry)).toBe(false);
    });

    it("respects a custom maxAgeMs — within window returns true", () => {
      const entry = makeEntry({ lastOkAt: Date.now() - 5_000 });
      expect(isProbeCacheFresh(entry, 10_000)).toBe(true);
    });

    it("respects a custom maxAgeMs — outside window returns false", () => {
      const entry = makeEntry({ lastOkAt: Date.now() - 5_000 });
      expect(isProbeCacheFresh(entry, 1_000)).toBe(false);
    });

    it("treats exactly-at-TTL as NOT fresh (strict < comparison)", () => {
      // Edge case: the implementation uses `Date.now() - lastOkAt < maxAgeMs`
      // so an entry that is exactly maxAgeMs old is considered stale.
      // We pin Date.now so the test is deterministic regardless of the
      // machine clock.
      const now = 1_700_000_000_000;
      const dateNowSpy = vi.spyOn(Date, "now").mockReturnValue(now);
      try {
        const exactlyAtTtl = makeEntry({ lastOkAt: now - 1_000 });
        expect(isProbeCacheFresh(exactlyAtTtl, 1_000)).toBe(false);
        expect(isProbeCacheFresh(exactlyAtTtl, 1_001)).toBe(true);
        expect(isProbeCacheFresh(exactlyAtTtl, 999)).toBe(false);
      } finally {
        dateNowSpy.mockRestore();
      }
    });
  });

  // --------------------------------------------------------------------------
  // getProbeCacheStats / resetProbeCacheStats
  // --------------------------------------------------------------------------

  describe("getProbeCacheStats / resetProbeCacheStats", () => {
    const zero: ProbeCacheStats = { hits: 0, misses: 0, forceClears: 0 };

    it("starts at all-zero on a fresh module load (reset by beforeEach)", () => {
      expect(getProbeCacheStats()).toEqual(zero);
    });

    it("increments hits on a fresh read", () => {
      mock.store.set(PROBE_CACHE_KEY, JSON.stringify(makeEntry()));
      readProbeCache();
      expect(getProbeCacheStats()).toEqual({ hits: 1, misses: 0, forceClears: 0 });
    });

    it("increments hits once per call and is independent of returned reference", () => {
      const entry = makeEntry();
      mock.store.set(PROBE_CACHE_KEY, JSON.stringify(entry));
      const first = readProbeCache();
      const second = readProbeCache();
      const third = readProbeCache();
      expect(first).toEqual(entry);
      expect(second).toEqual(entry);
      expect(third).toEqual(entry);
      expect(getProbeCacheStats()).toEqual({ hits: 3, misses: 0, forceClears: 0 });
    });

    it("returns a snapshot — mutating it does not affect future reads", () => {
      const snapshot = getProbeCacheStats();
      snapshot.hits = 999;
      snapshot.misses = 999;
      snapshot.forceClears = 999;
      // Module-level state must be untouched.
      expect(getProbeCacheStats()).toEqual(zero);
    });

    it("increments misses on a missing entry", () => {
      readProbeCache();
      expect(getProbeCacheStats()).toEqual({ hits: 0, misses: 1, forceClears: 0 });
    });

    it("increments misses on an expired entry", () => {
      mock.store.set(
        PROBE_CACHE_KEY,
        JSON.stringify(makeEntry({ lastOkAt: Date.now() - PROBE_CACHE_TTL_MS - 1 })),
      );
      readProbeCache();
      expect(getProbeCacheStats()).toEqual({ hits: 0, misses: 1, forceClears: 0 });
    });

    it("increments misses on a corrupt entry", () => {
      mock.store.set(PROBE_CACHE_KEY, "{not valid");
      readProbeCache();
      expect(getProbeCacheStats()).toEqual({ hits: 0, misses: 1, forceClears: 0 });
    });

    it("increments misses on a schema-broken entry (backend.ok !== true)", () => {
      mock.store.set(
        PROBE_CACHE_KEY,
        JSON.stringify({
          lastOkAt: Date.now(),
          backend: { ok: false, url: "x", status: 500, reason: "http_error", message: "no" },
        }),
      );
      readProbeCache();
      expect(getProbeCacheStats()).toEqual({ hits: 0, misses: 1, forceClears: 0 });
    });

    it("increments misses when getItem throws (private mode)", () => {
      mock.api.getItem = vi.fn(() => {
        throw new Error("SecurityError");
      });
      readProbeCache();
      expect(getProbeCacheStats()).toEqual({ hits: 0, misses: 1, forceClears: 0 });
    });

    it("increments misses on every null-return path; hit rate reflects reality", () => {
      // 3 misses (missing, corrupt, expired)
      readProbeCache();
      mock.store.set(PROBE_CACHE_KEY, "{bad");
      readProbeCache();
      mock.store.set(
        PROBE_CACHE_KEY,
        JSON.stringify(makeEntry({ lastOkAt: Date.now() - PROBE_CACHE_TTL_MS - 1 })),
      );
      readProbeCache();
      // 2 hits
      mock.store.set(PROBE_CACHE_KEY, JSON.stringify(makeEntry()));
      readProbeCache();
      mock.store.set(PROBE_CACHE_KEY, JSON.stringify(makeEntry()));
      readProbeCache();
      expect(getProbeCacheStats()).toEqual({ hits: 2, misses: 3, forceClears: 0 });
    });

    it("increments forceClears on every clearProbeCache call", () => {
      clearProbeCache();
      clearProbeCache();
      clearProbeCache();
      expect(getProbeCacheStats()).toEqual({ hits: 0, misses: 0, forceClears: 3 });
    });

    it("increments forceClears even when removeItem throws (the affordance is recorded)", () => {
      mock.api.removeItem = vi.fn(() => {
        throw new Error("SecurityError");
      });
      clearProbeCache();
      expect(getProbeCacheStats()).toEqual({ hits: 0, misses: 0, forceClears: 1 });
    });

    it("resetProbeCacheStats resets all three counters to zero", () => {
      mock.store.set(PROBE_CACHE_KEY, JSON.stringify(makeEntry()));
      readProbeCache();
      readProbeCache();
      clearProbeCache();
      // Pre-reset: 2 hits, 1 force clear
      expect(getProbeCacheStats()).toEqual({ hits: 2, misses: 0, forceClears: 1 });
      resetProbeCacheStats();
      expect(getProbeCacheStats()).toEqual(zero);
    });
  });
});
