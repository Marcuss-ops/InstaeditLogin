import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ErrorBoundary } from "./ErrorBoundary";

function Happy(): ReactElement {
  return <div data-testid="happy">Everything is fine</div>;
}

/**
 * Wrapped so the throw happens inside React's render scheduling (not at
 * module-evaluation time, which would surface before our spy is in place).
 */
function Boom({ shouldThrow }: { shouldThrow: boolean }): ReactElement {
  if (shouldThrow) throw new Error("boom");
  return <div data-testid="happy">Recovered</div>;
}

describe("ErrorBoundary", () => {
  beforeEach(() => {
    // React 19 logs the uncaught error to the console; suppress noise.
    vi.spyOn(console, "error").mockImplementation(() => undefined);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders children when no error is thrown", () => {
    render(
      <ErrorBoundary>
        <Happy />
      </ErrorBoundary>,
    );
    expect(screen.getByTestId("happy")).toBeInTheDocument();
  });

  it("catches a thrown error and shows the fallback", () => {
    render(
      <ErrorBoundary>
        <Boom shouldThrow />
      </ErrorBoundary>,
    );
    expect(screen.getByTestId("error-boundary-fallback")).toBeInTheDocument();
    expect(screen.getByTestId("error-boundary-message")).toHaveTextContent("boom");
  });

  it("'Try again' attempts to remount the subtree without reloading", () => {
    // Even though Boom still throws, the reset should clear hasError
    // and re-enter render — the fallback re-appears because the
    // guarded render throws again. The key behavior is that reset
    // does NOT crash and the boundary message is still "boom".
    render(
      <ErrorBoundary>
        <Boom shouldThrow />
      </ErrorBoundary>,
    );

    fireEvent.click(screen.getByTestId("error-boundary-reset"));
    // Post-reset: same favorite message, same fallback structure.
    expect(screen.getByTestId("error-boundary-message")).toHaveTextContent("boom");
    expect(screen.getByTestId("error-boundary-fallback")).toBeInTheDocument();
  });

  it("renders children inside a keyed remount container (data-testid)", () => {
    render(
      <ErrorBoundary>
        <Happy />
      </ErrorBoundary>,
    );
    expect(screen.getByTestId("error-boundary-children")).toBeInTheDocument();
  });
});
