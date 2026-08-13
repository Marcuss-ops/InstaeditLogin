/**
 * Auth helpers for the InstaEdit SPA.
 *
 *   - authedFetch attaches credentials: 'include' so the browser sends the
 *     session cookie, AND auto-injects X-CSRF-Token on unsafe methods
 *     (POST/PUT/PATCH/DELETE) by reading the `csrf_token` cookie set by
 *     /api/v1/auth/session. The backend CSRF middleware rejects unsafe
 *     requests missing this header (see internal/auth/csrf.go).
 *   - logout POSTs to /api/v1/auth/logout (which clears the cookie) and
 *     then hard-navigates to /login.
 *   - readCookie is the shared document.cookie reader; exported so call
 *     sites that need the csrf_token value outside of an HTTP request
 *     (rare) can reuse the same parsing logic.
 */

import { API_BASE_URL } from "./api";
import { apiClient } from "./api-client";
import { readCookie } from "./cookie";
import { toastBus } from "../components/toast";
import { demoSession, handleDemoRequest, isDemoMode } from "./demo";
import { withSessionRefresh } from "./session-refresh";

export type Session = {
  userId: number;
  name: string;
  username: string;
  email?: string;
  expiresAt: string;
  isAdmin: boolean;
  /** Workspace scope returned by /auth/me, reused by sidebar queries. */
  workspaceId?: number;
};

let sessionCache: Session | null | undefined = undefined;
let sessionPromise: Promise<Session | null> | null = null;

export async function fetchSession(): Promise<Session | null> {
  if (sessionCache !== undefined) return sessionCache;
  if (sessionPromise) return sessionPromise;

  if (isDemoMode()) {
    sessionCache = {
      userId: demoSession.user_id,
      name: demoSession.name,
      username: demoSession.username,
      expiresAt: demoSession.expires_at,
      isAdmin: demoSession.is_admin ?? false,
    };
    return sessionCache;
  }

  sessionPromise = (async () => {
    try {
      const data = await apiClient<{
        user_id: number;
        name?: string;
        email?: string;
        is_admin?: boolean;
        workspace_id?: number;
      }>("/api/v1/auth/me");
      sessionCache = {
        userId: data.user_id,
        name: data.name ?? "",
        username: "",
        email: data.email ?? undefined,
        expiresAt: "",
        isAdmin: data.is_admin ?? false,
        workspaceId:
          typeof data.workspace_id === "number" && Number.isInteger(data.workspace_id) && data.workspace_id > 0
            ? data.workspace_id
            : undefined,
      };
      return sessionCache;
    } catch {
      // 401, network failure, or any other error → fail closed (no session).
      // The caller treats null session as "not logged in" and routes to /login.
      sessionCache = null;
      return null;
    } finally {
      sessionPromise = null;
    }
  })();
  return sessionPromise;
}

export function clearSessionCache(): void {
  sessionCache = undefined;
  sessionPromise = null;
  if (typeof window !== "undefined") {
    window.dispatchEvent(new Event("instaedit:session-cleared"));
  }
}

/**
 * Fired when an authed request reaches a 401 that a single
 * session-refresh + retry could NOT heal — i.e. the session is
 * genuinely gone, not merely expired. The global SessionLossRedirect
 * listens for this to bounce the user to /login consistently from
 * EVERY surface (page hooks, sidebar polls, shared queries), so no
 * code path keeps silently 401-ing after the session dies.
 */
export const AUTH_EXPIRED_EVENT = "instaedit:auth-expired";

export function dispatchAuthExpiredEvent(): void {
  if (typeof window !== "undefined") {
    window.dispatchEvent(new Event(AUTH_EXPIRED_EVENT));
  }
}

export class AuthError extends Error {
  constructor() {
    super("not authenticated");
    this.name = "AuthError";
  }
}

export class ApiError extends Error {
  status: number;
  /**
   * Parsed JSON error body when the server replied with JSON, so
   * callers can read structured fields (e.g. the 409
   * `PROJECT_VERSION_CONFLICT` `current_version`). Undefined when the
   * body was not JSON. Additive — existing `new ApiError(status,
   * message)` call sites are unaffected.
   */
  data?: unknown;
  constructor(status: number, message: string, data?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.data = data;
  }
}

// HTTP methods the backend CSRF middleware protects. Every request
// matching one of these MUST carry an `X-CSRF-Token` header that
// equals the `csrf_token` cookie set by /api/v1/auth/session (see
// internal/auth/csrf.go). The header is auto-injected by authedFetch
// from document.cookie; a missing header yields
// `403 csrf rejected: missing_csrf_header` in production.
const UNSAFE_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"]);

/**
 * Re-exported from `./cookie` to preserve the existing
 * `import { readCookie } from "./auth"` call surface used by other
 * modules. The implementation lives in `./cookie` so both `auth.ts`
 * and `api-client.ts` can import it without creating a cycle.
 *
 * @see web/src/lib/cookie.ts
 */
export { readCookie } from "./cookie";

export async function authedFetch(
  path: string,
  init: RequestInit = {},
): Promise<Response> {
  if (isDemoMode()) {
    const demoResp = handleDemoRequest(path, init);
    if (demoResp) return demoResp;
  }

  // Backend CSRF protection (see internal/auth/csrf.go): unsafe
  // methods require a header matching the `csrf_token` cookie.
  // Auto-inject from document.cookie so callers don't have to thread
  // the value through every call site. A missing csrf_token cookie
  // (e.g. session expired) leaves the header absent — the backend
  // will then 403 with `missing_csrf_header`, which is the
  // expected signal to re-authenticate.
  //
  // The header is built INSIDE the request closure on purpose: the
  // session-refresh wrapper retries the request after rotating the
  // session cookies, and /auth/refresh regenerates the csrf_token
  // cookie — the retried unsafe request must send the FRESH token.
  const method = (init.method ?? "GET").toUpperCase();
  const request = (): Promise<Response> => {
    const headers = new Headers(init.headers);
    if (init.body && !headers.has("Content-Type")) {
      headers.set("Content-Type", "application/json");
    }
    if (UNSAFE_METHODS.has(method) && !headers.has("X-CSRF-Token")) {
      const csrfToken = readCookie("csrf_token");
      if (csrfToken) {
        headers.set("X-CSRF-Token", csrfToken);
      }
    }
    return fetch(`${API_BASE_URL}${path}`, {
      ...init,
      headers,
      credentials: "include",
    });
  };

  // Network-level rejection (DNS, CORS pre-flight, offline). The toast
  // fires BEFORE the re-throw so pages that don't have their own
  // bespoke error UX (Login, Compose at the boundary) still surface a
  // notification. Pages with `<ErrorState/>` get a parallel message
  // — the toast is at viewport level (top-right), the ErrorState is
  // in-place — both surfaces win.
  let response: Response;
  try {
    // 401 → single session refresh + one retry. An expired access JWT
    // (default TTL 15 min) is healed transparently; only a refresh
    // failure keeps the 401 so the caller can route to /login.
    response = await withSessionRefresh(request);
  } catch (err) {
    const message =
      err instanceof TypeError
        ? "Can't reach the server — check your connection."
        : err instanceof Error
          ? err.message
          : "Network request failed.";
    toastBus.push("error", message);
    throw err;
  }

  if (response.status === 401) {
    // 401 path intentionally does NOT emit a toast — the caller
    // navigates to /login instead, which already signals to the user.
    // The event lets the global SessionLossRedirect act as a safety
    // net for surfaces that swallow AuthError (pollers, shared
    // queries): they must not keep 401-ing after the session dies.
    clearSessionCache();
    dispatchAuthExpiredEvent();
    throw new AuthError();
  }

  if (!response.ok) {
    let message = `request failed (status ${response.status})`;
    let data: unknown;
    try {
      data = await response.json();
      if (data && typeof data === "object" && "error" in data) {
        const errorField = (data as { error?: unknown }).error;
        if (typeof errorField === "string" && errorField) message = errorField;
      }
    } catch {
      // body wasn't JSON — data stays undefined
    }
    // Auto-emit BEFORE the throw so the global toast viewport
    // picks up errors even on pages that forget to render a
    // bespoke error state.
    toastBus.push("error", message);
    throw new ApiError(response.status, message, data);
  }

  return response;
}

export async function logout(redirectTo: string = "/login"): Promise<void> {
  if (!isDemoMode()) {
    try {
      await fetch(`${API_BASE_URL}/api/v1/auth/logout`, {
        method: "POST",
        credentials: "include",
      });
    } catch {
      // network is down — navigate anyway
    }
  }
  clearSessionCache();
  window.location.href = redirectTo;
}
