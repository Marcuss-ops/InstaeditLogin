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
    expect(screen.getByText("Scheduled")).toBeInTheDocument();
  });
});
