import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";

const { fetchSessionMock, authedFetchMock } = vi.hoisted(() => ({
  fetchSessionMock: vi.fn(),
  authedFetchMock: vi.fn(),
}));

vi.mock("./lib/auth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./lib/auth")>()),
  fetchSession: fetchSessionMock,
  authedFetch: authedFetchMock,
}));

import App, { RETIRED_COVERS_REDIRECT, RetiredCoversRedirect } from "./App";

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname}</output>;
}

describe("retired covers route", () => {
  beforeEach(() => {
    fetchSessionMock.mockResolvedValue({
      userId: 1,
      name: "Test user",
      username: "test-user",
      expiresAt: "",
      isAdmin: false,
    });
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

  it("redirects legacy covers bookmarks to YouTube Studio", async () => {
    render(
      <MemoryRouter initialEntries={["/app/covers"]}>
        <RetiredCoversRedirect />
        <LocationProbe />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(screen.getByTestId("location")).toHaveTextContent(RETIRED_COVERS_REDIRECT);
    });
  });

  it.each(["/app/covers", "/app/covers/project-123"])(
    "registers the redirect in the real application router for %s",
    async (path) => {
      window.history.replaceState({}, "", path);
      render(<App />);

      await waitFor(() => {
        expect(window.location.pathname).toBe(RETIRED_COVERS_REDIRECT);
      });
    },
  );

  it("keeps the protected route contract for logged-out users", async () => {
    fetchSessionMock.mockResolvedValueOnce(null);
    window.history.replaceState({}, "", "/app/covers");
    render(<App />);

    await waitFor(() => {
      expect(window.location.pathname).toBe("/login");
    });
  });

  it("uses the same guard for nested legacy covers URLs", async () => {
    render(
      <MemoryRouter initialEntries={["/app/covers/project-123"]}>
        <RetiredCoversRedirect />
        <LocationProbe />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(screen.getByTestId("location")).toHaveTextContent(RETIRED_COVERS_REDIRECT);
    });
  });
});
