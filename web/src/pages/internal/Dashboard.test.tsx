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

    expect(screen.getByRole("heading", { name: "Connected accounts" })).toBeInTheDocument();
    expect(screen.getByText("Total posts")).toBeInTheDocument();
    expect(screen.getByText("Published")).toBeInTheDocument();
    expect(screen.getByText("Pending uploads")).toBeInTheDocument();
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

    expect(screen.getAllByText("0")).toHaveLength(4);
    expect(screen.getByText("No accounts connected yet.")).toBeInTheDocument();
  });
});
