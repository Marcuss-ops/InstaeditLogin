/**
 * Auth helpers for the InstaEdit SPA (Taglio 1.2).
 *
 * Migration notes:
 *   - The OAuth callback (backend /api/v1/auth/{provider}/callback) no
 *     longer returns the JWT in the URL. It returns a single-use code.
 *   - /auth/callback POSTs that code to /api/v1/auth/exchange, which sets
 *     a HttpOnly Secure SameSite=None `session` cookie carrying the JWT.
 *   - The SPA then fetches /api/v1/auth/me on every page load to learn who
 *     is logged in (no localStorage, no JWT in JS).
 *   - authedFetch attaches credentials: 'include' so the browser sends the
 *     session cookie. The backend middleware reads the cookie when no
 *     Authorization: Bearer header is set.
 *   - logout POSTs to /api/v1/auth/logout (which clears the cookie) and
 *     then hard-navigates to /login.
 */

import { API_BASE_URL } from "./supabase";

export type Session = {
  userId: number;
  name: string;
  username: string;
  expiresAt: string;
};

let sessionCache: Session | null | undefined = undefined;
let sessionPromise: Promise<Session | null> | null = null;

/**
 * Fetches the current session from /api/v1/auth/me. Returns null on 401
 * (no session) or any non-2xx. The result is cached in module-scope memory
 * for the lifetime of the SPA so a router re-render doesn't re-fetch.
 *
 * Use the cached version when navigating client-side; call clearSessionCache()
 * after logout so the next fetchSession re-reads the server.
 */
export async function fetchSession(): Promise<Session | null> {
  if (sessionCache !== undefined) {
    return sessionCache;
  }
  if (sessionPromise) {
    return sessionPromise;
  }
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
      // The backend currently returns just user_id. The SPA can fetch
      // richer profile data from /api/v1/accounts. For the dashboard
      // greeting we fall back to a generic label if name is unknown.
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

/**
 * Clears the in-memory session cache so the next fetchSession re-reads
 * /api/v1/auth/me. Call this from the Login page after the user signs out
 * of another tab, or after the exchange flow, to ensure stale data is
 * flushed.
 */
export function clearSessionCache(): void {
  sessionCache = undefined;
  sessionPromise = null;
}

/**
 * Performs an authenticated fetch against the Go backend.
 *
 * Behavior (Taglio 1.2):
 *   - prepends API_BASE_URL
 *   - sends credentials: 'include' so the HttpOnly session cookie
 *     (set by /api/v1/auth/exchange) is attached automatically
 *   - DOES NOT inject Authorization: Bearer anymore — the cookie path
 *     is the source of truth for the dashboard SPA. API-key clients
 *     (machine-to-machine) can still pass their own Authorization
 *     header in `init.headers`.
 *   - throws AuthError when the server returns 401
 *   - throws ApiError with the server-provided message when 4xx/5xx
 */
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

export async function authedFetch(
  path: string,
  init: RequestInit = {},
): Promise<Response> {
  const headers = new Headers(init.headers);

  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
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
      if (data?.error) {
        message = data.error;
      }
    } catch {
      // body wasn't JSON; keep generic message
    }
    throw new ApiError(response.status, message);
  }

  return response;
}

/**
 * Logs the user out: tells the backend to clear the HttpOnly session
 * cookie, then hard-navigates to /login. Best-effort: a network failure
 * here is swallowed (the cookie will expire naturally on the server).
 */
export async function logout(redirectTo: string = "/login"): Promise<void> {
  try {
    await fetch(`${API_BASE_URL}/api/v1/auth/logout`, {
      method: "POST",
      credentials: "include",
    });
  } catch {
    // network is down, but we still want to navigate the user out
  }
  clearSessionCache();
  window.location.href = redirectTo;
}

// ----------------------------------------------------------------------------
// Backend health probe (unchanged)
// ----------------------------------------------------------------------------

export type ProbeFailureReason =
  | "not_found"
  | "vercel_stale_deploy"
  | "http_error"
  | "unreachable"
  | "timeout";

export type ProbeResult =
  | { ok: true; url: string; status: number }
  | { ok: false; url: string; status: number | null; reason: ProbeFailureReason; message: string };

const HEALTH_PATH = "/api/v1/health";
const DEFAULT_TIMEOUT_MS = 5000;

export async function probeBackend(
  timeoutMs: number = DEFAULT_TIMEOUT_MS,
  externalSignal?: AbortSignal,
): Promise<ProbeResult> {
  const url = `${API_BASE_URL}${HEALTH_PATH}`;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);

  const onExternalAbort = () => controller.abort();
  if (externalSignal) {
    if (externalSignal.aborted) {
      controller.abort();
    } else {
      externalSignal.addEventListener("abort", onExternalAbort);
    }
  }

  try {
    const response = await fetch(url, { method: "GET", signal: controller.signal, credentials: "include" });
    clearTimeout(timer);

    if (response.ok) {
      return { ok: true, url: API_BASE_URL, status: response.status };
    }
    if (response.status === 404) {
      if (isVercelAppHost(API_BASE_URL)) {
        return {
          ok: false,
          url: API_BASE_URL,
          status: 404,
          reason: "vercel_stale_deploy",
          message:
            "VITE_API_BASE_URL appears to point at a removed or expired Vercel deployment (hostname *.vercel.app), not at the InstaEdit Go backend. Update VITE_API_BASE_URL on the frontend project to your real backend URL and redeploy.",
        };
      }
      return {
        ok: false,
        url: API_BASE_URL,
        status: 404,
        reason: "not_found",
        message: "The backend at this URL answered but /api/v1/health was missing. The deployment looks stale — check VITE_API_BASE_URL.",
      };
    }

    return {
      ok: false,
      url: API_BASE_URL,
      status: response.status,
      reason: "http_error",
      message: `Backend returned HTTP ${response.status}.`,
    };
  } catch {
    clearTimeout(timer);
    const aborted = controller.signal.aborted;
    if (aborted) {
      return {
        ok: false,
        url: API_BASE_URL,
        status: null,
        reason: "timeout",
        message: "Backend did not respond within the timeout window.",
      };
    }
    return {
      ok: false,
      url: API_BASE_URL,
      status: null,
      reason: "unreachable",
      message:
        "Backend is unreachable. Check that the API host is up and that this frontend's origin is in CORS_ALLOWED_ORIGINS.",
    };
  } finally {
    if (externalSignal) {
      externalSignal.removeEventListener("abort", onExternalAbort);
    }
  }
}

export function subscribeToVisibility(onChange: () => void): () => void {
  if (typeof window === "undefined") {
    return () => {};
  }
  const handler = () => {
    if (document.visibilityState === "visible") {
      onChange();
    }
  };
  window.addEventListener("focus", onChange);
  document.addEventListener("visibilitychange", handler);
  return () => {
    window.removeEventListener("focus", onChange);
    document.removeEventListener("visibilitychange", handler);
  };
}

export function isVercelAppHost(rawUrl: string): boolean {
  try {
    const hostname = new URL(rawUrl).hostname.toLowerCase();
    return hostname === "vercel.app" || hostname.endsWith(".vercel.app");
  } catch {
    return false;
  }
}

// ----------------------------------------------------------------------------
// Auth boundary probe (unchanged behavior, just credentials: 'include')
// ----------------------------------------------------------------------------

export type AuthBoundaryFailureReason = "cors" | "not_401" | "unreachable" | "timeout";

export type AuthBoundaryResult =
  | { ok: true; status: number }
  | {
      ok: false;
      status: number | null;
      reason: AuthBoundaryFailureReason;
      message: string;
    };

export async function probeAuthBoundary(
  timeoutMs: number = DEFAULT_TIMEOUT_MS,
  externalSignal?: AbortSignal,
): Promise<AuthBoundaryResult> {
  const url = `${API_BASE_URL}/api/v1/accounts`;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  const onExternalAbort = () => controller.abort();
  if (externalSignal) {
    if (externalSignal.aborted) {
      controller.abort();
    } else {
      externalSignal.addEventListener("abort", onExternalAbort);
    }
  }

  try {
    const response = await fetch(url, { method: "GET", signal: controller.signal, credentials: "include" });
    clearTimeout(timer);
    if (response.status === 401) {
      return { ok: true, status: 401 };
    }
    return {
      ok: false,
      status: response.status,
      reason: "not_401",
      message:
        "Protected endpoint answered without rejecting the unauthenticated request. The auth middleware may be mis-wired or CORS is bleating through incorrectly.",
    };
  } catch {
    clearTimeout(timer);
    if (controller.signal.aborted) {
      return {
        ok: false,
        status: null,
        reason: "timeout",
        message: "Auth-boundary probe timed out before responding.",
      };
    }
    return {
      ok: false,
      status: null,
      reason: "cors",
      message:
        "Browser blocked the protected-route probe — almost always a CORS preflight failure. Add this origin to CORS_ALLOWED_ORIGINS on the backend.",
    };
  } finally {
    if (externalSignal) {
      externalSignal.removeEventListener("abort", onExternalAbort);
    }
  }
}
