import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ErrorState } from "./ErrorState";

describe("ErrorState", () => {
  it("renders the default title and the message", () => {
    render(<ErrorState message="Server returned 503" />);
    expect(screen.getByText("Couldn't load")).toBeInTheDocument();
    expect(screen.getByText("Server returned 503")).toBeInTheDocument();
  });

  it("uses a custom title when provided", () => {
    render(<ErrorState title="Couldn't load posts" message="Boom" />);
    expect(screen.getByText("Couldn't load posts")).toBeInTheDocument();
  });

  it("renders the retry button and fires onRetry when clicked", () => {
    const onRetry = vi.fn();
    render(<ErrorState message="X" onRetry={onRetry} />);
    const btn = screen.getByTestId("error-state-retry");
    fireEvent.click(btn);
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("uses the custom retry label when provided", () => {
    render(<ErrorState message="X" onRetry={() => undefined} retryLabel="Try again" />);
    expect(screen.getByText("Try again")).toBeInTheDocument();
  });

  it("hides the retry button when onRetry is missing", () => {
    render(<ErrorState message="X" />);
    expect(screen.queryByTestId("error-state-retry")).not.toBeInTheDocument();
  });

  it("renders the helpText slot when provided", () => {
    render(
      <ErrorState
        message="X"
        helpText={<span data-testid="error-help">Request ID: req_abc123</span>}
      />,
    );
    expect(screen.getByTestId("error-help")).toBeInTheDocument();
  });

  it("uses the alert role for screen readers", () => {
    render(<ErrorState message="X" />);
    expect(screen.getByRole("alert")).toBeInTheDocument();
  });
});
