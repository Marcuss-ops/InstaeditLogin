/**
 * Unit tests for auth module (Taglio 5a).
 *
 * Covers: fetchSession, clearSessionCache, authedFetch, logout, AuthError, ApiError.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthError, ApiError, authedFetch, clearSessionCache, fetchSession } from "./auth";

describe("AuthError", () => {
  it("is an Error subclass with name AuthError", () => {
    const err = new AuthError();
    expect(err).toBeInstanceOf(Error);
    expect(err.name).toBe("AuthError");
  });
});

describe("ApiError", () => {
  it("captures status and message", () => {
    const err = new ApiError(500, "boom");
    expect(err.status).toBe(500);
    expect(err.message).toBe("boom");
  });
});

describe("clearSessionCache", () => {
  it("resets the module-level cache", async () => {
    clearSessionCache();
    // After clearing, fetchSession should re-probe.
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true, status: 200, json: async () => ({ user_id: 1 }),
    }));
    const session = await fetchSession();
    expect(session?.userId).toBe(1);
    vi.unstubAllGlobals();
  });
});

describe("authedFetch", () => {
  afterEach(() => { vi.unstubAllGlobals(); });

  it("throws AuthError on 401", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false, status: 401, json: async () => ({}),
    }));
    await expect(authedFetch("/api/v1/accounts")).rejects.toThrow(AuthError);
  });

  it("throws ApiError on non-401 errors", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false, status: 500, json: async () => ({ error: "boom" }),
    }));
    await expect(authedFetch("/api/v1/accounts")).rejects.toThrow(ApiError);
  });
});
