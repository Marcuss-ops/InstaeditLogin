import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { clearAccountsCache } from "../../features/channels/api/channelsApi";
import { clearSessionCache } from "../../lib/auth";
import { clearSharedQueryCache } from "../../lib/queryRegistry";
import { InternalDashboard } from "./Dashboard";
import { clearDashboardAnalyticsCache } from "./useDashboardAnalytics";

function mockJsonResponse(data: unknown, ok = true, status = 200) {
  return {
    ok,
    status,
    json: async () => data,
  } as unknown as Response;
}

function analyticsPayload(overrides: Record<string, unknown> = {}) {
  return {
    period_days: 28,
    aggregates: {
      channels: 2,
      views: 1500,
      subscribers: 8000,
      videos: 12,
      revenue_cents: 5000,
    },
    channels: [
      {
        id: 101,
        username: "channel-1",
        views: 1000,
        views_growth: { absolute: 200, percent: 25 },
        revenue_cents: 3000,
        revenue_growth: { absolute: 500, percent: 20 },
      },
      {
        id: 102,
        username: "channel-2",
        views: 500,
        views_growth: { absolute: -50, percent: -9.1 },
        revenue_cents: 2000,
        revenue_growth: null,
      },
    ],
    top_videos: [
      {
        video_id: "vid1",
        title: "Best video",
        thumbnail_url: "https://example.com/thumb.jpg",
        views: 900,
        published_at: "2026-08-01T00:00:00Z",
        channel_name: "channel-1",
        youtube_url: "https://www.youtube.com/watch?v=vid1",
      },
    ],
    generated_at: "2026-08-07T00:00:00Z",
    ...overrides,
  };
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
    // The dashboard hook caches per (user, period) in module state +
    // localStorage; clear it so every test starts from a cold cache.
    clearDashboardAnalyticsCache();
  });

  it("renders the analytics heading and total KPI cards", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1 });
        }
        if (url.endsWith("/api/v1/dashboard/analytics?days=28")) {
          return mockJsonResponse(analyticsPayload());
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("Views totali")).toBeInTheDocument();
    });
    expect(screen.getByText("1.5K")).toBeInTheDocument();
    expect(screen.getByText("8.0K")).toBeInTheDocument();
    expect(screen.getByText("$50.00")).toBeInTheDocument();
    expect(screen.getByText("Canali")).toBeInTheDocument();
    // Sections for the requested analytics tables ("Revenue", "Views", and
    // "Video" also appear as KPI labels / table headers, so assert presence
    // instead of uniqueness).
    expect(screen.getByText("Migliori video")).toBeInTheDocument();
    expect(screen.getAllByText("Views").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Revenue").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Video").length).toBeGreaterThan(0);
    expect(screen.getByText("Iscritti")).toBeInTheDocument();
  });

  it("switches period on button click and refetches", async () => {
    const requestedUrls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        requestedUrls.push(url);
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1 });
        }
        if (url.includes("/api/v1/dashboard/analytics")) {
          return mockJsonResponse(analyticsPayload());
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("Views totali")).toBeInTheDocument();
    });
    expect(requestedUrls.some((u) => u.endsWith("/api/v1/dashboard/analytics?days=28"))).toBe(true);

    fireEvent.click(screen.getByText("7G"));

    await waitFor(() => {
      expect(requestedUrls.some((u) => u.endsWith("/api/v1/dashboard/analytics?days=7"))).toBe(true);
    });
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
      expect(screen.getByText("Couldn't load dashboard analytics")).toBeInTheDocument();
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
        if (url.endsWith("/api/v1/dashboard/analytics?days=28")) {
          return mockJsonResponse(analyticsPayload());
        }
        if (url.endsWith("/api/v1/groups/aggregate")) {
          throw new Error(`unexpected group aggregate request: ${url}`);
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("Views totali")).toBeInTheDocument();
    });
    // Group management belongs to /app/groups, not the analytics dashboard.
    expect(screen.queryByText("Account disponibili")).not.toBeInTheDocument();
    expect(screen.queryByTestId("dashboard-groups")).not.toBeInTheDocument();
    expect(requestedUrls.some((url) => url.endsWith("/api/v1/groups/aggregate"))).toBe(false);
  });

  it("renders zero aggregates and empty tables when no data exists", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1 });
        }
        if (url.endsWith("/api/v1/dashboard/analytics?days=28")) {
          return mockJsonResponse(
            analyticsPayload({
              aggregates: { channels: 0, views: 0, subscribers: 0, videos: 0, revenue_cents: null },
              channels: [],
              top_videos: [],
            }),
          );
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("Views totali")).toBeInTheDocument();
    });
    // The KPI cards render a tick after the heading appears; wait for the
    // zero values instead of asserting immediately (was flaky in CI). With all
    // aggregates zero, four KPI cards show "0" (Views totali, Iscritti,
    // Canali, Video); Revenue shows "—" because revenue_cents is null.
    await waitFor(() => {
      expect(screen.getAllByText("0")).toHaveLength(4);
    });
    expect(screen.getByText("Nessun video pubblicato in questo periodo.")).toBeInTheDocument();
  });
});
