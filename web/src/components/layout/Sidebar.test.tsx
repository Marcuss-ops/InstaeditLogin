import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";

// Session shape mirrors what the backend /auth/me returns to a normal
// session. Only `isAdmin` is read by the Sidebar; the other fields keep
// the mock aligned with the shared session contract used elsewhere.
let sessionMock = {
  userId: 7,
  name: "Mario Rossi",
  email: "mario@instaedit.org",
  username: "demo_user",
  expiresAt: "",
  isAdmin: false,
};

vi.mock("../../lib/auth", () => ({
  fetchSession: async () => sessionMock,
  logout: vi.fn(),
}));

// The live badge is out of scope here; a null count keeps the Sidebar
// mountable without starting the shared polling query.
vi.mock("../../hooks/useActiveLiveCount", () => ({
  useActiveLiveCount: () => null,
}));

import { Sidebar } from "./Sidebar";

function renderSidebar() {
  return render(
    <MemoryRouter>
      <Sidebar collapsed={false} onToggle={() => {}} />
    </MemoryRouter>,
  );
}

describe("Sidebar navigation", () => {
  it("does not render the removed Content Inbox entry", () => {
    renderSidebar();

    expect(screen.queryByText("Content Inbox")).not.toBeInTheDocument();
  });

  it("still renders the remaining navigation entries", () => {
    renderSidebar();

    for (const label of [
      "Dashboard",
      "Performance",
      "Calendar",
      "Groups",
      "Copertine",
      "Live streaming",
      "Linking",
    ]) {
      expect(screen.getByText(label)).toBeInTheDocument();
    }
  });
});
