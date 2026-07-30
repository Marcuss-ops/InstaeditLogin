import { defineConfig, devices } from "@playwright/test";

/**
 * Playwright E2E config — InstaeditLogin web/.
 *
 * Scoped narrowly: chromium-only, single worker, retry-free in
 * dev. No cross-browser matrix yet (the booking_events flow has
 * no Safari/Firefox-specific concerns; expanding the matrix
 * triples CI cost without coverage value). Add Firefox/WebKit
 * once the project has a CI budget allocated.
 *
 * Critical defaults:
 *   - baseURL http://localhost:5173 = Vite's default dev port.
 *   - webServer spawns `npm run dev` (Vite) instead of an actual
 *     production server. Mirrors the dev loop a contributor runs
 *     locally, so flows tested here are exactly what a manual
 *     smoke-test sees. Production parity comes from running the
 *     same spec against a `vite build && vite preview` server
 *     (`npm run preview`) when ready.
 *   - retries = 0 in dev; CI bumps to 1 via the `CI` env var
 *     (which Playwright sets when running under most CI providers).
 *   - screenshot, video, trace remain on `only-on-failure` /
 *     `retain-on-failure` so the repo's screenshot volume stays
 *     sane and the per-test trace artefacts never accumulate.
 *
 * Browser channel: we use Playwright's bundled chromium (NOT
 * system Chrome via `channel: 'chrome'`) because the BookingProvider
 * modal exercises popup-style behaviour (window.open with a
 * third-argument feature string), and the bundled build has a
 * pinned, reproducible WebKit/Chromium behaviour. System Chrome
 * is more variable across hosts. The browser binary install is a
 * one-time per dev host:
 *
 *   npx playwright install chromium --with-deps
 *
 * (or `npm run test:e2e:install` — same effect).
 */
export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: "list",
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "http://localhost:5173",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
  webServer: {
    command: "npm run dev",
    url: "http://localhost:5173",
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
    // Keep the Vite strict-mode validator quiet in dev context;
    // the booking_events POST is intercepted by Playwright so the
    // dev proxy (which normally forwards /api → :8080) never runs.
    env: {
      VITE_API_BASE_URL: "",
    },
  },
});
