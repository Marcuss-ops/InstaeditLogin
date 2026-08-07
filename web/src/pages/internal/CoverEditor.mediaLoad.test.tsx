/**
 * CoverEditorPage — media picker / server-image load scenario.
 *
 * Covers image-object sourcing:
 *   • "Immagine" inserts an object from the Media Library picker;
 *   • reopening a project whose revision references images loads them from
 *     the SERVER (presigned URL minted by the resolver), never a local
 *     blob;
 *   • when the server cannot resolve a referenced media row (missing,
 *     foreign, not-ready, expired), the editor shows a placeholder instead
 *     of inventing a local source.
 */
import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import {
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

  it("riapre il progetto e carica le immagini dal server (mai blob locali)", async () => {
    // A revision whose snapshot references an image by media_id — as it
    // would exist after a cache clear, a device switch, or a full
    // service restart (API/worker/MinIO/PostgreSQL).
    const imageRevision = {
      ...REVISION,
      id: "thumbrev_img",
      snapshot_json: {
        canvas: { width: 1920, height: 1080, background: "#30305a" },
        objects: [
          {
            id: "img-1",
            type: "image",
            media_id: "00000000-0000-4000-8000-000000000001",
            x: 0,
            y: 0,
            width: 480,
            height: 270,
            scale_x: 1,
            scale_y: 1,
            rotation: 0,
            visible: true,
          },
        ],
      },
    };
    let snapshotPuts = 0;
    authedFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url.endsWith("/snapshot?workspace_id=1") && init?.method === "PUT") {
        snapshotPuts += 1;
        return jsonResponse({
          project_id: "thumbproj_1",
          revision_id: "thumbrev_2",
          revision_number: 2,
          version: 2,
          saved_at: "2026-08-04T10:00:00Z",
          snapshot_sha256: "aabbccdd",
        });
      }
      if (url === "/api/v1/workspaces") {
        return jsonResponse({ workspaces: [{ id: 1, name: "Personal" }] });
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_1?workspace_id=1") {
        return jsonResponse({ ...PROJECT, current_revision_id: "thumbrev_img" });
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_1/revisions/thumbrev_img?workspace_id=1") {
        return jsonResponse({ revision: imageRevision });
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_1/revisions?workspace_id=1") {
        return jsonResponse({ items: [imageRevision] });
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_1/assignments?workspace_id=1") {
        return jsonResponse({ items: [] });
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_1/media/resolve?workspace_id=1") {
        const body = JSON.parse(String(init?.body ?? "{}")) as { media_ids?: string[] };
        return jsonResponse({
          items: (body.media_ids ?? []).map((id) => ({
            media_id: id,
            url: `https://cdn.example/srv/${id}`,
            content_type: "image/png",
            size_bytes: 2048,
            created_at: "2026-08-04T10:00:00Z",
          })),
        });
      }
      throw new Error(`Unexpected URL in test mock: ${url}`);
    });

    renderPage();
    await screen.findByTestId("canvas-surface");
    const imageObject = await screen.findByTestId("canvas-object");
    expect(imageObject).toHaveAttribute("data-object-type", "image");
    // The <img> must point at the SERVER-presigned URL — the resolver
    // mints it from the media row in the workspace, never a local blob.
    const img = imageObject.querySelector("img");
    expect(img?.getAttribute("src")).toBe(
      "https://cdn.example/srv/00000000-0000-4000-8000-000000000001",
    );
    // No spurious snapshot PUT for a freshly loaded project.
    await new Promise((resolve) => setTimeout(resolve, 200));
    expect(snapshotPuts).toBe(0);
  });

  it("mostra un placeholder quando la media non è risolvibile (bloccata dal server)", async () => {
    // The server resolves none of the referenced media (missing, foreign,
    // not-ready, expired) — the editor must NOT invent a local source.
    const imageRevision = {
      ...REVISION,
      id: "thumbrev_missing",
      snapshot_json: {
        canvas: { width: 1920, height: 1080, background: "#30305a" },
        objects: [
          {
            id: "img-missing",
            type: "image",
            media_id: "00000000-0000-4000-8000-0000000009aa",
            x: 0,
            y: 0,
            width: 480,
            height: 270,
            scale_x: 1,
            scale_y: 1,
            rotation: 0,
            visible: true,
          },
        ],
      },
    };
    setEditorEndpoints(vi.fn()); // base endpoints (workspaces, snapshot, …)
    const baseImpl = authedFetchMock.getMockImplementation();
    authedFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === "/api/v1/thumbnail-projects/thumbproj_1?workspace_id=1") {
        return jsonResponse({ ...PROJECT, current_revision_id: "thumbrev_missing" });
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_1/revisions/thumbrev_missing?workspace_id=1") {
        return jsonResponse({ revision: imageRevision });
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_1/revisions?workspace_id=1") {
        return jsonResponse({ items: [imageRevision] });
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_1/media/resolve?workspace_id=1") {
        return jsonResponse({ items: [] }); // server blocks it
      }
      return baseImpl!(url, init);
    });

    renderPage();
    await screen.findByTestId("canvas-surface");
    const imageObject = await screen.findByTestId("canvas-object");
    expect(imageObject).toHaveAttribute("data-object-type", "image");
    expect(imageObject.querySelector("img")).toBeNull();
  });
});
