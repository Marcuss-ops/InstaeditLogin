/**
 * Auth helpers for the InstaEdit SPA (Taglio 1.2 / Taglio 5a).
 *
 *   - authedFetch attaches credentials: 'include' so the browser sends the
 *     session cookie. The backend middleware reads the cookie when no
 *     Authorization: Bearer header is set.
 *   - logout POSTs to /api/v1/auth/logout (which clears the cookie) and
 *     then hard-navigates to /login.
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
