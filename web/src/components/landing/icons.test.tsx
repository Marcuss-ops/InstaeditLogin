import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { IconAnalyze, IconSchedule } from "./icons";

describe("landing functional icons", () => {
  it("renders custom attributes without depending on the brand catalog", () => {
    const { container } = render(
      <IconSchedule className="custom-icon" data-testid="schedule-icon" />,
    );

    const icon = container.querySelector("svg");
    expect(icon).toHaveClass("custom-icon");
    expect(icon).toHaveAttribute("data-testid", "schedule-icon");
  });

  it("renders the analysis icon with the shared functional-icon shape", () => {
    const { container } = render(<IconAnalyze aria-label="Analyze" />);

    expect(container.querySelector("svg")).toHaveAttribute("aria-label", "Analyze");
    expect(container.querySelectorAll("rect")).toHaveLength(3);
  });
});
