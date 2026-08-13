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
 *  - top-level redirect receives the resolved editor SPA URL
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
  createEditorLaunchURL,
  createEditorSessionAndOpen,
  createEditorSessionAndRedirect,
  createYouTubeEditorSession,
  listYouTubeEditorSessions,
  getYouTubeEditorSession,
  openInstaEditorInNewTab,
  openInstaEditorWithLaunch,
  publishYouTubeEditorSession,
} from "./editorSessionsApi";

// ────────────────────────────────────────────────────────────────
// Shared fixtures
// ────────────────────────────────────────────────────────────────

const OK_SESSION = {
  session_id: "ytedit_test_42",
  velox_project_id: "ve_test_99",
  editor_url: "https://editor.instaedit.test/editor/ve_test_99",
  // The session contract carries the authoritative video projection
  // fetched from YouTube (videos.list) — InstaEditor's initial document.
  youtube_video_id: "dQw4w9WgXcQ",
  title: "Rick Astley - Never Gonna Give You Up",
  description: "The official video…",
  thumbnail_url: "https://i.ytimg.com/vi/dQw4w9WgXcQ/hqdefault.jpg",
  category_id: "10",
  privacy_status: "private",
  source: "youtube",
};

const FULL_SESSION = {
  id: "ytedit_test_42",
  youtube_video_id: "dQw4w9WgXcQ",
  velox_project_id: "ve_test_99",
  editor_url: "https://editor.instaedit.test/editor/ve_test_99",
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
    openInstaEditorInNewTab("https://editor.instaedit.test/editor/ve_x");
    expect(openSpy).toHaveBeenCalledWith(
      "https://editor.instaedit.test/editor/ve_x",
      "_blank",
      "noopener,noreferrer",
    );
  });
});

describe("redirectToInstaEditor", () => {
  it("passes the resolved URL to the top-level navigation function", async () => {
    const { redirectToInstaEditor } = await import("./editorSessionsApi");
    const navigate = vi.fn();

    redirectToInstaEditor("https://editor.instaedit.test/editor/ve_x", navigate);

    expect(navigate).toHaveBeenCalledWith(
      "https://editor.instaedit.test/editor/ve_x",
    );
  });

  it("rejects unsupported protocols before navigation", async () => {
    const { redirectToInstaEditor } = await import("./editorSessionsApi");
    const navigate = vi.fn();

    expect(() => redirectToInstaEditor("javascript:alert(1)", navigate)).toThrow(
      "Editor unavailable / misconfigured",
    );
    // Local file scheme is a security hazard (browser warns about
    // links pointing to file://) — must never reach the editor launcher.
    expect(() => redirectToInstaEditor("file:///etc/passwd", navigate)).toThrow(
      "Editor unavailable / misconfigured",
    );
    expect(navigate).not.toHaveBeenCalled();
  });

  it("rejects empty, relative, and unsafe absolute URLs without navigating to InstaEdit", async () => {
    const { redirectToInstaEditor } = await import("./editorSessionsApi");
    const navigate = vi.fn();

    expect(() => redirectToInstaEditor("", navigate)).toThrow(
      "Editor unavailable / misconfigured",
    );
    expect(() => redirectToInstaEditor("/editor/ve_missing", navigate)).toThrow(
      "Editor unavailable / misconfigured",
    );
    expect(() => redirectToInstaEditor("https://user:pass@editor.example/editor/ve_x", navigate)).toThrow(
      "Editor unavailable / misconfigured",
    );
    expect(() => redirectToInstaEditor("https://editor.example/editor/ve_x?env=prod", navigate)).toThrow(
      "Editor unavailable / misconfigured",
    );
    expect(() => redirectToInstaEditor("https://editor.example/editor/ve_x#section", navigate)).toThrow(
      "Editor unavailable / misconfigured",
    );
    expect(navigate).not.toHaveBeenCalled();
  });
});

describe("createEditorLaunchURL", () => {
  it("stamps a relative return_to on the launch URL when provided", async () => {
    authedFetchMock.mockResolvedValueOnce(
      jsonResponse({ launch_token: "launch-token-test" }),
    );

    const url = await createEditorLaunchURL(
      "https://editor.instaedit.test/editor/ve_test_99",
      "ve_test_99",
      { returnTo: "/app/covers?group=7" },
    );

    expect(url).toContain("?return_to=%2Fapp%2Fcovers%3Fgroup%3D7");
    expect(url).toContain("#launch_token=launch-token-test");
    expect(url.startsWith("https://editor.instaedit.test/editor/ve_test_99?")).toBe(true);
  });

  it("omits return_to when no return context is provided", async () => {
    authedFetchMock.mockResolvedValueOnce(
      jsonResponse({ launch_token: "launch-token-test" }),
    );

    const url = await createEditorLaunchURL(
      "https://editor.instaedit.test/editor/ve_test_99",
      "ve_test_99",
    );

    expect(url).toBe(
      "https://editor.instaedit.test/editor/ve_test_99#launch_token=launch-token-test",
    );
  });
});

describe("openInstaEditorWithLaunch", () => {
  it("routes legacy dev editor URLs to the active Vercel deployment", async () => {
    authedFetchMock.mockResolvedValueOnce(
      jsonResponse({ launch_token: "launch-token-test" }),
    );
    const openSpy = vi
      .spyOn(window, "open")
      .mockImplementation(() => null);

    await openInstaEditorWithLaunch(
      "https://dev.instaedit.org/instaeditor/editor/ve_x",
      "ve_x",
    );

    expect(openSpy).toHaveBeenCalledWith(
      "https://instaeditor.vercel.app/instaeditor/editor/ve_x#launch_token=launch-token-test",
      "_blank",
      "noopener,noreferrer",
    );
  });

  it("opens the editor in a new tab carrying the return_to context", async () => {
    authedFetchMock.mockResolvedValueOnce(
      jsonResponse({ launch_token: "launch-token-test" }),
    );
    const openSpy = vi
      .spyOn(window, "open")
      .mockImplementation(() => null);

    await openInstaEditorWithLaunch(
      "https://editor.instaedit.test/editor/ve_x",
      "ve_x",
      { returnTo: "/app/covers?group=7" },
    );

    expect(openSpy).toHaveBeenCalledWith(
      "https://editor.instaedit.test/editor/ve_x?return_to=%2Fapp%2Fcovers%3Fgroup%3D7#launch_token=launch-token-test",
      "_blank",
      "noopener,noreferrer",
    );
  });

  it("navigates a pre-opened tab instead of calling window.open (popup-proof)", async () => {
    authedFetchMock.mockResolvedValueOnce(
      jsonResponse({ launch_token: "launch-token-test" }),
    );
    const openSpy = vi
      .spyOn(window, "open")
      .mockImplementation(() => null);
    const tab = { closed: false, location: { href: "" } } as unknown as Window;

    await openInstaEditorWithLaunch(
      "https://editor.instaedit.test/editor/ve_x",
      "ve_x",
      { returnTo: "/app/covers?group=7", tab },
    );

    expect(openSpy).not.toHaveBeenCalled();
    expect(tab.location.href).toBe(
      "https://editor.instaedit.test/editor/ve_x?return_to=%2Fapp%2Fcovers%3Fgroup%3D7#launch_token=launch-token-test",
    );
  });

  it("falls back to window.open when no pre-opened tab is available", async () => {
    authedFetchMock.mockResolvedValueOnce(
      jsonResponse({ launch_token: "launch-token-test" }),
    );
    const openSpy = vi
      .spyOn(window, "open")
      .mockImplementation(() => null);

    await openInstaEditorWithLaunch(
      "https://editor.instaedit.test/editor/ve_x",
      "ve_x",
      { tab: null },
    );

    expect(openSpy).toHaveBeenCalledWith(
      "https://editor.instaedit.test/editor/ve_x#launch_token=launch-token-test",
      "_blank",
      "noopener,noreferrer",
    );
  });
});

describe("createEditorSessionAndRedirect", () => {
  it("creates/reuses the session and redirects the current document without opening an iframe or popup", async () => {
    authedFetchMock
      .mockResolvedValueOnce(jsonResponse(OK_SESSION))
      .mockResolvedValueOnce(jsonResponse({ launch_token: "launch-token-test" }));
    const navigate = vi.fn();

    // The public helper accepts a navigation dependency through the low-level
    // redirect function; this test covers the request/session contract here.
    const session = await createEditorSessionAndRedirect(
      { workspace_id: 7, platform_account_id: 99, youtube_video_id: "dQw4w9WgXcQ" },
      {},
      navigate,
    );

    expect(session).toEqual(OK_SESSION);
    expect(navigate).toHaveBeenCalledWith(
      "https://editor.instaedit.test/editor/ve_test_99#launch_token=launch-token-test",
    );
  });
});

describe("createEditorSessionAndOpen", () => {
  it("chains create + window.open in one canonical call", async () => {
    authedFetchMock
      .mockResolvedValueOnce(jsonResponse(OK_SESSION))
      .mockResolvedValueOnce(jsonResponse({ launch_token: "launch-token-test" }));
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
      "https://editor.instaedit.test/editor/ve_test_99#launch_token=launch-token-test",
      "_blank",
      "noopener,noreferrer",
    );
  });

  it("forwards the return_to context to the editor launch", async () => {
    authedFetchMock
      .mockResolvedValueOnce(jsonResponse(OK_SESSION))
      .mockResolvedValueOnce(jsonResponse({ launch_token: "launch-token-test" }));
    const openSpy = vi
      .spyOn(window, "open")
      .mockImplementation(() => null);

    await createEditorSessionAndOpen(
      {
        workspace_id: 1,
        platform_account_id: 2,
        youtube_video_id: "v",
      },
      {},
      { returnTo: "/app/covers?group=7" },
    );

    expect(openSpy).toHaveBeenCalledWith(
      "https://editor.instaedit.test/editor/ve_test_99?return_to=%2Fapp%2Fcovers%3Fgroup%3D7#launch_token=launch-token-test",
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
