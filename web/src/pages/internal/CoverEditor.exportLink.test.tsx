/**
 * CoverEditorPage — export / save-as-copy / link-to-video scenario.
 *
 * Covers the export and cross-object flows:
 *   • "Genera copertina" FLUSHES the pending autosave BEFORE rendering
 *     (mai revisioni stantie presentate come fresche);
 *   • "Salva come copia" creates a NEW autonomous project from the local
 *     snapshot;
 *   • "Collega a un video" walks Gruppo → Canale → Video → Lingua →
 *     Preview → Conferma without writing to the project itself;
 *   • the export panel shows media_id/sha256/status and proves
 *     same-snapshot origin.
 */
import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import {
  ApiError,
  EXPORT,
  PROJECT,
  authedFetchMock,
  jsonResponse,
  registerEditorHooks,
  renderPage,
  setEditorEndpoints,
} from "./CoverEditor.testUtils";

registerEditorHooks();

describe("CoverEditorPage", () => {
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

  it("Collega a un video: Gruppo → Canale → Video → Lingua → Preview → Conferma, senza toccare il progetto", async () => {
    setEditorEndpoints(vi.fn());
    const originalImpl = authedFetchMock.getMockImplementation();

    // Stateful mock: after the assignment POST, the list endpoint returns it.
    let createdAssignment: unknown[] = [];
    // Any write against the PROJECT (snapshot PUT / PATCH / POST lifecycle)
    // would violate "il progetto resta valido con 0 assignment": the link
    // must only insert a thumbnail_assignments row.
    let projectWrites = 0;
    const assignmentsImpl = async (url: string, init?: RequestInit) => {
      if (
        url.startsWith("/api/v1/thumbnail-projects/thumbproj_1") &&
        !url.includes("/media/resolve") &&
        (init?.method === "PUT" || init?.method === "PATCH" || init?.method === "POST")
      ) {
        projectWrites += 1;
      }
      if (url === "/api/v1/groups/aggregate") {
        return jsonResponse({
          groups: [
            { id: 1, workspace_id: 1, name: "WWE", account_ids: [2] },
            { id: 2, workspace_id: 1, name: "Vuoto", account_ids: [] },
          ],
        });
      }
      if (new URL(url, "http://localhost").pathname === "/api/v1/accounts") {
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
            {
              id: 3,
              platform: "youtube",
              platform_user_id: "UCother",
              username: "altro_demo",
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
      if (url === "/api/v1/thumbnail-projects/thumbproj_1/media/resolve?workspace_id=1") {
        // Resolve the rendered export so the dialog can show its preview.
        const body = JSON.parse(String(init?.body ?? "{}")) as { media_ids?: string[] };
        return jsonResponse({
          items: (body.media_ids ?? []).map((id) => ({
            media_id: id,
            url: `https://cdn.example/preview/${id}`,
            content_type: "image/png",
            size_bytes: 2048,
            created_at: "2026-08-04T10:00:00Z",
          })),
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
    // Before any link: the project is valid with 0 assignments.
    expect(
      screen.getByText(/Nessun collegamento — la copertina esiste in modo autonomo/),
    ).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Collega a un video" }));
    expect(
      await screen.findByRole("dialog", { name: "Collega a un video" }),
    ).toBeInTheDocument();

    // Passo 1 — Gruppo: seleziona "WWE" → il canale "altro_demo" (fuori
    // dal gruppo) sparisce dalle opzioni.
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Gruppo" }), "1");
    await screen.findByText("wwe_demo");
    expect(screen.queryByText("altro_demo")).not.toBeInTheDocument();

    // Passo 2 — Canale (filtrato dal gruppo).
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Canale YouTube" }), "2");
    await screen.findByText("Riservata");

    // Passo 3 — Video privato.
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Video privato" }),
      "video_1",
    );

    // Passo 4 — Lingua + Passo 5 — Preview (il file renderizzato dal server).
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Lingua del testo" }), "it");
    const preview = await screen.findByTestId("link-preview");
    expect(preview).toHaveAttribute(
      "src",
      "https://cdn.example/preview/00000000-0000-4000-8000-000000000001",
    );

    // Passo 6 — Conferma: crea l'assignment, non tocca il progetto.
    const snapshotBefore = projectWrites;
    await userEvent.click(screen.getByRole("button", { name: "Conferma collegamento" }));

    await waitFor(
      () => {
        expect(screen.queryByRole("dialog", { name: "Collega a un video" })).not.toBeInTheDocument();
      },
      { timeout: 4000 },
    );
    // The assignments panel refreshes with the new link.
    expect(await screen.findByText("video_1")).toBeInTheDocument();
    // Zero writes against the project itself — only the assignment row.
    expect(projectWrites).toBe(snapshotBefore);
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
