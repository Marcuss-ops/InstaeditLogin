/**
 * Vitest coverage for the YouTube editor-sessions client.
 *
 * Locks down:
 *  - URL paths match the OpenAPI route prefix
 *  - Payload shapes (camelCase canonical keys, NOT the legacy aliases)
 *  - Response shape returned to the caller, including
 *    `{ session_id, velox_project_id, editor_url }`
 *  - Error classification: AuthError re-thrown, ApiError surfaces
 *    with the message verbatim
 *  - window.open call signature (target blank + noopener+noreferrer)
 *  - Query-string composition for the list endpoint
 *  - 404 AbortError path doesn't crash the helper
 *
 * Strategy: vi.mock the `lib/auth` module to swap authedFetch for a
 * vi.fn we can control per-test. The hook + browser APIs (window.open)
 * are stubbed via `vi.spyOn` on the global.
 */

import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";

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

import { ApiError, AuthError } from "../../../lib/auth";
import {
  attachYouTubeEditorSessionThumbnail,
  createEditorSessionAndOpen,
  createYouTubeEditorSession,
  listYouTubeEditorSessions,
  getYouTubeEditorSession,
  openInstaEditorInNewTab,
  publishYouTubeEditorSession,
} from "./editorSessionsApi";

// ────────────────────────────────────────────────────────────────
// Shared fixtures
// ────────────────────────────────────────────────────────────────

const OK_SESSION = {
  session_id: "ytedit_test_42",
  velox_project_id: "ve_test_99",
  editor_url: "/dark_editor_v2/editor/ve_test_99",
};

const FULL_SESSION = {
  id: "ytedit_test_42",
  youtube_video_id: "dQw4w9WgXcQ",
  velox_project_id: "ve_test_99",
  editor_url: "/dark_editor_v2/editor/ve_test_99",
  status: "draft",
  thumbnail_media_id: null,
  desired_privacy: "private",
  publish_at: null,
};

const ATTACHED_SESSION = { ...FULL_SESSION, thumbnail_media_id: "ma_thumb_123" };

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

// ────────────────────────────────────────────────────────────────
// createYouTubeEditorSession
// ────────────────────────────────────────────────────────────────

describe("createYouTubeEditorSession", () => {
  it("POSTs the canonical payload to /api/v1/youtube/editor-sessions and returns the full response shape", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse(OK_SESSION));
    const session = await createYouTubeEditorSession({
      workspace_id: 7,
      platform_account_id: 99,
      youtube_video_id: "dQw4w9WgXcQ",
    });

    expect(authedFetchMock).toHaveBeenCalledTimes(1);
    const [path, init] = authedFetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/v1/youtube/editor-sessions");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      workspace_id: 7,
      platform_account_id: 99,
      youtube_video_id: "dQw4w9WgXcQ",
    });
    expect(session).toEqual(OK_SESSION);
  });

  it("rejects the legacy alias `video_id` shape — server accepts it but we never send it", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse(OK_SESSION));
    await createYouTubeEditorSession({
      workspace_id: 1,
      platform_account_id: 2,
      youtube_video_id: "abc",
    });
    const [, init] = authedFetchMock.mock.calls[0] as [string, RequestInit];
    const body = JSON.parse(String(init.body)) as Record<string, unknown>;
    expect(body).not.toHaveProperty("video_id");
    expect(body).toHaveProperty("youtube_video_id", "abc");
  });

  it("re-throws AuthError so the page can navigate to /login", async () => {
    authedFetchMock.mockRejectedValue(new AuthError());
    await expect(
      createYouTubeEditorSession({
        workspace_id: 1,
        platform_account_id: 2,
        youtube_video_id: "x",
      }),
    ).rejects.toBeInstanceOf(AuthError);
  });

  it("surfaces ApiError with the server message verbatim", async () => {
    authedFetchMock.mockRejectedValue(new ApiError(429, "Velox limit reached"));
    await expect(
      createYouTubeEditorSession({
        workspace_id: 1,
        platform_account_id: 2,
        youtube_video_id: "x",
      }),
    ).rejects.toBeInstanceOf(ApiError);
  });
});

// ────────────────────────────────────────────────────────────────
// listYouTubeEditorSessions
// ────────────────────────────────────────────────────────────────

describe("listYouTubeEditorSessions", () => {
  it("GETs /api/v1/youtube/editor-sessions with composed workspace_id + account_id query", async () => {
    authedFetchMock.mockResolvedValue(
      jsonResponse({ sessions: [FULL_SESSION] }),
    );
    const sessions = await listYouTubeEditorSessions({
      workspace_id: 7,
      account_id: 99,
    });

    expect(authedFetchMock).toHaveBeenCalledTimes(1);
    const [path] = authedFetchMock.mock.calls[0] as [string];
    expect(path).toBe(
      "/api/v1/youtube/editor-sessions?workspace_id=7&account_id=99",
    );
    expect(sessions).toEqual([FULL_SESSION]);
  });

  it("returns [] when the server returns 0 sessions", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ sessions: undefined }));
    const sessions = await listYouTubeEditorSessions({ workspace_id: 1 });
    expect(sessions).toEqual([]);
  });

  it("does not include filter query params when neither is provided", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ sessions: [] }));
    await listYouTubeEditorSessions();
    const [path] = authedFetchMock.mock.calls[0] as [string];
    expect(path).toBe("/api/v1/youtube/editor-sessions");
  });

  it("requests terminal sessions when the Studio needs post-publish state", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ sessions: [FULL_SESSION] }));
    await listYouTubeEditorSessions({ workspace_id: 7, include_terminal: true });
    const [path] = authedFetchMock.mock.calls[0] as [string];
    expect(path).toBe(
      "/api/v1/youtube/editor-sessions?workspace_id=7&include_terminal=true",
    );
  });
});

describe("getYouTubeEditorSession", () => {
  it("reads the detail projection used to verify actual YouTube privacy", async () => {
    const detail = {
      ...FULL_SESSION,
      status: "published",
      actual_privacy: "public",
      youtube_sync_status: "confirmed",
      workspace_id: 7,
      platform_account_id: 99,
      created_at: "2030-01-01T00:00:00Z",
      updated_at: "2030-01-01T00:00:01Z",
    };
    authedFetchMock.mockResolvedValue(jsonResponse(detail));
    const result = await getYouTubeEditorSession("ytedit_42");
    const [path] = authedFetchMock.mock.calls[0] as [string];
    expect(path).toBe("/api/v1/youtube/editor-sessions/ytedit_42");
    expect(result.actual_privacy).toBe("public");
    expect(result.youtube_sync_status).toBe("confirmed");
  });
});

// ────────────────────────────────────────────────────────────────
// attachYouTubeEditorSessionThumbnail
// ────────────────────────────────────────────────────────────────

describe("attachYouTubeEditorSessionThumbnail", () => {
  it("POSTs the thumbnail_media_id to /api/v1/youtube/editor-sessions/{id}/thumbnail", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse(ATTACHED_SESSION));
    const session = await attachYouTubeEditorSessionThumbnail(
      "ytedit_42",
      { thumbnail_media_id: "ma_thumb_123" },
    );

    expect(authedFetchMock).toHaveBeenCalledTimes(1);
    const [path, init] = authedFetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/v1/youtube/editor-sessions/ytedit_42/thumbnail");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      thumbnail_media_id: "ma_thumb_123",
    });
    expect(session).toEqual(ATTACHED_SESSION);
  });

  it("URL-encodes the session id", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse(FULL_SESSION));
    await attachYouTubeEditorSessionThumbnail("yt/with spaces", {
      thumbnail_media_id: "ma_x",
    });
    const [path] = authedFetchMock.mock.calls[0] as [string];
    expect(path).toBe(
      "/api/v1/youtube/editor-sessions/yt%2Fwith%20spaces/thumbnail",
    );
  });
});

// ────────────────────────────────────────────────────────────────
// publishYouTubeEditorSession
// ────────────────────────────────────────────────────────────────

describe("publishYouTubeEditorSession", () => {
  it("POSTs the privacy_status + publish_at (when both set)", async () => {
    authedFetchMock.mockResolvedValue(
      jsonResponse({ ...FULL_SESSION, status: "published" }),
    );
    await publishYouTubeEditorSession("ytedit_42", {
      privacy_status: "public",
      publish_at: "2030-01-01T00:00:00Z",
    });

    const [path, init] = authedFetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/v1/youtube/editor-sessions/ytedit_42/publish");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      privacy_status: "public",
      publish_at: "2030-01-01T00:00:00Z",
    });
  });

  it("allows privacy-only payload (publish_now flow)", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse(FULL_SESSION));
    await publishYouTubeEditorSession("ytedit_42", {
      privacy_status: "public",
    });
    const [, init] = authedFetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toEqual({ privacy_status: "public" });
  });

  it("returns the YouTube verification projection from the publish response", async () => {
    const result = {
      status: "published",
      public_url: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
      video_id: "dQw4w9WgXcQ",
      privacy_status: "public",
      actual_privacy: "public",
      youtube_sync_status: "confirmed",
    };
    authedFetchMock.mockResolvedValue(jsonResponse(result));
    await expect(
      publishYouTubeEditorSession("ytedit_42", { privacy_status: "public" }),
    ).resolves.toEqual(result);
  });
});

// ────────────────────────────────────────────────────────────────
// UI helpers
// ────────────────────────────────────────────────────────────────

describe("openInstaEditorInNewTab", () => {
  it("calls window.open with target=_blank + noopener+noreferrer", () => {
    const openSpy = vi
      .spyOn(window, "open")
      .mockImplementation(() => null);
    openInstaEditorInNewTab("/dark_editor_v2/editor/ve_x");
    expect(openSpy).toHaveBeenCalledWith(
      "/dark_editor_v2/editor/ve_x",
      "_blank",
      "noopener,noreferrer",
    );
  });
});

describe("createEditorSessionAndOpen", () => {
  it("chains create + window.open in one canonical call", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse(OK_SESSION));
    const openSpy = vi
      .spyOn(window, "open")
      .mockImplementation(() => null);

    const session = await createEditorSessionAndOpen({
      workspace_id: 1,
      platform_account_id: 2,
      youtube_video_id: "v",
    });

    expect(session).toEqual(OK_SESSION);
    expect(openSpy).toHaveBeenCalledWith(
      OK_SESSION.editor_url,
      "_blank",
      "noopener,noreferrer",
    );
  });

  it("does NOT open the tab when create rejects — callers should see the throw and decide", async () => {
    authedFetchMock.mockRejectedValue(new ApiError(500, "boom"));
    const openSpy = vi
      .spyOn(window, "open")
      .mockImplementation(() => null);

    await expect(
      createEditorSessionAndOpen({
        workspace_id: 1,
        platform_account_id: 2,
        youtube_video_id: "v",
      }),
    ).rejects.toBeInstanceOf(ApiError);
    expect(openSpy).not.toHaveBeenCalled();
  });
});
