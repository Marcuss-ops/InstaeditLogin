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
      const response = await fetch(`${API_BASE_URL}/api/v1/auth/me`, {
        method: "GET",
        credentials: "include",
      });
      if (response.status === 401) {
        sessionCache = null;
        return null;
      }
      if (!response.ok) {
        sessionCache = null;
        return null;
      }
      const data = (await response.json()) as { user_id: number };
      sessionCache = {
        userId: data.user_id,
        name: "",
        username: "",
        expiresAt: "",
      };
      return sessionCache;
    } catch {
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
 * Reads the value of a cookie by name from `document.cookie`.
 * Returns null when no cookie with that name is set, or when called
 * outside a browser (no `document` global, e.g. SSR or a node
 * worker that doesn't load jsdom).
 *
 * Used by `authedFetch` to attach `X-CSRF-Token` on unsafe methods.
 * Browser cookie-domain scope matters: when COOKIE_DOMAIN is set to
 * e.g. ".instaedit.org" via fly secrets, the `csrf_token` cookie is
 * shared across subdomains so the SPA on `app.instaedit.org` can
 * read the value that `api.instaedit.org` set. The dev default is
 * host-only (cookie set on the API origin) and the SPA must hit the
 * API on the same browser-visible origin (e.g. via Vite proxy at
 * localhost:5173 → localhost:8080) for document.cookie to contain
 * the value.
 *
 * The lookup prefix is the literal cookie name (no URL-encoding):
 * browsers store cookie names as-is in `document.cookie` and only
 * URL-encode the value. Encoding the name would silently miss
 * cookies whose name contains a reserved character (e.g. `+`, `/`).
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

  const response = await fetch(`${API_BASE_URL}${path}`, {
    ...init,
    headers,
    credentials: "include",
  });

  if (response.status === 401) {
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
