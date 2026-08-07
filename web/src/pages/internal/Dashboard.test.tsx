import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { clearAccountsCache } from "../../features/channels/api/channelsApi";
import { clearSessionCache } from "../../lib/auth";
import { clearSharedQueryCache } from "../../lib/queryRegistry";
import { InternalDashboard } from "./Dashboard";

function mockJsonResponse(data: unknown, ok = true, status = 200) {
  return {
    ok,
    status,
    json: async () => data,
  } as unknown as Response;
}

function renderDashboard() {
  return render(
    <MemoryRouter>
      <Routes>
        <Route path="/" element={<InternalDashboard />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("InternalDashboard", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    clearAccountsCache();
    clearSessionCache();
    clearSharedQueryCache();
  });

  it("renders the dashboard heading and stats", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1 });
        }
        if (new URL(url, "http://localhost").pathname === "/api/v1/accounts") {
          return mockJsonResponse({
            accounts: [
              { id: 1, platform: "instagram", username: "demo", created_at: new Date().toISOString() },
            ],
          });
        }
        if (url.endsWith("/api/v1/posts")) {
          return mockJsonResponse({
            posts: [
              { id: 1, status: "published", scheduled_at: null },
              { id: 2, status: "queued", scheduled_at: new Date().toISOString() },
            ],
          });
        }
        if (url.endsWith("/api/v1/uploads/counts")) {
          return mockJsonResponse({
            counts: [{ account_id: 1, count: 0, next_publish_at: null }],
            total_uploads: 0,
            total_targets: 0,
          });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("Connected accounts")).toBeInTheDocument();
    });

    expect(screen.getByText("Upload in coda")).toBeInTheDocument();
    expect(screen.getByText("Upload in coda")).toBeInTheDocument();
  });

  it("shows an error state when data cannot be loaded", async () => {
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

    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("Couldn't load dashboard")).toBeInTheDocument();
    });
  });

  it("stays analytics-only: no group/aggregate fetch and no groups UI", async () => {
    const requestedUrls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        requestedUrls.push(url);
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1, workspace_id: 7 });
        }
        if (new URL(url, "http://localhost").pathname === "/api/v1/accounts") {
          return mockJsonResponse({
            accounts: [
              { id: 101, platform: "youtube", username: "channel-1", status: "active", created_at: new Date().toISOString() },
              { id: 102, platform: "youtube", username: "channel-2", status: "active", created_at: new Date().toISOString() },
            ],
          });
        }
        if (url.endsWith("/api/v1/posts")) {
          return mockJsonResponse({ posts: [] });
        }
        if (url.endsWith("/api/v1/uploads/counts")) {
          return mockJsonResponse({ counts: [], total_uploads: 0, total_targets: 0 });
        }
        if (url.endsWith("/api/v1/groups/aggregate")) {
          throw new Error(`unexpected group aggregate request: ${url}`);
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("Connected accounts")).toBeInTheDocument();
    });
    expect(screen.getByText("Upload in coda")).toBeInTheDocument();
    // Group management belongs to /app/groups, not the analytics dashboard.
    expect(screen.queryByText("Account disponibili")).not.toBeInTheDocument();
    expect(screen.queryByTestId("dashboard-groups")).not.toBeInTheDocument();
    expect(requestedUrls.some((url) => url.endsWith("/api/v1/groups/aggregate"))).toBe(false);
  });

  it("renders zero stats and empty accounts when no data exists", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1 });
        }
        if (new URL(url, "http://localhost").pathname === "/api/v1/accounts") {
          return mockJsonResponse({ accounts: [] });
        }
        if (url.endsWith("/api/v1/posts")) {
          return mockJsonResponse({ posts: [] });
        }
        if (url.endsWith("/api/v1/uploads/counts")) {
          return mockJsonResponse({
            counts: [],
            total_uploads: 0,
            total_targets: 0,
          });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("Connected accounts")).toBeInTheDocument();
    });

    // The stat cards render a tick after the heading appears; wait for the
    // zero values instead of asserting immediately (was flaky in CI).
    await waitFor(() => {
      expect(screen.getAllByText("0")).toHaveLength(2);
    });
  });

  it("offers to add a channel through the same OAuth flow as Linking", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1 });
        }
        if (new URL(url, "http://localhost").pathname === "/api/v1/accounts") {
          return mockJsonResponse({ accounts: [] });
        }
        if (url.endsWith("/api/v1/posts")) {
          return mockJsonResponse({ posts: [] });
        }
        if (url.endsWith("/api/v1/uploads/counts")) {
          return mockJsonResponse({ counts: [], total_uploads: 0, total_targets: 0 });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("Connected accounts")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByTestId("dashboard-add-channel"));

    const youtubeItem = screen.getByRole("menuitem", { name: /YouTube/ });
    expect(youtubeItem).toHaveAttribute(
      "href",
      expect.stringContaining("/api/v1/auth/youtube/login?mode=add"),
    );
    expect(screen.getByRole("menuitem", { name: /TikTok/ })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: /Instagram/ })).toBeInTheDocument();
  });
});
