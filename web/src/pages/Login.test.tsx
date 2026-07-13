import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BrowserRouter } from "react-router-dom";
import { Login } from "./Login";

function jsonResponse(data: unknown, ok = true, status = 200) {
  return {
    ok,
    status,
    json: async () => data,
  };
}

function renderLogin() {
  return render(
    <BrowserRouter>
      <Login />
    </BrowserRouter>,
  );
}

describe("Login page (magic-link)", () => {
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it("renders the email form", () => {
    renderLogin();
    expect(
      screen.getByRole("heading", { name: /Welcome to/i }),
    ).toBeTruthy();
    expect(screen.getByTestId("login-email")).toBeTruthy();
    expect(screen.getByTestId("login-send")).toBeTruthy();
  });

  it("rejects an invalid email locally before hitting the API", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({}));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderLogin();
    await user.type(screen.getByTestId("login-email"), "not-an-email");
    await user.click(screen.getByTestId("login-send"));
    // findByTestId polls for the toast testid (avoids relying on
    // the native browser email-validation tooltip text, and is
    // robust against the fadeUp animation timing).
    const toast = await screen.findByTestId("toast-err");
    expect(toast.textContent).toMatch(/Enter a valid email/i);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("calls POST /magic-link/start and shows the sent card", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({
          status: "sent",
          email: "pippo@example.com",
          magic_link_token: "DEV_TOKEN_abc123",
        }),
      ),
    );
    const user = userEvent.setup();
    renderLogin();
    await user.type(screen.getByTestId("login-email"), "pippo@example.com");
    await user.click(screen.getByTestId("login-send"));
    await waitFor(() => {
      expect(screen.getByTestId("login-sent")).toBeTruthy();
    });
    expect(screen.getByTestId("dev-magic-link-token").textContent).toContain(
      "DEV_TOKEN_abc123",
    );
  });

  it("surfaces a server error when /start returns non-ok", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({ error: "rate_limited" }, false, 429),
      ),
    );
    const user = userEvent.setup();
    renderLogin();
    await user.type(screen.getByTestId("login-email"), "pippo@example.com");
    await user.click(screen.getByTestId("login-send"));
    await waitFor(() => {
      expect(screen.getByText(/rate_limited/i)).toBeTruthy();
    });
  });
});
