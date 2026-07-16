import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { Skeleton } from "./Skeleton";

describe("Skeleton", () => {
  describe("variant=text", () => {
    it("renders an animated placeholder line", () => {
      const { container } = render(<Skeleton variant="text" />);
      const el = container.firstChild as HTMLElement;
      expect(el).toHaveClass("animate-pulse");
      expect(el).toHaveClass("bg-white/[0.06]");
      expect(el.getAttribute("aria-hidden")).toBe("true");
    });

    it("respects the width prop", () => {
      const { container } = render(<Skeleton variant="text" width="60%" />);
      expect((container.firstChild as HTMLElement).style.width).toBe("60%");
    });
  });

  describe("variant=circle", () => {
    it("renders a circular placeholder", () => {
      const { container } = render(<Skeleton variant="circle" size={32} />);
      const el = container.firstChild as HTMLElement;
      expect(el).toHaveClass("rounded-full");
      expect(el.style.width).toBe("32px");
      expect(el.style.height).toBe("32px");
      expect(el).toHaveClass("shrink-0");
    });

    it("falls back to a sensible default size", () => {
      const { container } = render(<Skeleton variant="circle" />);
      expect((container.firstChild as HTMLElement).style.width).toBe("40px");
    });
  });

  describe("variant=card", () => {
    it("renders a rectangular block with the default height", () => {
      const { container } = render(<Skeleton variant="card" />);
      const el = container.firstChild as HTMLElement;
      expect(el).toHaveClass("rounded-xl");
      expect(el.style.height).toBe("120px");
    });

    it("respects the height prop", () => {
      const { container } = render(<Skeleton variant="card" height={80} />);
      expect((container.firstChild as HTMLElement).style.height).toBe("80px");
    });
  });

  describe("variant=list-row", () => {
    it("composes an avatar + two text lines", () => {
      const { container } = render(<Skeleton variant="list-row" />);
      // 1 circle + 2 text placeholders => three aria-hidden children.
      const hidden = container.querySelectorAll('[aria-hidden="true"]');
      expect(hidden.length).toBe(3);
    });

    it("respects the gap prop (defaults to 12px)", () => {
      const { container } = render(<Skeleton variant="list-row" gap={16} />);
      // The list-row container uses inline-style gap.
      const wrapper = container.firstChild as HTMLElement;
      expect(wrapper.style.gap).toBe("16px");
    });
  });
});
