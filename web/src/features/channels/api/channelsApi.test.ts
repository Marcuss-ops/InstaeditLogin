import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  clearAccountsCache,
  filterYouTube,
  listAllAccounts,
} from "./channelsApi";
import type { PlatformAccount } from "../../../types/uploads";

const account = (overrides: Partial<PlatformAccount>): PlatformAccount => ({
  id: 1,
  platform: "youtube",
  platform_user_id: "UC1",
  username: "channel",
  status: "active",
  created_at: "2026-01-01T00:00:00Z",
  ...overrides,
});

describe("filterYouTube", () => {
  it("keeps only publishable YouTube accounts", () => {
    const result = filterYouTube([
      account({ id: 1, account_state: "valid", is_publishable: true }),
      account({ id: 2, account_state: "reconnect_required", is_publishable: false }),
      account({ id: 3, account_state: "suspended", is_publishable: false }),
      account({ id: 4, account_state: "deleted", is_publishable: false }),
      account({ id: 5, platform: "instagram", account_state: "valid", is_publishable: true }),
    ]);

    expect(result.map((item) => item.id)).toEqual([1]);
  });

  it("supports legacy responses before is_publishable was added", () => {
    const result = filterYouTube([
      account({ id: 1, status: "active", is_publishable: undefined }),
      account({ id: 2, status: "reauth_required", is_publishable: undefined }),
    ]);

    expect(result.map((item) => item.id)).toEqual([1]);
  });
});

function jsonResponse(data: unknown, ok = true, status = 200) {
  return { ok, status, json: async () => data } as unknown as Response;
}

describe("listAllAccounts — shared manifest cache (N+1 DoD)", () => {
  beforeEach(() => {
    clearAccountsCache();
    vi.unstubAllGlobals();
  });

  it("collapses concurrent callers into ONE network request", async () => {
    let calls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async () => {
        calls += 1;
        return jsonResponse({ accounts: [] });
      }),
    );

    const [a, b, c] = await Promise.all([
      listAllAccounts(),
      listAllAccounts(),
      listAllAccounts(),
    ]);

    expect(calls).toBe(1);
    expect(a).toEqual([]);
    expect(b).toEqual([]);
    expect(c).toEqual([]);
  });

  it("serves the cached manifest within the 60s stale window without a new request", async () => {
    let calls = 0;
    const fixture = [account({ id: 1, account_state: "valid", is_publishable: true })];
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async () => {
        calls += 1;
        return jsonResponse({ accounts: fixture });
      }),
    );

    const first = await listAllAccounts();
    const second = await listAllAccounts();

    expect(calls).toBe(1);
    expect(second).toEqual(first);
  });

  it("follows every account page and sends the bounded limit on the first request", async () => {
    const requested: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        requested.push(url);
        if (requested.length === 1) {
          return jsonResponse({
            accounts: [account({ id: 1 })],
            has_more: true,
            next_cursor: "page-2",
          });
        }
        return jsonResponse({ accounts: [account({ id: 2 })], has_more: false });
      }),
    );

    await expect(listAllAccounts({ force: true })).resolves.toHaveLength(2);
    expect(requested.map((url) => {
      // Base URL makes relative fetch paths (API_BASE_URL empty in tests)
      // parseable; pathname+search is what the backend actually receives.
      const parsed = new URL(url, "http://relative.test");
      return `${parsed.pathname}?${parsed.searchParams.toString()}`;
    })).toEqual([
      "/api/v1/accounts?limit=100",
      "/api/v1/accounts?limit=100&cursor=page-2",
    ]);
  });

  it("fails closed when the server cannot provide a continuation cursor", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({ accounts: [], has_more: true }),
      ),
    );

    await expect(listAllAccounts({ force: true })).rejects.toThrow(
      "invalid continuation cursor",
    );
  });

  it("force: true bypasses the cache and refetches", async () => {
    let calls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async () => {
        calls += 1;
        return jsonResponse({ accounts: [] });
      }),
    );

    await listAllAccounts();
    await listAllAccounts({ force: true });

    expect(calls).toBe(2);
  });  // DoD "refetchOnWindowFocus: false": the manifest cache registers NO
  // window-focus listener, so returning to the tab must never re-fetch
  // /api/v1/accounts. Header, Linking page, groups and the account
  // selector all share this single source of truth within the stale
  // window, no matter how often the user switches tabs.
  it("never refetches the manifest on window focus (refetchOnWindowFocus: false)", async () => {
    let calls = 0;
    const fixture = [account({ id: 1, account_state: "valid", is_publishable: true })];
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async () => {
        calls += 1;
        return jsonResponse({ accounts: fixture });
      }),
    );

    await listAllAccounts();
    expect(calls).toBe(1);

    // Simulate the user leaving and returning to the tab.
    window.dispatchEvent(new Event("focus"));
    window.dispatchEvent(new Event("focus"));

    const served = await listAllAccounts();
    expect(calls).toBe(1);
    expect(served).toEqual(fixture);
  });

  // Review finding #1: a failed shared request must NOT poison the cache
  // for the whole session. The in-flight promise is reset in a finally
  // block, so after a rejection the next call starts a fresh request
  // instead of re-serving the rejected promise.
  it("recovers after a failed request — the in-flight promise is reset", async () => {
    let calls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async () => {
        calls += 1;
        if (calls === 1) {
          throw new Error("network down");
        }
        return jsonResponse({ accounts: [account({ id: 9 })] });
      }),
    );

    await expect(listAllAccounts()).rejects.toThrow("network down");

    // Second call must issue a NEW request (in-flight was reset), not
    // re-throw the cached rejection.
    await expect(listAllAccounts()).resolves.toHaveLength(1);
    expect(calls).toBe(2);
  });
});
