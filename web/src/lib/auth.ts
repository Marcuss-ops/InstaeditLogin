/**
 * Auth helpers for the InstaEdit SPA.
 *
 * The OAuth callback (backend /api/v1/auth/{provider}/callback) issues a JWT
 * and redirects the browser here with the token in the URL query string.
 * /auth/callback reads it from window.location, stores it in localStorage
 * under JWT_STORAGE_KEY, then replaces the history entry so the token never
 * lands in browser history. From then on authedFetch() attaches it as a
 * Bearer header on every API call.
 */

import { API_BASE_URL } from "./supabase";

const JWT_STORAGE_KEY = "instaedit_jwt";
const USER_STORAGE_KEY = "instaedit_user";
const EXPIRES_STORAGE_KEY = "instaedit_jwt_expires_at";

export type StoredSession = {
  jwt: string;
  userId: string;
  expiresAt: string;
  name: string;
};

export function getJwt(): string | null {
  return localStorage.getItem(JWT_STORAGE_KEY);
}

export function getSession(): StoredSession | null {
  const jwt = getJwt();
  const userId = localStorage.getItem(USER_STORAGE_KEY);
  const expiresAt = localStorage.getItem(EXPIRES_STORAGE_KEY);
  const name = localStorage.getItem("instaedit_name") ?? "";
  if (!jwt || !userId || !expiresAt) {
    return null;
  }
  return { jwt, userId, expiresAt, name };
}

export function setSession(jwt: string, userId: string, expiresAt: string, name: string): void {
  localStorage.setItem(JWT_STORAGE_KEY, jwt);
  localStorage.setItem(USER_STORAGE_KEY, userId);
  localStorage.setItem(EXPIRES_STORAGE_KEY, expiresAt);
  localStorage.setItem("instaedit_name", name);
}

export function clearSession(): void {
  localStorage.removeItem(JWT_STORAGE_KEY);
  localStorage.removeItem(USER_STORAGE_KEY);
  localStorage.removeItem(EXPIRES_STORAGE_KEY);
  localStorage.removeItem("instaedit_name");
}

/**
 * Performs an authenticated fetch against the Go backend.
 *
 * Behavior:
 *  - prepends API_BASE_URL
 *  - injects Authorization: Bearer <jwt> when a token is stored
 *  - throws AuthError when the server returns 401 (caller decides how to react)
 *  - throws ApiError with the server-provided message when 4xx/5xx
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
  const jwt = getJwt();
  const headers = new Headers(init.headers);

  if (jwt) {
    headers.set("Authorization", `Bearer ${jwt}`);
  }
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }

  const response = await fetch(`${API_BASE_URL}${path}`, {
    ...init,
    headers,
  });

  if (response.status === 401) {
    clearSession();
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
 * Logs the user out: clears the stored JWT and bounces to /login with a flag
 * the Login page can read via useSearchParams (or just show a notice).
 */
export function logout(redirectTo: string = "/login"): void {
  clearSession();
  window.location.href = redirectTo;
}

// ----------------------------------------------------------------------------
// Backend health probe
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

/**
 * Pings the backend's /api/v1/health endpoint to learn if the configured
 * API_BASE_URL is reachable and actually serving the InstaEdit API.
 *
 * Used by /login to render a status pill and (when the backend is dead) a
 * red banner with deployment-fix guidance. Used by /dashboard to surface
 * a clear "backend offline" state distinct from "no accounts yet".
 *
 * Reasons:
 *  - not_found   → backend answered but /api/v1/health was 404. Almost
 *                  certainly a stale URL (e.g. a deleted Vercel project).
 *  - http_error  → non-2xx, non-404 status from backend.
 *  - unreachable → fetch threw (network/CORS/the host is dead).
 *  - timeout     → no response within timeoutMs.
 *
 * An optional AbortSignal lets callers cancel an in-flight probe (e.g. when
 * /dashboard user spam-clicks the Retry button).
 */
export async function probeBackend(
  timeoutMs: number = DEFAULT_TIMEOUT_MS,
  externalSignal?: AbortSignal,
): Promise<ProbeResult> {
  const url = `${API_BASE_URL}${HEALTH_PATH}`;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);

  // Forward external signal to our internal one without requiring AbortSignal.any().
  const onExternalAbort = () => controller.abort();
  if (externalSignal) {
    if (externalSignal.aborted) {
      controller.abort();
    } else {
      externalSignal.addEventListener("abort", onExternalAbort);
    }
  }

  try {
    const response = await fetch(url, { method: "GET", signal: controller.signal });
    clearTimeout(timer);

    if (response.ok) {
      return { ok: true, url: API_BASE_URL, status: response.status };
    }    if (response.status === 404) {
      // Vercel edge returns 404 with body `DEPLOYMENT_NOT_FOUND` when a
      // deployment has been removed or expired. When the configured
      // VITE_API_BASE_URL points at a *.vercel.app host, that is the
      // overwhelmingly likely cause — surface a dedicated reason so the
      // banner can recommend the specific fix (update env var on
      // Vercel + redeploy) instead of the generic "URL stale" wording.
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
    // Fetch TypeError is the canonical "network or CORS preflight refused".
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

/**
 * Returns a teardown function that re-runs `onChange` whenever the tab regains
 * focus or becomes visible. Useful for self-healing: a developer noticing the
 * backend 404 can start the server, switch back to the tab, and the UI snaps
 * to "connected" without a hard reload.
 */
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

/**
 * Returns true if the given URL's hostname is `vercel.app` or any subdomain
 * of it (e.g. `instaedit-login-abc123.vercel.app`).
 *
 * Used internally by probeBackend's 404 handler to switch the failure
 * reason to `vercel_stale_deploy`; also exported because the unit tests in
 * `auth.test.ts` assert on this predicate directly. Production callers
 * should go through probeBackend instead of invoking this directly.
 *
 * Case-insensitive on the hostname; scheme/port/path are irrelevant.
 * Invalid URLs (those rejected by `new URL`) return false rather than
 * throwing — the caller can treat them as "definitely not Vercel" and
 * fall through to the generic `not_found` path.
 */
export function isVercelAppHost(rawUrl: string): boolean {
  try {
    const hostname = new URL(rawUrl).hostname.toLowerCase();
    return hostname === "vercel.app" || hostname.endsWith(".vercel.app");
  } catch {
    return false;
  }
}

// ----------------------------------------------------------------------------
// Auth boundary probe
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

/**
 * Pings a JWT-protected route WITHOUT a Bearer token. The healthy expected
 * response is HTTP 401 — but it must come back with valid CORS headers.
 *
 * Use this alongside probeBackend() to catch the subtle
 * "/health returns 200 but /api/v1/accounts CORS fails" class of bug. A
 * blocked fetch surfaces as reason="cors". A 200/500 from the protected
 * route surfaces as reason="not_401".
 */
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
		const response = await fetch(url, { method: "GET", signal: controller.signal });
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
		};  } catch {
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
