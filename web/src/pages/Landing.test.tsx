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
 *
 * What this intentionally DOES NOT test:
 *   - Visual correctness (handled by manual/visual QA — there are no
 *     snapshot tests here on purpose; pixel diffs are too brittle for a
 *     landing page that's intentionally evolving).
 *   - Behaviour (no clicks, no router navigation) — Landing is purely a
 *     static presentation page; route behaviour belongs in App.test.tsx.
 *   - The short/long-form aspect ratio — the `aspect-[9/16]` /
 *     `aspect-[16/9]` JIT classes are covered by the static-class
 *     convention in YouTubeEmbed, and a class change here would already
 *     be visible in the iframe-title assertions below.
 *
 * The video ID arrays mirror ShippedString constants in Landing.tsx
 * (SHORT_DEMOS / LONGFORM_DEMOS). When those constants change, this test
 * must change too — that's intentional: that's exactly the regression
 * we want to catch.
 */
describe("Landing", () => {
  it("renders hero copy, workflow, dashboard mockup, and 6 YouTube embeds", () => {
    render(
      <MemoryRouter>
        <Landing />
      </MemoryRouter>,
    );

    // --- Hero -------------------------------------------------------------
    // Single <h1> with two gradient phrases split by <br />. Asserting both
    // catches:
    //   - copy that drops one phrase
    //   - copy that splits the h1 into two separate headings (level 1 count
    //     would not change but `Screen.getByRole("heading", {level: 1})`
    //     could throw on multiple matches if we ever split it — fail loud).
    const h1 = screen.getByRole("heading", { level: 1 });
    expect(h1).toHaveTextContent(/Publish once\./);
    expect(h1).toHaveTextContent(/Ship to every channel\./);

    // --- Workflow ---------------------------------------------------------
    const WORKFLOW_TITLES = [
      "Upload once",
      "Schedule once",
      "Publish everywhere",
      "Track in one view",
    ];
    for (const title of WORKFLOW_TITLES) {
      expect(
        screen.getByRole("heading", { name: title }),
        `expected workflow step "${title}" to be in the document`,
      ).toBeInTheDocument();
    }

    // --- Dashboard mockup -------------------------------------------------
    // The window-chrome string "instaedit.app · Calendar" is sufficiently
    // unique that it acts as a fingerprint. If the mockup gets removed, the
    // hero restructured, or the title text changed, this assertion fires.
    expect(
      screen.getByText(/instaedit\.app · Calendar/),
      "expected the dashboard mockup window-chrome title to be rendered",
    ).toBeInTheDocument();

    // --- YouTube short-form embeds ---------------------------------------
    // SHORT_DEMOS — 2 vertical 9:16 iframes. Each iframe carries a unique
    // `title={demo.title}` that we can match against.
    const SHORT_DEMO_IDS = ["MVwXsmRLnwM", "XCIWzK2BuRo"];
    for (const id of SHORT_DEMO_IDS) {
      expect(
        screen.getByTitle(`YouTube Shorts demo ${id}`),
        `expected short-form YouTube embed "${id}"`,
      ).toBeInTheDocument();
    }

    // --- YouTube long-form embeds ----------------------------------------
    // LONGFORM_DEMOS — 4 horizontal 16:9 iframes split across the upper
    // headline row (2) and the lower "ribbon" (2).
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
    // Defensive: even if individual titles match, this catches the case
    // where someone accidentally duplicates an embed row. 6 = 2 shorts +
    // 4 longform.
    expect(
      document.querySelectorAll("iframe"),
      "expected exactly 6 YouTube iframes on the landing (2 shorts + 4 longform)",
    ).toHaveLength(6);
  });
});
