import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { Landing } from "./Landing";
import { BookingProvider } from "../components/booking/BookingProvider";

/**
 * Smoke test for the public marketing landing.
 *
 * Goal: a single cheap assertion block that fails the moment any of:
 *   - the hero h1 copy is rewritten or the core promise drops
 *
 * The previous revision also asserted the YT Studio monetization
 * mockup window-chrome text. That component has been removed in
 * chore(landing): center the title (the YT Studio card was dropped
 * from the Hero composition); the Revenue panel is now carried by
 * the ResultsSection further down the page.
 */
describe("Landing", () => {
  it("renders the centered hero copy", () => {
    render(
      <MemoryRouter>
        <BookingProvider>
          <Landing />
        </BookingProvider>
      </MemoryRouter>,
    );

    // --- Hero -------------------------------------------------------------
    const h1 = screen.getByRole("heading", { level: 1 });
    expect(h1).toHaveTextContent(/Your First/i);
    expect(h1).toHaveTextContent(/Video/i);
  });
});
