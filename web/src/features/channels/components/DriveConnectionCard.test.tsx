import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { DriveConnectionCard } from "./DriveConnectionCard";

describe("DriveConnectionCard", () => {
  it("offers the canonical OAuth reconnect flow when the grant is stale", () => {
    render(
      <DriveConnectionCard
        accountState="reconnect_required"
        reconnectHref="/api/v1/auth/google-drive/login?mode=reconnect"
      />,
    );

    expect(screen.getByTestId("drive-connection-card")).toHaveTextContent(
      "aggiornare il token",
    );
    expect(screen.getByTestId("drive-reconnect")).toHaveAttribute(
      "href",
      "/api/v1/auth/google-drive/login?mode=reconnect",
    );
    expect(screen.getByTestId("drive-reconnect")).toHaveTextContent(
      "Ricollega Google Drive",
    );
  });

  it("shows a connected state without YouTube controls", () => {
    render(
      <DriveConnectionCard
        accountState="valid"
        reconnectHref="/api/v1/auth/google-drive/login?mode=reconnect"
      />,
    );

    expect(screen.getByTestId("drive-connection-card")).toHaveTextContent(
      "Google Drive collegato e pronto per gli upload",
    );
    expect(screen.queryByTestId("drive-reconnect")).toBeNull();
  });
});
