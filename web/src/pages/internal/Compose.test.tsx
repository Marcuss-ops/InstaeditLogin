import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { InternalCompose } from "./Compose";

function mockJsonResponse(data: unknown, ok = true, status = 200) {
  return {
    ok,
    status,
    json: async () => data,
  } as unknown as Response;
}

function renderCompose() {
  return render(
    <MemoryRouter>
      <Routes>
        <Route path="/" element={<InternalCompose />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("InternalCompose", () => {
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it("renders the compose form with workspaces and accounts", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1 });
        }
        if (url.endsWith("/api/v1/workspaces")) {
          return mockJsonResponse({ workspaces: [{ id: 1, name: "Marketing" }] });
        }
        if (url.endsWith("/api/v1/accounts")) {
          return mockJsonResponse({
            accounts: [{ id: 10, platform: "instagram", username: "demo" }],
          });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    renderCompose();

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /Compose/i })).toBeInTheDocument();
    });

    expect(screen.getByLabelText(/Workspace/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/Title/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/Caption/i)).toBeInTheDocument();
    expect(screen.getByText(/Instagram/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Save draft/i })).toBeInTheDocument();
  });
});
