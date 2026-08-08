import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";

const {
  authedFetchMock,
  openInstaEditorWithLaunchMock,
  createYouTubeEditorSessionMock,
  navigateMock,
  toastMock,
} = vi.hoisted(() => ({
  authedFetchMock: vi.fn(),
  openInstaEditorWithLaunchMock: vi.fn(),
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

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: () => navigateMock,
  };
});

vi.mock("../../features/youtube/api/editorSessionsApi", () => ({
  openInstaEditorWithLaunch: openInstaEditorWithLaunchMock,
  createYouTubeEditorSession: createYouTubeEditorSessionMock,
  createEditorSessionAndOpen: vi.fn(),
  coversHubReturnTo: (groupId: number) => `/app/covers?group=${groupId}`,
}));

import { GroupCovers } from "./GroupCovers";

function renderPanel(groupName = "Amish") {
  return render(
    <MemoryRouter>
      <GroupCovers groupId={7} groupName={groupName} />
    </MemoryRouter>,
  );
}

function jsonResponse(data: unknown) {
  return { json: async () => data };
}

const coverFixture = (overrides: Record<string, unknown> = {}) => ({
  project_id: "ytes_cover_1",
  workspace_id: 7,
  session_id: "ytes_cover_1",
  velox_project_id: "ve_cover_1",
  editor_url: "https://editor.instaedit.test/editor/ve_cover_1",
  name: "YouTube cover",
  project_status: "ready",
  edit_status: "editing",
  youtube_video_id: "fwFGQglE9c0",
  platform_account_id: 42,
  channel_name: "Wrestling Insider RU",
  language: "ru",
  project_version: 2,
  created_at: "2026-08-01T10:00:00Z",
  updated_at: "2026-08-01T10:00:00Z",
  ...overrides,
});

const privateVideoFixture = (overrides: Record<string, unknown> = {}) => ({
  youtube_video_id: "video-1",
  title: "Video privato",
  thumbnail_url: "https://i.ytimg.com/vi/video-1/hqdefault.jpg",
  platform_account_id: 42,
  channel_name: "Wrestling Insider RU",
  actual_privacy: "private",
  ...overrides,
});

/**
 * Route authedFetch by URL so the covers grid and the video manifest
 * (needed by the one-click quick-create) each get the right payload.
 */
function routeFetch({
  covers = [],
  videos = [],
}: {
  covers?: unknown[];
  videos?: unknown[];
} = {}) {
  authedFetchMock.mockImplementation(async (url: string) => {
    if (url.startsWith("/api/v1/groups/7/covers")) {
      return jsonResponse({ covers });
    }
    if (url.startsWith("/api/v1/groups/7/youtube/videos")) {
      return jsonResponse({ videos });
    }
    if (url === "/api/v1/groups/7") {
      return jsonResponse({ workspace_id: 7 });
    }
    if (url.startsWith("/api/v1/youtube/editor-sessions/by-project/")) {
      return jsonResponse({});
    }
    return jsonResponse({});
  });
}

beforeEach(() => {
  authedFetchMock.mockReset();
  openInstaEditorWithLaunchMock.mockReset();
  createYouTubeEditorSessionMock.mockReset();
  navigateMock.mockReset();
  toastMock.success.mockReset();
  toastMock.error.mockReset();
  toastMock.info.mockReset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("GroupCovers", () => {
  it("fetches the group covers endpoint on mount and renders the covers grid", async () => {
    routeFetch({
      covers: [
        coverFixture(),
        coverFixture({
          project_id: "ytes_cover_2",
          project_status: "archived",
          channel_name: "Wwe Insider De",
          youtube_video_id: "sY6Ce0bTuwo",
        }),
      ],
    });

    renderPanel();
    expect(authedFetchMock).toHaveBeenCalledWith(
      "/api/v1/groups/7/covers",
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );

    await waitFor(() => {
      expect(screen.getAllByTestId("group-cover-card")).toHaveLength(2);
    });
    expect(screen.getByText(/wrestling insider ru/i)).toBeInTheDocument();
    expect(screen.getByText(/wwe insider de/i)).toBeInTheDocument();
    expect(screen.getByText(/archiviata/i)).toBeInTheDocument();
  });

  it("shows an always-visible Crea copertina button in the header even with a full grid", async () => {
    routeFetch({
      covers: [
        coverFixture(),
        coverFixture({
          project_id: "ytes_cover_2",
          project_status: "archived",
          channel_name: "Wwe Insider De",
          youtube_video_id: "sY6Ce0bTuwo",
        }),
      ],
    });

    renderPanel();

    await waitFor(() => {
      expect(screen.getAllByTestId("group-cover-card")).toHaveLength(2);
    });
    expect(screen.getByTestId("group-covers-create-header")).toBeInTheDocument();
  });

  it("opens InstaEditor directly from the header + on the most recent private video with a random name", async () => {
    routeFetch({
      videos: [
        privateVideoFixture({
          youtube_video_id: "video-2",
          platform_account_id: 43,
          desired_privacy: "private",
        }),
        privateVideoFixture(),
      ],
    });
    createYouTubeEditorSessionMock.mockResolvedValueOnce({
      session_id: "session-1",
      velox_project_id: "ve_created",
      editor_url: "https://editor.instaedit.test/editor/ve_created",
    });

    renderPanel();

    // The header + is always visible, so wait for the video manifest load
    // to land in hook state before clicking — the one-click create must
    // see a ready group (not the initial loading state).
    await waitFor(() => {
      expect(authedFetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/api/v1/groups/7/youtube/videos"),
        expect.anything(),
      );
    });
    await act(async () => {});

    fireEvent.click(screen.getByTestId("group-covers-create-header"));

    await waitFor(() => {
      expect(createYouTubeEditorSessionMock).toHaveBeenCalledOnce();
    });
    // Draft PUT stamps the random name before the editor opens.
    const draftCall = authedFetchMock.mock.calls.find(([url]) =>
      String(url).includes("/draft"),
    );
    expect(draftCall).toBeDefined();
    const draftBody = JSON.parse(String((draftCall as unknown[])[1] && (draftCall[1] as RequestInit).body));
    // The random name embeds the group name (Amish-<Noun>-<Number>).
    expect(draftBody.title).toMatch(/^Amish-[A-Z][a-z]+-\d{1,2}$/);
    expect(openInstaEditorWithLaunchMock).toHaveBeenCalledWith(
      "https://editor.instaedit.test/editor/ve_created",
      "ve_created",
      { returnTo: "/app/covers?group=7" },
    );
  });

  it("renders a short empty state with a Crea copertina button", async () => {
    routeFetch();

    renderPanel();

    await waitFor(() => {
      expect(screen.getByText(/nessuna copertina in questo gruppo/i)).toBeInTheDocument();
    });
    // The long explanatory description was removed on purpose.
    expect(
      screen.queryByText(/Quando crei una copertina per un video di questo gruppo/i),
    ).not.toBeInTheDocument();
    expect(screen.getByTestId("group-covers-create")).toBeInTheDocument();
  });

  it("surfaces a toast when the group has no private videos to draw a cover on", async () => {
    routeFetch({ videos: [] });

    renderPanel();

    // Same deterministic wait: the empty-videos response must be applied
    // to hook state before the click so quickCreateCover reports the
    // missing-videos error instead of the loading toast.
    await waitFor(() => {
      expect(authedFetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/api/v1/groups/7/youtube/videos"),
        expect.anything(),
      );
    });
    await act(async () => {});
    fireEvent.click(screen.getByTestId("group-covers-create-header"));

    await waitFor(() => {
      expect(toastMock.error).toHaveBeenCalledWith(
        expect.stringMatching(/nessun video privato nel gruppo/i),
      );
    });
    expect(openInstaEditorWithLaunchMock).not.toHaveBeenCalled();
  });

  it("filters by project status via the filter chips", async () => {
    routeFetch({
      covers: [
        coverFixture({ project_id: "ytes_draft", project_status: "draft", draft_title: "Draft cover" }),
        coverFixture({ project_id: "ytes_ready", project_status: "ready", draft_title: "Ready cover" }),
        coverFixture({ project_id: "ytes_arch", project_status: "archived", draft_title: "Archived cover" }),
      ],
    });

    renderPanel();

    await waitFor(() => {
      expect(screen.getAllByTestId("group-cover-card")).toHaveLength(3);
    });
    expect(screen.getByText("Draft cover")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Archiviate" }));
    await waitFor(() => {
      expect(screen.getAllByTestId("group-cover-card")).toHaveLength(1);
    });
    expect(screen.getByText("Archived cover")).toBeInTheDocument();
    expect(screen.queryByText("Draft cover")).not.toBeInTheDocument();
    expect(screen.queryByText("Ready cover")).not.toBeInTheDocument();
  });

  it("shows the draft title when present", async () => {
    routeFetch({
      covers: [coverFixture({ draft_title: "Il mio nuovo design" })],
    });

    renderPanel();

    await waitFor(() => {
      expect(screen.getByText("Il mio nuovo design")).toBeInTheDocument();
    });
  });

  it("surfaces an actionable error on failure", async () => {
    authedFetchMock.mockRejectedValue(new Error("boom"));

    renderPanel();

    await waitFor(() => {
      expect(screen.getByRole("alert")).toBeInTheDocument();
    });
    expect(screen.getByText(/impossibile caricare le copertine/i)).toBeInTheDocument();
  });
});
