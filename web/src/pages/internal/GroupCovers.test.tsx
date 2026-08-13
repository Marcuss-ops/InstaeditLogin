import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
  invalidateSharedQueries: vi.fn(),
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

// Tabs "opened" by the component's synchronous window.open (popup-proof
// pattern); the tests assert they are navigated (not closed) on success
// and closed when the flow bails out, so no blank tab leaks.
let fakeTabs: Array<{ closed: boolean; location: { href: string }; close: ReturnType<typeof vi.fn> }>;

beforeEach(() => {
  authedFetchMock.mockReset();
  openInstaEditorWithLaunchMock.mockReset();
  createYouTubeEditorSessionMock.mockReset();
  navigateMock.mockReset();
  toastMock.success.mockReset();
  toastMock.error.mockReset();
  toastMock.info.mockReset();
  fakeTabs = [];
  // The covers zone opens the destination tab synchronously in the click
  // gesture (popup-proof); jsdom would otherwise throw on window.open.
  vi.spyOn(window, "open").mockImplementation(() => {
    const tab = { closed: false, location: { href: "" }, close: vi.fn() };
    fakeTabs.push(tab);
    return tab as unknown as Window;
  });
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

  it("shows the Photoshop-style create tile before the full grid", async () => {
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
    expect(screen.getByTestId("group-covers-create-card")).toBeInTheDocument();
    expect(screen.queryByTestId("group-covers-create-header")).not.toBeInTheDocument();
    // The covers zone stays a simplified create-tile + grid (no status
    // tabs); the Video/Cover manager below the grid has its own controls.
    const coversZone = within(screen.getByTestId("group-covers"));
    expect(coversZone.queryByText("Tutte")).not.toBeInTheDocument();
    expect(coversZone.queryByText("Bozze")).not.toBeInTheDocument();
    expect(coversZone.queryByText("Pronte")).not.toBeInTheDocument();
    expect(coversZone.queryByText("Archiviate")).not.toBeInTheDocument();
    expect(coversZone.queryByText("Aggiorna")).not.toBeInTheDocument();
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

    // The first grid tile is always visible, so wait for the video manifest load
    // to land in hook state before clicking — the one-click create must
    // see a ready group (not the initial loading state).
    await waitFor(() => {
      expect(authedFetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/api/v1/groups/7/youtube/videos"),
        expect.anything(),
      );
    });
    await act(async () => {});

    fireEvent.click(screen.getByTestId("group-covers-create-card"));

    // The tab is reserved synchronously inside the click gesture, then
    // navigated once the launch URL is ready (immune to popup blockers).
    expect(window.open).toHaveBeenCalledWith("about:blank", "_blank");
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
      { returnTo: "/app/covers?group=7", tab: expect.anything() },
    );
    // The reserved tab was handed to the editor (navigated), not closed.
    expect(fakeTabs[0]?.close).not.toHaveBeenCalled();
  });

  it("opens InstaEditor for an existing cover through the synchronously-reserved tab", async () => {
    routeFetch({ covers: [coverFixture()] });

    renderPanel();

    await waitFor(() => {
      expect(screen.getAllByTestId("group-cover-card")).toHaveLength(1);
    });
    fireEvent.click(screen.getByRole("button", { name: /modifica in instaeditor/i }));

    await waitFor(() => {
      expect(openInstaEditorWithLaunchMock).toHaveBeenCalledWith(
        "https://editor.instaedit.test/editor/ve_cover_1",
        "ve_cover_1",
        { returnTo: "/app/covers?group=7", tab: expect.anything() },
      );
    });
    expect(fakeTabs[0]?.close).not.toHaveBeenCalled();
  });

  it("renders the create tile as the first cover when the group is empty", async () => {
    routeFetch();

    renderPanel();

    await waitFor(() => {
      expect(screen.getByTestId("group-covers-create-card")).toBeInTheDocument();
    });
    expect(screen.getByText("Crea copertina")).toBeInTheDocument();
    expect(screen.getByText(/clicca per creare una nuova copertina/i)).toBeInTheDocument();
    expect(screen.queryByText(/nessuna copertina in questo gruppo/i)).not.toBeInTheDocument();
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
    fireEvent.click(screen.getByTestId("group-covers-create-card"));

    await waitFor(() => {
      expect(toastMock.error).toHaveBeenCalledWith(
        expect.stringMatching(/nessun video privato nel gruppo/i),
      );
    });
    expect(openInstaEditorWithLaunchMock).not.toHaveBeenCalled();
    // The reserved tab is closed again so no blank tab is left behind.
    expect(fakeTabs[0]?.close).toHaveBeenCalled();
  });

  it("renders every project status together without status filters", async () => {
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
    expect(screen.getByText("Archived cover")).toBeInTheDocument();
    expect(screen.getByText("Ready cover")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Archiviate" })).not.toBeInTheDocument();
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

  it("renames a cover inline: clicking the title PUTs the partial {title} to the draft endpoint and updates the card", async () => {
    routeFetch({
      covers: [coverFixture({ draft_title: "Rap-Vortex-15" })],
    });

    renderPanel();

    await waitFor(() => {
      expect(screen.getAllByTestId("group-cover-card")).toHaveLength(1);
    });

    // Click the editable title → input appears with the current name.
    fireEvent.click(screen.getByTestId("cover-title-edit"));
    const input = screen.getByTestId("cover-title-input") as HTMLInputElement;
    expect(input.value).toBe("Rap-Vortex-15");

    // Type the new name and commit (blur).
    fireEvent.change(input, { target: { value: "Nuovo Nome Copertina" } });
    fireEvent.blur(input);

    // Partial PUT — title only, never wiping description/tags/privacy.
    await waitFor(() => {
      const draftCall = authedFetchMock.mock.calls.find(([url]) =>
        String(url).includes("/draft"),
      );
      expect(draftCall).toBeDefined();
      const [, init] = draftCall as [string, RequestInit];
      expect(JSON.parse(String(init.body))).toEqual({ title: "Nuovo Nome Copertina" });
    });
    expect(toastMock.success).toHaveBeenCalledWith(
      expect.stringMatching(/titolo copertina salvato/i),
    );

    // The card now shows the renamed title (optimistic local update).
    await waitFor(() => {
      expect(screen.getByText("Nuovo Nome Copertina")).toBeInTheDocument();
    });
  });

  it("cancels the inline rename on Escape without PUTting anything", async () => {
    routeFetch({
      covers: [coverFixture({ draft_title: "Rap-Vortex-15" })],
    });

    renderPanel();

    await waitFor(() => {
      expect(screen.getAllByTestId("group-cover-card")).toHaveLength(1);
    });

    fireEvent.click(screen.getByTestId("cover-title-edit"));
    const input = screen.getByTestId("cover-title-input") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "Da non salvare" } });
    fireEvent.keyDown(input, { key: "Escape" });

    // No draft PUT, card keeps the original title.
    const draftCall = authedFetchMock.mock.calls.find(([url]) =>
      String(url).includes("/draft"),
    );
    expect(draftCall).toBeUndefined();
    expect(screen.getByText("Rap-Vortex-15")).toBeInTheDocument();
  });

  it("skips the PUT when the renamed title is unchanged (no-op)", async () => {
    routeFetch({
      covers: [coverFixture({ draft_title: "Rap-Vortex-15" })],
    });

    renderPanel();

    await waitFor(() => {
      expect(screen.getAllByTestId("group-cover-card")).toHaveLength(1);
    });

    fireEvent.click(screen.getByTestId("cover-title-edit"));
    const input = screen.getByTestId("cover-title-input") as HTMLInputElement;
    fireEvent.blur(input); // same value → no-op

    const draftCall = authedFetchMock.mock.calls.find(([url]) =>
      String(url).includes("/draft"),
    );
    expect(draftCall).toBeUndefined();
  });

  it("commits exactly ONE draft PUT when committing via the ✓ button (no blur+click double-fire)", async () => {
    routeFetch({
      covers: [coverFixture({ draft_title: "Rap-Vortex-15" })],
    });

    renderPanel();

    await waitFor(() => {
      expect(screen.getAllByTestId("group-cover-card")).toHaveLength(1);
    });

    fireEvent.click(screen.getByTestId("cover-title-edit"));
    const input = screen.getByTestId("cover-title-input") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "Titolo ✓" } });
    fireEvent.click(screen.getByRole("button", { name: "Conferma titolo" }));

    await waitFor(() => {
      const draftCalls = authedFetchMock.mock.calls.filter(([url]) =>
        String(url).includes("/draft"),
      );
      // The guard (committingRef) collapses blur + click into a single PUT.
      expect(draftCalls).toHaveLength(1);
    });
  });

  it("keeps the old title and toasts when the rename PUT fails", async () => {
    routeFetch({
      covers: [coverFixture({ draft_title: "Rap-Vortex-15" })],
    });

    renderPanel();

    await waitFor(() => {
      expect(screen.getAllByTestId("group-cover-card")).toHaveLength(1);
    });

    // Make the draft PUT fail (any non-2xx / rejection).
    authedFetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/api/v1/groups/7/covers")) {
        return jsonResponse({ covers: [coverFixture({ draft_title: "Rap-Vortex-15" })] });
      }
      if (url.startsWith("/api/v1/groups/7/youtube/videos")) {
        return jsonResponse({ videos: [] });
      }
      if (url === "/api/v1/groups/7") {
        return jsonResponse({ workspace_id: 7 });
      }
      if (url.startsWith("/api/v1/youtube/editor-sessions/by-project/")) {
        throw new Error("boom");
      }
      return jsonResponse({});
    });

    fireEvent.click(screen.getByTestId("cover-title-edit"));
    const input = screen.getByTestId("cover-title-input") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "Nome che non passa" } });
    fireEvent.blur(input);

    await waitFor(() => {
      expect(toastMock.error).toHaveBeenCalled();
    });
    // Card keeps the DB title — no optimistic update on failure.
    await waitFor(() => {
      expect(screen.getByText("Rap-Vortex-15")).toBeInTheDocument();
    });
    expect(screen.queryByText("Nome che non passa")).not.toBeInTheDocument();
  });

  it("surfaces an actionable error on failure", async () => {
    authedFetchMock.mockRejectedValue(new Error("boom"));

    renderPanel();

    // The covers zone and the Video/Cover manager each render their own
    // error block; scope to the covers zone for its specific copy.
    const coversZone = () => within(screen.getByTestId("group-covers"));
    await waitFor(() => {
      expect(coversZone().getByRole("alert")).toBeInTheDocument();
    });
    expect(coversZone().getByText(/impossibile caricare le copertine/i)).toBeInTheDocument();
  });

  it("renders the Video/Cover manager (search, tabs, category) sharing the canonical list", async () => {
    routeFetch({
      videos: [
        privateVideoFixture({ youtube_video_id: "v1", title: "Primo video", category_id: "24", category_title: "Intrattenimento" }),
        privateVideoFixture({ youtube_video_id: "v2", title: "Secondo video" }),
      ],
    });

    renderPanel();

    // The manager body is part of the Copertine hub page.
    await waitFor(() => {
      expect(screen.getByTestId("group-videos-search")).toBeInTheDocument();
    });
    expect(screen.getByTestId("group-videos-category")).toBeInTheDocument();
    expect(screen.getByTestId("group-videos-filter-all")).toBeInTheDocument();
    expect(screen.getByTestId("group-videos-filter-private")).toBeInTheDocument();
    expect(screen.getByTestId("group-videos-filter-public")).toBeInTheDocument();

    await waitFor(() => {
      expect(screen.getByText("Primo video")).toBeInTheDocument();
      expect(screen.getByText("Secondo video")).toBeInTheDocument();
    });

    // ONE canonical list serves both the quick-create flow and the
    // manager (a single hook instance per group).
    const videosCalls = authedFetchMock.mock.calls.filter(([url]) =>
      String(url).includes("/youtube/videos"),
    );
    expect(videosCalls).toHaveLength(1);
  });
});
