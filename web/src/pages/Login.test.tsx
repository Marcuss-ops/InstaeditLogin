import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BrowserRouter } from "react-router-dom";
import { Login, extractMagicLinkToken } from "./Login";

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

describe("extractMagicLinkToken", () => {
  it("returns trimmed input when not a URL", () => {
    expect(extractMagicLinkToken("abc123def")).toBe("abc123def");
    expect(extractMagicLinkToken("  abc123def  ")).toBe("abc123def");
  });

  it("extracts the `token` query param from a full URL", () => {
    expect(
      extractMagicLinkToken(
        "https://app.instaedit.org/auth/callback?token=tokfromurl&provider=instagram",
      ),
    ).toBe("tokfromurl");
  });

  it("returns the raw input when the URL has no `token` param", () => {
    // The URL parses cleanly but has no `?token=`. This is almost
    // certainly a wrong paste (the user copied a different page
    // from the email). Return "" so the caller's local empty-check
    // surfaces "Paste the code from your email." instead of POSTing
    // the entire URL to /verify and getting a 401.
    expect(
      extractMagicLinkToken("https://example.com/something?other=foo"),
    ).toBe("");
  });

  it("returns empty string when the URL has `?token=` with an empty value", () => {
    // URLSearchParams.get("token") returns "" (empty string, not
    // null) for `?token=`. "" is falsy, so the helper falls
    // through to its "URL without token" branch. Pin the
    // contract: a future change that returns "" verbatim as the
    // token would otherwise be silent.
    expect(extractMagicLinkToken("https://example.com/?token=")).toBe("");
    expect(
      extractMagicLinkToken("https://example.com/?other=foo&token="),
    ).toBe("");
  });

  it("returns empty string for empty / whitespace input", () => {
    expect(extractMagicLinkToken("")).toBe("");
    expect(extractMagicLinkToken("   ")).toBe("");
  });
});

describe("Login page (magic-link)", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    // Reset the location stub so window.location.replace spy from a
    // prior test doesn't carry over. window.location itself is
    // jsdom-immutable; we replace the spy each test.
  });

  afterEach(() => {
    // restoreAllMocks unmounts the window.location.replace spy
    // created by some tests so it doesn't leak into tests that
    // don't set one up. vi.resetAllMocks (in beforeEach) clears
    // call history but does NOT restore original implementations.
    vi.restoreAllMocks();
  });

  it("renders the email form", () => {
    renderLogin();
    expect(screen.getByRole("heading", { name: /Welcome to/i })).toBeTruthy();
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

  it("calls POST /magic-link/start and shows the verify screen", async () => {
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
      expect(screen.getByTestId("login-verify")).toBeTruthy();
    });
    // The verify screen has BOTH a code input AND the dev-token
    // panel (in dev). Pin both: prod users use the input, devs
    // use the panel.
    expect(screen.getByTestId("login-code")).toBeTruthy();
    expect(screen.getByTestId("login-verify-submit")).toBeTruthy();
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

  it("submits the typed code to POST /magic-link/verify on a successful 204", async () => {
    // The actual navigation to /accounts is a window.location.replace
    // side effect — not testable in JSDOM without redefining a
    // non-configurable getter (vi.spyOn on window.location.replace
    // fails on the second test in the file with "Cannot redefine
    // property: replace"). The contract we pin here is the API call:
    // the typed code reaches /verify unmodified.
    const fetchMock = vi
      .fn()
      // /start returns 200 with a dev token
      .mockResolvedValueOnce(
        jsonResponse({
          status: "sent",
          email: "pippo@example.com",
          magic_link_token: null,
        }),
      )
      // /verify returns 204
      .mockResolvedValueOnce({
        ok: true,
        status: 204,
        json: async () => ({}),
      } as unknown as Response);
    vi.stubGlobal("fetch", fetchMock);

    const user = userEvent.setup();
    renderLogin();
    await user.type(screen.getByTestId("login-email"), "pippo@example.com");
    await user.click(screen.getByTestId("login-send"));
    await waitFor(() => {
      expect(screen.getByTestId("login-verify")).toBeTruthy();
    });

    await user.type(screen.getByTestId("login-code"), "usertypedtoken");
    await user.click(screen.getByTestId("login-verify-submit"));

    // The verify call must carry the typed code in the body.
    await waitFor(() => {
      const verifyCall = fetchMock.mock.calls.find((call) => {
        const url = call[0];
        return (
          typeof url === "string" &&
          url.includes("/api/v1/auth/magic-link/verify")
        );
      });
      expect(verifyCall).toBeTruthy();
      const body = JSON.parse((verifyCall![1] as RequestInit).body as string);
      expect(body.token).toBe("usertypedtoken");
    });
  });

  it("extracts the token when the user pastes a full URL into the code field", async () => {
    // The /verify mock returns 204 so the component reaches the
    // window.location.replace side effect. We don't spy on
    // window.location.replace (see the comment in the previous
    // test for the JSDOM reason). The contract pinned here is
    // that the URL's `?token=` value, not the URL itself, reaches
    // the /verify body.
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        jsonResponse({
          status: "sent",
          email: "pippo@example.com",
          magic_link_token: null,
        }),
      )
      .mockResolvedValueOnce({
        ok: true,
        status: 204,
        json: async () => ({}),
      } as unknown as Response);
    vi.stubGlobal("fetch", fetchMock);

    const user = userEvent.setup();
    renderLogin();
    await user.type(screen.getByTestId("login-email"), "pippo@example.com");
    await user.click(screen.getByTestId("login-send"));
    await waitFor(() => {
      expect(screen.getByTestId("login-verify")).toBeTruthy();
    });

    await user.type(
      screen.getByTestId("login-code"),
      "https://app.instaedit.org/auth/callback?provider=instagram&status=connected&token=extractedFromURL",
    );
    await user.click(screen.getByTestId("login-verify-submit"));

    await waitFor(() => {
      const verifyCall = fetchMock.mock.calls.find((call) => {
        const url = call[0];
        return (
          typeof url === "string" &&
          url.includes("/api/v1/auth/magic-link/verify")
        );
      });
      expect(verifyCall).toBeTruthy();
      const body = JSON.parse((verifyCall![1] as RequestInit).body as string);
      expect(body.token).toBe("extractedFromURL");
    });
  });

  it("disables the submit button when the code field is empty", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({
          status: "sent",
          email: "pippo@example.com",
          magic_link_token: null,
        }),
      ),
    );
    const user = userEvent.setup();
    renderLogin();
    await user.type(screen.getByTestId("login-email"), "pippo@example.com");
    await user.click(screen.getByTestId("login-send"));
    await waitFor(() => {
      expect(screen.getByTestId("login-verify")).toBeTruthy();
    });
    // No code typed — the button is disabled (so user can't fire an
    // empty /verify call).
    const submit = screen.getByTestId("login-verify-submit") as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
  });

  it("shows a friendly message when /verify returns 401 (expired or invalid code)", async () => {
    // /verify returns 401 on expired/invalid/replayed tokens. The
    // previous raw-fetch code surfaced the backend's data.error
    // string; the new authedFetch-based code wraps 401 as AuthError
    // (message "not authenticated") and we translate to a user-
    // friendly line so the user knows to request a new link.
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        jsonResponse({
          status: "sent",
          email: "pippo@example.com",
          magic_link_token: null,
        }),
      )
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        json: async () => ({ error: "invalid or expired token" }),
      } as unknown as Response);
    vi.stubGlobal("fetch", fetchMock);

    const user = userEvent.setup();
    renderLogin();
    await user.type(screen.getByTestId("login-email"), "pippo@example.com");
    await user.click(screen.getByTestId("login-send"));
    await waitFor(() => {
      expect(screen.getByTestId("login-verify")).toBeTruthy();
    });

    await user.type(screen.getByTestId("login-code"), "expiredcode");
    await user.click(screen.getByTestId("login-verify-submit"));

    // The friendly message must reach the toast (not any <p> that
    // might accidentally live in the verify screen), and the
    // verify phase must be re-rendered so the user can retry.
    await waitFor(() => {
      expect(screen.getByTestId("toast-err").textContent).toMatch(
        /Invalid or expired code/i,
      );
    });
    expect(screen.getByTestId("login-verify")).toBeTruthy();
  });

  it("returns to the email phase via 'Send to a different email' and clears the code input", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({
          status: "sent",
          email: "pippo@example.com",
          magic_link_token: null,
        }),
      ),
    );
    const user = userEvent.setup();
    renderLogin();
    await user.type(screen.getByTestId("login-email"), "pippo@example.com");
    await user.click(screen.getByTestId("login-send"));
    await waitFor(() => {
      expect(screen.getByTestId("login-verify")).toBeTruthy();
    });

    // Type a code, then go back — the code should be cleared so a
    // re-send doesn't pre-fill a stale token.
    await user.type(screen.getByTestId("login-code"), "stale");
    await user.click(screen.getByTestId("login-back-to-email"));
    expect(screen.getByTestId("login-email")).toBeTruthy();

    // Re-send, then check the code input is empty.
    await user.click(screen.getByTestId("login-send"));
    await waitFor(() => {
      expect(screen.getByTestId("login-verify")).toBeTruthy();
    });
    expect((screen.getByTestId("login-code") as HTMLInputElement).value).toBe(
      "",
    );
  });
});
