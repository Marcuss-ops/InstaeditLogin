import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { InternalPosts } from "./Posts";

function mockJsonResponse(data: unknown, ok = true, status = 200) {
  return {
    ok,
    status,
    json: async () => data,
  } as unknown as Response;
}

function renderPosts() {
  return render(
    <MemoryRouter>
      <Routes>
        <Route path="/" element={<InternalPosts />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("InternalPosts", () => {
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it("renders the posts heading and a list of posts", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1 });
        }
        if (url.endsWith("/api/v1/posts")) {
          return mockJsonResponse({
            posts: [
              {
                id: 1,
                workspace_id: 1,
                title: "Launch post",
                caption: "Hello world",
                scheduled_at: null,
                status: "draft",
                created_at: new Date().toISOString(),
              },
            ],
          });
        }
        if (url.endsWith("/api/v1/workspaces")) {
          return mockJsonResponse({
            workspaces: [{ id: 1, name: "Marketing" }],
          });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    renderPosts();

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /Posts/i })).toBeInTheDocument();
    });

    expect(screen.getByText("Launch post")).toBeInTheDocument();
    expect(screen.getByText("Hello world")).toBeInTheDocument();
  });

  it("shows an error state when posts cannot be loaded", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1 });
        }
        if (url.endsWith("/api/v1/workspaces")) {
          return mockJsonResponse({ workspaces: [{ id: 1, name: "Marketing" }] });
        }
        return mockJsonResponse({ error: "boom" }, false, 500);
      }),
    );

    renderPosts();

    await waitFor(() => {
      expect(screen.getByText("Couldn't load posts")).toBeInTheDocument();
    });
  });

  it("renders the empty state when no posts exist", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1 });
        }
        if (url.endsWith("/api/v1/posts")) {
          return mockJsonResponse({ posts: [] });
        }
        if (url.endsWith("/api/v1/workspaces")) {
          return mockJsonResponse({ workspaces: [{ id: 1, name: "Marketing" }] });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    renderPosts();

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /Posts/i })).toBeInTheDocument();
    });

    expect(screen.getByText("No posts yet")).toBeInTheDocument();
    expect(screen.getByText("Create post")).toBeInTheDocument();
  });
});
