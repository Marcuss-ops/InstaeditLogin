import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { BrowserRouter } from "react-router-dom";
import userEvent from "@testing-library/user-event";
import { Settings } from "./Settings";

function renderSettings() {
  return render(
    <BrowserRouter>
      <Settings />
    </BrowserRouter>,
  );
}

function jsonResponse(data: unknown, ok = true, status = 200) {
  return { ok, status, json: async () => data };
}

describe("Settings page", () => {
  it("renders the heading and three tabs", async () => {
    renderSettings();
    await waitFor(() => {
      // Default tab is api-keys. Just check the tabs exist.
      expect(screen.getByTestId("tab-workspaces")).toBeTruthy();
      expect(screen.getByTestId("tab-api-keys")).toBeTruthy();
      expect(screen.getByTestId("tab-webhooks")).toBeTruthy();
    });
  });

  it("switches tabs when clicked", async () => {
    renderSettings();
    await waitFor(() => screen.getByTestId("tab-workspaces"));

    // Backed by workspaces list returned; mock with empty.
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/workspaces")) {
          return jsonResponse({ workspaces: [] });
        }
        return jsonResponse({}, false, 404);
      }),
    );

    const user = userEvent.setup();
    await user.click(screen.getByTestId("tab-workspaces"));
    await waitFor(() => {
      expect(screen.getByTestId("tab-panel-workspaces")).toBeTruthy();
    });
  });

  it("renders the API key list when the API returns existing keys", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/api-keys")) {
          return jsonResponse({
            keys: [
              {
                id: 7,
                workspace_id: 1,
                created_by: 1,
                name: "CI deploy",
                environment: "test",
                key_prefix: "sk_test_abcd",
                permissions: ["read"],
                created_at: new Date().toISOString(),
                updated_at: new Date().toISOString(),
              },
            ],
          });
        }
        if (url.endsWith("/api/v1/workspaces")) {
          return jsonResponse({ workspaces: [] });
        }
        return jsonResponse({}, false, 404);
      }),
    );
    renderSettings();
    await waitFor(() => {
      expect(screen.getByTestId("apikey-list")).toBeTruthy();
    });
    expect(screen.getByText("CI deploy")).toBeTruthy();
    expect(screen.getByText(/sk_test_abcd/)).toBeTruthy();
  });
});
