import { describe, expect, it } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { useRef, useState } from "react";
import { useClickOutside } from "./useClickOutside";

function TestComponent({ enabled }: { enabled: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(true);
  useClickOutside(ref, () => setOpen(false), enabled);

  return (
    <div>
      {open && (
        <div ref={ref} data-testid="inside">
          Inside
        </div>
      )}
      <div data-testid="outside">Outside</div>
    </div>
  );
}

describe("useClickOutside", () => {
  it("closes when clicking outside", () => {
    render(<TestComponent enabled />);
    expect(screen.getByTestId("inside")).toBeInTheDocument();

    fireEvent.mouseDown(document.body);

    expect(screen.queryByTestId("inside")).not.toBeInTheDocument();
  });

  it("does not close when clicking inside", () => {
    render(<TestComponent enabled />);
    const inside = screen.getByTestId("inside");

    fireEvent.mouseDown(inside);

    expect(screen.getByTestId("inside")).toBeInTheDocument();
  });

  it("closes when pressing Escape", () => {
    render(<TestComponent enabled />);
    expect(screen.getByTestId("inside")).toBeInTheDocument();

    fireEvent.keyDown(document, { key: "Escape" });

    expect(screen.queryByTestId("inside")).not.toBeInTheDocument();
  });

  it("does not attach listeners when disabled", () => {
    render(<TestComponent enabled={false} />);
    expect(screen.getByTestId("inside")).toBeInTheDocument();

    fireEvent.mouseDown(document.body);

    expect(screen.getByTestId("inside")).toBeInTheDocument();
  });
});
