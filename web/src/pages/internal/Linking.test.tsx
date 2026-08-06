import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { InternalLinking } from "./Linking";

function mockJsonResponse(data: unknown, ok = true, status = 200) {
  return {
    ok,
    status,
    json: async () => data,
  } as unknown as Response;
}

function renderLinking() {
  return render(
    <MemoryRouter>
      <Routes>
        <Route path="/" element={<InternalLinking />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("InternalLinking", () => {
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it("renders the linking heading and all 6 provider cards", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1 });
        }
        if (url.endsWith("/api/v1/accounts")) {
          return mockJsonResponse({ accounts: [] });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    renderLinking();

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /Linking/i })).toBeInTheDocument();
    });

    expect(screen.getByText("YouTube")).toBeInTheDocument();
    expect(screen.getByText("TikTok")).toBeInTheDocument();
    expect(screen.getByText("Facebook")).toBeInTheDocument();
    expect(screen.getByText("Instagram")).toBeInTheDocument();
    expect(screen.getByText("Threads")).toBeInTheDocument();
    expect(screen.getByText("Google Drive")).toBeInTheDocument();
  });

  it("shows an error state when accounts cannot be loaded", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1 });
        }
        return mockJsonResponse({ error: "boom" }, false, 500);
      }),
    );

    renderLinking();

    await waitFor(() => {
      expect(screen.getByText("Couldn't load providers")).toBeInTheDocument();
    });
  });

  it("renders all providers as not connected when no accounts exist", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1 });
        }
        if (url.endsWith("/api/v1/accounts")) {
          return mockJsonResponse({ accounts: [] });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    renderLinking();

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /Linking/i })).toBeInTheDocument();
    });

    const notConnected = screen.getAllByText("Not connected");
    expect(notConnected.length).toBe(6);
  });

  it("renders accounts from the list with avatar placeholders and ZERO per-account detail fan-out", async () => {
    const requestedUrls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        requestedUrls.push(url);
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1 });
        }
        if (url.endsWith("/api/v1/accounts")) {
          return mockJsonResponse({
            accounts: [
              {
                id: 1,
                platform: "youtube",
                platform_user_id: "UC-1",
                username: "NoAvatarChannel",
                status: "active",
                account_state: "valid",
                is_publishable: true,
                created_at: "2026-08-05T10:00:00Z",
              },
              {
                id: 2,
                platform: "youtube",
                platform_user_id: "UC-2",
                username: "AvatarChannel",
                avatar_url: "https://avatars/2",
                status: "active",
                account_state: "valid",
                is_publishable: true,
                created_at: "2026-08-05T10:00:00Z",
              },
            ],
          });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    renderLinking();

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /Linking/i })).toBeInTheDocument();
    });

    // Expand the YouTube card so its accounts render.
    fireEvent.click(screen.getByRole("button", { name: /YouTube/i }));

    await waitFor(() => {
      expect(screen.getByText("@NoAvatarChannel")).toBeInTheDocument();
    });
    expect(screen.getByText("@AvatarChannel")).toBeInTheDocument();

    // The avatar-less channel renders the account-initial placeholder...
    expect(screen.getByText("N")).toBeInTheDocument();

    // ...and the page fired NO /accounts/{id} detail requests (N+1 fixed).
    const detailCalls = requestedUrls.filter((u) => /\/api\/v1\/accounts\/\d+$/.test(u));
    expect(detailCalls.length).toBe(0);
  });

  it("schedules a background refresh for all channels via POST /accounts/sync-all", async () => {
    const requestedUrls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        requestedUrls.push(url);
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1 });
        }
        if (url.endsWith("/api/v1/accounts/sync-all")) {
          return mockJsonResponse({ status: "scheduled", count: 3 });
        }
        if (url.endsWith("/api/v1/accounts")) {
          return mockJsonResponse({ accounts: [] });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    renderLinking();

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /Linking/i })).toBeInTheDocument();
    });

    fireEvent.click(screen.getByTestId("sync-all-accounts"));

    await waitFor(() => {
      expect(screen.getByTestId("sync-all-notice")).toHaveTextContent(
        /Refresh scheduled for 3 channels/,
      );
    });

    // Exactly ONE bulk enqueue request — no per-account fan-out.
    const syncAllCalls = requestedUrls.filter((u) => u.includes("/api/v1/accounts/sync-all"));
    expect(syncAllCalls.length).toBe(1);
    const detailCalls = requestedUrls.filter((u) => /\/api\/v1\/accounts\/\d+$/.test(u));
    expect(detailCalls.length).toBe(0);
  });
});
