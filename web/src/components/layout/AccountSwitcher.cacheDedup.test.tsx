import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { clearAccountsCache } from "../../features/channels/api/channelsApi";
import { AccountSwitcher } from "./AccountSwitcher";
import { InternalLinking } from "../../pages/internal/Linking";

// ────────────────────────────────────────────────────────────────────
// Shared accounts manifest cache — cross-consumer dedup (N+1 DoD Fase 6)
//
// The header (AccountSwitcher) and the Linking page (InternalLinking)
// both mount on app start and both call listAllAccounts(). Without the
// shared 60s cache they would fire TWO identical GET /api/v1/accounts
// requests on every page load. This test renders both components
// together — the exact production shape — and asserts exactly ONE
// network request for the manifest, plus zero /accounts/{id} fan-out.
// ────────────────────────────────────────────────────────────────────

function mockJsonResponse(data: unknown, ok = true, status = 200) {
  return {
    ok,
    status,
    json: async () => data,
  } as unknown as Response;
}

// API_BASE_URL is empty in tests, so fetch URLs are relative paths; the
// base-URL constructor keeps `new URL` from throwing (same pattern as
// channelsApi.test.ts). pathname+search is what the backend receives.
const parseUrl = (url: string): URL => new URL(url, "http://relative.test");

const ACCOUNTS = {
  accounts: [
    {
      id: 21,
      platform: "youtube",
      platform_user_id: "UC-21",
      username: "wwe-channel",
      status: "active",
      account_state: "valid",
      is_publishable: true,
      created_at: "2026-01-01T00:00:00Z",
    },
    {
      id: 22,
      platform: "instagram",
      platform_user_id: "IG-22",
      username: "brand_page",
      status: "active",
      account_state: "valid",
      is_publishable: true,
      created_at: "2026-01-02T00:00:00Z",
    },
  ],
};

describe("AccountSwitcher + InternalLinking — shared cache dedup", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    // The module-level manifest cache persists across tests (and files in
    // this pool); reset it so each test counts its own fetch stub.
    clearAccountsCache();
  });

  it("fires exactly ONE GET /api/v1/accounts for header + page mounted together", async () => {
    const requestedUrls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        requestedUrls.push(url);
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1, name: "Mario" });
        }
        if (parseUrl(url).pathname === "/api/v1/accounts") {
          return mockJsonResponse(ACCOUNTS);
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    render(
      <MemoryRouter>
        <Routes>
          <Route
            path="/"
            element={
              <div>
                {/* Header — rendered on every internal page via InternalLayout. */}
                <AccountSwitcher />
                <InternalLinking />
              </div>
            }
          />
        </Routes>
      </MemoryRouter>,
    );

    // The Linking page mounted and loaded its manifest.
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /Linking/i })).toBeInTheDocument();
    });

    // The header also consumed the manifest: open the dropdown and check
    // the connected-account list rendered (session-name independent, so
    // the test is hermetic against lib/auth's persisted sessionCache).
    const switcherButton = await waitFor(() => {
      const button = document.getElementById("account-switcher-button");
      if (!button) throw new Error("account switcher not mounted");
      return button;
    });
    fireEvent.click(switcherButton);
    await waitFor(() => {
      expect(screen.getByText("Connected accounts")).toBeInTheDocument();
    });
    expect(screen.getByText("@wwe-channel")).toBeInTheDocument();
    expect(screen.getByText("@brand_page")).toBeInTheDocument();

    // DoD: header + page share ONE manifest request — no duplicate refetch.
    const accountListCalls = requestedUrls.filter(
      (u) => parseUrl(u).pathname === "/api/v1/accounts",
    );
    expect(accountListCalls.length).toBe(1);

    // And the N+1 guarantee still holds: zero per-account detail calls.
    const detailCalls = requestedUrls.filter((u) => /\/api\/v1\/accounts\/\d+$/.test(u));
    expect(detailCalls.length).toBe(0);
  });
});
