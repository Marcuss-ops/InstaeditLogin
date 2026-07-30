/**
 * Typed fetch wrapper — replaces raw `fetch(...)` call sites in web/src/.
 *
 * Contract:
 *   - auto credentials: "include" (cookie-based session auth)
 *   - auto X-CSRF-Token for unsafe methods (POST/PUT/PATCH/DELETE), read
 *     from the `csrf_token` cookie that /api/v1/auth/session sets
 *   - when `options.body` is present, JSON.stringify it + set
 *     "Content-Type: application/json" (overridable by caller's headers)
 *   - on success (HTTP 2xx): parse JSON and return as Promise<T>; empty
 *     / non-JSON bodies resolve to `{} as T`
 *   - on network failure: throw ApiClientError("Can't reach the server…")
 *   - on non-2xx: parse body, take `data.error` if present, throw
 *     ApiClientError(message, status); default fallback is status code text
 */

import { API_BASE_URL } from "./api";
import { readCookie } from "./cookie";
import { handleDemoRequest, isDemoMode } from "./demo";

// Backend CSRF protection (internal/auth/csrf.go): every unsafe method
// MUST carry an X-CSRF-Token header matching the `csrf_token` cookie.
// Re-declared locally rather than imported from auth.ts to keep
// api-client.ts orthogonal to the auth-specific helpers it shares.
const UNSAFE_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"]);

export class ApiClientError extends Error {
  status: number | undefined;
  constructor(message: string, status?: number) {
    super(message);
    this.name = "ApiClientError";
    this.status = status;
  }
}

interface ApiClientOptions extends Omit<RequestInit, "body"> {
  body?: unknown;
}

export async function apiClient<T = unknown>(
  path: string,
  options: ApiClientOptions = {},
): Promise<T> {
  if (isDemoMode()) {
    const demoResp = handleDemoRequest(path, options);
    if (demoResp) {
      if (!demoResp.ok) {
        const text = await demoResp.text().catch(() => "demo request failed");
        throw new ApiClientError(text, demoResp.status);
      }
      return (await demoResp.json().catch(() => ({}))) as T;
    }
  }

  const headers = new Headers(options.headers);
  const method = (options.method ?? "GET").toUpperCase();
  let body: string | undefined;

  if (options.body !== undefined) {
    body = JSON.stringify(options.body);
    if (!headers.has("Content-Type")) {
      headers.set("Content-Type", "application/json");
    }
  }

  if (UNSAFE_METHODS.has(method) && !headers.has("X-CSRF-Token")) {
    const csrf = readCookie("csrf_token");
    if (csrf) headers.set("X-CSRF-Token", csrf);
  }

  let response: Response;
  try {
    response = await fetch(`${API_BASE_URL}${path}`, {
      ...options,
      method,
      headers,
      body,
      credentials: "include",
    });
  } catch {
    // DNS failure, offline, CORS pre-flight rejection, etc. The typed
    // shape (status undefined) lets callers branch on "server was
    // unreachable" vs "server replied with an error code".
    throw new ApiClientError(
      "Can't reach the server — check your connection and try again.",
    );
  }

  if (!response.ok) {
    let message = `request failed (status ${response.status})`;
    try {
      const data = (await response.json()) as { error?: string };
      if (data?.error) message = data.error;
    } catch {
      // body wasn't JSON — fall through to the default status message
    }
    throw new ApiClientError(message, response.status);
  }

  return (await response.json().catch(() => ({}))) as T;
}
