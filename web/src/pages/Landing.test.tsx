import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { Landing } from "./Landing";

/**
 * Smoke test for the public marketing landing.
 *
 * Goal: a single cheap assertion block that fails the moment any of:
 *   - the hero h1 copy is rewritten or one of the two gradient phrases drops
 *   - a workflow step is added, removed, or renamed
 *   - the dashboard mockup gets removed or restructured enough that its
 *     window-chrome title text isn't in the DOM anymore
 *   - one of the six YouTube embed IDs (SHORT_DEMOS or LONGFORM_DEMOS) is
 *     removed, swapped, or duplicated
 */
describe("Landing", () => {
  it("renders hero copy, workflow, dashboard mockup, and 6 YouTube embeds", () => {
    render(
      <MemoryRouter>
        <Landing />
      </MemoryRouter>,
    );

    // --- Hero -------------------------------------------------------------
    const h1 = screen.getByRole("heading", { level: 1 });
    expect(h1).toHaveTextContent(/Your creativity/);
    expect(h1).toHaveTextContent(/Our distribution/);

    // --- Workflow ---------------------------------------------------------
    const WORKFLOW_TITLES = [
      "Pick your niche",
      "Create or generate",
      "AI optimizes for views",
      "Post at peak times",
      "Go live everywhere",
      "Track your earnings",
    ];
    for (const title of WORKFLOW_TITLES) {
      expect(
        screen.getByRole("heading", { name: title }),
        `expected workflow step "${title}" to be in the document`,
      ).toBeInTheDocument();
    }

    // --- Dashboard mockup -------------------------------------------------
    expect(
      screen.getByText(/instaedit\.app · Calendar/),
      "expected the dashboard mockup window-chrome title to be rendered",
    ).toBeInTheDocument();

    // --- YouTube short-form embeds ---------------------------------------
    const SHORT_DEMO_IDS = ["MVwXsmRLnwM", "XCIWzK2BuRo"];
    for (const id of SHORT_DEMO_IDS) {
      expect(
        screen.getByTitle(`YouTube Shorts demo ${id}`),
        `expected short-form YouTube embed "${id}"`,
      ).toBeInTheDocument();
    }

    // --- YouTube long-form embeds ----------------------------------------
    const LONGFORM_DEMO_IDS = [
      "fLhv7d6N_3c",
      "iA1WT69NFbw",
      "R18AVWQ92fs",
      "lpKX9SKqSMw",
    ];
    for (const id of LONGFORM_DEMO_IDS) {
      expect(
        screen.getByTitle(`YouTube long-form demo ${id}`),
        `expected long-form YouTube embed "${id}"`,
      ).toBeInTheDocument();
    }

    // --- Total iframe count ----------------------------------------------
    expect(
      document.querySelectorAll("iframe"),
      "expected exactly 6 YouTube iframes on the landing (2 shorts + 4 longform)",
    ).toHaveLength(6);
  });
});
