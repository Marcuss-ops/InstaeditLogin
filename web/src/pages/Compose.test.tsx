import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { BrowserRouter } from "react-router-dom";
import userEvent from "@testing-library/user-event";
import { Compose } from "./Compose";

function renderCompose() {
  return render(
    <BrowserRouter>
      <Compose />
    </BrowserRouter>,
  );
}

function mockJsonResponse(data: unknown, ok = true, status = 200) {
  return {
    ok,
    status,
    json: async () => data,
  };
}

describe("Compose page", () => {
  it("renders the heading and composer fields", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/workspaces")) {
          return mockJsonResponse({ workspaces: [{ id: 1, name: "Marketing", owner_id: 1 }] });
        }
        if (url.endsWith("/api/v1/accounts")) {
          return mockJsonResponse({ accounts: [] });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );
    renderCompose();
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /Compose a post/i })).toBeTruthy();
      expect(screen.getByTestId("compose-title")).toBeTruthy();
      expect(screen.getByTestId("compose-caption")).toBeTruthy();
      expect(screen.getByTestId("compose-save-draft")).toBeTruthy();
    });

    const user = userEvent.setup();
    await user.type(screen.getByTestId("compose-title"), "Hello world");
    expect((screen.getByTestId("compose-title") as HTMLInputElement).value).toBe("Hello world");
  });

  it("shows an empty accounts message when none are connected", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/workspaces")) {
          return mockJsonResponse({ workspaces: [{ id: 1, name: "Marketing", owner_id: 1 }] });
        }
        if (url.endsWith("/api/v1/accounts")) {
          return mockJsonResponse({ accounts: [] });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );
    renderCompose();
    await waitFor(() => {
      expect(screen.getByText(/No connected accounts in this workspace/i)).toBeTruthy();
    });
  });
});
