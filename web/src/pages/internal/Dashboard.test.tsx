import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
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
  });

  it("renders the dashboard heading and stats", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1 });
        }
        if (url.endsWith("/api/v1/accounts")) {
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
      expect(screen.getByRole("heading", { name: /Dashboard/i })).toBeInTheDocument();
    });

    expect(screen.getByText("Connected accounts")).toBeInTheDocument();
    expect(screen.getByText("Video privati da pubblicare")).toBeInTheDocument();
    expect(screen.getByTestId("dashboard-private-video-period")).toHaveValue("90");
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

  it("loads group memberships from one aggregate response without per-group fan-out", async () => {
    const requestedUrls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        requestedUrls.push(url);
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1, workspace_id: 7 });
        }
        if (url.endsWith("/api/v1/accounts")) {
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
          return mockJsonResponse({
            groups: [
              { id: 1, workspace_id: 7, name: "Editorial", account_ids: [101, 102] },
            ],
          });
        }
        if (/\/api\/v1\/groups\/\d+\/accounts$/.test(url)) {
          throw new Error(`unexpected per-group membership request: ${url}`);
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("Editorial")).toBeInTheDocument();
    });
    expect(requestedUrls.filter((url) => url.endsWith("/api/v1/groups/aggregate"))).toHaveLength(1);
    expect(requestedUrls.some((url) => /\/api\/v1\/groups\/\d+\/accounts$/.test(url))).toBe(false);
  });

  it("renders zero stats and empty accounts when no data exists", async () => {
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
      expect(screen.getByRole("heading", { name: /Dashboard/i })).toBeInTheDocument();
    });

    expect(screen.getAllByText("0")).toHaveLength(2);
    expect(screen.getByTestId("dashboard-groups")).toBeInTheDocument();
  });
});
