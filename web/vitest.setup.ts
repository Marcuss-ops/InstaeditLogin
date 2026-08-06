import { afterEach, vi } from "vitest";
import { cleanup } from "@testing-library/react";
// Registers jest-dom's matchers with Vitest's `expect.extend(...)` at
// runtime. Vitest runs this setup file from `vite.config.ts`'s
// `setupFiles`, so the import side-effect fires before any test executes.
//
// Type-augmentation (so tsc knows about `toBeInTheDocument`,
// `toHaveClass`, `toHaveTextContent`) is loaded separately by
// `src/types/jest-dom.d.ts`, which is part of `tsconfig.app.json`'s
// `include: ["src"]` scope. The runtime import here and the type-only
// import there intentionally live in different files because they target
// different toolchains (Vitest runtime vs. tsc).
import "@testing-library/jest-dom/vitest";
// Global IntersectionObserver mock. Several components (e.g. ScrollReveal)
// use IntersectionObserver to trigger scroll animations. jsdom does not
// implement it, so we provide a minimal stub that lets components mount
// without errors. The callback is never fired, which keeps the reveal
// elements in their initial state during tests.
class IntersectionObserverMock {
  observe = vi.fn();
  disconnect = vi.fn();
  unobserve = vi.fn();
}

Object.defineProperty(window, "IntersectionObserver", {
  writable: true,
  configurable: true,
  value: IntersectionObserverMock,
});
// Global ResizeObserver stub (jsdom does not implement it). The canvas
// stage in CoverEditor uses it to size the scaled canvas; the stub keeps
// the component mountable in tests. Tests that need the stage to actually
// layout override this with a callback-firing mock (see CoverEditor.test).
class ResizeObserverMock {
  observe = vi.fn();
  disconnect = vi.fn();
  unobserve = vi.fn();
}

Object.defineProperty(window, "ResizeObserver", {
  writable: true,
  configurable: true,
  value: ResizeObserverMock,
});
// Global toast-bus reset for cross-test hygiene.
//
// Why: `web/src/lib/auth.ts`'s `authedFetch` auto-emits a toast.error on
// every non-401 rejection. When test files (Login.test.tsx, Compose.test.tsx,
// Settings.test.tsx, Posts.test.tsx) mock a non-ok response, those toasts
// silently land on the module-level `toastBus` singleton. None of those
// tests assert on the bus today, so tests still pass — but residual
// entries outlive their test (5s real-time auto-dismiss), and any future
// test that queries the toast DOM inherits a polluted baseline.
//
// Centralizing the reset here means EVERY test file (those that
// explicitly use the bus for unit tests, AND those that side-effect it
// through auth.ts's auto-emit) starts with an empty queue.
import { toastBus } from "./src/components/toast/toast-bus";
import { clearSharedQueryCache } from "./src/lib/queryRegistry";
// Global accounts-manifest cache reset for cross-test hygiene.
//
// Why: `listAllAccounts` (channelsApi) caches the /api/v1/accounts
// manifest for 60s and dedupes concurrent callers. Page tests that stub
// fetch (Compose, Uploads, Linking, DriveBatchImportDialog, ...) rely on
// their OWN stub being hit; without a reset, a cache entry populated by an
// earlier test (same file or same worker) short-circuits the stub and the
// page renders stale data. Centralizing the reset here means every test
// file starts with an empty accounts cache, exactly like toastBus.
//
// The import is DYNAMIC on purpose: a static import here would resolve the
// real channelsApi → lib/auth graph at setup time, BEFORE a test file's
// vi.mock("../../lib/auth", ...) registration (e.g.
// AccountSwitcher.test.tsx) — the mock would then never apply to the
// already-cached module instance. The afterEach dynamic import resolves
// the per-file registry AFTER the file's mocks are in place.
afterEach(async () => {
  cleanup();
  toastBus.__resetForTests();
  clearSharedQueryCache();
  // Files that vi.mock() the whole channelsApi module (e.g.
  // useYouTubeChannels.test.ts) resolve a strict mock with NO
  // clearAccountsCache export — vitest throws on property access, so a
  // try/catch is required (not just optional chaining). Skipping is
  // exactly right there because those files never touch the real cache.
  try {
    const { clearAccountsCache } = await import(
      "./src/features/channels/api/channelsApi",
    );
    clearAccountsCache?.();
  } catch {
    // mocked channelsApi without the cache reset export
  }
});

// Stub VITE_API_BASE_URL so isomorphic fetch mocks in component tests
// resolve against a stable localhost origin rather than an empty
// string. Required by Landing.test.tsx (YouTube embeds mock path)
// and the InternalUploads test cluster (waitFor over fetch mocks).
// Production builds set this explicitly via .env / fly secrets —
// see scripts/verify-api-base-url.ts for the live-env contract.
vi.stubEnv('VITE_API_BASE_URL', 'http://localhost:8080');
