import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { EmptyState } from "./EmptyState";

describe("EmptyState", () => {
  it("renders the title and the optional description", () => {
    render(
      <EmptyState
        title="No posts yet"
        description="Compose your first post and publish to a connected account."
      />,
    );
    expect(screen.getByText("No posts yet")).toBeInTheDocument();
    expect(
      screen.getByText("Compose your first post and publish to a connected account."),
    ).toBeInTheDocument();
  });

  it("renders the icon when provided", () => {
    render(
      <EmptyState
        title="X"
        icon={<span data-testid="empty-icon">icon</span>}
      />,
    );
    expect(screen.getByTestId("empty-icon")).toBeInTheDocument();
  });

  it("renders the CTA when provided", () => {
    render(
      <EmptyState
        title="X"
        cta={<a href="/compose" data-testid="empty-cta">Compose</a>}
      />,
    );
    expect(screen.getByTestId("empty-cta")).toBeInTheDocument();
  });

  it("uses the status role for screen readers", () => {
    render(<EmptyState title="X" />);
    expect(screen.getByRole("status")).toBeInTheDocument();
  });

  it("exposes data-testid for test selector assertions", () => {
    render(<EmptyState title="X" />);
    expect(screen.getByTestId("empty-state")).toBeInTheDocument();
  });
});
