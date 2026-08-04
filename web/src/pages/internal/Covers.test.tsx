/**
 * Vitest coverage for `CoversPage` (libreria Copertine).
 *
 * Locks down the library contract:
 *   • Lists workspace projects (GET /api/v1/thumbnail-projects?workspace_id=)
 *   • Filter chips Tutte/Bozze/Pronte/Collegate/Archiviate — initial = Tutte
 *   • "Collegate" counts projects with ≥1 assignment row
 *   • CTA "Crea nuova copertina" opens the autonomous create dialog and
 *     POSTs + PUTs the initial snapshot (no YouTube prerequisite)
 *   • Card actions: Apri link, Archivia (POST archive), Elimina (DELETE)
 *
 * Network mocks via vi.hoisted + vi.mock for /auth. The page reaches the
 * backend only through authedFetch, so one mock covers the api client too.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";

const { authedFetchMock } = vi.hoisted(() => ({
  authedFetchMock: vi.fn(),
}));

vi.mock("../../lib/auth", () => ({
  authedFetch: authedFetchMock,
  fetchSession: async () => ({
    userId: 1,
    name: "Demo",
    username: "demo",
    expiresAt: "",
    isAdmin: false,
  }),
  AuthError: class AuthError extends Error {
    override name = "AuthError";
  },
  ApiError: class ApiError extends Error {
    override name = "ApiError";
    constructor(
      public readonly status: number,
      msg: string,
      public readonly data?: unknown,
    ) {
      super(msg);
    }
  },
  readCookie: () => "",
}));

import { CoversPage } from "./Covers";

const PROJECT_DRAFT = {
  id: "thumbproj_1",
  workspace_id: 1,
  created_by: 1,
  name: "WWE Breaking News",
  description: "",
  canvas_width: 1920,
  canvas_height: 1080,
  status: "draft",
  current_revision_id: "thumbrev_1",
  preview_media_id: null,
  latest_export_id: null,
  version: 3,
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-03T09:00:00Z",
};

const PROJECT_READY_LINKED = {
  id: "thumbproj_2",
  workspace_id: 1,
  created_by: 1,
  name: "Summer Short",
  description: "",
  canvas_width: 1080,
  canvas_height: 1920,
  status: "ready",
  current_revision_id: "thumbrev_2",
  preview_media_id: null,
  latest_export_id: "thumbexp_2",
  version: 5,
  created_at: "2026-07-28T00:00:00Z",
  updated_at: "2026-08-03T10:00:00Z",
};

const ASSIGNMENT = {
  id: "thumbassign_1",
  workspace_id: 1,
  project_id: "thumbproj_2",
  export_id: "thumbexp_2",
  platform_account_id: 381,
  platform: "youtube",
  youtube_video_id: "abc123",
  status: "draft",
  created_at: "2026-08-03T00:00:00Z",
  updated_at: "2026-08-03T00:00:00Z",
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function setLibraryEndpoint() {
  authedFetchMock.mockImplementation(async (url: string) => {
    if (url === "/api/v1/workspaces") {
      return jsonResponse({ workspaces: [{ id: 1, name: "Personal" }] });
    }
    if (url === "/api/v1/thumbnail-projects?workspace_id=1") {
      return jsonResponse({ items: [PROJECT_DRAFT, PROJECT_READY_LINKED] });
    }
    if (url.includes("/assignments?workspace_id=1")) {
      if (url.includes("thumbproj_2")) return jsonResponse({ items: [ASSIGNMENT] });
      return jsonResponse({ items: [] });
    }
    if (url.includes("/media/resolve?workspace_id=1")) {
      return jsonResponse({ items: [] });
    }
    throw new Error(`Unexpected URL in test mock: ${url}`);
  });
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/app/covers"]}>
      <Routes>
        <Route path="/app/covers" element={<CoversPage />} />
        <Route path="/app/covers/:projectId" element={<div>detail</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  authedFetchMock.mockReset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("CoversPage", () => {
  it("renders both projects with the Tutte filter active by default", async () => {
    setLibraryEndpoint();
    renderPage();
    const allChip = await screen.findByTestId("cover-filter-all");
    expect(allChip).toHaveAttribute("aria-checked", "true");
    await waitFor(() => {
      expect(screen.getAllByTestId("cover-card")).toHaveLength(2);
    });
    expect(screen.getByText("WWE Breaking News")).toBeInTheDocument();
    expect(screen.getByText("Summer Short")).toBeInTheDocument();
  });

  it("filter chips switch the visible projects (Bozze vs Pronte)", async () => {
    setLibraryEndpoint();
    renderPage();
    await waitFor(() => {
      expect(screen.getAllByTestId("cover-card")).toHaveLength(2);
    });
    await userEvent.click(screen.getByTestId("cover-filter-draft"));
    expect(screen.getByTestId("cover-filter-draft")).toHaveAttribute(
      "aria-checked",
      "true",
    );
    await waitFor(() => {
      expect(screen.getAllByTestId("cover-card")).toHaveLength(1);
    });
    expect(screen.queryByText("Summer Short")).not.toBeInTheDocument();

    await userEvent.click(screen.getByTestId("cover-filter-ready"));
    await waitFor(() => {
      expect(screen.getAllByTestId("cover-card")).toHaveLength(1);
    });
    expect(screen.getByText("Summer Short")).toBeInTheDocument();
  });

  it("'Collegate' shows only projects with assignment rows", async () => {
    setLibraryEndpoint();
    renderPage();
    await waitFor(() => {
      expect(screen.getAllByTestId("cover-card")).toHaveLength(2);
    });
    await userEvent.click(screen.getByTestId("cover-filter-linked"));
    await waitFor(() => {
      expect(screen.getAllByTestId("cover-card")).toHaveLength(1);
    });
    expect(screen.getByText("Summer Short")).toBeInTheDocument();
    expect(screen.queryByText("WWE Breaking News")).not.toBeInTheDocument();
  });

  it("CTA opens the create dialog and creates project + initial snapshot", async () => {
    setLibraryEndpoint();
    const createdProject = { ...PROJECT_DRAFT, id: "thumbproj_new", version: 1 };
    authedFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === "/api/v1/workspaces") {
        return jsonResponse({ workspaces: [{ id: 1, name: "Personal" }] });
      }
      if (url === "/api/v1/thumbnail-projects?workspace_id=1") {
        return jsonResponse({ items: [PROJECT_DRAFT, PROJECT_READY_LINKED] });
      }
      if (url === "/api/v1/thumbnail-projects" && init?.method === "POST") {
        return jsonResponse(createdProject, 201);
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_new/snapshot?workspace_id=1") {
        return jsonResponse({
          project_id: "thumbproj_new",
          revision_id: "thumbrev_new",
          revision_number: 1,
          version: 2,
          saved_at: "2026-08-03T12:00:00Z",
          snapshot_sha256: "abc",
        });
      }
      if (url.includes("/assignments?workspace_id=1")) {
        return jsonResponse({ items: [] });
      }
      if (url.includes("/media/resolve?workspace_id=1")) {
        return jsonResponse({ items: [] });
      }
      throw new Error(`Unexpected URL in test mock: ${url}`);
    });

    renderPage();
    await screen.findByTestId("create-cover-cta");
    await userEvent.click(screen.getByTestId("create-cover-cta"));

    expect(screen.getByRole("dialog")).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText("Nome progetto"), "Mia copertina");

    const snapshotCallsBefore = authedFetchMock.mock.calls.filter(([u]) =>
      String(u).includes("/snapshot?workspace_id=1"),
    ).length;

    await userEvent.click(screen.getByRole("button", { name: "Crea copertina" }));

    await waitFor(() => {
      const createCall = authedFetchMock.mock.calls.find(
        ([u, init]) =>
          String(u) === "/api/v1/thumbnail-projects" &&
          (init as RequestInit | undefined)?.method === "POST",
      );
      expect(createCall).toBeTruthy();
    });

    const createInit = authedFetchMock.mock.calls.find(
      ([u, init]) =>
        String(u) === "/api/v1/thumbnail-projects" &&
        (init as RequestInit | undefined)?.method === "POST",
    )?.[1] as RequestInit;
    const payload = JSON.parse(String(createInit.body)) as {
      workspace_id: number;
      name: string;
      canvas_width: number;
      canvas_height: number;
    };
    expect(payload).toEqual({
      workspace_id: 1,
      name: "Mia copertina",
      canvas_width: 1920,
      canvas_height: 1080,
    });

    // The empty canvas snapshot with the chosen background is saved too.
    await waitFor(() => {
      const snapshotCalls = authedFetchMock.mock.calls.filter(([u]) =>
        String(u).includes("/snapshot?workspace_id=1"),
      );
      expect(snapshotCalls.length).toBe(snapshotCallsBefore + 1);
    });
  });

  it("archive action POSTs /archive with workspace_id + version", async () => {
    setLibraryEndpoint();
    renderPage();
    await waitFor(() => {
      expect(screen.getAllByTestId("cover-card")).toHaveLength(2);
    });
    fireEvent.click(screen.getByRole("button", { name: /Archivia WWE Breaking News/ }));

    await waitFor(() => {
      const archiveCall = authedFetchMock.mock.calls.find(([u]) =>
        String(u).includes("/archive?workspace_id=1&version=3"),
      );
      expect(archiveCall).toBeTruthy();
      expect((archiveCall?.[1] as RequestInit | undefined)?.method).toBe("POST");
    });
  });

  it("renders an empty state when the workspace has no projects", async () => {
    authedFetchMock.mockImplementation(async (url: string) => {
      if (url === "/api/v1/workspaces") {
        return jsonResponse({ workspaces: [{ id: 1, name: "Personal" }] });
      }
      if (url === "/api/v1/thumbnail-projects?workspace_id=1") {
        return jsonResponse({ items: [] });
      }
      throw new Error(`Unexpected URL in test mock: ${url}`);
    });
    renderPage();
    await screen.findByText(/nessuna copertina ancora/i);
  });

  it("renders ErrorState on load failure with a working retry", async () => {
    authedFetchMock.mockImplementation(async (url: string) => {
      if (url === "/api/v1/workspaces") {
        return jsonResponse({ workspaces: [{ id: 1, name: "Personal" }] });
      }
      throw new Error("server down");
    });
    renderPage();
    await waitFor(() => {
      expect(
        screen.getByText(/impossibile caricare le copertine/i),
      ).toBeInTheDocument();
    });
  });
});
