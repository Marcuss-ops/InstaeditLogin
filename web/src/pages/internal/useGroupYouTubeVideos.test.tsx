import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";

const { authedFetchMock, redirectToInstaEditorMock, createEditorSessionAndRedirectMock, navigateMock, toastMock } = vi.hoisted(() => ({
  authedFetchMock: vi.fn(),
  redirectToInstaEditorMock: vi.fn(),
  createEditorSessionAndRedirectMock: vi.fn(),
  navigateMock: vi.fn(),
  toastMock: {
    success: vi.fn(),
    error: vi.fn(),
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
  createYouTubeEditorSession: vi.fn(),
  createEditorSessionAndRedirect: createEditorSessionAndRedirectMock,
  redirectToInstaEditor: redirectToInstaEditorMock,
}));

vi.mock("react-router-dom", () => ({
  useNavigate: () => navigateMock,
}));

import { useGroupYouTubeVideos } from "./useGroupYouTubeVideos";
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

beforeEach(() => {
  authedFetchMock.mockReset();
  redirectToInstaEditorMock.mockReset();
  createEditorSessionAndRedirectMock.mockReset();
  navigateMock.mockReset();
  toastMock.success.mockReset();
  toastMock.error.mockReset();
  authedFetchMock.mockResolvedValue(jsonResponse({ videos: [] }));
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("useGroupYouTubeVideos — Groups → Modifica", () => {
  it("redirects directly to the existing project URL without recreating it", async () => {
    const existing = {
      ...video,
      velox_project_id: "ve_existing",
      editor_url: "https://editor.example.test/editor/ve_existing",
    };
    const { result } = renderHook(() => useGroupYouTubeVideos(7));

    await act(async () => {
      await result.current.openThumbnailEditor(existing);
    });

    expect(redirectToInstaEditorMock).toHaveBeenCalledOnce();
    expect(redirectToInstaEditorMock).toHaveBeenCalledWith(existing.editor_url);
    expect(createEditorSessionAndRedirectMock).not.toHaveBeenCalled();
    expect(authedFetchMock).toHaveBeenCalledTimes(1);
    expect(toastMock.error).not.toHaveBeenCalled();
  });

  it("resolves the group workspace once, create-or-resolves the project, and redirects to the returned SPA URL", async () => {
    authedFetchMock
      .mockResolvedValueOnce(jsonResponse({ videos: [] }))
      .mockResolvedValueOnce(jsonResponse({ workspace_id: 7 }));
    createEditorSessionAndRedirectMock.mockResolvedValueOnce({
      session_id: "session-1",
      velox_project_id: "ve_created",
      editor_url: "https://editor.example.test/editor/ve_created",
    });

    const { result } = renderHook(() => useGroupYouTubeVideos(7));

    await act(async () => {
      await result.current.openThumbnailEditor(video);
    });

    expect(authedFetchMock).toHaveBeenNthCalledWith(
      2,
      "/api/v1/groups/7",
    );
    expect(createEditorSessionAndRedirectMock).toHaveBeenCalledOnce();
    expect(createEditorSessionAndRedirectMock).toHaveBeenCalledWith({
      workspace_id: 7,
      platform_account_id: 42,
      youtube_video_id: "video-1",
      source_thumbnail_url: "https://i.ytimg.com/vi/video-1/hqdefault.jpg",
    });
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

    expect(createEditorSessionAndRedirectMock).not.toHaveBeenCalled();
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
    createEditorSessionAndRedirectMock.mockImplementationOnce(
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

    expect(createEditorSessionAndRedirectMock).not.toHaveBeenCalled();
    resolveWorkspace?.(jsonResponse({ workspace_id: 7 }));
    await act(async () => {
      await Promise.resolve();
    });
    expect(createEditorSessionAndRedirectMock).toHaveBeenCalledOnce();

    resolveCreate?.({
      session_id: "session-1",
      velox_project_id: "ve_created",
      editor_url: "https://editor.example.test/editor/ve_created",
    });
    await act(async () => {
      await first;
      await second;
    });
  });
});
