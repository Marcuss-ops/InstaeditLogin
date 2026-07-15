import { describe, expect, it, beforeEach, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { PlatformPage } from "./PlatformPage";

const MOBILE_WIDTH = 375;
const PLATFORMS = [
  "tiktok",
  "instagram",
  "facebook",
  "threads",
  "youtube",
  "linkedin",
  "twitter",
];

function renderPlatformPage(slug: string) {
  return render(
    <MemoryRouter initialEntries={[`/${slug}`]}>
      <Routes>
        <Route path="/:slug" element={<PlatformPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("PlatformPage mobile layout regression", () => {
  const originalInnerWidth = window.innerWidth;

  beforeEach(() => {
    Object.defineProperty(window, "innerWidth", {
      writable: true,
      configurable: true,
      value: MOBILE_WIDTH,
    });
  });

  afterEach(() => {
    Object.defineProperty(window, "innerWidth", {
      writable: true,
      configurable: true,
      value: originalInnerWidth,
    });
  });

  it.each(PLATFORMS)(
    "renders the %s platform page on mobile",
    async (slug) => {
      renderPlatformPage(slug);

      // Wait for the page to finish loading by looking for the code preview
      await screen.findByTestId("code-preview-section");
    },
  );

  it.each(PLATFORMS)(
    "renders the code preview block with horizontal scrolling for %s",
    async (slug) => {
      renderPlatformPage(slug);

      const codeSection = await screen.findByTestId("code-preview-section");
      const pre = codeSection.querySelector("pre");
      expect(pre).not.toBeNull();
      expect(pre).toHaveClass("overflow-x-auto");
      expect(pre).toHaveClass("min-w-0");
    },
  );

  it.each(PLATFORMS)(
    "renders comparison cards with mobile padding for %s",
    async (slug) => {
      renderPlatformPage(slug);

      const usCard = await screen.findByTestId("comparison-us-card");
      const themCard = screen.getByTestId("comparison-them-card");

      expect(usCard).toHaveClass("p-6");
      expect(themCard).toHaveClass("p-6");
    },
  );

  it.each(PLATFORMS)(
    "renders FAQ section with mobile horizontal padding for %s",
    async (slug) => {
      renderPlatformPage(slug);

      const faqSection = await screen.findByTestId("faq-section");
      expect(faqSection).toHaveClass("px-4");
    },
  );

  it.each(PLATFORMS)(
    "renders the CTA section with mobile horizontal padding for %s",
    async (slug) => {
      renderPlatformPage(slug);

      const ctaSection = await screen.findByTestId("cta-section");
      expect(ctaSection).toHaveClass("px-4");
    },
  );
});
