/**
 * Self-healing recovery for stale-bundle chunk-load failures.
 *
 * Vite emits hashed chunk filenames, so after a new deploy lands any
 * browser that still holds an OLD `index.html` (an open tab or a stale
 * cache entry) tries to `import()` chunk files that no longer exist on
 * the CDN. The SPA fallback then serves `index.html` (HTML) where the
 * module should be, and the lazy import rejects with messages like:
 *
 *   - Chrome:  "Failed to fetch dynamically imported module: <url>"
 *   - Firefox: "error loading dynamically imported module: <url>"
 *   - Safari:  "Importing a module script failed"
 *
 * Instead of leaving the user on the error boundary forever, reload the
 * page once: the fresh `index.html` references the current chunks and
 * the app boots normally. `sessionStorage` guards against reload loops.
 */
const RELOAD_FLAG = "instaedit:chunk-reload-attempted";

const MODULE_LOAD_ERROR_PATTERNS: RegExp[] = [
  /failed to fetch dynamically imported module/i,
  /error loading dynamically imported module/i,
  /importing a module script failed/i,
  /expected a javascript module script but the server responded with a mime type/i,
];

function eventMessage(event: ErrorEvent | PromiseRejectionEvent): string {
  if ("reason" in event) {
    const reason = (event as PromiseRejectionEvent).reason;
    return String(reason instanceof Error ? reason.message : reason ?? "");
  }
  return String((event as ErrorEvent).message ?? "");
}

function isModuleLoadError(event: ErrorEvent | PromiseRejectionEvent): boolean {
  const message = eventMessage(event);
  return MODULE_LOAD_ERROR_PATTERNS.some((pattern) => pattern.test(message));
}

export function installChunkLoadRecovery(): void {
  if (typeof window === "undefined" || typeof sessionStorage === "undefined") {
    return;
  }

  const reloadOnce = (): void => {
    if (sessionStorage.getItem(RELOAD_FLAG)) return;
    sessionStorage.setItem(RELOAD_FLAG, "1");
    // Give the current error a moment to surface, then boot fresh.
    window.setTimeout(() => window.location.reload(), 250);
  };

  window.addEventListener("error", (event) => {
    if (isModuleLoadError(event)) reloadOnce();
  });

  window.addEventListener("unhandledrejection", (event) => {
    if (isModuleLoadError(event)) reloadOnce();
  });
}
