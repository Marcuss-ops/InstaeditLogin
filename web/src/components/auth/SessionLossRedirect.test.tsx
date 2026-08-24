import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { SessionLossRedirect } from "./SessionLossRedirect";
import { AUTH_EXPIRED_EVENT } from "../../lib/auth";

vi.mock("../../lib/demo", () => ({ isDemoMode: () => false }));

function LocationProbe() {
  const location = useLocation();
  return <div data-testid="location">{location.pathname + location.search}</div>;
}

function renderWith(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <SessionLossRedirect />
      <Routes>
        <Route path="*" element={<LocationProbe />} />
      </Routes>
    </MemoryRouter>,
  );
}

function fireAuthExpired() {
  act(() => {
    window.dispatchEvent(new Event(AUTH_EXPIRED_EVENT));
  });
}

describe("SessionLossRedirect", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("redirects a protected page to /login keeping the current URL as ?next", async () => {
    const { container } = renderWith("/app/covers?group=7");
    fireAuthExpired();
    expect(container.querySelector('[data-testid="location"]')?.textContent).toBe(
      "/login?next=%2Fapp%2Fcovers%3Fgroup%3D7&reason=session_expired",
    );
  });

  it("redirects admin routes too", async () => {
    const { container } = renderWith("/admin/dashboard");
    fireAuthExpired();
    expect(container.querySelector('[data-testid="location"]')?.textContent).toBe(
      "/login?next=%2Fadmin%2Fdashboard&reason=session_expired",
    );
  });

  it("never redirects when already on /login (no loop)", async () => {
    const { container } = renderWith("/login");
    fireAuthExpired();
    expect(container.querySelector('[data-testid="location"]')?.textContent).toBe("/login");
  });

  it("leaves public pages alone", async () => {
    const { container } = renderWith("/");
    fireAuthExpired();
    expect(container.querySelector('[data-testid="location"]')?.textContent).toBe("/");
  });
});
