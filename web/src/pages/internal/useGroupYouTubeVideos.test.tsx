import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";

const {
  authedFetchMock,
  openInstaEditorWithLaunchMock,
  createEditorSessionAndOpenMock,
  createYouTubeEditorSessionMock,
  navigateMock,
  toastMock,
} = vi.hoisted(() => ({
  authedFetchMock: vi.fn(),
  openInstaEditorWithLaunchMock: vi.fn(),
  createEditorSessionAndOpenMock: vi.fn(),
  createYouTubeEditorSessionMock: vi.fn(),
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

// The hook's loadVideos filter keeps only videos whose privacy is
// "private" (and not phantom), so quick-create fixtures must carry it.
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
  navigateMock.mockReset();
  toastMock.success.mockReset();
  toastMock.error.mockReset();
  toastMock.info.mockReset();
  authedFetchMock.mockResolvedValue(jsonResponse({ videos: [] }));
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
