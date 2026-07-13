import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { Connections } from "./Connections";
import { PROVIDERS } from "../lib/providers";
import { clearSessionCache } from "../lib/auth";
import { API_BASE_URL } from "../lib/api";

function renderConnections(initialEntries: string[] = ["/connections"]) {
  return render(
    <MemoryRouter initialEntries={initialEntries}>
      <Routes>
        <Route path="/connections" element={<Connections />} />
        <Route path="/login" element={<div data-testid="login-redirect" />} />
        <Route path="/accounts" element={<div data-testid="dashboard-stub" />} />
      </Routes>
    </MemoryRouter>,
  );
}

const SESSION_OK = {
  ok: true,
  status: 200,
  json: async () => ({ user_id: 1 }),
} as unknown as Response;

const NO_ACCOUNTS = {
  ok: true,
  status: 200,
  json: async () => ({ accounts: [] }),
} as unknown as Response;

function withAccounts(
  accounts: { id: number; platform: string; username: string; created_at: string }[],
) {
  return {
    ok: true,
    status: 200,
    json: async () => ({ accounts }),
  } as unknown as Response;
}

describe("Connections page", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    window.history.replaceState({}, "", "/connections");
    // auth.ts holds a module-level sessionCache + sessionPromise.
    // Without this reset, the first test's 401 (which sets
    // sessionCache = null) leaks into every subsequent test in
    // the file — they all immediately "have no session" and
    // navigate to /login, regardless of what their fetchMock
    // returns. clearSessionCache resets both to undefined/null
    // so each test re-probes the mock.
    clearSessionCache();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("redirects to /login when the session is missing", async () => {
    const fetchMock = vi
      .fn()
      // /api/v1/auth/me returns 401 → fetchSession returns null
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        json: async () => ({}),
      } as unknown as Response);
    vi.stubGlobal("fetch", fetchMock);

    renderConnections();
    await waitFor(() => {
      expect(screen.getByTestId("login-redirect")).toBeTruthy();
    });
  });

  it("renders all 7 provider cards when the user has no connected accounts", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(SESSION_OK) // fetchSession
      .mockResolvedValueOnce(NO_ACCOUNTS); // /api/v1/accounts
    vi.stubGlobal("fetch", fetchMock);

    renderConnections();
    await waitFor(() => {
      expect(screen.getByTestId("connections-grid")).toBeTruthy();
    });
    for (const p of PROVIDERS) {
      expect(screen.getByTestId(`connection-card-${p.id}`)).toBeTruthy();
    }
  });

  it("links each unconnected card to {API_BASE_URL}/api/v1/auth/{provider}/login", async () => {
    // The component builds the href as `${API_BASE_URL}/api/v1/auth/${id}/login`
    // (the full URL, not a relative path) so the browser navigates
    // to the API origin directly in cross-origin deploys. The
    // relative path would only work when the SPA is reverse-proxied
    // through the same origin as the API.
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(SESSION_OK)
      .mockResolvedValueOnce(NO_ACCOUNTS);
    vi.stubGlobal("fetch", fetchMock);

    renderConnections();
    await waitFor(() => {
      expect(screen.getByTestId("connections-grid")).toBeTruthy();
    });
    for (const p of PROVIDERS) {
      const card = screen.getByTestId(`connection-card-${p.id}`);
      expect(card.tagName).toBe("A");
      expect((card as HTMLAnchorElement).getAttribute("href")).toBe(
        `${API_BASE_URL}/api/v1/auth/${p.id}/login`,
      );
    }
  });

  it("shows a 'Connected' pill and the @username for connected providers", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(SESSION_OK)
      .mockResolvedValueOnce(
        withAccounts([
          {
            id: 42,
            platform: "instagram",
            username: "pippo_ig",
            created_at: new Date().toISOString(),
          },
        ]),
      );
    vi.stubGlobal("fetch", fetchMock);

    renderConnections();
    await waitFor(() => {
      expect(screen.getByTestId("connection-pill-instagram")).toBeTruthy();
    });
    expect(screen.getByText("@pippo_ig")).toBeTruthy();
    // Other providers are still shown as unconnected cards.
    expect(screen.getByTestId("connection-card-tiktok")).toBeTruthy();
  });

  it("surfaces a success toast when ?provider=…&status=connected is on the URL", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(SESSION_OK)
      .mockResolvedValueOnce(NO_ACCOUNTS);
    vi.stubGlobal("fetch", fetchMock);

    renderConnections(["/connections?provider=instagram&status=connected"]);
    await waitFor(() => {
      expect(screen.getByTestId("toast-ok").textContent).toMatch(
        /Instagram connected/,
      );
    });
    // URL is cleaned so a refresh doesn't re-trigger the toast.
    expect(window.location.search).toBe("");
  });

  it("surfaces an error toast when ?provider=…&status=failed is on the URL", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(SESSION_OK)
      .mockResolvedValueOnce(NO_ACCOUNTS);
    vi.stubGlobal("fetch", fetchMock);

    renderConnections(["/connections?provider=tiktok&status=failed"]);
    await waitFor(() => {
      expect(screen.getByTestId("toast-err").textContent).toMatch(
        /TikTok connection failed/,
      );
    });
    expect(window.location.search).toBe("");
  });

  it("ignores unknown status values and shows no toast", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(SESSION_OK)
      .mockResolvedValueOnce(NO_ACCOUNTS);
    vi.stubGlobal("fetch", fetchMock);

    renderConnections(["/connections?provider=instagram&status=banana"]);
    await waitFor(() => {
      expect(screen.getByTestId("connections-grid")).toBeTruthy();
    });
    expect(screen.queryByTestId("toast-ok")).toBeNull();
    expect(screen.queryByTestId("toast-err")).toBeNull();
  });

  it("renders 7 anchor cards (one per provider) for an empty accounts list", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(SESSION_OK)
      .mockResolvedValueOnce(NO_ACCOUNTS);
    vi.stubGlobal("fetch", fetchMock);

    renderConnections();
    await waitFor(() => {
      expect(screen.getByTestId("connections-grid")).toBeTruthy();
    });
    const anchors = screen
      .getByTestId("connections-grid")
      .querySelectorAll("a");
    expect(anchors.length).toBe(PROVIDERS.length);
  });

  it("does not crash when the accounts list includes a created_at the browser cannot parse", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(SESSION_OK)
      .mockResolvedValueOnce(
        withAccounts([
          {
            id: 1,
            platform: "facebook",
            username: "fb_user",
            created_at: "not-a-date",
          },
        ]),
      );
    vi.stubGlobal("fetch", fetchMock);

    renderConnections();
    await waitFor(() => {
      expect(screen.getByTestId("connection-pill-facebook")).toBeTruthy();
    });
    // The "New" badge must NOT appear for an unparseable date.
    expect(screen.queryByText(/new/i)).toBeNull();
  });
});
