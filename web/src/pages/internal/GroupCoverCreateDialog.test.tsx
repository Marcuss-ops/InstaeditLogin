import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

const {
  authedFetchMock,
  openInstaEditorWithLaunchMock,
  createEditorSessionAndOpenMock,
  navigateMock,
  toastMock,
} = vi.hoisted(() => ({
  authedFetchMock: vi.fn(),
  openInstaEditorWithLaunchMock: vi.fn(),
  createEditorSessionAndOpenMock: vi.fn(),
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
  createEditorSessionAndOpen: createEditorSessionAndOpenMock,
  openInstaEditorWithLaunch: openInstaEditorWithLaunchMock,
}));

vi.mock("react-router-dom", () => ({
  useNavigate: () => navigateMock,
}));

import { GroupCoverCreateDialog } from "./GroupCoverCreateDialog";

function jsonResponse(data: unknown) {
  return { json: async () => data };
}

const privateVideo = (overrides: Record<string, unknown> = {}) => ({
  youtube_video_id: "video-1",
  title: "Video privato uno",
  thumbnail_url: "https://i.ytimg.com/vi/video-1/hqdefault.jpg",
  privacy_status: "private",
  platform_account_id: 42,
  channel_name: "Wrestling Insider RU",
  ...overrides,
});

const onClose = vi.fn();
const onCreated = vi.fn();

function renderDialog() {
  return render(
    <GroupCoverCreateDialog groupId={7} onClose={onClose} onCreated={onCreated} />,
  );
}

beforeEach(() => {
  authedFetchMock.mockReset();
  openInstaEditorWithLaunchMock.mockReset();
  createEditorSessionAndOpenMock.mockReset();
  navigateMock.mockReset();
  toastMock.success.mockReset();
  toastMock.error.mockReset();
  onClose.mockReset();
  onCreated.mockReset();
  authedFetchMock.mockResolvedValue(jsonResponse({ videos: [] }));
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("GroupCoverCreateDialog", () => {
  it("lists the group private videos to pick a cover target", async () => {
    authedFetchMock.mockResolvedValue(
      jsonResponse({
        videos: [
          privateVideo(),
          privateVideo({
            youtube_video_id: "video-2",
            title: "Video privato due",
            channel_name: "Wwe Insider De",
          }),
        ],
      }),
    );

    renderDialog();

    await waitFor(() => {
      expect(screen.getByText("Video privato uno")).toBeInTheDocument();
    });
    expect(screen.getByText("Video privato due")).toBeInTheDocument();
    expect(screen.getAllByTestId("group-cover-create-video")).toHaveLength(2);
  });

  it("opens InstaEditor in a new tab for an existing project and fires onCreated", async () => {
    const existing = privateVideo({
      velox_project_id: "ve_existing",
      editor_url: "https://editor.instaedit.test/editor/ve_existing",
    });
    authedFetchMock.mockResolvedValue(
      jsonResponse({ videos: [existing] }),
    );

    renderDialog();

    const row = await screen.findByTestId("group-cover-create-video");
    fireEvent.click(row);

    await waitFor(() => {
      expect(openInstaEditorWithLaunchMock).toHaveBeenCalledOnce();
    });
    expect(openInstaEditorWithLaunchMock).toHaveBeenCalledWith(
      existing.editor_url,
      existing.velox_project_id,
    );
    expect(onCreated).toHaveBeenCalledOnce();
  });

  it("creates the editor session for a video without a project and fires onCreated", async () => {
    authedFetchMock
      .mockResolvedValueOnce(jsonResponse({ videos: [privateVideo()] }))
      .mockResolvedValueOnce(jsonResponse({ workspace_id: 7 }));
    createEditorSessionAndOpenMock.mockResolvedValueOnce({
      session_id: "session-1",
      velox_project_id: "ve_created",
      editor_url: "https://editor.instaedit.test/editor/ve_created",
    });

    renderDialog();

    const row = await screen.findByTestId("group-cover-create-video");
    fireEvent.click(row);

    await waitFor(() => {
      expect(createEditorSessionAndOpenMock).toHaveBeenCalledOnce();
    });
    expect(createEditorSessionAndOpenMock).toHaveBeenCalledWith({
      workspace_id: 7,
      platform_account_id: 42,
      youtube_video_id: "video-1",
      source_thumbnail_url: "https://i.ytimg.com/vi/video-1/hqdefault.jpg",
    });
    expect(onCreated).toHaveBeenCalledOnce();
  });

  it("does not fire onCreated when opening the editor fails", async () => {
    authedFetchMock
      .mockResolvedValueOnce(jsonResponse({ videos: [privateVideo()] }))
      .mockResolvedValueOnce(jsonResponse({ workspace_id: 0 }));

    renderDialog();

    const row = await screen.findByTestId("group-cover-create-video");
    fireEvent.click(row);

    await waitFor(() => {
      expect(toastMock.error).toHaveBeenCalledWith("Il gruppo non ha un workspace valido.");
    });
    expect(onCreated).not.toHaveBeenCalled();
  });

  it("shows an empty state when the group has no private videos", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ videos: [] }));

    renderDialog();

    await waitFor(() => {
      expect(screen.getByText(/nessun video privato nel gruppo/i)).toBeInTheDocument();
    });
  });

  it("closes on Escape", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ videos: [] }));
    renderDialog();
    await waitFor(() => {
      expect(screen.getByRole("dialog")).toBeInTheDocument();
    });
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("closes via the X button", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ videos: [] }));
    renderDialog();
    fireEvent.click(await screen.findByRole("button", { name: /chiudi/i }));
    expect(onClose).toHaveBeenCalledOnce();
  });
});
