import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { GroupBadges } from "./GroupBadges";
import { groupAccent, type GroupAccent } from "./groupAccent";

// jsdom normalizes #rrggbbaa to rgba(r, g, b, a) and #rrggbb to rgb(r, g, b).
function accentCss(accent: GroupAccent): { bg: string; text: string } {
  const channels = (hex: string) =>
    `${parseInt(hex.slice(1, 3), 16)}, ${parseInt(hex.slice(3, 5), 16)}, ${parseInt(hex.slice(5, 7), 16)}`;
  const alpha = (parseInt(accent.bg.slice(7, 9), 16) / 255).toFixed(2);
  return { bg: `rgba(${channels(accent.text)}, ${alpha})`, text: `rgb(${channels(accent.text)})` };
}

describe("GroupBadges", () => {
  it("renders nothing when there are no groups", () => {
    const { container } = render(<GroupBadges names={[]} />);
    expect(container.firstChild).toBeNull();
  });

  it("renders a chip per group name with the group title", () => {
    render(<GroupBadges names={["Rap", "HipHop"]} />);
    expect(screen.getByTitle("Rap")).toBeInTheDocument();
    expect(screen.getByTitle("HipHop")).toBeInTheDocument();
  });

  it("slices to max and shows the overflow marker", () => {
    render(<GroupBadges names={["Rap", "HipHop", "Boxe"]} max={2} />);
    expect(screen.getByTitle("Rap")).toBeInTheDocument();
    expect(screen.getByTitle("HipHop")).toBeInTheDocument();
    expect(screen.queryByTitle("Boxe")).not.toBeInTheDocument();
    expect(screen.getByText("+1")).toBeInTheDocument();
  });

  it("renders the optional label before the chips", () => {
    render(<GroupBadges names={["Rap"]} label="già in" />);
    expect(screen.getByText("già in")).toBeInTheDocument();
  });

  it("tints each chip with its group's stable accent color", () => {
    render(<GroupBadges names={["Rap", "HipHop"]} />);
    expect(screen.getByTitle("Rap").style.backgroundColor).toBe(accentCss(groupAccent("Rap")).bg);
    expect(screen.getByTitle("Rap").style.color).toBe(accentCss(groupAccent("Rap")).text);
    expect(screen.getByTitle("HipHop").style.backgroundColor).toBe(accentCss(groupAccent("HipHop")).bg);
  });
});
