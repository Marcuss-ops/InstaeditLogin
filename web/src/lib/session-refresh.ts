/**
 * Session auto-refresh for the SPA.
 *
 * The API's `session` cookie carries a short-lived access JWT
 * (`JWT_ACCESS_TTL_MINUTES`, default 15 min) while the HttpOnly
 * `refresh` cookie lasts 30 days. Historically the SPA never used the
 * refresh cookie, so ~15 minutes after login `/auth/me` started
 * returning 401 and the app silently logged the user out.
 *
 * - `refreshSession()` POSTs `/api/v1/auth/refresh` (CSRF-exempt) and
 *   rotates the access/refresh cookie pair.
 * - `withSessionRefresh(request)` wraps any authed request: on a 401
 *   it attempts exactly ONE refresh and retries the original request
 *   once. A genuine auth failure still surfaces the 401 to the caller
 *   (which routes to /login).
 * - `maybeRefreshSession()` is the heartbeat guard used by the
 *   protected layout: refresh at most once every 10 minutes while the
 *   tab is visible, coordinated across tabs via localStorage so two
 *   tabs don't rotate the same refresh token concurrently.
 *
 * SAFETY: the backend treats a replayed refresh token as theft and
 * revokes the ENTIRE token family (`SessionsService.Refresh` →
 * `RevokeFamily(..., "refresh_reuse_detected")`). Every refresh path
 * is therefore single-flight (one in-flight POST shared by all
 * callers), the heartbeat stamps an optimistic cooldown before the
 * POST to shrink the multi-tab race window, and retries are bounded
 * to a single attempt. Residual cross-tab risk is limited to two tabs
 * firing a refresh within the same few milliseconds.
 *
 * Known edge (accepted): if `fetchSession` cached a `null` session
 * during a transient network blip, a later successful heartbeat
 * refresh does NOT invalidate that cache (invalidating it would clear
 * the shared query registries on every refresh). A page reload heals
 * it; the reactive 401→refresh path in the fetch wrappers prevents
 * the common expiry case from reaching this state.
 */

import { API_BASE_URL } from "./api";
import { isDemoMode } from "./demo";

const REFRESH_URL = "/api/v1/auth/refresh";
/** Heartbeat cooldown: refresh at most once per 10 minutes (access TTL is 15 min). */
export const SESSION_REFRESH_INTERVAL_MS = 10 * 60 * 1000;
/** localStorage key for the cross-tab last-refresh timestamp. */
export const SESSION_REFRESH_AT_KEY = "instaedit:session-refresh-at";

let refreshInFlight: Promise<boolean> | null = null;

function readLastRefreshAt(): number {
  try {
    const raw = localStorage.getItem(SESSION_REFRESH_AT_KEY);
    const parsed = raw ? Number(raw) : 0;
    return Number.isFinite(parsed) ? parsed : 0;
  } catch {
    return 0;
  }
}

function stampLastRefreshAt(): void {
  try {
    localStorage.setItem(SESSION_REFRESH_AT_KEY, String(Date.now()));
  } catch {
    // storage unavailable (private mode / SSR) — the in-memory
    // single-flight guard still prevents concurrent refreshes.
  }
}

/**
 * Rotates the session cookie pair. Single-flight: concurrent callers
 * share one in-flight POST, so the refresh token is never presented
 * twice at the same time (a replay would revoke the whole family).
 * Returns true when the refresh succeeded (new cookies set).
 */
export async function refreshSession(): Promise<boolean> {
  if (refreshInFlight) return refreshInFlight;
  refreshInFlight = (async () => {
    try {
      if (isDemoMode()) return false;
      // Optimistic cooldown stamp BEFORE the POST (not after success):
      //   - shrinks the cross-tab race window — two tabs both passing
      //     the guard and rotating the SAME refresh token concurrently
      //     would trigger the backend's family-wide revocation;
      //   - prevents hammering /auth/refresh when the session is dead
      //     (a failed refresh still backs off for the full interval).
      stampLastRefreshAt();
      const resp = await fetch(`${API_BASE_URL}${REFRESH_URL}`, {
        method: "POST",
        credentials: "include",
      });
      return resp.ok;
    } catch {
      return false;
    } finally {
      refreshInFlight = null;
    }
  })();
  return refreshInFlight;
}

/**
 * Wraps an authed request with 401 → refresh → retry (exactly once).
 * Non-401 responses pass through untouched; a 401 that could not be
 * healed by a refresh is returned as-is so callers keep their usual
 * "not authenticated" handling (AuthError / redirect to /login).
 */
export async function withSessionRefresh(
  request: () => Promise<Response>,
): Promise<Response> {
  const first = await request();
  if (first.status !== 401) return first;
  const refreshed = await refreshSession();
  if (!refreshed) return first;
  return request();
}

/**
 * Heartbeat guard: refreshes the session at most once per
 * SESSION_REFRESH_INTERVAL_MS while the tab is visible. The
 * localStorage timestamp coordinates multiple tabs so near-simultaneous
 * timers do not rotate the same refresh token concurrently. Returns
 * true when a refresh actually ran and succeeded.
 */
export async function maybeRefreshSession(): Promise<boolean> {
  if (typeof document === "undefined" || document.visibilityState !== "visible") {
    return false;
  }
  if (Date.now() - readLastRefreshAt() < SESSION_REFRESH_INTERVAL_MS) {
    return false;
  }
  return refreshSession();
}
