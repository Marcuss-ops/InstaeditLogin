import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { GroupBadges } from "./GroupBadges";

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
});
