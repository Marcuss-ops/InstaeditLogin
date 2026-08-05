import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";

// The session shape is data-driven so tests can exercise both the
// name-preference and the email-fallback branches of the header.
let sessionMock: {
  userId: number;
  name: string;
  email: string | undefined;
  username: string;
  expiresAt: string;
  isAdmin: boolean;
} = {
  userId: 7,
  name: "Mario Rossi",
  email: "mario@instaedit.org",
  username: "demo_user",
  expiresAt: "",
  isAdmin: false,
};

const { authedFetchMock } = vi.hoisted(() => ({ authedFetchMock: vi.fn() }));

vi.mock("../../lib/auth", () => ({
  authedFetch: authedFetchMock,
  fetchSession: async () => sessionMock,
  AuthError: class AuthError extends Error {
    override name = "AuthError";
  },
}));

import { AccountSwitcher } from "./AccountSwitcher";

// The backend /api/v1/accounts now hides tombstones by default, so the
// fixture mirrors what the endpoint returns to a normal session.
const ACCOUNTS = {
  accounts: [
    {
      id: 21,
      platform: "youtube",
      username: "wwe-channel",
      status: "active",
      account_state: "connected",
      is_publishable: true,
      created_at: "2026-01-01T00:00:00Z",
    },
    {
      id: 22,
      platform: "instagram",
      username: "brand_page",
      status: "active",
      account_state: "connected",
      is_publishable: true,
      created_at: "2026-01-02T00:00:00Z",
    },
  ],
};

function renderSwitcher() {
  return render(
    <MemoryRouter>
      <AccountSwitcher />
    </MemoryRouter>,
  );
}

describe("AccountSwitcher header", () => {
  beforeEach(() => {
    sessionMock = {
      userId: 7,
      name: "Mario Rossi",
      email: "mario@instaedit.org",
      username: "demo_user",
      expiresAt: "",
      isAdmin: false,
    };
    authedFetchMock.mockReset();
    authedFetchMock.mockResolvedValue({
      ok: true,
      json: async () => ACCOUNTS,
    } as Response);
  });

  it("shows the logged-in InstaEdit user name instead of the linked channel", async () => {
    renderSwitcher();

    // Session name (not the channel handle) is the header label.
    expect(await screen.findByText("Mario Rossi")).toBeInTheDocument();
    expect(screen.queryByText("@wwe-channel")).not.toBeInTheDocument();
  });

  it("falls back to the account email when the name is empty", async () => {
    sessionMock = { ...sessionMock, name: "" };

    renderSwitcher();

    expect(await screen.findByText("mario@instaedit.org")).toBeInTheDocument();
    expect(screen.queryByText("@wwe-channel")).not.toBeInTheDocument();
  });

  it("still lists the connected channels in the dropdown for switching", async () => {
    const user = userEvent.setup();
    renderSwitcher();

    const button = await screen.findByRole("button", { name: /mario rossi/i });
    await user.click(button);

    expect(await screen.findByText("@wwe-channel")).toBeInTheDocument();
    expect(screen.getByText("@brand_page")).toBeInTheDocument();
  });
});
