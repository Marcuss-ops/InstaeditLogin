import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
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

  it("renders the linking heading and all 5 provider cards", async () => {
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
  });
});
