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

afterEach(() => {
  cleanup();
  toastBus.__resetForTests();
});
