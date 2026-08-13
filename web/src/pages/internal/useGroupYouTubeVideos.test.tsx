import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";

const {
  authedFetchMock,
  openInstaEditorWithLaunchMock,
  createEditorSessionAndOpenMock,
  createYouTubeEditorSessionMock,
  patchGroupVideoMetadataMock,
  useGroupVideosInvalidationMock,
  navigateMock,
  toastMock,
} = vi.hoisted(() => ({
  authedFetchMock: vi.fn(),
  openInstaEditorWithLaunchMock: vi.fn(),
  createEditorSessionAndOpenMock: vi.fn(),
  createYouTubeEditorSessionMock: vi.fn(),
  patchGroupVideoMetadataMock: vi.fn(),
  useGroupVideosInvalidationMock: vi.fn(),
  navigateMock: vi.fn(),
  toastMock: {
    success: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
  },
}));

vi.mock("../../lib/auth", () => ({
  authedFetch: authedFetchMock,
  AuthError: class AuthError extends Error {
    override name = "AuthError";
  },
  ApiError: class ApiError extends Error {
    override name = "ApiError";
    constructor(public readonly status: number, message: string) {
      super(message);
    }
  },
}));

vi.mock("../../components/toast", () => ({
  useToast: () => toastMock,
}));

vi.mock("../../lib/queryRegistry", () => ({
  useSharedPolling: () => vi.fn(async () => undefined),
  invalidateSharedQueries: vi.fn(),
}));

vi.mock("../../features/youtube/hooks/useGroupVideosInvalidation", () => ({
  useGroupVideosInvalidation: useGroupVideosInvalidationMock,
}));

vi.mock("../../features/youtube/api/videosApi", () => ({
  patchGroupVideoMetadata: patchGroupVideoMetadataMock,
}));

vi.mock("../../features/youtube/api/editorSessionsApi", () => ({
  createYouTubeEditorSession: createYouTubeEditorSessionMock,
  createEditorSessionAndOpen: createEditorSessionAndOpenMock,
  openInstaEditorWithLaunch: openInstaEditorWithLaunchMock,
  coversHubReturnTo: (groupId: number) => `/app/covers?group=${groupId}`,
}));

vi.mock("react-router-dom", () => ({
  useNavigate: () => navigateMock,
}));

import { generateCoverName, slugifyGroupName, useGroupYouTubeVideos } from "./useGroupYouTubeVideos";
import type { GroupYouTubeVideo } from "./groupYouTubeVideosTypes";

function jsonResponse(data: unknown) {
  return { json: async () => data };
}

const video: GroupYouTubeVideo = {
  youtube_video_id: "video-1",
  title: "Video privato",
  thumbnail_url: "https://i.ytimg.com/vi/video-1/hqdefault.jpg",
  platform_account_id: 42,
};

// The hook returns EVERY manageable video (canonical list); quick-create
// derives the first private (non-phantom) row client-side, so fixtures
// carrying actual_privacy "private" are the ones it must pick.
const privateVideo: GroupYouTubeVideo = {
  ...video,
  actual_privacy: "private",
};

const createdSession = {
  session_id: "session-1",
  velox_project_id: "ve_created",
  editor_url: "https://editor.example.test/editor/ve_created",
};

beforeEach(() => {
  authedFetchMock.mockReset();
  openInstaEditorWithLaunchMock.mockReset();
  createEditorSessionAndOpenMock.mockReset();
  createYouTubeEditorSessionMock.mockReset();
  patchGroupVideoMetadataMock.mockReset();
  useGroupVideosInvalidationMock.mockReset();
  navigateMock.mockReset();
  toastMock.success.mockReset();
  toastMock.error.mockReset();
  toastMock.info.mockReset();
  authedFetchMock.mockResolvedValue(jsonResponse({ videos: [] }));
  patchGroupVideoMetadataMock.mockResolvedValue({});
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("useGroupYouTubeVideos — Groups → Modifica", () => {
  it("opens the existing project URL in a new tab without recreating it", async () => {
    const existing = {
      ...video,
      velox_project_id: "ve_existing",
      editor_url: "https://editor.example.test/editor/ve_existing",
    };
    const { result } = renderHook(() => useGroupYouTubeVideos(7));

    await act(async () => {
      await result.current.openThumbnailEditor(existing);
    });

    expect(openInstaEditorWithLaunchMock).toHaveBeenCalledOnce();
    expect(openInstaEditorWithLaunchMock).toHaveBeenCalledWith(
      existing.editor_url,
      existing.velox_project_id,
      { returnTo: "/app/covers?group=7" },
    );
    expect(createEditorSessionAndOpenMock).not.toHaveBeenCalled();
    expect(authedFetchMock).toHaveBeenCalledTimes(1);
    expect(toastMock.error).not.toHaveBeenCalled();
  });

  it("resolves the group workspace once, create-or-resolves the project, and opens the returned editor URL in a new tab", async () => {
    authedFetchMock
      .mockResolvedValueOnce(jsonResponse({ videos: [] }))
      .mockResolvedValueOnce(jsonResponse({ workspace_id: 7 }));
    createEditorSessionAndOpenMock.mockResolvedValueOnce(createdSession);

    const { result } = renderHook(() => useGroupYouTubeVideos(7));

    await act(async () => {
      await result.current.openThumbnailEditor(video);
    });

    expect(authedFetchMock).toHaveBeenNthCalledWith(
      2,
      "/api/v1/groups/7",
    );
    expect(createEditorSessionAndOpenMock).toHaveBeenCalledOnce();
    expect(createEditorSessionAndOpenMock).toHaveBeenCalledWith(
      {
        workspace_id: 7,
        platform_account_id: 42,
        youtube_video_id: "video-1",
        source_thumbnail_url: "https://i.ytimg.com/vi/video-1/hqdefault.jpg",
      },
      {},
      { returnTo: "/app/covers?group=7" },
    );
    expect(toastMock.error).not.toHaveBeenCalled();
  });

  it("does not create a project when the group workspace cannot be resolved", async () => {
    authedFetchMock
      .mockResolvedValueOnce(jsonResponse({ videos: [] }))
      .mockResolvedValueOnce(jsonResponse({ workspace_id: 0 }));

    const { result } = renderHook(() => useGroupYouTubeVideos(7));
    await act(async () => {
      await result.current.openThumbnailEditor(video);
    });

    expect(createEditorSessionAndOpenMock).not.toHaveBeenCalled();
    expect(toastMock.error).toHaveBeenCalledWith("Il gruppo non ha un workspace valido.");
  });

  it("does not issue a second create request while the same video is opening", async () => {
    let resolveWorkspace: ((value: unknown) => void) | undefined;
    let resolveCreate: ((value: unknown) => void) | undefined;
    authedFetchMock
      .mockResolvedValueOnce(jsonResponse({ videos: [] }))
      .mockImplementationOnce(() => new Promise((resolve) => {
        resolveWorkspace = resolve;
      }));
    createEditorSessionAndOpenMock.mockImplementationOnce(
      () => new Promise((resolve) => {
        resolveCreate = resolve;
      }),
    );

    const { result } = renderHook(() => useGroupYouTubeVideos(7));
    let first: Promise<void>;
    let second: Promise<void>;
    await act(async () => {
      first = result.current.openThumbnailEditor(video);
      second = result.current.openThumbnailEditor(video);
    });

    expect(createEditorSessionAndOpenMock).not.toHaveBeenCalled();
    resolveWorkspace?.(jsonResponse({ workspace_id: 7 }));
    await act(async () => {
      await Promise.resolve();
    });
    expect(createEditorSessionAndOpenMock).toHaveBeenCalledOnce();

    resolveCreate?.(createdSession);
    await act(async () => {
      await first;
      await second;
    });
  });
});

describe("useGroupYouTubeVideos — quick create (Crea copertina)", () => {
  it("opens InstaEditor directly on the most recent private video and stamps a random draft title before opening", async () => {
    authedFetchMock
      .mockResolvedValueOnce(jsonResponse({ videos: [privateVideo] }))
      .mockResolvedValueOnce(jsonResponse({ workspace_id: 7 }))
      .mockResolvedValueOnce(jsonResponse({}));
    createYouTubeEditorSessionMock.mockResolvedValueOnce(createdSession);

    const { result } = renderHook(() => useGroupYouTubeVideos(7, true, "Amish"));

    await act(async () => {
      // Flush the initial videos load so state.kind becomes "ready".
      await Promise.resolve();
    });

    let opened = false;
    await act(async () => {
      opened = await result.current.quickCreateCover();
    });

    expect(opened).toBe(true);
    // No picker dialog: the session is created for the first private video.
    expect(createYouTubeEditorSessionMock).toHaveBeenCalledWith({
      workspace_id: 7,
      platform_account_id: 42,
      youtube_video_id: "video-1",
      source_thumbnail_url: "https://i.ytimg.com/vi/video-1/hqdefault.jpg",
    });
    // The random name embeds the group name (Amish-<Noun>-<Number>) and is
    // written to the draft BEFORE the editor tab opens.
    const draftCall = authedFetchMock.mock.calls.find(([url]) =>
      String(url).includes("/draft"),
    );
    expect(draftCall).toBeDefined();
    const draftBody = JSON.parse(String((draftCall as unknown[])[1] && (draftCall[1] as RequestInit).body));
    expect(draftBody.title).toMatch(/^Amish-[A-Z][a-z]+-\d{1,2}$/);
    expect(openInstaEditorWithLaunchMock).toHaveBeenCalledWith(
      createdSession.editor_url,
      createdSession.velox_project_id,
      { returnTo: "/app/covers?group=7" },
    );
  });

  it("keeps every manageable video in the canonical list (no private-only filter)", async () => {
    authedFetchMock.mockResolvedValueOnce(
      jsonResponse({
        videos: [
          { ...video, youtube_video_id: "public-1", privacy_status: "public", actual_privacy: "public" },
          { ...video, youtube_video_id: "private-1", privacy_status: "private", actual_privacy: "private" },
          { ...video, youtube_video_id: "unlisted-1", privacy_status: "unlisted", actual_privacy: "unlisted" },
        ],
      }),
    );

    const { result } = renderHook(() => useGroupYouTubeVideos(7));
    await act(async () => {
      await Promise.resolve();
    });

    expect(result.current.state.kind).toBe("ready");
    if (result.current.state.kind !== "ready") return;
    expect(result.current.state.videos.map((v) => v.youtube_video_id)).toEqual([
      "public-1",
      "private-1",
      "unlisted-1",
    ]);
  });

  it("quick-create picks the newest private video even when a public video is first", async () => {
    authedFetchMock
      .mockResolvedValueOnce(
        jsonResponse({
          videos: [
            { ...video, youtube_video_id: "public-1", privacy_status: "public", actual_privacy: "public" },
            { ...privateVideo, youtube_video_id: "private-1" },
          ],
        }),
      )
      .mockResolvedValueOnce(jsonResponse({ workspace_id: 7 }))
      .mockResolvedValueOnce(jsonResponse({}));
    createYouTubeEditorSessionMock.mockResolvedValueOnce(createdSession);

    const { result } = renderHook(() => useGroupYouTubeVideos(7));
    await act(async () => {
      await Promise.resolve();
    });

    let opened = false;
    await act(async () => {
      opened = await result.current.quickCreateCover();
    });

    expect(opened).toBe(true);
    expect(createYouTubeEditorSessionMock).toHaveBeenCalledWith(
      expect.objectContaining({ youtube_video_id: "private-1" }),
    );
  });

  it("does not open the editor and surfaces a toast when the group has no private videos", async () => {
    authedFetchMock.mockResolvedValueOnce(jsonResponse({ videos: [] }));

    const { result } = renderHook(() => useGroupYouTubeVideos(7));

    await act(async () => {
      await Promise.resolve();
    });

    let opened = true;
    await act(async () => {
      opened = await result.current.quickCreateCover();
    });

    expect(opened).toBe(false);
    expect(toastMock.error).toHaveBeenCalledWith(
      expect.stringMatching(/nessun video privato nel gruppo/i),
    );
    expect(createYouTubeEditorSessionMock).not.toHaveBeenCalled();
    expect(openInstaEditorWithLaunchMock).not.toHaveBeenCalled();
  });
});

describe("useGroupYouTubeVideos — targeted group-videos invalidation", () => {
  it("subscribes to group-videos invalidation and background-refreshes the canonical list", async () => {
    authedFetchMock.mockResolvedValueOnce(
      jsonResponse({ videos: [privateVideo] }),
    );

    const { result } = renderHook(() => useGroupYouTubeVideos(7));
    await act(async () => {
      await Promise.resolve();
    });

    // The hook subscribes the invalidation handler for its groupId.
    expect(useGroupVideosInvalidationMock).toHaveBeenCalledWith(
      7,
      expect.any(Function),
    );
    const handler = useGroupVideosInvalidationMock.mock.calls[0]?.[1] as () => void;
    expect(handler).toBeTypeOf("function");

    await act(async () => {
      handler();
      await Promise.resolve();
    });

    // Only the video list refetches, with refresh=true (bypasses the
    // backend list cache) — no other InstaEdit surface is reloaded.
    const lastCall = authedFetchMock.mock.calls.at(-1)?.[0];
    expect(String(lastCall)).toContain("/api/v1/groups/7/youtube/videos");
    expect(String(lastCall)).toContain("refresh=true");
    // The current rows stay rendered: no loading-state reset.
    expect(result.current.state.kind).toBe("ready");
  });

  it("refetches the canonical list when the tab regains focus (cross-origin cover publish)", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ videos: [privateVideo] }));

    const { result } = renderHook(() => useGroupYouTubeVideos(7));
    await act(async () => {
      await Promise.resolve();
    });

    const callsBefore = authedFetchMock.mock.calls.length;

    await act(async () => {
      window.dispatchEvent(new Event("focus"));
      await Promise.resolve();
    });

    expect(authedFetchMock.mock.calls.length).toBeGreaterThan(callsBefore);
    const lastCall = authedFetchMock.mock.calls.at(-1)?.[0];
    expect(String(lastCall)).toContain("/api/v1/groups/7/youtube/videos");
    // Plain refetch — the backend already invalidated its cache on
    // publish, so no refresh=true bypass is needed here.
    expect(String(lastCall)).not.toContain("refresh=true");
    // Current rows stay rendered: no loading-state reset.
    expect(result.current.state.kind).toBe("ready");
  });

  it("saves video metadata through the shared videosApi", async () => {
    const existing = {
      ...video,
      velox_project_id: "ve_existing",
      editor_url: "https://editor.example.test/editor/ve_existing",
    };
    const { result } = renderHook(() => useGroupYouTubeVideos(7));

    await act(async () => {
      await result.current.openVideoPreview(existing);
    });
    expect(result.current.preview).not.toBeNull();

    await act(async () => {
      await result.current.saveVideoMetadata();
    });

    expect(toastMock.success).toHaveBeenCalledWith("Metadati video salvati.");
    // The drawer saves through the single metadata PATCH in videosApi
    // (not the editor-session draft); the backend merges into the
    // canonical snippet and the API layer invalidates the group cache.
    expect(patchGroupVideoMetadataMock).toHaveBeenCalledWith(7, "video-1", {
      platform_account_id: 42,
      title: "Video privato",
      description: "",
      category_id: "",
      privacy_status: "private",
    });
    expect(patchGroupVideoMetadataMock).toHaveBeenCalledTimes(1);
  });

  it("PATCHes privacy_status when the visibility changes and reflects it on the preview", async () => {
    const existing = { ...video, privacy_status: "private", actual_privacy: "private" };
    const { result } = renderHook(() => useGroupYouTubeVideos(7));

    await act(async () => {
      await result.current.openVideoPreview(existing);
    });
    expect(result.current.editPrivacyStatus).toBe("private");

    await act(async () => {
      result.current.setEditPrivacyStatus("public");
    });
    await act(async () => {
      await result.current.saveVideoMetadata();
    });

    expect(patchGroupVideoMetadataMock).toHaveBeenCalledWith(7, "video-1", {
      platform_account_id: 42,
      title: "Video privato",
      description: "",
      category_id: "",
      privacy_status: "public",
    });
    expect(result.current.preview?.video.privacy_status).toBe("public");
    expect(result.current.preview?.video.actual_privacy).toBe("public");
  });

  it("seeds the category draft from the video and PATCHes title/description/category on save", async () => {
    const existing = { ...video, category_id: "24", description: "Descrizione esistente" };
    const { result } = renderHook(() => useGroupYouTubeVideos(7));

    await act(async () => {
      await result.current.openVideoPreview(existing);
    });
    expect(result.current.editCategoryID).toBe("24");
    expect(result.current.draftTitle).toBe("Video privato");
    expect(result.current.draftDescription).toBe("Descrizione esistente");

    await act(async () => {
      result.current.setDraftTitle("Nuovo titolo");
      result.current.setEditCategoryID("20");
    });
    await act(async () => {
      await result.current.saveVideoMetadata();
    });

    expect(patchGroupVideoMetadataMock).toHaveBeenCalledWith(7, "video-1", {
      platform_account_id: 42,
      title: "Nuovo titolo",
      description: "Descrizione esistente",
      category_id: "20",
      privacy_status: "private",
    });
    // The drawer preview reflects the edited values immediately.
    expect(result.current.preview?.video.title).toBe("Nuovo titolo");
    expect(result.current.preview?.video.category_id).toBe("20");
  });
});

describe("generateCoverName", () => {
  it("embeds the group name when provided (Group-Noun-Number)", () => {
    for (let i = 0; i < 50; i += 1) {
      expect(generateCoverName("Amish")).toMatch(/^Amish-[A-Z][a-z]+-\d{1,2}$/);
    }
  });

  it("falls back to an Adjective-Noun-Number name without a group name", () => {
    for (let i = 0; i < 50; i += 1) {
      expect(generateCoverName()).toMatch(/^[A-Z][a-z]+-[A-Z][a-z]+-\d{1,2}$/);
      expect(generateCoverName("")).toMatch(/^[A-Z][a-z]+-[A-Z][a-z]+-\d{1,2}$/);
      expect(generateCoverName("   ")).toMatch(/^[A-Z][a-z]+-[A-Z][a-z]+-\d{1,2}$/);
    }
  });
});

describe("slugifyGroupName", () => {
  it("folds spaces and special chars to hyphens and strips accents", () => {
    expect(slugifyGroupName("Wrestling Insider RU")).toBe("Wrestling-Insider-RU");
    expect(slugifyGroupName("Città dei Sogni!")).toBe("Citta-dei-Sogni");
    expect(slugifyGroupName("  Amish  ")).toBe("Amish");
    expect(slugifyGroupName("123 & More")).toBe("123-More");
  });
});
