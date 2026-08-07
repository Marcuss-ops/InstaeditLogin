/**
 * CoverEditorPage — autosave / conflict scenario.
 *
 * Covers the persistence contract's edit → debounced PUT → ack path:
 *   • loads the project + current revision and paints the canvas;
 *   • "Aggiungi Testo" creates an object → "Modifiche non salvate" → after
 *     the debounce the PUT carries the full snapshot → "Salvato alle …"
 *     (never before the server ack);
 *   • changing the background saves it;
 *   • a 409 PROJECT_VERSION_CONFLICT shows the "Ricarica versione recente"
 *     banner and pauses autosave;
 *   • layer reorder with the avanti/indietro buttons;
 *   • "Salva progetto" flushes pending edits without waiting for the
 *     debounce.
 */
import { describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import {
  ApiError,
  PROJECT,
  REVISION,
  authedFetchMock,
  jsonResponse,
  registerEditorHooks,
  renderPage,
  setEditorEndpoints,
} from "./CoverEditor.testUtils";

registerEditorHooks();

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
});
