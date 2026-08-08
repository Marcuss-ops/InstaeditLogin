/**
 * Locks the session auto-refresh contract:
 *   - refreshSession: POST /auth/refresh, single-flight, returns
 *     whether new cookies were issued.
 *   - withSessionRefresh: 401 → ONE refresh → ONE retry; genuine auth
 *     failures keep the 401 so callers route to /login.
 *   - maybeRefreshSession: heartbeat guard (visible tab + 10-min
 *     cross-tab cooldown).
 */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./demo", () => ({ isDemoMode: () => false, handleDemoRequest: vi.fn() }));

import {
  SESSION_REFRESH_AT_KEY,
  maybeRefreshSession,
  refreshSession,
  withSessionRefresh,
} from "./session-refresh";

function jsonResponse(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function noContentResponse(): Response {
  return new Response(null, { status: 204 });
}

describe("refreshSession", () => {
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

  it("POSTs /auth/refresh with credentials and returns true on 204", async () => {
    fetchMock.mockResolvedValue(noContentResponse());
    await expect(refreshSession()).resolves.toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toContain("/api/v1/auth/refresh");
    expect(init.method).toBe("POST");
    expect(init.credentials).toBe("include");
    // Success stamps the cross-tab cooldown timestamp.
    expect(localStorage.getItem(SESSION_REFRESH_AT_KEY)).not.toBeNull();
  });

  it("returns false on 401 (no refresh token / session gone)", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ error: "no refresh token" }, 401));
    await expect(refreshSession()).resolves.toBe(false);
  });

  it("returns false on network failure", async () => {
    fetchMock.mockRejectedValue(new TypeError("network down"));
    await expect(refreshSession()).resolves.toBe(false);
  });

  it("is single-flight: concurrent callers share one POST", async () => {
    let resolveFetch!: (r: Response) => void;
    fetchMock.mockImplementation(
      () => new Promise<Response>((resolve) => (resolveFetch = resolve)),
    );
    const first = refreshSession();
    const second = refreshSession();
    const third = refreshSession();
    resolveFetch(noContentResponse());
    const results = await Promise.all([first, second, third]);
    expect(results).toEqual([true, true, true]);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe("withSessionRefresh", () => {
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

  it("passes non-401 responses through untouched", async () => {
    const request = vi.fn().mockResolvedValue(jsonResponse({ ok: true }, 200));
    const resp = await withSessionRefresh(request);
    expect(resp.status).toBe(200);
    expect(request).toHaveBeenCalledTimes(1);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("refreshes once and retries the request on 401", async () => {
    const request = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ error: "unauthorized" }, 401))
      .mockResolvedValueOnce(jsonResponse({ user_id: 1 }, 200));
    fetchMock.mockResolvedValue(noContentResponse());
    const resp = await withSessionRefresh(request);
    expect(resp.status).toBe(200);
    expect(request).toHaveBeenCalledTimes(2);
    expect(String(fetchMock.mock.calls[0][0])).toContain("/api/v1/auth/refresh");
  });

  it("returns the original 401 when the refresh fails (no retry, no loop)", async () => {
    const request = vi.fn().mockResolvedValue(jsonResponse({ error: "unauthorized" }, 401));
    fetchMock.mockResolvedValue(jsonResponse({ error: "no refresh token" }, 401));
    const resp = await withSessionRefresh(request);
    expect(resp.status).toBe(401);
    expect(request).toHaveBeenCalledTimes(1);
  });

  it("does not retry on a 500 (only 401 heals)", async () => {
    const request = vi.fn().mockResolvedValue(jsonResponse({ error: "boom" }, 500));
    const resp = await withSessionRefresh(request);
    expect(resp.status).toBe(500);
    expect(request).toHaveBeenCalledTimes(1);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe("maybeRefreshSession (heartbeat guard)", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    localStorage.clear();
    Object.defineProperty(document, "visibilityState", {
      value: "visible",
      configurable: true,
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("refreshes when the tab is visible and outside the cooldown", async () => {
    fetchMock.mockResolvedValue(noContentResponse());
    await expect(maybeRefreshSession()).resolves.toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("skips when the tab is hidden", async () => {
    Object.defineProperty(document, "visibilityState", { value: "hidden", configurable: true });
    fetchMock.mockResolvedValue(noContentResponse());
    await expect(maybeRefreshSession()).resolves.toBe(false);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("respects the cross-tab 10-minute cooldown", async () => {
    localStorage.setItem(SESSION_REFRESH_AT_KEY, String(Date.now()));
    fetchMock.mockResolvedValue(noContentResponse());
    await expect(maybeRefreshSession()).resolves.toBe(false);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("refreshes again once the cooldown has passed", async () => {
    localStorage.setItem(SESSION_REFRESH_AT_KEY, String(Date.now() - 11 * 60 * 1000));
    fetchMock.mockResolvedValue(noContentResponse());
    await expect(maybeRefreshSession()).resolves.toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
