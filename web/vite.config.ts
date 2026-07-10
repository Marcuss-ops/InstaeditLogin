/// <reference types="vitest/config" />
import { defineConfig, type ResolvedConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
// `.ts` extension is required by `nodenext` module resolution (tsc -b flags
// generic TS2835 on extension-less relative imports). Vite/esbuild resolve
// the .ts file at runtime just fine.
import { validateApiBaseUrl } from './scripts/verify-api-base-url.ts'

/**
 * Vite plugin that fails the build when VITE_API_BASE_URL is misconfigured
 * for the deployment context. Reads VERCEL_ENV (Vercel-injected at build
 * time) to decide how strict to be — production/preview require a real
 * https URL, while local builds only warn.
 *
 * Why configResolved and not a separate hook: configResolved runs once at
 * the very start of `vite build`, before any expensive work like esbuild
 * module resolution / Rollup chunking. Failing here means CI sees the
 * [FAIL] line within a few hundred ms of starting the build, not after a
 * 30-second compile pass.
 *
 * `command === 'build'` gate: `vite dev` and `vitest run` both load this
 * config but should NOT fail — and on dev/test the success case stays
 * silent (no `[OK]` log spam every time Vite restarts). WARN and FAIL
 * still surface so the developer sees them.
 */
function verifyApiBaseUrlPlugin() {
  return {
    name: 'verify-api-base-url',
    configResolved(config: ResolvedConfig) {
      const isBuild = config.command === 'build';
      const result = validateApiBaseUrl();

      if (!isBuild && result.level === 'ok') {
        // dev / vitest runs: stay quiet on success to avoid per-restart noise
        return;
      }

      const tag =
        result.level === 'error'
          ? '[FAIL]'
          : result.level === 'warn'
            ? '[WARN]'
            : '[OK]  ';
      for (const msg of result.messages) {
        // Using a stable prefix so Vercel build logs are easy to grep.
        console.log(`verify-api-base-url ${tag} ${msg}`);
      }
      if (isBuild && result.level === 'error') {
        throw new Error(
          `VITE_API_BASE_URL validation failed in ${result.context} context. ` +
            `Vercel deployment would 404 at runtime — see the [FAIL] message above for the fix.`,
        );
      }
    },
  };
}

export default defineConfig({
  plugins: [react(), tailwindcss(), verifyApiBaseUrlPlugin()],
  test: {
    // jsdom for React component tests (Nav, DemoModal, etc.).
    // Component rendering uses jsdom; set via test.environment in vitest config.
    environment: 'jsdom',
    // Run tests in-process per file to keep logs readable; switch to
    // 'threads' (default) if a future test suite becomes slow.
    globals: false,
    setupFiles: ['./vitest.setup.ts'],
    include: ['src/**/*.test.{ts,tsx}', 'scripts/**/*.test.ts'],
  },
})
