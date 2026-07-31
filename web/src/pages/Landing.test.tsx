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
 * The Hero's visual proof is rendered by the ResultsSection; this
 * smoke test therefore focuses on the current centered hero promise.
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

  it("renders the founder video before the FAQ", () => {
    render(
      <MemoryRouter>
        <BookingProvider>
          <Landing />
        </BookingProvider>
      </MemoryRouter>,
    );

    const founderSection = screen.getByText(/How InstaEdit/i).closest("section");
    const faqHeading = screen.getByRole("heading", { name: /Questions\?/i });
    const iframe = founderSection?.querySelector(
      'iframe[src*="youtube.com/embed/mB0NHhVMrKQ"]',
    );

    expect(founderSection).not.toBeNull();
    expect(iframe).not.toBeNull();
    // The founder section must come before the FAQ in the DOM order.
    expect(founderSection?.compareDocumentPosition(faqHeading)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
  });
});
