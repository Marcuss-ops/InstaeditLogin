import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BrowserRouter } from "react-router-dom";
import { Login } from "./Login";

function jsonResponse(data: unknown, ok = true, status = 200) {
  return {
    ok,
    status,
    json: async () => data,
  } as unknown as Response;
}

function renderLogin() {
  return render(
    <BrowserRouter>
      <Login />
    </BrowserRouter>,
  );
}

describe("Login page (email/password)", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    // Defensive: reset window.location back to a navigable stub so
    // the navigation test can override it again without leaking
    // across cases.
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { replace: vi.fn(), href: "" } as unknown as Location,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the email and password form", () => {
    renderLogin();
    expect(screen.getByRole("heading", { name: /Welcome to/i })).toBeTruthy();
    expect(screen.getByTestId("login-email")).toBeTruthy();
    expect(screen.getByTestId("login-password")).toBeTruthy();
    expect(screen.getByTestId("login-submit")).toBeTruthy();
  });

  it("rejects an invalid email locally before hitting the API", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({}));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderLogin();
    await user.type(screen.getByTestId("login-email"), "not-an-email");
    await user.type(screen.getByTestId("login-password"), "password1");
    await user.click(screen.getByTestId("login-submit"));

    const toast = await screen.findByTestId("toast-err");
    expect(toast.textContent).toMatch(/Enter a valid email/i);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("rejects a missing password locally before hitting the API", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({}));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderLogin();
    await user.type(screen.getByTestId("login-email"), "pippo@example.com");
    await user.click(screen.getByTestId("login-submit"));

    const toast = await screen.findByTestId("toast-err");
    expect(toast.textContent).toMatch(/Enter your password/i);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("calls POST /auth/login with email and password", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({}));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderLogin();

    await user.type(screen.getByTestId("login-email"), "pippo@example.com");
    await user.type(screen.getByTestId("login-password"), "password1");
    await user.click(screen.getByTestId("login-submit"));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/api/v1/auth/login"),
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({
            email: "pippo@example.com",
            password: "password1",
          }),
        }),
      );
    });
  });

  it("redirects to /accounts on successful login", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({}, true, 200));
    vi.stubGlobal("fetch", fetchMock);
    // jsdom's window.location is read-only + non-configurable, so
    // we can't spy on `replace`. Override the whole Location-like
    // object via Object.defineProperty (configurable) and assert on
    // a method we control.
    const replaceMock = vi.fn();
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { replace: replaceMock } as unknown as Location,
    });

    const user = userEvent.setup();
    renderLogin();

    await user.type(screen.getByTestId("login-email"), "pippo@example.com");
    await user.type(screen.getByTestId("login-password"), "password1");
    await user.click(screen.getByTestId("login-submit"));

    await waitFor(() => {
      expect(replaceMock).toHaveBeenCalledWith("/accounts");
    });
  });

  it("shows a friendly error when the backend returns 401", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ error: "invalid email or password" }, false, 401));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderLogin();

    await user.type(screen.getByTestId("login-email"), "pippo@example.com");
    await user.type(screen.getByTestId("login-password"), "wrongpassword");
    await user.click(screen.getByTestId("login-submit"));

    const toast = await screen.findByTestId("toast-err");
    expect(toast.textContent).toMatch(/Invalid email or password/i);
  });

  it("shows a generic error when the backend returns a non-200 status", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ error: "boom" }, false, 500));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderLogin();

    await user.type(screen.getByTestId("login-email"), "pippo@example.com");
    await user.type(screen.getByTestId("login-password"), "password1");
    await user.click(screen.getByTestId("login-submit"));

    const toast = await screen.findByTestId("toast-err");
    expect(toast.textContent).toMatch(/Login failed/i);
  });
});
