/**
 * Vitest coverage for `CreateCoverDialog`.
 *
 * Certifies the autonomous creation contract:
 *   • Asks ONLY Nome progetto, Formato, Dimensione, Sfondo iniziale —
 *     never channel/video/OAuth/group/language fields.
 *   • Live preview reflects the chosen format and background.
 *   • Validation gates the submit (empty name, invalid hex, bad dims).
 *   • Submit persists the project immediately (POST) and writes the
 *     initial empty canvas snapshot (PUT) with the chosen background —
 *     "salvataggio immediato del progetto vuoto".
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";

const { authedFetchMock } = vi.hoisted(() => ({
  authedFetchMock: vi.fn(),
}));

vi.mock("../../../lib/auth", () => ({
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

import { CreateCoverDialog } from "./CreateCoverDialog";

const CREATED_PROJECT = {
  id: "thumbproj_created",
  workspace_id: 1,
  created_by: 1,
  name: "WWE Breaking News",
  description: "",
  canvas_width: 1920,
  canvas_height: 1080,
  status: "draft",
  current_revision_id: null,
  preview_media_id: null,
  latest_export_id: null,
  version: 1,
  created_at: "2026-08-04T00:00:00Z",
  updated_at: "2026-08-04T00:00:00Z",
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function renderDialog() {
  return render(
    <MemoryRouter>
      <CreateCoverDialog
        workspaceId={1}
        onCreated={vi.fn()}
        onClose={vi.fn()}
      />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  authedFetchMock.mockReset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("CreateCoverDialog", () => {
  it("asks only name, format, dimensions and background — no YouTube fields", () => {
    renderDialog();
    expect(screen.getByLabelText("Nome progetto")).toBeInTheDocument();
    expect(screen.getByText("Formato")).toBeInTheDocument();
    expect(screen.getByText("Sfondo iniziale")).toBeInTheDocument();
    // No channel/video/OAuth/group/language form surface (the intro
    // line mentions them only to negate them, so assert on fields).
    expect(screen.queryByLabelText(/Canale|Video|Lingua|Gruppo/i)).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    expect(screen.queryByText(/Canale YouTube|Lingua canale|OAuth/i)).not.toBeInTheDocument();
  });

  it("shows a live empty-canvas preview with the default YouTube dimensions", () => {
    renderDialog();
    expect(screen.getByTestId("create-cover-preview")).toHaveTextContent("1920×1080");
  });

  it("updates the live preview when a format preset is selected", async () => {
    renderDialog();
    await userEvent.click(screen.getByRole("button", { name: /Short 9:16/ }));
    expect(screen.getByTestId("create-cover-preview")).toHaveTextContent("1080×1920");
  });

  it("disables submit until the name is provided", async () => {
    renderDialog();
    const submit = screen.getByRole("button", { name: "Crea copertina" });
    expect(submit).toBeDisabled();
    await userEvent.type(screen.getByLabelText("Nome progetto"), "Mia copertina");
    expect(submit).toBeEnabled();
  });

  it("rejects an invalid hex background with an inline error", async () => {
    renderDialog();
    const hex = screen.getByLabelText("Sfondo esadecimale");
    await userEvent.clear(hex);
    await userEvent.type(hex, "rosso");
    expect(screen.getByText(/colore non valido/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Crea copertina" })).toBeDisabled();
  });

  it("POSTs the project and PUTs the initial empty snapshot on submit", async () => {
    authedFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === "/api/v1/thumbnail-projects" && init?.method === "POST") {
        return jsonResponse(CREATED_PROJECT, 201);
      }
      if (url === "/api/v1/thumbnail-projects/thumbproj_created/snapshot?workspace_id=1") {
        return jsonResponse({
          project_id: "thumbproj_created",
          revision_id: "thumbrev_1",
          revision_number: 1,
          version: 2,
          saved_at: "2026-08-04T00:00:00Z",
          snapshot_sha256: "abc",
        });
      }
      throw new Error(`Unexpected URL in test mock: ${url}`);
    });

    renderDialog();
    await userEvent.type(screen.getByLabelText("Nome progetto"), "WWE Breaking News");
    // Switch to the Short preset to prove the snapshot carries it.
    await userEvent.click(screen.getByRole("button", { name: /Short 9:16/ }));
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
    expect(JSON.parse(String(createInit.body))).toEqual({
      workspace_id: 1,
      name: "WWE Breaking News",
      canvas_width: 1080,
      canvas_height: 1920,
    });

    await waitFor(() => {
      const snapshotCall = authedFetchMock.mock.calls.find(([u]) =>
        String(u).includes("/snapshot?workspace_id=1"),
      );
      expect(snapshotCall).toBeTruthy();
    });
    const snapshotInit = authedFetchMock.mock.calls.find(([u]) =>
      String(u).includes("/snapshot?workspace_id=1"),
    )?.[1] as RequestInit;
    expect(JSON.parse(String(snapshotInit.body))).toEqual({
      schema_version: 1,
      snapshot: {
        canvas: { width: 1080, height: 1920, background: "#30305a" },
        objects: [],
      },
      renderer_version: "go-canvas-v1",
      base_version: 1,
    });
  });

  it("seeds custom dimensions from the selected preset", async () => {
    renderDialog();
    // Pick the Short preset, then enable custom — the fields must start
    // from 1080×1920, not the stale default 1920×1080.
    expect(screen.queryByLabelText("Larghezza")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Short 9:16/ }));
    await userEvent.click(
      screen.getByRole("checkbox", { name: /Dimensione personalizzata/i }),
    );
    expect(screen.getByLabelText("Larghezza")).toHaveValue(1080);
    expect(screen.getByLabelText("Altezza")).toHaveValue(1920);
  });

  it("honors custom dimensions when the checkbox is enabled", async () => {
    authedFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === "/api/v1/thumbnail-projects" && init?.method === "POST") {
        return jsonResponse(CREATED_PROJECT, 201);
      }
      if (url.includes("/snapshot?workspace_id=1")) {
        return jsonResponse({ version: 2 });
      }
      throw new Error(`Unexpected URL in test mock: ${url}`);
    });
    renderDialog();
    await userEvent.type(screen.getByLabelText("Nome progetto"), "Custom");
    await userEvent.click(screen.getByRole("checkbox", { name: /Dimensione personalizzata/i }));

    const widthInput = screen.getByLabelText("Larghezza");
    const heightInput = screen.getByLabelText("Altezza");
    await userEvent.clear(widthInput);
    await userEvent.type(widthInput, "1280");
    await userEvent.clear(heightInput);
    await userEvent.type(heightInput, "720");

    expect(screen.getByTestId("create-cover-preview")).toHaveTextContent("1280×720");

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
      canvas_width: number;
      canvas_height: number;
    };
    expect(payload.canvas_width).toBe(1280);
    expect(payload.canvas_height).toBe(720);
  });
});
