import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";

const { fetchSessionMock, authedFetchMock } = vi.hoisted(() => ({
  fetchSessionMock: vi.fn(),
  authedFetchMock: vi.fn(),
}));

vi.mock("./lib/auth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./lib/auth")>()),
  fetchSession: fetchSessionMock,
  authedFetch: authedFetchMock,
}));

import App from "./App";

describe("covers hub route", () => {
  beforeEach(() => {
    fetchSessionMock.mockResolvedValue({
      userId: 1,
      name: "Test user",
      username: "test-user",
      expiresAt: "",
      isAdmin: false,
    });
    // Default: no groups / no accounts — the hub still renders its shell.
    authedFetchMock.mockResolvedValue(
      new Response(JSON.stringify({}), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  });

  afterEach(() => {
    cleanup();
    window.history.replaceState({}, "", "/");
    vi.clearAllMocks();
  });

  it("renders the Copertine hub at /app/covers for authenticated users", async () => {
    window.history.replaceState({}, "", "/app/covers");
    render(<App />);

    await waitFor(
      () => {
        expect(screen.getByRole("heading", { name: /copertine/i })).toBeInTheDocument();
      },
      { timeout: 5000 },
    );
  });

  it("keeps nested legacy covers bookmarks on the hub (no redirect away)", async () => {
    window.history.replaceState({}, "", "/app/covers/project-123");
    render(<App />);

    await waitFor(
      () => {
        expect(screen.getByRole("heading", { name: /copertine/i })).toBeInTheDocument();
      },
      { timeout: 5000 },
    );
  });

  it("keeps the protected route contract for logged-out users", async () => {
    fetchSessionMock.mockResolvedValueOnce(null);
    window.history.replaceState({}, "", "/app/covers");
    render(<App />);

    await waitFor(() => {
      expect(window.location.pathname).toBe("/login");
    });
  });

  it("preselects the group from the ?group= deep link (editor return)", async () => {
    authedFetchMock.mockImplementation(async (url: string) => {
      if (url === "/api/v1/auth/me") {
        return new Response(JSON.stringify({ workspace_id: 5 }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (url.startsWith("/api/v1/groups/aggregate")) {
        return new Response(
          JSON.stringify({
            groups: [
              { id: 1, name: "Amish" },
              { id: 7, name: "Wwe" },
            ],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        );
      }
      if (url.startsWith("/api/v1/accounts")) {
        return new Response(JSON.stringify({ accounts: [] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      // covers responses (any group) come back empty
      return new Response(JSON.stringify({ covers: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });
    window.history.replaceState({}, "", "/app/covers?group=7");
    render(<App />);

    // The hub must mount the covers grid for group 7 (the deep-linked
    // group), not the first group in the tree.
    await waitFor(() => {
      expect(authedFetchMock).toHaveBeenCalledWith(
        "/api/v1/groups/7/covers",
        expect.objectContaining({ signal: expect.any(AbortSignal) }),
      );
    });
  });
});
