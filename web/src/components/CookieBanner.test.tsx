import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CookieBanner } from "./CookieBanner";

describe("CookieBanner", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    window.localStorage.clear();
  });

  it("renders on first visit and hides after accepting", async () => {
    render(<CookieBanner />);
    await waitFor(() => {
      expect(screen.getByTestId("cookie-banner")).toBeTruthy();
    });
    const user = userEvent.setup();
    await user.click(screen.getByTestId("cookie-accept"));
    await waitFor(() => {
      expect(screen.queryByTestId("cookie-banner")).toBeNull();
    });
  });

  it("does not render when consent is already stored", async () => {
    window.localStorage.setItem(
      "instaedit.cookie-consent.v1",
      JSON.stringify({ choice: "essential", at: new Date().toISOString() }),
    );
    render(<CookieBanner />);
    // Allow useEffect to flush.
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByTestId("cookie-banner")).toBeNull();
  });

  it("supports the 'essential only' choice", async () => {
    render(<CookieBanner />);
    await waitFor(() => screen.getByTestId("cookie-essential"));
    const user = userEvent.setup();
    await user.click(screen.getByTestId("cookie-essential"));
    await waitFor(() => {
      expect(screen.queryByTestId("cookie-banner")).toBeNull();
    });
  });
});
