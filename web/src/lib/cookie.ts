/**
 * Tiny shared helper — read a cookie value by name from `document.cookie`.
 *
 * Extracted from `web/src/lib/auth.ts` into its own leaf module so that
 * `web/src/lib/api-client.ts` can import it WITHOUT creating a circular
 * dependency with `auth.ts` (which itself imports `apiClient`). The new
 * file has no internal imports, so the import graph stays a DAG:
 *
 *     auth.ts ──▶ api-client.ts ──▶ cookie.ts
 *         │                            ▲
 *         └────────────────────────────┘
 *               (auth re-export)
 *
 * Used by:
 *   - web/src/lib/api-client.ts — auto-injects `X-CSRF-Token` header on
 *     unsafe methods (POST/PUT/PATCH/DELETE) by reading the `csrf_token`
 *     cookie that /api/v1/auth/session sets.
 *   - web/src/lib/auth.ts — re-exports `readCookie` for backward
 *     compatibility with existing callers that historically imported
 *     it from `auth.ts`.
 *
 * ## Cookie-domain scope (operational)
 *
 * The original JSDoc in `auth.ts` carried content that migrated here
 * verbatim because call-site debugging ends up reading this file:
 *
 *   When `COOKIE_DOMAIN` is set (e.g. `.instaedit.org` via fly secrets),
 *   the `csrf_token` cookie is shared across subdomains — the SPA on
 *   `app.instaedit.org` reads the value that `api.instaedit.org` set.
 *   The dev default is host-only (cookie set on the API origin); the
 *   SPA must hit the API on the same browser-visible origin (e.g. via
 *   Vite proxy `localhost:5173 → localhost:8080`) for `document.cookie`
 *   to contain the value.
 *
 * ## Lookup prefix gotcha
 *
 * The lookup prefix is the literal cookie name (no URL-encoding):
 * browsers store cookie names as-is in `document.cookie` and only
 * URL-encode the value. Encoding the name would silently miss cookies
 * whose name contains a reserved character (e.g. `+`, `/`).
 *
 * ## SSR safety
 *
 * Returns null when `document` is undefined (e.g. in a node-side
 * prerender or vitest without jsdom).
 */
export function readCookie(name: string): string | null {
  if (typeof document === "undefined" || !document.cookie) {
    return null;
  }
  const prefix = `${name}=`;
  for (const part of document.cookie.split(";")) {
    const value = part.trim();
    if (value.startsWith(prefix)) {
      return decodeURIComponent(value.slice(prefix.length));
    }
  }
  return null;
}
