/**
 * Shared fixtures, mocks and helpers for the `CoverEditorPage` contract
 * suite (Vitest).
 *
 * The 13 persistence-contract tests are split by scenario across:
 *   • CoverEditor.autosave.test.tsx  — load/edit/autosave/conflict/reorder
 *     (6 tests);
 *   • CoverEditor.exportLink.test.tsx — export flush, save-as-copy,
 *     link-to-video (4 tests);
 *   • CoverEditor.mediaLoad.test.tsx — media picker, server-image reopen,
 *     unresolvable placeholder (3 tests).
 *
 * Every scenario file calls `registerEditorHooks()` and imports the shared
 * fixtures from this module, so the authedFetch mock, endpoint wiring and
 * the data-testid / aria-label contract stay in exactly one place.
 */
import { afterEach, beforeEach, vi } from "vitest";
import { render } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";

import { clearAccountsCache } from "../../features/channels/api/channelsApi";
import { CoverEditorPage } from "./CoverEditor";

// Callback-firing ResizeObserver so the canvas stage lays out (width 800).
// Re-stubbed in `registerEditorHooks`' beforeEach: `vi.unstubAllGlobals()`
// in afterEach restores the setup-file no-op stub after every test, so a
// module-level stubGlobal would only apply to the FIRST test.
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

// The mock classes are created ONCE in vi.hoisted and reused by every
// execution of the factory below. If they were defined inside the factory
// body, vitest would mint a NEW class per factory run, so `instanceof
// ApiError` in thumbnailProjectsApi would never match the error thrown by
// the test mock (the two conflict tests would regress to a generic save
// error instead of the conflict banner).
const authMock = vi.hoisted(() => {
  class AuthError extends Error {
    override name = "AuthError";
  }
  class ApiError extends Error {
    override name = "ApiError";
    readonly status: number;
    readonly data?: unknown;
    constructor(status: number, msg: string, data?: unknown) {
      super(msg);
      this.status = status;
      this.data = data;
    }
  }
  return {
    authedFetchMock: vi.fn(),
    AuthError,
    ApiError,
  };
});

vi.mock("../../lib/auth", () => ({
  authedFetch: authMock.authedFetchMock,
  fetchSession: async () => ({
    userId: 1,
    name: "Demo",
    username: "demo",
    expiresAt: "",
    isAdmin: false,
  }),
  AuthError: authMock.AuthError,
  ApiError: authMock.ApiError,
  readCookie: () => "",
}));

// Export the HOISTED class object directly instead of re-exporting through
// the mocked module: vitest resolves `export { x } from "..."` before the
// mock registry kicks in, so a re-export would hand the tests a DIFFERENT
// ApiError than the one thumbnailProjectsApi's parseProjectVersionConflict
// checks with `instanceof`. The hoisted class IS the class the mock factory
// installs, so `new ApiError(409, …)` thrown by the tests is recognized by
// the autosave hook's conflict detection (same object identity).
export const ApiError = authMock.ApiError;

export const PROJECT = {
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

export const REVISION = {
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

export function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

export const EXPORT = {
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

export function setEditorEndpoints(
  snapshotMock: (...args: unknown[]) => unknown,
) {
  authMock.authedFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
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
            size_bytes: 2048,
            created_at: "2026-08-04T10:00:00Z",
            live_compatibility: "unknown",
          },
        ],
      });
    }
    if (url === "/api/v1/media/00000000-0000-4000-8000-000000000001") {
      return jsonResponse({
        id: "00000000-0000-4000-8000-000000000001",
        filename: "bg.png",
        content_type: "image/png",
        preview_url: "https://cdn.example/bg.png",
        width: 1920,
        height: 1080,
        live_compatibility: "unknown",
      });
    }
    throw new Error(`Unexpected URL in test mock: ${url}`);
  });
}

export function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/app/covers/thumbproj_1"]}>
      <Routes>
        <Route path="/app/covers/:projectId" element={<CoverEditorPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

/** Registers the per-test reset hooks every scenario file needs. */
export function registerEditorHooks() {
  beforeEach(() => {
    authMock.authedFetchMock.mockReset();
    clearAccountsCache();
    vi.stubGlobal("ResizeObserver", ResizeObserverFire);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });
}

// Named alias so scenario files import `authedFetchMock` directly (same
// object identity as the module the vi.mock factory installs).
export const authedFetchMock = authMock.authedFetchMock;
