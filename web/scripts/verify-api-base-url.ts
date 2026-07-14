/**
 * Build-time validator for VITE_API_BASE_URL.
 *
 * Fired during `vite build` (via an inline plugin in `vite.config.ts`) AND as
 * a standalone CLI script via `npm run check:env`. The goal is to catch
 * obvious misconfigurations BEFORE the deployment goes live — the most
 * common one being a `VITE_API_BASE_URL` that still points at
 * http://localhost:8080 or has been left empty (which caused the recurring
 * `vercel DEPLOYMENT_NOT_FOUND` 404 the moment a user clicked an OAuth button).
 *
 * Behavior is context-aware, driven by VERCEL_ENV (Vercel-injected at build
 * time):
 *
 *   context=production | preview    (real Vercel deployment)
 *     - empty VITE_API_BASE_URL          → ERROR
 *     - hostname is localhost / 127.x    → ERROR  (Vercel can't reach your laptop)
 *     - protocol is http                 → ERROR  (browser blocks mixed content)
 *     - malformed URL                    → ERROR
 *     - valid https URL                  → OK
 *
 *   context=vercel_dev    (VERCEL_ENV=development, i.e. `vercel dev` locally)
 *     - localhost is allowed because vercel dev runs on the same machine;
 *       protocol warnings are downgraded to WARN.
 *
 *   context=local         (VERCEL_ENV unset, i.e. pure laptop dev build)
 *     - empty URL  → WARN (might be intentional during initial bootstrap)
 *     - localhost  → OK   (that's the whole point of local dev)
 *     - http       → OK
 *
 * The validator is a pure function over an env-like object so vitest can
 * mock process.env without leaking into other suites. The CLI wrapper
 * (runValidationCli) exits non-zero on ERROR so it works both as a fail-fast
 * pre-step in CI and as a quiet pass in local dev.
 *
 * IMPORTANT — STRIP-TYPES COMPATIBILITY:
 * This file is ALSO executed via `node --experimental-strip-types` (no
 * compilation step, only TS syntax stripping). Keep it plain: NO `enum`,
 * NO `const` assertions (`as const`), NO namespace values, NO
 * `experimentalDecorators`, NO `import = require()`. Interfaces, type
 * aliases, optional chaining `?.`, and nullish coalescing `??` are all fine
 * — those are JS syntax that Node 22 natively understands.
 */

import { fileURLToPath } from "node:url";
import { resolve as resolvePath } from "node:path";

export type ValidationLevel = "ok" | "warn" | "error";

export type ValidationContext = "production" | "preview" | "vercel_dev" | "local";

export interface ValidationResult {
  level: ValidationLevel;
  context: ValidationContext;
  messages: string[];
}

/**
 * Hostnames that resolve to the local machine and therefore cannot be
 * reached from Vercel's edge network. IPv6 loopback `[::1]` is included
 * for completeness even though it's an unusual choice for a backend URL.
 */
const LOCAL_LOOPBACK_HOSTS: ReadonlySet<string> = new Set([
  "localhost",
  "127.0.0.1",
  "0.0.0.0",
  "[::1]",
]);

/**
 * When the SPA runs in demo mode (VITE_DEMO_MODE=true), every API
 * call short-circuits to mock data and the VITE_API_BASE_URL value
 * is unused. Skipping the URL check entirely lets the user deploy
 * a frontend preview on Vercel while the Go backend is offline
 * (e.g. Fly.io payment blocked during bootstrap). See
 * web/src/lib/demo.ts for the runtime contract.
 */
function isDemoMode(env: Env): boolean {
  return (env.VITE_DEMO_MODE ?? "").toLowerCase() === "true";
}

type Env = Readonly<Record<string, string | undefined>>;

/**
 * Run the full check and return a structured result. Pure function — does
 * not log, not exit, just inspects the supplied env. Tests pass a synthetic
 * env object; production callers (Vite plugin, CLI) pass process.env.
 */
export function validateApiBaseUrl(env: Env = process.env): ValidationResult {
  // Demo mode supersedes every other check: the URL is unused at
  // runtime (mockFetch returns canned Responses), so accepting an
  // empty / invalid / localhost value here is intentional and the
  // only way the user can ship a frontend-only Vercel preview.
  if (isDemoMode(env)) {
    const rawUrl = env.VITE_API_BASE_URL?.trim() ?? "";
    const vercelEnv = (env.VERCEL_ENV ?? "").toLowerCase();
    const context: ValidationContext =
      vercelEnv === "production"
        ? "production"
        : vercelEnv === "preview"
          ? "preview"
          : vercelEnv === "development"
            ? "vercel_dev"
            : "local";
    return {
      level: "ok",
      context,
      messages: [
        `VITE_DEMO_MODE=true — VITE_API_BASE_URL is ignored${rawUrl ? ` (was: ${redactUrl(rawUrl)})` : " (empty)"}. ` +
          `Every authedFetch call returns mock data; see web/src/lib/demo.ts. ` +
          `Remove VITE_DEMO_MODE from Vercel once the Go backend is live.`,
      ],
    };
  }

  const rawUrl = env.VITE_API_BASE_URL?.trim() ?? "";
  const vercelEnv = (env.VERCEL_ENV ?? "").toLowerCase();
  const context: ValidationContext =
    vercelEnv === "production"
      ? "production"
      : vercelEnv === "preview"
        ? "preview"
        : vercelEnv === "development"
          ? "vercel_dev"
          : "local";
  const isDeployed = context === "production" || context === "preview";

  // ---- Empty value ----
  if (rawUrl === "") {
    if (isDeployed) {
      return {
        level: "error",
        context,
        messages: [
          "VITE_API_BASE_URL is empty. The deployment will 404 with DEPLOYMENT_NOT_FOUND at runtime.\n" +
            "   Fix: Vercel → Project Settings → Environment Variables → add VITE_API_BASE_URL → redeploy.",
        ],
      };
    }
    return {
      level: "warn",
      context,
      messages: [
        "VITE_API_BASE_URL is empty. Login buttons will fall back to http://localhost:8080 (probably what you want during initial dev).",
      ],
    };
  }

  // ---- Parse URL ----
  let parsed: URL;
  try {
    parsed = new URL(rawUrl);
  } catch {
    if (isDeployed) {
      return {
        level: "error",
        context,
        messages: [
          `VITE_API_BASE_URL is not a valid URL: "${rawUrl}".\n` +
            `   Expected format: https://api.example.com (no trailing slash).`,
        ],
      };
    }
    return {
      level: "warn",
      context,
      messages: [
        `VITE_API_BASE_URL is not a valid URL: "${rawUrl}" — ignored for local build.`,
      ],
    };
  }

  const messages: string[] = [];
  let level: ValidationLevel = "ok";

  // ---- Localhost / loopback check ----
  if (isDeployed && LOCAL_LOOPBACK_HOSTS.has(parsed.hostname.toLowerCase())) {
    messages.push(
      `VITE_API_BASE_URL points to ${parsed.hostname} which is the local machine. Vercel's edge network cannot reach your laptop.\n` +
        `   Fix: set it to the deployed Go backend URL — Settings → Environment Variables → redeploy.`,
    );
    level = "error";
  }

  // ---- HTTPS-only requirement for deployed contexts ----
  if (isDeployed && parsed.protocol !== "https:") {
    messages.push(
      `VITE_API_BASE_URL uses ${parsed.protocol}// but production/preview MUST be https://.\n` +
        `   Browsers block mixed content on https pages — OAuth redirects would fail.`,
    );
    if (level === "ok") level = "error";
  }

  // ---- Success line ----
  if (messages.length === 0) {
    messages.push(`VITE_API_BASE_URL = ${redactUrl(parsed)} (context: ${context})`);
  }

  return { level, context, messages };
}

/**
 * Strip query string, userinfo/password, AND hash fragment from a URL so it
 * can be logged without leaking secrets. Common leak vectors for VITE_API_BASE_URL
 * are unlikely but possible (e.g. tokens appended after a copy/paste from a
 * docs page like `https://api.example.com?token=…&redirect=…#anchor`), and
 * once leaked into a Vercel build log the value is permanent.
 *
 * The URL setters (search/password/hash) re-serialize on assignment, so the
 * `?redacted` / `***` / `#redacted` placeholders round-trip cleanly.
 */
function redactUrl(u: URL | string): string {
  try {
    const parsed = typeof u === "string" ? new URL(u) : u;
    if (parsed.search) parsed.search = "?redacted";
    if (parsed.password) parsed.password = "***";
    if (parsed.hash) parsed.hash = "#redacted";
    return parsed.toString();
  } catch {
    return String(u);
  }
}

/**
 * CLI entrypoint: logs the validator output and exits non-zero on ERROR.
 * Designed to be invoked via `npm run check:env` and is a no-op when this
 * module is imported (e.g. from `vite.config.ts` or from vitest) thanks to
 * the import.meta.url guard at the bottom of the file.
 *
 * We rely on Node's default behavior when a top-level throw escapes: exit
 * code is 1 and the error message is printed. No need to set process.exitCode
 * manually, which would risk both error paths firing concurrently and
 * doubling the output noise.
 */
export function runValidationCli(): void {
  const result = validateApiBaseUrl();
  const tag =
    result.level === "error" ? "[FAIL]" : result.level === "warn" ? "[WARN]" : "[OK]  ";
  for (const msg of result.messages) {
    console.log(`verify-api-base-url ${tag} ${msg}`);
  }
  if (result.level === "error") {
    throw new Error(
      `VITE_API_BASE_URL validation failed in ${result.context} context. ` +
        `Vercel deployment would 404 at runtime — see the [FAIL] message above for the fix.`,
    );
  }
}

// ---------------------------------------------------------------------------
// Auto-run: only when this file is the CLI entrypoint.
// ---------------------------------------------------------------------------
//
// `tsc -b` and `vite build` both load this module via static `import`, which
// does NOT set process.argv[1] to this file's path, so the guard below is
// false → runValidationCli is a no-op during type-checking / Vite plugin
// resolution. Only when invoked directly via
// `node --experimental-strip-types scripts/verify-api-base-url.ts` (or
// `npm run check:env`) does the comparison match.
//
// Robust guard spanning Windows + Git Bash quirks:
//   1. `resolvePath(argv[1])` normalizes:
//      - relative paths → cwd-relative → absolute
//      - Windows backslashes → forward slashes (on Windows the resolved
//        absolute path uses backslashes, but fileURLToPath returns matching
//        style)
//      - case mismatches (only on case-insensitive filesystems, e.g. macOS)
//   2. `fileURLToPath(import.meta.url)` returns the canonical absolute path
//      of the loaded module.
//   3. The two absolute paths are compared verbatim.
//
// Compared to the simpler `import.meta.url === pathToFileURL(argv[1]).href`,
// this handles relative argv[1] (e.g. `tsx scripts/...`) and Git Bash's
// forward-slash vs Windows backslash ambiguity.
const invokedDirectly =
  typeof process !== "undefined" &&
  !!process.argv[1] &&
  (() => {
    try {
      const argvPath = resolvePath(process.argv[1]);
      return argvPath === fileURLToPath(import.meta.url);
    } catch {
      return false;
    }
  })();

if (invokedDirectly) {
  runValidationCli();
}
