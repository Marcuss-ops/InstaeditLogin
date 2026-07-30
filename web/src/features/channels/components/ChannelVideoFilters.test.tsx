/**
 * ChannelVideoFilters vitest coverage.
 *
 * Goals:
 *   1. Renders all 4 chips in the spec order (Tutti / Privati / Non
 *      in elenco / Pubblici).
 *   2. The chip matching `value` carries `aria-checked="true"` and
 *      the visual active class.
 *   3. Clicking a non-active chip fires `onChange(chip.id)` exactly
 *      once. Clicking the already-active chip does NOT fire
 *      (avoids spurious re-renders downstream).
 *   4. Optional `counts` render as small subscript pills on each chip.
 *   5. The radiogroup exposes the correct `aria-label`.
 *   6. `disabled` blocks clicks.
 */
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ChannelVideoFilters } from "./ChannelVideoFilters";

describe("ChannelVideoFilters", () => {
  it("renders all 4 spec-defined chips in order", () => {
    render(<ChannelVideoFilters value="all" onChange={() => {}} />);
    expect(
      screen.getByTestId("channel-video-filter-all"),
    ).toHaveTextContent("Tutti");
    expect(
      screen.getByTestId("channel-video-filter-private"),
    ).toHaveTextContent("Privati");
    expect(screen.getByTestId("channel-video-filter-unlisted")).toHaveTextContent(
      "Non in elenco",
    );
    expect(screen.getByTestId("channel-video-filter-public")).toHaveTextContent(
      "Pubblici",
    );
  });

  it("marks the active chip with aria-checked=true", () => {
    render(<ChannelVideoFilters value="private" onChange={() => {}} />);
    const active = screen.getByTestId("channel-video-filter-private");
    expect(active).toHaveAttribute("aria-checked", "true");
    expect(active.className).toContain("bg-white");
    // Other chips remain unchecked.
    expect(screen.getByTestId("channel-video-filter-all")).toHaveAttribute(
      "aria-checked",
      "false",
    );
    expect(screen.getByTestId("channel-video-filter-public")).toHaveAttribute(
      "aria-checked",
      "false",
    );
  });

  it("fires onChange exactly once when an inactive chip is clicked", () => {
    const onChange = vi.fn();
    render(<ChannelVideoFilters value="all" onChange={onChange} />);
    fireEvent.click(screen.getByTestId("channel-video-filter-unlisted"));
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith("unlisted");
  });

  it("does NOT fire onChange when clicking the already-active chip", () => {
    const onChange = vi.fn();
    render(<ChannelVideoFilters value="public" onChange={onChange} />);
    fireEvent.click(screen.getByTestId("channel-video-filter-public"));
    expect(onChange).not.toHaveBeenCalled();
  });

  it("renders per-chip counts when provided", () => {
    render(
      <ChannelVideoFilters
        value="all"
        onChange={() => {}}
        counts={{ all: 42, private: 12, unlisted: 5, public: 25 }}
      />,
    );
    // Match by sibling text under each chip.
    const all = screen.getByTestId("channel-video-filter-all");
    expect(all).toHaveTextContent("42");
    const priv = screen.getByTestId("channel-video-filter-private");
    expect(priv).toHaveTextContent("12");
    const unlist = screen.getByTestId("channel-video-filter-unlisted");
    expect(unlist).toHaveTextContent("5");
    const pub = screen.getByTestId("channel-video-filter-public");
    expect(pub).toHaveTextContent("25");
  });

  it("omits the count pill when no count for that chip is provided", () => {
    render(
      <ChannelVideoFilters
        value="all"
        onChange={() => {}}
        counts={{ private: 7 }}
      />,
    );
    const priv = screen.getByTestId("channel-video-filter-private");
    expect(priv).toHaveTextContent("7");
    // No pill under others means no second number rendered.
    const all = screen.getByTestId("channel-video-filter-all");
    expect(all.textContent?.match(/\d/)).toBeNull();
  });

  it("exposes aria-label=Filtra i video per privacy on the radiogroup", () => {
    render(<ChannelVideoFilters value="all" onChange={() => {}} />);
    expect(screen.getByRole("radiogroup")).toHaveAttribute(
      "aria-label",
      "Filtra i video per privacy",
    );
  });

  it("blocks clicks while disabled", () => {
    const onChange = vi.fn();
    render(
      <ChannelVideoFilters value="all" onChange={onChange} disabled />,
    );
    const inactive = screen.getByTestId("channel-video-filter-private");
    expect(inactive).toBeDisabled();
    fireEvent.click(inactive);
    expect(onChange).not.toHaveBeenCalled();
  });
});
