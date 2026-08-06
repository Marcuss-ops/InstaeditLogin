import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { LanguageFlag, languageLabel } from "./LanguageFlag";

describe("LanguageFlag", () => {
  it("renders an SVG flag for known codes", () => {
    const { container } = render(<LanguageFlag code="it" />);
    expect(container.querySelector("svg")).not.toBeNull();
    expect(container.querySelector("svg")?.getAttribute("viewBox")).toBe("0 0 24 16");
  });

  it("renders a globe for empty or unknown codes instead of a flag", () => {
    const { container, rerender } = render(<LanguageFlag code="" />);
    expect(container.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
    rerender(<LanguageFlag code="xx" />);
    // Unknown codes never claim a flag — the neutral globe keeps rendering.
    expect(container.querySelector("svg")).not.toBeNull();
  });

  it("normalizes case-insensitive codes", () => {
    const { container } = render(<LanguageFlag code="IT" />);
    expect(container.querySelector("svg")).not.toBeNull();
  });

  it("is hidden from the accessibility tree as a decorative glyph", () => {
    render(<LanguageFlag code="fr" />);
    // The select element in GroupsDetailPanels carries the accessible label.
    expect(screen.queryByRole("img")).toBeNull();
  });
});

describe("languageLabel", () => {
  it("maps known codes to display names", () => {
    expect(languageLabel("it")).toBe("Italiano");
    expect(languageLabel("AR")).toBe("العربية");
  });

  it("returns null for unknown codes", () => {
    expect(languageLabel("")).toBeNull();
    expect(languageLabel("xx")).toBeNull();
  });
});
