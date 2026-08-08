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

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /copertine/i })).toBeInTheDocument();
    });
  });

  it("keeps nested legacy covers bookmarks on the hub (no redirect away)", async () => {
    window.history.replaceState({}, "", "/app/covers/project-123");
    render(<App />);

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /copertine/i })).toBeInTheDocument();
    });
  });

  it("keeps the protected route contract for logged-out users", async () => {
    fetchSessionMock.mockResolvedValueOnce(null);
    window.history.replaceState({}, "", "/app/covers");
    render(<App />);

    await waitFor(() => {
      expect(window.location.pathname).toBe("/login");
    });
  });
});
