/**
 * Vitest coverage for the Thumbnail Projects API client.
 *
 * Locks down:
 *   - URL paths + mandatory `workspace_id` query composition
 *   - Payload shapes (snapshot with base_version, restore, render, …)
 *   - If-Match "version-N" header when an explicit version is supplied
 *   - 204 handling for archive/delete
 *   - 409 PROJECT_VERSION_CONFLICT parsing (code + current_version)
 *
 * Strategy mirrors editorSessionsApi.test.ts: `vi.mock` the `lib/auth`
 * module and control `authedFetch` per-test via a `vi.hoisted` fn.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { authedFetchMock } = vi.hoisted(() => ({
  authedFetchMock: vi.fn(),
}));

vi.mock("../../../lib/auth", async (orig) => {
  const actual = await orig();
  return {
    ...actual,
    authedFetch: (...args: unknown[]) => authedFetchMock(...args),
  };
});

import { ApiError } from "../../../lib/auth";
import {
  archiveThumbnailProject,
  createThumbnailAssignments,
  createThumbnailProject,
  deleteThumbnailProject,
  getThumbnailExport,
  getThumbnailProject,
  getThumbnailRevision,
  listThumbnailAssignments,
  listThumbnailProjects,
  listThumbnailRevisions,
  parseProjectVersionConflict,
  renderThumbnailProject,
  resolveThumbnailProjectMedia,
  restoreThumbnailRevision,
  saveThumbnailSnapshot,
  toProjectVersionConflictError,
  updateThumbnailProject,
  ProjectVersionConflictError,
} from "./thumbnailProjectsApi";

const PROJECT: Record<string, unknown> = {
  id: "thumbproj_1",
  workspace_id: 7,
  created_by: 1,
  name: "Cover",
  description: "",
  canvas_width: 1920,
  canvas_height: 1080,
  status: "draft",
  version: 1,
  created_at: "2026-08-03T00:00:00Z",
  updated_at: "2026-08-03T00:00:00Z",
};

const REVISION: Record<string, unknown> = {
  id: "thumbrev_1",
  project_id: "thumbproj_1",
  revision_number: 1,
  schema_version: 1,
  snapshot_json: { canvas: { background: "#30305a" }, objects: [] },
  snapshot_sha256: "b64hash",
  renderer_version: "go-canvas-v1",
  created_by: 1,
  created_at: "2026-08-03T00:00:00Z",
};

const EXPORT: Record<string, unknown> = {
  id: "thumbexp_1",
  project_id: "thumbproj_1",
  revision_id: "thumbrev_1",
  media_id: "00000000-0000-4000-8000-000000000001",
  content_type: "image/png",
  width: 1920,
  height: 1080,
  file_size: 512,
  sha256: "b64sha",
  renderer_version: "go-canvas-v1",
  status: "ready",
  last_error: "",
  created_at: "2026-08-03T00:00:00Z",
};

const ASSIGNMENT: Record<string, unknown> = {
  id: "thumbassign_1",
  workspace_id: 7,
  project_id: "thumbproj_1",
  export_id: "thumbexp_1",
  platform_account_id: 381,
  platform: "youtube",
  youtube_video_id: "abc123",
  status: "draft",
  created_at: "2026-08-03T00:00:00Z",
  updated_at: "2026-08-03T00:00:00Z",
};

function jsonResponse(
  body: unknown,
  init: { status?: number } = {},
): Response {
  return new Response(JSON.stringify(body), {
    status: init.status ?? 200,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  authedFetchMock.mockReset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

// ─── Projects ─────────────────────────────────────────────────────

describe("createThumbnailProject", () => {
  it("POSTs the canonical payload to /api/v1/thumbnail-projects", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse(PROJECT));
    const project = await createThumbnailProject({
      workspace_id: 7,
      name: "Cover",
      canvas_width: 1920,
      canvas_height: 1080,
    });
    const [path, init] = authedFetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/v1/thumbnail-projects");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      workspace_id: 7,
      name: "Cover",
      canvas_width: 1920,
      canvas_height: 1080,
    });
    expect(project).toEqual(PROJECT);
  });
});

describe("listThumbnailProjects", () => {
  it("GETs with the mandatory workspace_id query and returns items", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ items: [PROJECT] }));
    const projects = await listThumbnailProjects(7);
    const [path] = authedFetchMock.mock.calls[0] as [string];
    expect(path).toBe("/api/v1/thumbnail-projects?workspace_id=7");
    expect(projects).toEqual([PROJECT]);
  });

  it("returns [] when the server omits items", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({}));
    await expect(listThumbnailProjects(1)).resolves.toEqual([]);
  });
});

describe("getThumbnailProject", () => {
  it("URL-encodes the project id and appends workspace_id", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse(PROJECT));
    await getThumbnailProject(7, "thumbproj/1");
    const [path] = authedFetchMock.mock.calls[0] as [string];
    expect(path).toBe("/api/v1/thumbnail-projects/thumbproj%2F1?workspace_id=7");
  });
});

describe("updateThumbnailProject", () => {
  it("PATCHes with the version token in the body", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ ...PROJECT, name: "New" }));
    await updateThumbnailProject(7, "thumbproj_1", { name: "New", version: 1 });
    const [path, init] = authedFetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/v1/thumbnail-projects/thumbproj_1?workspace_id=7");
    expect(init.method).toBe("PATCH");
    expect(JSON.parse(String(init.body))).toEqual({ name: "New", version: 1 });
  });
});

describe("archiveThumbnailProject / deleteThumbnailProject", () => {
  it("archive POSTs /archive with workspace_id + version and resolves on 204", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse(undefined, { status: 204 }));
    await archiveThumbnailProject(7, "thumbproj_1", 3);
    const [path, init] = authedFetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe(
      "/api/v1/thumbnail-projects/thumbproj_1/archive?workspace_id=7&version=3",
    );
    expect(init.method).toBe("POST");
  });

  it("delete DELETEs with workspace_id + version", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse(undefined, { status: 204 }));
    await deleteThumbnailProject(7, "thumbproj_1", 3);
    const [path, init] = authedFetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe(
      "/api/v1/thumbnail-projects/thumbproj_1?workspace_id=7&version=3",
    );
    expect(init.method).toBe("DELETE");
  });
});

// ─── Snapshot + revisions ─────────────────────────────────────────

describe("saveThumbnailSnapshot", () => {
  it("PUTs the snapshot with schema_version, renderer_version and base_version", async () => {
    authedFetchMock.mockResolvedValue(
      jsonResponse({
        project_id: "thumbproj_1",
        revision_id: "thumbrev_2",
        revision_number: 2,
        version: 2,
        saved_at: "2026-08-03T00:00:00Z",
        snapshot_sha256: "abc",
      }),
    );
    const result = await saveThumbnailSnapshot(7, "thumbproj_1", {
      schema_version: 1,
      snapshot: { canvas: { background: "#30305a" }, objects: [] },
      renderer_version: "go-canvas-v1",
      base_version: 1,
    });
    const [path, init] = authedFetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/v1/thumbnail-projects/thumbproj_1/snapshot?workspace_id=7");
    expect(init.method).toBe("PUT");
    expect(JSON.parse(String(init.body))).toEqual({
      schema_version: 1,
      snapshot: { canvas: { background: "#30305a" }, objects: [] },
      renderer_version: "go-canvas-v1",
      base_version: 1,
    });
    expect(result.version).toBe(2);
  });

  it("sends If-Match \"version-N\" when ifMatchVersion is supplied", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ version: 8 }));
    await saveThumbnailSnapshot(
      7,
      "thumbproj_1",
      {
        schema_version: 1,
        snapshot: { objects: [] },
        renderer_version: "r1",
        base_version: 7,
      },
      { ifMatchVersion: 7 },
    );
    const [, init] = authedFetchMock.mock.calls[0] as [string, RequestInit];
    const headers = new Headers(init.headers);
    expect(headers.get("If-Match")).toBe('"version-7"');
  });
});

describe("listThumbnailRevisions", () => {
  it("GETs /revisions with workspace_id and returns items", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ items: [REVISION] }));
    const revisions = await listThumbnailRevisions(7, "thumbproj_1");
    const [path] = authedFetchMock.mock.calls[0] as [string];
    expect(path).toBe("/api/v1/thumbnail-projects/thumbproj_1/revisions?workspace_id=7");
    expect(revisions).toEqual([REVISION]);
  });
});

describe("getThumbnailRevision", () => {
  it("reads { revision } from the detail endpoint", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ revision: REVISION }));
    const revision = await getThumbnailRevision(7, "thumbproj_1", "thumbrev_1");
    const [path] = authedFetchMock.mock.calls[0] as [string];
    expect(path).toBe(
      "/api/v1/thumbnail-projects/thumbproj_1/revisions/thumbrev_1?workspace_id=7",
    );
    expect(revision).toEqual(REVISION);
  });
});

describe("restoreThumbnailRevision", () => {
  it("POSTs base_version to /restore/{revision_id}", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ version: 4 }));
    await restoreThumbnailRevision(7, "thumbproj_1", "thumbrev_old", {
      base_version: 3,
      renderer_version: "go-canvas-v1",
    });
    const [path, init] = authedFetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe(
      "/api/v1/thumbnail-projects/thumbproj_1/restore/thumbrev_old?workspace_id=7",
    );
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      base_version: 3,
      renderer_version: "go-canvas-v1",
    });
  });
});

// ─── Render + exports ─────────────────────────────────────────────

describe("renderThumbnailProject", () => {
  it("POSTs the render request and returns the created export", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse(EXPORT));
    const export_ = await renderThumbnailProject(7, "thumbproj_1", {
      content_type: "image/png",
    });
    const [path, init] = authedFetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/v1/thumbnail-projects/thumbproj_1/render?workspace_id=7");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({ content_type: "image/png" });
    expect(export_).toEqual(EXPORT);
  });

  it("defaults to an empty body so the server derives the revision", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse(EXPORT));
    await renderThumbnailProject(7, "thumbproj_1");
    const [, init] = authedFetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toEqual({});
  });
});

describe("getThumbnailExport", () => {
  it("GETs /api/v1/thumbnail-exports/{export_id} with workspace_id", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse(EXPORT));
    await getThumbnailExport(7, "thumbexp_1");
    const [path] = authedFetchMock.mock.calls[0] as [string];
    expect(path).toBe("/api/v1/thumbnail-exports/thumbexp_1?workspace_id=7");
  });
});

// ─── Assignments ──────────────────────────────────────────────────

describe("createThumbnailAssignments", () => {
  it("POSTs targets to /api/v1/thumbnail-exports/{export_id}/assignments with workspace_id", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ items: [ASSIGNMENT] }));
    const assignments = await createThumbnailAssignments(7, "thumbexp_1", {
      targets: [{ platform_account_id: 381, youtube_video_id: "abc123" }],
    });
    const [path, init] = authedFetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe(
      "/api/v1/thumbnail-exports/thumbexp_1/assignments?workspace_id=7",
    );
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      targets: [{ platform_account_id: 381, youtube_video_id: "abc123" }],
    });
    expect(assignments).toEqual([ASSIGNMENT]);
  });

  it("accepts a bare array response as well", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse([ASSIGNMENT]));
    const assignments = await createThumbnailAssignments(7, "thumbexp_1", {
      targets: [{ platform_account_id: 1, youtube_video_id: "v" }],
    });
    expect(assignments).toEqual([ASSIGNMENT]);
  });
});

describe("listThumbnailAssignments", () => {
  it("GETs /api/v1/thumbnail-projects/{id}/assignments with workspace_id", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ items: [ASSIGNMENT] }));
    const assignments = await listThumbnailAssignments(7, "thumbproj_1");
    const [path] = authedFetchMock.mock.calls[0] as [string];
    expect(path).toBe(
      "/api/v1/thumbnail-projects/thumbproj_1/assignments?workspace_id=7",
    );
    expect(assignments).toEqual([ASSIGNMENT]);
  });

  it("returns [] when the server omits items (unlinked project)", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({}));
    await expect(listThumbnailAssignments(1, "thumbproj_1")).resolves.toEqual([]);
  });
});

// ─── Media resolver ───────────────────────────────────────────────

describe("resolveThumbnailProjectMedia", () => {
  it("POSTs media_ids to /media/resolve with workspace_id and returns items", async () => {
    authedFetchMock.mockResolvedValue(
      jsonResponse({
        items: [
          {
            media_id: "00000000-0000-4000-8000-000000000001",
            url: "https://cdn.example/x?X-Amz-Signature=abc",
            content_type: "image/jpeg",
            size_bytes: 2048,
            created_at: "2026-08-03T00:00:00Z",
          },
        ],
      }),
    );
    const items = await resolveThumbnailProjectMedia(7, "thumbproj_1", [
      "00000000-0000-4000-8000-000000000001",
    ]);
    const [path, init] = authedFetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe(
      "/api/v1/thumbnail-projects/thumbproj_1/media/resolve?workspace_id=7",
    );
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      media_ids: ["00000000-0000-4000-8000-000000000001"],
    });
    expect(items).toHaveLength(1);
    expect(items[0].url).toContain("X-Amz-Signature");
  });

  it("returns [] when the server omits items", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({}));
    const items = await resolveThumbnailProjectMedia(7, "thumbproj_1", [
      "00000000-0000-4000-8000-000000000001",
    ]);
    expect(items).toEqual([]);
  });
});

// ─── 409 PROJECT_VERSION_CONFLICT ─────────────────────────────────

describe("parseProjectVersionConflict", () => {
  it("extracts code + current_version from a 409 ApiError with data", () => {
    const conflict = parseProjectVersionConflict(
      new ApiError(409, "version conflict", {
        code: "PROJECT_VERSION_CONFLICT",
        current_version: 9,
        error: "expected=8 current=9",
      }),
    );
    expect(conflict).toEqual({ code: "PROJECT_VERSION_CONFLICT", current_version: 9 });
  });

  it("still detects the conflict when the server omits current_version", () => {
    const conflict = parseProjectVersionConflict(
      new ApiError(409, "conflict", { code: "PROJECT_VERSION_CONFLICT" }),
    );
    expect(conflict).toEqual({ code: "PROJECT_VERSION_CONFLICT" });
    expect(conflict?.current_version).toBeUndefined();
  });

  it("returns null for a 409 without structured data", () => {
    expect(parseProjectVersionConflict(new ApiError(409, "conflict"))).toBeNull();
  });

  it("returns null for non-409 errors and non-ApiError throws", () => {
    expect(parseProjectVersionConflict(new ApiError(500, "boom"))).toBeNull();
    expect(parseProjectVersionConflict(new Error("boom"))).toBeNull();
    expect(parseProjectVersionConflict(null)).toBeNull();
  });
});

describe("toProjectVersionConflictError", () => {
  it("wraps a conflicting 409 into ProjectVersionConflictError", () => {
    const wrapped = toProjectVersionConflictError(
      new ApiError(409, "conflict", {
        code: "PROJECT_VERSION_CONFLICT",
        current_version: 9,
      }),
    );
    expect(wrapped).toBeInstanceOf(ProjectVersionConflictError);
    expect(wrapped?.currentVersion).toBe(9);
  });

  it("produces a generic conflict error when current_version is missing", () => {
    const wrapped = toProjectVersionConflictError(
      new ApiError(409, "conflict", { code: "PROJECT_VERSION_CONFLICT" }),
    );
    expect(wrapped).toBeInstanceOf(ProjectVersionConflictError);
    expect(wrapped?.currentVersion).toBeUndefined();
  });

  it("returns null for unrelated errors", () => {
    expect(toProjectVersionConflictError(new ApiError(503, "down"))).toBeNull();
  });
});
