/**
 * Vitest coverage for `CoverEditorPage` (editor canvas autonomo).
 *
 * Certifies the persistence contract end-to-end in the UI:
 *   • loads the project + current revision and paints the canvas;
 *   • "Aggiungi Testo" creates an object → "Modifiche non salvate" →
 *     after the debounce the PUT carries the full snapshot →
 *     "Salvato alle …" (never before the server ack);
 *   • changing the background saves it;
 *   • a 409 PROJECT_VERSION_CONFLICT shows the "Ricarica versione
 *     recente" banner and pauses autosave;
 *   • layer reorder + media picker insert image objects.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";

// Callback-firing ResizeObserver so the canvas stage lays out (width 800).
// Re-stubbed in `beforeEach`: `vi.unstubAllGlobals()` in `afterEach`
// restores the setup-file no-op stub after every test, so a module-level
// stubGlobal would only apply to the FIRST test.
class ResizeObserverFire {
  private callback: ResizeObserverCallback;
  constructor(callback: ResizeObserverCallback) {
    this.callback = callback;
  }
  observe = () => {
    this.callback(
      [{ contentRect: { width: 800 } } as ResizeObserverEntry],
      this as unknown as ResizeObserver,
    );
  };
  disconnect = vi.fn();
  unobserve = vi.fn();
}

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

// Resolves to the SAME class the mocked module exposes, so `instanceof`
// checks inside the autosave hook (ApiError from thumbnailProjectsApi →
// src/lib/auth) match the error thrown by the fetch mock.
import { ApiError } from "../../lib/auth";
import { CoverEditorPage } from "./CoverEditor";

const PROJECT = {
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
  version: 1,
  created_at: "2026-08-04T00:00:00Z",
  updated_at: "2026-08-04T00:00:00Z",
};

const REVISION = {
  id: "thumbrev_1",
  project_id: "thumbproj_1",
  revision_number: 1,
  schema_version: 1,
  snapshot_json: {
    canvas: { width: 1920, height: 1080, background: "#30305a" },
    objects: [],
  },
  snapshot_sha256: "b64hash",
  renderer_version: "go-canvas-v1",
  created_by: 1,
  created_at: "2026-08-04T00:00:00Z",
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const EXPORT = {
  id: "thumbexp_1",
  project_id: "thumbproj_1",
  revision_id: "thumbrev_1",
  media_id: "00000000-0000-4000-8000-000000000001",
  content_type: "image/png",
  width: 1920,
  height: 1080,
  file_size: 1024,
  sha256: "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefab",
  renderer_version: "go-canvas-v1",
  status: "ready",
  last_error: "",
  created_at: "2026-08-04T10:00:00Z",
};

function setEditorEndpoints(snapshotMock: ReturnType<typeof vi.fn>) {
  authedFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
    if (url === "/api/v1/workspaces") {
      return jsonResponse({ workspaces: [{ id: 1, name: "Personal" }] });
    }
    if (url === "/api/v1/thumbnail-projects/thumbproj_1?workspace_id=1") {
      return jsonResponse(PROJECT);
    }
    if (url === "/api/v1/thumbnail-projects/thumbproj_1/revisions/thumbrev_1?workspace_id=1") {
      return jsonResponse({ revision: REVISION });
    }
    if (url === "/api/v1/thumbnail-projects/thumbproj_1/revisions?workspace_id=1") {
      return jsonResponse({ items: [REVISION] });
    }
    if (url === "/api/v1/thumbnail-projects/thumbproj_1/assignments?workspace_id=1") {
      return jsonResponse({ items: [] });
    }
    if (url === "/api/v1/thumbnail-projects/thumbproj_1/media/resolve?workspace_id=1") {
      return jsonResponse({ items: [] });
    }
    if (url === "/api/v1/thumbnail-projects/thumbproj_1/snapshot?workspace_id=1") {
      snapshotMock(url, init);
      return jsonResponse({
        project_id: "thumbproj_1",
        revision_id: "thumbrev_2",
        revision_number: 2,
        version: 2,
        saved_at: "2026-08-04T10:00:00Z",
        snapshot_sha256: "aabbccdd",
      });
    }
    if (url === "/api/v1/thumbnail-projects/thumbproj_1/render?workspace_id=1") {
      return jsonResponse(EXPORT, 201);
    }
    if (url === "/api/v1/media?limit=100") {
      return jsonResponse({
        items: [
          {
            id: "00000000-0000-4000-8000-000000000001",
            filename: "bg.png",
            content_type: "image/png",
            preview_url: "https://cdn.example/bg.png",
            width: 1920,
            height: 1080,
          },
        ],
      });
    }
    throw new Error(`Unexpected URL in test mock: ${url}`);
  });
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/app/covers/thumbproj_1"]}>
      <Routes>
        <Route path="/app/covers/:projectId" element={<CoverEditorPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  authedFetchMock.mockReset();
  vi.stubGlobal("ResizeObserver", ResizeObserverFire);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("CoverEditorPage", () => {
  it("loads the project and paints the empty canvas", async () => {
    setEditorEndpoints(vi.fn());
    renderPage();
    expect(await screen.findByText("WWE Breaking News")).toBeInTheDocument();
    // The surface appears after the ResizeObserver layout pass; wait for it.
    expect(await screen.findByTestId("canvas-surface")).toBeInTheDocument();
    // Read-only state, not yet touched → no snapshot PUT expected soon.
    expect(screen.getByTestId("save-indicator")).toBeInTheDocument();
  });

  it("adds a text object and autosaves it through the debounced PUT", async () => {
    const snapshotMock = vi.fn();
    setEditorEndpoints(snapshotMock);
    renderPage();
    await screen.findByTestId("canvas-surface");

    await userEvent.click(screen.getByRole("button", { name: "Testo" }));
    expect(screen.getAllByTestId("canvas-object")).toHaveLength(1);

    // Immediately after the edit the indicator is honest: not saved yet.
    expect(screen.getByTestId("save-indicator")).toHaveTextContent(
      /modifiche non salvate/i,
    );

    // After the debounce the PUT fires with the full snapshot…
    await waitFor(
      () => {
        expect(snapshotMock).toHaveBeenCalledTimes(1);
      },
      { timeout: 4000 },
    );
    const [, init] = snapshotMock.mock.calls[0] as [string, RequestInit];
    const payload = JSON.parse(String(init.body)) as {
      schema_version: number;
      renderer_version: string;
      base_version: number;
      snapshot: { objects: Array<{ type: string; text?: string }> };
    };
    expect(payload.schema_version).toBe(1);
    expect(payload.renderer_version).toBe("go-canvas-v1");
    expect(payload.base_version).toBe(1);
    expect(payload.snapshot.objects).toHaveLength(1);
    expect(payload.snapshot.objects[0]).toMatchObject({ type: "text", text: "Testo" });

    // …and only then the indicator reports "Salvato".
    await waitFor(
      () => {
        expect(screen.getByTestId("save-indicator")).toHaveTextContent(/salvato/i);
      },
      { timeout: 4000 },
    );
  });

  it("saves the background change through the autosave", async () => {
    const snapshotMock = vi.fn();
    setEditorEndpoints(snapshotMock);
    renderPage();
    await screen.findByTestId("canvas-surface");

    const backgroundHex = screen.getByLabelText("Sfondo esadecimale");
    await userEvent.clear(backgroundHex);
    await userEvent.type(backgroundHex, "#123456");

    await waitFor(
      () => {
        expect(snapshotMock).toHaveBeenCalledTimes(1);
      },
      { timeout: 4000 },
    );
    const [, init] = snapshotMock.mock.calls[0] as [string, RequestInit];
    const payload = JSON.parse(String(init.body)) as {
      snapshot: { canvas: { background: string } };
    };
    expect(payload.snapshot.canvas.background).toBe("#123456");
  });

  it("shows the conflict banner and pauses on 409 PROJECT_VERSION_CONFLICT", async () => {
    const snapshotMock = vi.fn();
    authedFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === "/api/v1/workspaces") {
        return jsonResponse({ workspaces: [{ id: 1, name: "Personal" }] });
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_1?workspace_id=1") {
        return jsonResponse(PROJECT);
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_1/revisions/thumbrev_1?workspace_id=1") {
        return jsonResponse({ revision: REVISION });
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_1/revisions?workspace_id=1") {
        return jsonResponse({ items: [REVISION] });
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_1/assignments?workspace_id=1") {
        return jsonResponse({ items: [] });
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_1/media/resolve?workspace_id=1") {
        return jsonResponse({ items: [] });
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_1/snapshot?workspace_id=1") {
        snapshotMock(url, init);
        // Real authedFetch throws ApiError on non-2xx; the autosave hook
        // detects the conflict from that throw.
        throw new ApiError(
          409,
          "expected=1 current=9",
          { code: "PROJECT_VERSION_CONFLICT", current_version: 9 },
        );
      }
      throw new Error(`Unexpected URL in test mock: ${url}`);
    });

    renderPage();
    await screen.findByTestId("canvas-surface");
    await userEvent.click(screen.getByRole("button", { name: "Testo" }));

    await waitFor(
      () => {
        expect(screen.getByTestId("conflict-banner")).toBeInTheDocument();
      },
      { timeout: 4000 },
    );
    expect(screen.getByTestId("conflict-banner")).toHaveTextContent(
      /versione attuale 9/i,
    );
    expect(screen.getByRole("button", { name: /Ricarica versione recente/i })).toBeInTheDocument();
    // Paused: further edits must not fire more PUTs.
    await userEvent.click(screen.getByRole("button", { name: "Rettangolo" }));
    await new Promise((resolve) => setTimeout(resolve, 200));
    expect(snapshotMock).toHaveBeenCalledTimes(1);
  });

  it("reorders layers with the avanti/indietro buttons", async () => {
    setEditorEndpoints(vi.fn());
    renderPage();
    await screen.findByTestId("canvas-surface");
    await userEvent.click(screen.getByRole("button", { name: "Testo" }));
    await userEvent.click(screen.getByRole("button", { name: "Rettangolo" }));
    expect(screen.getAllByTestId("layer-row")).toHaveLength(2);
    // Top row = rect (added last). Send it back.
    const rows = screen.getAllByTestId("layer-row");
    expect(rows[0]!.textContent).toContain("Rettangolo");
    // The top row is the rect; send it back one layer. Scope the button
    // to that row (every row has its own Porta avanti/indietro pair).
    fireEvent.click(
      within(rows[0]!).getByRole("button", { name: "Porta indietro" }),
    );
    const rowsAfter = screen.getAllByTestId("layer-row");
    expect(rowsAfter[0]!.textContent).toContain("Testo");
    expect(rowsAfter[1]!.textContent).toContain("Rettangolo");
  });

  it("inserts an image object from the Media Library picker", async () => {
    const snapshotMock = vi.fn();
    setEditorEndpoints(snapshotMock);
    renderPage();
    await screen.findByTestId("canvas-surface");

    await userEvent.click(screen.getByRole("button", { name: "Immagine" }));
    expect(screen.getByRole("dialog", { name: "Media Library" })).toBeInTheDocument();
    await screen.findByText("bg.png");
    await userEvent.click(screen.getByRole("button", { name: /bg\.png/ }));

    await waitFor(
      () => {
        expect(screen.getAllByTestId("canvas-object").length).toBe(1);
      },
      { timeout: 4000 },
    );
    const imgObject = screen.getByTestId("canvas-object");
    expect(imgObject).toHaveAttribute("data-object-type", "image");
  });

  it("Genera copertina FLUSHES the pending autosave BEFORE rendering (mai revisioni stantie)", async () => {
    const callOrder: string[] = [];
    const snapshotMock = vi.fn((url: string, init?: RequestInit) => {
      callOrder.push("snapshot");
      void url;
      void init;
    });
    setEditorEndpoints(snapshotMock);
    // Track the render call too (comes through the same authedFetch mock).
    const originalImpl = authedFetchMock.getMockImplementation();
    authedFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === "/api/v1/thumbnail-projects/thumbproj_1/render?workspace_id=1") {
        callOrder.push("render");
        return jsonResponse(EXPORT, 201);
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_1/snapshot?workspace_id=1") {
        callOrder.push("snapshot");
        snapshotMock(url, init);
        return jsonResponse({
          project_id: "thumbproj_1",
          revision_id: "thumbrev_2",
          revision_number: 2,
          version: 2,
          saved_at: "2026-08-04T10:00:00Z",
          snapshot_sha256: "aabbccdd",
        });
      }
      return originalImpl!(url, init);
    });

    renderPage();
    await screen.findByTestId("canvas-surface");

    // Make an edit so there is a PENDING autosave, then export immediately.
    await userEvent.click(screen.getByRole("button", { name: "Testo" }));
    await userEvent.click(screen.getByRole("button", { name: "Genera copertina" }));

    await waitFor(
      () => {
        expect(callOrder.filter((c) => c === "render").length).toBe(1);
      },
      { timeout: 4000 },
    );
    // The flush must have persisted the edit BEFORE the render request.
    const snapshotIdx = callOrder.lastIndexOf("snapshot");
    const renderIdx = callOrder.indexOf("render");
    expect(snapshotIdx).toBeGreaterThan(-1);
    expect(snapshotIdx).toBeLessThan(renderIdx);

    // Export result panel appears with the export id.
    expect(await screen.findByText("thumbexp_1")).toBeInTheDocument();
    expect(screen.getByText("Scarica PNG")).toBeInTheDocument();
    // The flush advanced latestRevisionId to thumbrev_2 while this mock
    // export is pinned to thumbrev_1 → the panel HONESTLY flags the
    // mismatch (mai revisioni stantie presentate come fresche).
    expect(screen.getByTestId("export-origin-check")).toHaveTextContent(
      /Export da revisione stantia/,
    );
  });

  it("Salva come copia creates a NEW autonomous project with the local snapshot", async () => {
    setEditorEndpoints(vi.fn());
    const copyProject = {
      ...PROJECT,
      id: "thumbproj_copia",
      name: "WWE Breaking News (copia)",
      version: 1,
    };
    const baseImpl = authedFetchMock.getMockImplementation();
    const posted: Array<{ url: string; body?: unknown }> = [];
    // Layer 1: intercept the copy-project create + its snapshot.
    const copyImpl = async (url: string, init?: RequestInit) => {
      if (url === "/api/v1/thumbnail-projects" && init?.method === "POST") {
        posted.push({ url, body: init.body });
        return jsonResponse(copyProject, 201);
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_copia/snapshot?workspace_id=1") {
        posted.push({ url, body: init?.body });
        return jsonResponse({
          project_id: "thumbproj_copia",
          revision_id: "thumbrev_copia_1",
          revision_number: 1,
          version: 2,
          saved_at: "2026-08-04T10:00:00Z",
          snapshot_sha256: "copia",
        });
      }
      return baseImpl!(url, init);
    };
    authedFetchMock.mockImplementation(copyImpl);

    renderPage();
    await screen.findByTestId("canvas-surface");

    // Layer 2: make the ORIGINAL project's snapshot save return 409.
    authedFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === "/api/v1/thumbnail-projects/thumbproj_1/snapshot?workspace_id=1") {
        throw new ApiError(
          409,
          "expected=1 current=9",
          { code: "PROJECT_VERSION_CONFLICT", current_version: 9 },
        );
      }
      return copyImpl(url, init);
    });

    await userEvent.click(screen.getByRole("button", { name: "Testo" }));
    await waitFor(
      () => {
        expect(screen.getByTestId("conflict-banner")).toBeInTheDocument();
      },
      { timeout: 4000 },
    );

    await userEvent.click(screen.getByRole("button", { name: "Salva come copia" }));

    await waitFor(
      () => {
        expect(posted.length).toBe(2);
      },
      { timeout: 4000 },
    );
    const createBody = JSON.parse(String(posted[0]!.body)) as {
      workspace_id: number;
      name: string;
      canvas_width: number;
      canvas_height: number;
    };
    expect(createBody.workspace_id).toBe(1);
    expect(createBody.name).toBe("WWE Breaking News (copia)");
    expect(createBody.canvas_width).toBe(1920);
    const snapshotBody = JSON.parse(String(posted[1]!.body)) as {
      base_version: number;
      snapshot: { objects: unknown[] };
    };
    expect(snapshotBody.base_version).toBe(1);
    expect(snapshotBody.snapshot.objects).toHaveLength(1); // the Testo object
  });

  it("Collega a un video opens the assignment dialog from a ready export", async () => {
    setEditorEndpoints(vi.fn());
    const originalImpl = authedFetchMock.getMockImplementation();

    // Stateful mock: after the assignment POST, the list endpoint returns it.
    let createdAssignment: unknown[] = [];
    const assignmentsImpl = async (url: string, init?: RequestInit) => {
      if (url === "/api/v1/accounts") {
        return jsonResponse({
          accounts: [
            {
              id: 2,
              platform: "youtube",
              platform_user_id: "UCdemo",
              username: "wwe_demo",
              status: "connected",
              is_publishable: true,
              created_at: "2026-08-01T00:00:00Z",
            },
          ],
        });
      }
      if (url === "/api/v1/accounts/2/content?limit=50&privacy=private") {
        return jsonResponse({
          items: [{ external_id: "video_1", title: "Riservata", privacy: "private" }],
        });
      }
      if (url === "/api/v1/thumbnail-exports/thumbexp_1/assignments?workspace_id=1") {
        createdAssignment = [
          {
            id: "thumbass_1",
            workspace_id: 1,
            project_id: "thumbproj_1",
            export_id: "thumbexp_1",
            platform_account_id: 2,
            platform: "youtube",
            youtube_video_id: "video_1",
            target_language: null,
            status: "draft",
            created_at: "2026-08-04T10:00:00Z",
            updated_at: "2026-08-04T10:00:00Z",
          },
        ];
        return jsonResponse({ items: createdAssignment }, 201);
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_1/assignments?workspace_id=1") {
        return jsonResponse({ items: createdAssignment });
      }
      return originalImpl!(url, init);
    };
    authedFetchMock.mockImplementation(assignmentsImpl);

    renderPage();
    await screen.findByTestId("canvas-surface");

    // Generate an export first (the dialog requires a ready export).
    await userEvent.click(screen.getByRole("button", { name: "Genera copertina" }));
    await screen.findByText("thumbexp_1");
    await userEvent.click(screen.getByRole("button", { name: "Collega a un video" }));

    expect(
      await screen.findByRole("dialog", { name: "Collega a un video" }),
    ).toBeInTheDocument();
    await screen.findByText("wwe_demo");
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Canale YouTube" }), "2");
    await screen.findByText("Riservata");
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Video privato" }),
      "video_1",
    );
    await userEvent.click(screen.getByRole("button", { name: "Conferma collegamento" }));

    await waitFor(
      () => {
        expect(screen.queryByRole("dialog", { name: "Collega a un video" })).not.toBeInTheDocument();
      },
      { timeout: 4000 },
    );
    // The assignments panel refreshes with the new link.
    expect(await screen.findByText("video_1")).toBeInTheDocument();
  });

  it("Salva progetto flushes pending edits immediately (senza attendere il debounce)", async () => {
    const snapshotMock = vi.fn();
    setEditorEndpoints(snapshotMock);
    renderPage();
    await screen.findByTestId("canvas-surface");

    await userEvent.click(screen.getByRole("button", { name: "Testo" }));
    // Click "Salva progetto" BEFORE the 1.5s debounce would fire.
    await userEvent.click(screen.getByRole("button", { name: "Salva progetto" }));

    await waitFor(
      () => {
        expect(snapshotMock).toHaveBeenCalledTimes(1);
      },
      { timeout: 4000 },
    );
    const [, init] = snapshotMock.mock.calls[0] as [string, RequestInit];
    const payload = JSON.parse(String(init.body)) as {
      snapshot: { objects: unknown[] };
    };
    expect(payload.snapshot.objects).toHaveLength(1);
    // The indicator reports "Salvato" only after the server ack.
    await waitFor(
      () => {
        expect(screen.getByTestId("save-indicator")).toHaveTextContent(/salvato/i);
      },
      { timeout: 4000 },
    );
  });

  it("export shows media_id/sha256/status and proves same-snapshot origin", async () => {
    setEditorEndpoints(vi.fn());
    renderPage();
    await screen.findByTestId("canvas-surface");

    // No edits: flush has nothing to save, so the latest persisted
    // revision stays thumbrev_1 — matching the mock export's revision.
    await userEvent.click(screen.getByRole("button", { name: "Genera copertina" }));

    expect(await screen.findByTestId("export-id")).toHaveTextContent("thumbexp_1");
    // media_id (truncated in view, full value in the title tooltip).
    const mediaSpan = screen.getByTitle(/^media /);
    expect(mediaSpan.title).toContain("00000000-0000-4000-8000");
    // sha256 truncated in view, full hex value in the title tooltip.
    const shaSpan = screen.getByTitle(/^abcdef/);
    expect(shaSpan.title.length).toBeGreaterThan(40);
    // Status badge is explicit.
    expect(screen.getByTestId("export-status")).toHaveTextContent("pronto");
    // Same-origin proof: export revision == latest revision, dims == canvas.
    expect(screen.getByTestId("export-origin-check")).toHaveTextContent(
      /Stessa revisione dell'ultimo snapshot/,
    );
    expect(screen.getByTestId("export-origin-check")).toHaveTextContent(
      /1920×1080 identiche al canvas/,
    );
    expect(screen.getByText("Scarica PNG")).toBeInTheDocument();
  });
});
