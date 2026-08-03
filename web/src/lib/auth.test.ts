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

import { ApiError, AuthError, authedFetch } from "./auth";

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

  it("leaves ApiError.data undefined when the error body is not JSON", async () => {
    fetchMock.mockResolvedValue(new Response("plain text failure", { status: 500 }));
    const err = await authedFetch("/api/v1/x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(500);
    expect((err as ApiError).data).toBeUndefined();
  });
});
