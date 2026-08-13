/**
 * Locks the `authedFetch` error contract:
 *   - 401 → AuthError (callers navigate to /login)
 *   - non-2xx JSON body → ApiError with `.status`, the server `error`
 *     string as `.message`, and the full parsed body on `.data` (the
 *     thumbnail client reads `{ code: "PROJECT_VERSION_CONFLICT",
 *     current_version }` from `.data`).
 *   - non-JSON error body → ApiError with `.data === undefined`.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./cookie", () => ({ readCookie: () => "test-csrf-token" }));
vi.mock("../components/toast", () => ({ toastBus: { push: vi.fn() } }));

import { ApiError, AUTH_EXPIRED_EVENT, AuthError, authedFetch } from "./auth";

function jsonResponse(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("authedFetch error contract", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    localStorage.clear();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("attaches the parsed JSON body to ApiError.data on a structured 409", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(
        {
          code: "PROJECT_VERSION_CONFLICT",
          current_version: 9,
          error: "thumbnail project version conflict: expected=8 current=9",
        },
        409,
      ),
    );
    const err = await authedFetch("/api/v1/thumbnail-projects/x/snapshot", {
      method: "PUT",
      body: "{}",
    }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    const apiErr = err as ApiError;
    expect(apiErr.status).toBe(409);
    // The `.message` carries the server `error` text verbatim...
    expect(apiErr.message).toContain("version conflict");
    expect(apiErr.data).toEqual({
      code: "PROJECT_VERSION_CONFLICT",
      current_version: 9,
      error: "thumbnail project version conflict: expected=8 current=9",
    });
  });

  it("throws AuthError on 401 so pages can redirect to /login", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ error: "unauthorized" }, 401));
    await expect(authedFetch("/api/v1/thumbnail-projects")).rejects.toBeInstanceOf(
      AuthError,
    );
  });

  it("dispatches instaedit:auth-expired when a 401 cannot be healed", async () => {
    const listener = vi.fn();
    window.addEventListener(AUTH_EXPIRED_EVENT, listener);
    fetchMock.mockResolvedValue(jsonResponse({ error: "unauthorized" }, 401));
    await expect(authedFetch("/api/v1/thumbnail-projects")).rejects.toBeInstanceOf(
      AuthError,
    );
    // The global SessionLossRedirect relies on this event to bounce
    // pollers and shared queries to /login consistently.
    expect(listener).toHaveBeenCalledTimes(1);
    window.removeEventListener(AUTH_EXPIRED_EVENT, listener);
  });

  it("heals an expired access token: 401 → session refresh → retry → resolves", async () => {
    // First call 401s (expired access JWT), the refresh returns 204
    // (new cookies set), and the retried request succeeds.
    fetchMock
      .mockResolvedValueOnce(jsonResponse({ error: "unauthorized" }, 401))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(jsonResponse({ user_id: 1, workspace_id: 2 }, 200));
    const resp = await authedFetch("/api/v1/thumbnail-projects");
    expect(resp.status).toBe(200);
    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(String(fetchMock.mock.calls[1][0])).toContain("/api/v1/auth/refresh");
  });

  it("leaves ApiError.data undefined when the error body is not JSON", async () => {
    fetchMock.mockResolvedValue(new Response("plain text failure", { status: 500 }));
    const err = await authedFetch("/api/v1/x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(500);
    expect((err as ApiError).data).toBeUndefined();
  });
});
