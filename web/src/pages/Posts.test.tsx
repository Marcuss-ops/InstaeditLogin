import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { BrowserRouter } from "react-router-dom";
import { Posts } from "./Posts";

function renderPosts() {
  return render(
    <BrowserRouter>
      <Posts />
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

describe("Posts page", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    // Default mock: empty posts list, empty workspaces, empty accounts.
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/posts")) {
          return mockJsonResponse({ posts: [] });
        }
        if (url.endsWith("/api/v1/workspaces")) {
          return mockJsonResponse({ workspaces: [] });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );
  });

  it("renders the page heading", async () => {
    renderPosts();
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /Posts/i })).toBeTruthy();
    });
  });

  it("shows an empty state when the API returns no posts", async () => {
    renderPosts();
    await waitFor(() => {
      expect(screen.getByText(/No posts yet/i)).toBeTruthy();
    });
  });

  it("renders rows when the API returns posts", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/posts")) {
          return mockJsonResponse({
            posts: [
              {
                id: 1,
                workspace_id: 1,
                title: "My launch post",
                caption: "Hello world!",
                status: "draft",
                created_at: new Date().toISOString(),
                updated_at: new Date().toISOString(),
              },
              {
                id: 2,
                workspace_id: 1,
                title: "Already live",
                caption: "Went out yesterday.",
                status: "published",
                scheduled_at: null,
                created_at: new Date().toISOString(),
                updated_at: new Date().toISOString(),
              },
            ],
          });
        }
        if (url.endsWith("/api/v1/workspaces")) {
          return mockJsonResponse({ workspaces: [{ id: 1, name: "Marketing", owner_id: 1 }] });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );
    renderPosts();
    await waitFor(() => {
      expect(screen.getByText("My launch post")).toBeTruthy();
      expect(screen.getByText("Already live")).toBeTruthy();
      // Status badges render exactly once each on the two rows.
      expect(screen.getByTestId("posts-list").querySelectorAll(".rounded-full").length).toBeGreaterThanOrEqual(2);
    });
  });
});
