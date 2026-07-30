import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { ToastProvider } from "../../components/toast";
import { CalendarPage } from "./Calendar";

// Stub CalendarGrid so the test can read the `posts` prop as a data
// attribute on the rendered stub. We deliberately avoid rendering the real
// FullCalendar inside jsdom (which would surface FullCalendar's own quirks
// unrelated to the filter contract under test).
vi.mock("./CalendarGrid", () => ({
  CalendarGrid: (props: { posts: Array<{ id: number }> }) => (
    <div
      data-testid="calendar-grid-stub"
      data-posts-ids={JSON.stringify(props.posts.map((p) => p.id))}
    />
  ),
}));

function mockJsonResponse(data: unknown, ok = true, status = 200) {
  return {
    ok,
    status,
    json: async () => data,
  } as unknown as Response;
}

const samplePosts = [
  {
    id: 11,
    workspace_id: 47,
    title: "Photo A",
    status: "queued",
    scheduled_at: "2026-07-21T10:00:00Z",
    created_at: "2026-07-15T10:00:00Z",
  },
  {
    id: 22,
    workspace_id: 47,
    title: "Photo B",
    status: "published",
    scheduled_at: "2026-07-22T10:00:00Z",
    created_at: "2026-07-15T11:00:00Z",
  },
  {
    id: 33,
    workspace_id: 99,
    title: "Photo C",
    status: "queued",
    scheduled_at: "2026-07-23T10:00:00Z",
    created_at: "2026-07-15T12:00:00Z",
  },
];

function setupFetchMock() {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation(async (input: RequestInfo) => {
      const url = typeof input === "string" ? input : input.url;
      if (url.endsWith("/api/v1/posts")) {
        return mockJsonResponse({ posts: samplePosts });
      }
      if (url.endsWith("/api/v1/workspaces")) {
        return mockJsonResponse({
          workspaces: [
            { id: 47, name: "Personal" },
            { id: 99, name: "Client X" },
          ],
        });
      }
      return mockJsonResponse({}, false, 404);
    }),
  );
}

function renderPage(initialUrl = "/app/calendar") {
  return render(
    <MemoryRouter initialEntries={[initialUrl]}>
      <ToastProvider>
        <Routes>
          <Route path="/app/calendar" element={<CalendarPage />} />
          {/* Stub for /app/compose CTA — clicking "New post" otherwise 404s
              inside the test router. */}
          <Route path="/app/compose" element={<div data-testid="compose-stub" />} />
        </Routes>
      </ToastProvider>
    </MemoryRouter>,
  );
}

describe("CalendarPage filter", () => {
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it("renders the toolbar with status + workspace selects after load", async () => {
    setupFetchMock();
    renderPage();

    await waitFor(() => {
      expect(screen.getByTestId("calendar-filter-status")).toBeInTheDocument();
    });
    expect(screen.getByTestId("calendar-filter-workspace")).toBeInTheDocument();
    const status = screen.getByTestId("calendar-filter-status") as HTMLSelectElement;
    expect(status.value).toBe("all");
  });

  it("CalendarGrid receives ALL posts when no filter is set", async () => {
    setupFetchMock();
    renderPage();
    await waitFor(() =>
      expect(screen.getByTestId("calendar-grid-stub")).toBeInTheDocument(),
    );
    expect(
      screen.getByTestId("calendar-grid-stub").getAttribute("data-posts-ids"),
    ).toBe("[11,22,33]");
  });

  it("CalendarGrid receives status-filtered posts only (?status=queued)", async () => {
    setupFetchMock();
    renderPage("/app/calendar?status=queued");
    await waitFor(() =>
      expect(screen.getByTestId("calendar-grid-stub")).toBeInTheDocument(),
    );
    expect(
      screen.getByTestId("calendar-grid-stub").getAttribute("data-posts-ids"),
    ).toBe("[11,33]"); // Photo A (queued) + Photo C (queued); Photo B is published
  });

  it("CalendarGrid receives workspace-filtered posts only (?workspace_id=47)", async () => {
    setupFetchMock();
    renderPage("/app/calendar?workspace_id=47");
    await waitFor(() =>
      expect(screen.getByTestId("calendar-grid-stub")).toBeInTheDocument(),
    );
    expect(
      screen.getByTestId("calendar-grid-stub").getAttribute("data-posts-ids"),
    ).toBe("[11,22]"); // Photo A + Photo B; Photo C is in workspace 99
  });

  it("CalendarGrid receives the intersection of both filters (?status=queued&workspace_id=47)", async () => {
    setupFetchMock();
    // Photo A is the only post that matches both: workspace 47 AND queued.
    // Photo B is published (queued filter excludes it); Photo C is in
    // workspace 99 (workspace=47 filter excludes it).
    renderPage("/app/calendar?status=queued&workspace_id=47");
    await waitFor(() =>
      expect(screen.getByTestId("calendar-grid-stub")).toBeInTheDocument(),
    );
    expect(
      screen.getByTestId("calendar-grid-stub").getAttribute("data-posts-ids"),
    ).toBe("[11]");
  });

  it("EmptyState appears when filters narrow to zero posts; CalendarGrid is NOT rendered", async () => {
    setupFetchMock();
    // status=published + workspace_id=99 — no post matches.
    renderPage("/app/calendar?status=published&workspace_id=99");

    await waitFor(() => {
      expect(screen.getByText(/Nessun post corrisponde ai filtri/i)).toBeInTheDocument();
    });
    expect(screen.queryByTestId("calendar-grid-stub")).not.toBeInTheDocument();
    expect(screen.getByTestId("calendar-empty-clear")).toBeInTheDocument();
  });

  it("EmptyState for fresh user (no posts AND no filters active)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/posts")) {
          return mockJsonResponse({ posts: [] });
        }
        if (url.endsWith("/api/v1/workspaces")) {
          return mockJsonResponse({ workspaces: [] });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );
    renderPage("/app/calendar");

    await waitFor(() => {
      expect(screen.getByText(/Nessun post ancora programmato/i)).toBeInTheDocument();
    });
    expect(screen.queryByTestId("calendar-grid-stub")).not.toBeInTheDocument();
    expect(screen.getByTestId("calendar-empty-compose")).toBeInTheDocument();
  });

  it("EmptyState wins over filter when there are zero posts even with active URL filters", async () => {
    // Regression guard: previously a user with zero posts AND a URL filter
    // (e.g. ?status=queued bookmark) landed in a silent fallthrough and
    // saw neither EmptyState nor CalendarGrid. Now the no-posts state
    // wins regardless of filter state because filters can't hide what
    // doesn't exist.
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/posts")) {
          return mockJsonResponse({ posts: [] });
        }
        if (url.endsWith("/api/v1/workspaces")) {
          return mockJsonResponse({ workspaces: [] });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );
    renderPage("/app/calendar?status=queued");

    await waitFor(() => {
      expect(screen.getByText(/Nessun post ancora programmato/i)).toBeInTheDocument();
    });
    expect(screen.queryByTestId("calendar-grid-stub")).not.toBeInTheDocument();
    expect(screen.getByTestId("calendar-empty-compose")).toBeInTheDocument();
  });

  it("Calendar still renders when /workspaces fetch fails (best-effort)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/posts")) {
          return mockJsonResponse({ posts: samplePosts });
        }
        if (url.endsWith("/api/v1/workspaces")) {
          return mockJsonResponse(
            { error: "workspaces temporarily unavailable" },
            false,
            503,
          );
        }
        return mockJsonResponse({}, false, 404);
      }),
    );
    renderPage("/app/calendar");

    // CalendarGrid still loads all 3 posts — workspace select simply omitted.
    await waitFor(() =>
      expect(screen.getByTestId("calendar-grid-stub")).toBeInTheDocument(),
    );
    expect(
      screen.getByTestId("calendar-grid-stub").getAttribute("data-posts-ids"),
    ).toBe("[11,22,33]");
    expect(screen.queryByTestId("calendar-filter-workspace")).not.toBeInTheDocument();
  });

  it("Status select change updates the select's current value (URL sync via replace)", async () => {
    setupFetchMock();
    const user = userEvent.setup();
    renderPage();

    await waitFor(() =>
      expect(screen.getByTestId("calendar-filter-status")).toBeInTheDocument(),
    );

    await user.selectOptions(
      screen.getByTestId("calendar-filter-status"),
      "queued",
    );

    await waitFor(() => {
      const status = screen.getByTestId("calendar-filter-status") as HTMLSelectElement;
      expect(status.value).toBe("queued");
    });
    expect(
      screen.getByTestId("calendar-grid-stub").getAttribute("data-posts-ids"),
    ).toBe("[11,33]");
  });

  it("Workspace select change updates the select's current value", async () => {
    setupFetchMock();
    const user = userEvent.setup();
    renderPage();

    await waitFor(() =>
      expect(screen.getByTestId("calendar-filter-workspace")).toBeInTheDocument(),
    );

    await user.selectOptions(
      screen.getByTestId("calendar-filter-workspace"),
      "47",
    );

    await waitFor(() => {
      const ws = screen.getByTestId("calendar-filter-workspace") as HTMLSelectElement;
      expect(ws.value).toBe("47");
    });
  });

  it("honors pre-existing status + workspace params on mount", async () => {
    setupFetchMock();
    renderPage("/app/calendar?status=queued&workspace_id=47");

    await waitFor(() => {
      const status = screen.getByTestId("calendar-filter-status") as HTMLSelectElement;
      const ws = screen.getByTestId("calendar-filter-workspace") as HTMLSelectElement;
      expect(status.value).toBe("queued");
      expect(ws.value).toBe("47");
    });
    expect(screen.queryByText(/Nessun post corrisponde ai filtri/i)).not.toBeInTheDocument();
  });

  it("Clear-filters button resets both selects back to 'all' and restores the unfiltered grid", async () => {
    setupFetchMock();
    const user = userEvent.setup();
    renderPage("/app/calendar?status=queued&workspace_id=99");

    await waitFor(() =>
      expect(screen.getByTestId("calendar-filter-clear")).toBeInTheDocument(),
    );

    await user.click(screen.getByTestId("calendar-filter-clear"));

    await waitFor(() => {
      const status = screen.getByTestId("calendar-filter-status") as HTMLSelectElement;
      const ws = screen.getByTestId("calendar-filter-workspace") as HTMLSelectElement;
      expect(status.value).toBe("all");
      expect(ws.value).toBe("all");
    });
    // After clearing, all 3 posts come back to CalendarGrid.
    await waitFor(() =>
      expect(
        screen.getByTestId("calendar-grid-stub").getAttribute("data-posts-ids"),
      ).toBe("[11,22,33]"),
    );
  });
});
