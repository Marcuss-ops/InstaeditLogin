/**
 * Auth helpers for the InstaEdit SPA (Taglio 1.2 / Taglio 5a / Blocco #2.4 CSRF).
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

export type Session = {
  userId: number;
  name: string;
  username: string;
  expiresAt: string;
};

let sessionCache: Session | null | undefined = undefined;
let sessionPromise: Promise<Session | null> | null = null;

export async function fetchSession(): Promise<Session | null> {
  if (sessionCache !== undefined) return sessionCache;
  if (sessionPromise) return sessionPromise;

  sessionPromise = (async () => {
    try {
      const data = await apiClient<{ user_id: number }>("/api/v1/auth/me");
      sessionCache = {
        userId: data.user_id,
        name: "",
        username: "",
        expiresAt: "",
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
}

export class AuthError extends Error {
  constructor() {
    super("not authenticated");
    this.name = "AuthError";
  }
}

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
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
  const headers = new Headers(init.headers);
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  // Backend CSRF protection (see internal/auth/csrf.go): unsafe
  // methods require a header matching the `csrf_token` cookie.
  // Auto-inject from document.cookie so callers don't have to thread
  // the value through every call site. A missing csrf_token cookie
  // (e.g. session expired) leaves the header absent — the backend
  // will then 403 with `missing_csrf_header`, which is the
  // expected signal to re-authenticate.
  const method = (init.method ?? "GET").toUpperCase();
  if (UNSAFE_METHODS.has(method) && !headers.has("X-CSRF-Token")) {
    const csrfToken = readCookie("csrf_token");
    if (csrfToken) {
      headers.set("X-CSRF-Token", csrfToken);
    }
  }

  // Network-level rejection (DNS, CORS pre-flight, offline). The toast
  // fires BEFORE the re-throw so pages that don't have their own
  // bespoke error UX (Login, Compose at the boundary) still surface a
  // notification. Pages with `<ErrorState/>` get a parallel message
  // — the toast is at viewport level (top-right), the ErrorState is
  // in-place — both surfaces win.
  let response: Response;
  try {
    response = await fetch(`${API_BASE_URL}${path}`, {
      ...init,
      headers,
      credentials: "include",
    });
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
    clearSessionCache();
    throw new AuthError();
  }

  if (!response.ok) {
    let message = `request failed (status ${response.status})`;
    try {
      const data = (await response.json()) as { error?: string };
      if (data?.error) message = data.error;
    } catch {
      // body wasn't JSON
    }
    // Auto-emit BEFORE the throw so the global toast viewport
    // picks up errors even on pages that forget to render a
    // bespoke error state.
    toastBus.push("error", message);
    throw new ApiError(response.status, message);
  }

  return response;
}

export async function logout(redirectTo: string = "/login"): Promise<void> {
  try {
    await fetch(`${API_BASE_URL}/api/v1/auth/logout`, {
      method: "POST",
      credentials: "include",
    });
  } catch {
    // network is down — navigate anyway
  }
  clearSessionCache();
  window.location.href = redirectTo;
}
