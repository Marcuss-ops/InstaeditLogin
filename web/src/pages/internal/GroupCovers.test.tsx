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

// The 'Modifica video' drawer consumes the centralized category resource;
// in the Copertine hub tests it resolves to the canonical snapshot.
vi.mock("../../features/youtube/hooks/useYouTubeCategories", () => ({
  useYouTubeCategories: () => ({
    data: [
      { id: "17", label: "Sport" },
      { id: "20", label: "Gaming" },
      { id: "24", label: "Intrattenimento" },
    ],
    isLoading: false,
  }),
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
    expect(screen.queryByText(/archiviata/i)).not.toBeInTheDocument();
  });

  it("does not show the source YouTube thumbnail when no cover asset is available", async () => {
    const sourceThumbnail = "https://i.ytimg.com/vi/video-1/hqdefault.jpg";
    routeFetch({
      covers: [coverFixture({ source_thumbnail_url: sourceThumbnail })],
    });

    renderPanel();

    await waitFor(() => {
      expect(screen.getAllByTestId("group-cover-card")).toHaveLength(1);
    });
    expect(document.querySelector(`img[src="${sourceThumbnail}"]`)).not.toBeInTheDocument();
    expect(screen.getByText("Copertina non ancora esportata")).toBeInTheDocument();
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
    fireEvent.click(screen.getByTitle("Clicca per modificare in InstaEditor"));

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

  it("does not render technical status, privacy, or category badges on cover cards", async () => {
    routeFetch({
      covers: [
        coverFixture({ category_id: "24", privacy_status: "public" }),
        coverFixture({
          project_id: "ytes_cover_2",
          category_id: "20",
          privacy_status: "unlisted",
        }),
        coverFixture({ project_id: "ytes_cover_3", privacy_status: "private" }),
        // No privacy_status/category on purpose: the card renders the
        // neutral "Sconosciuta" badge and no category chip.
        coverFixture({ project_id: "ytes_cover_4" }),
      ],
    });

    renderPanel();

    await waitFor(() => {
      expect(screen.getAllByTestId("group-cover-card")).toHaveLength(4);
    });
    expect(screen.queryByText("In modifica")).not.toBeInTheDocument();
    expect(screen.queryByText("Applicata")).not.toBeInTheDocument();
    expect(screen.queryByText("Standalone")).not.toBeInTheDocument();
    expect(screen.queryByTitle(/^Visibilità video:/)).not.toBeInTheDocument();
    expect(screen.queryByTitle(/^Categoria:/)).not.toBeInTheDocument();
  });

  it("opens InstaEditor when clicking the cover image", async () => {
    routeFetch({
      covers: [coverFixture({ category_id: "17", privacy_status: "private" })],
    });

    renderPanel();

    await waitFor(() => {
      expect(screen.getAllByTestId("group-cover-card")).toHaveLength(1);
    });
    fireEvent.click(screen.getByTitle("Clicca per modificare in InstaEditor"));
    await waitFor(() => {
      expect(openInstaEditorWithLaunchMock).toHaveBeenCalled();
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

  it("filters the Video/Cover manager grid by visibility tabs with real counts", async () => {
    routeFetch({
      videos: [
        privateVideoFixture({ youtube_video_id: "pub-1", title: "Video pubblico", privacy_status: "public", actual_privacy: "public" }),
        privateVideoFixture({ youtube_video_id: "priv-1", title: "Video privato", privacy_status: "private", actual_privacy: "private" }),
        privateVideoFixture({ youtube_video_id: "unl-1", title: "Video non in elenco", privacy_status: "unlisted", actual_privacy: "unlisted" }),
      ],
    });

    renderPanel();

    await waitFor(() => {
      expect(screen.getByText("Video pubblico")).toBeInTheDocument();
      expect(screen.getByText("Video privato")).toBeInTheDocument();
      expect(screen.getByText("Video non in elenco")).toBeInTheDocument();
    });

    // The pills carry counts derived from the single canonical list.
    expect(screen.getByTestId("group-videos-filter-all")).toHaveTextContent("3");
    expect(screen.getByTestId("group-videos-filter-private")).toHaveTextContent("1");
    expect(screen.getByTestId("group-videos-filter-unlisted")).toHaveTextContent("1");
    expect(screen.getByTestId("group-videos-filter-public")).toHaveTextContent("1");

    fireEvent.click(screen.getByTestId("group-videos-filter-private"));
    await waitFor(() => {
      expect(screen.getByText("Video privato")).toBeInTheDocument();
    });
    expect(screen.queryByText("Video pubblico")).not.toBeInTheDocument();
    expect(screen.queryByText("Video non in elenco")).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId("group-videos-filter-public"));
    await waitFor(() => {
      expect(screen.getByText("Video pubblico")).toBeInTheDocument();
    });
    expect(screen.queryByText("Video privato")).not.toBeInTheDocument();
  });

  it("filters the manager grid by category within the Copertine hub", async () => {
    routeFetch({
      videos: [
        privateVideoFixture({ youtube_video_id: "cat-1", title: "Video sport", category_id: "17", category_title: "Sport" }),
        privateVideoFixture({ youtube_video_id: "cat-2", title: "Video gaming", category_id: "20" }),
      ],
    });

    renderPanel();

    await waitFor(() => {
      expect(screen.getByText("Video sport")).toBeInTheDocument();
      expect(screen.getByText("Video gaming")).toBeInTheDocument();
    });

    const select = screen.getByTestId("group-videos-category") as HTMLSelectElement;
    fireEvent.change(select, { target: { value: "17" } });
    await waitFor(() => {
      expect(screen.getByText("Video sport")).toBeInTheDocument();
    });
    expect(screen.queryByText("Video gaming")).not.toBeInTheDocument();

    fireEvent.change(select, { target: { value: "all" } });
    await waitFor(() => {
      expect(screen.getByText("Video gaming")).toBeInTheDocument();
    });
  });

  it("filters the manager grid by search within the Copertine hub", async () => {
    routeFetch({
      videos: [
        privateVideoFixture({ youtube_video_id: "abc-1", title: "Wrestling Highlights", channel_name: "Wrestling Insider RU" }),
        privateVideoFixture({ youtube_video_id: "xyz-2", title: "Cucina veloce", channel_name: "Chef Mario" }),
      ],
    });

    renderPanel();

    await waitFor(() => {
      expect(screen.getByText("Wrestling Highlights")).toBeInTheDocument();
      expect(screen.getByText("Cucina veloce")).toBeInTheDocument();
    });

    fireEvent.change(screen.getByTestId("group-videos-search"), { target: { value: "chef" } });
    await waitFor(() => {
      expect(screen.getByText("Cucina veloce")).toBeInTheDocument();
    });
    expect(screen.queryByText("Wrestling Highlights")).not.toBeInTheDocument();
  });

  it("opens the 'Modifica video' drawer from Dettagli, prefilled, and saves via the metadata PATCH", async () => {
    routeFetch({
      videos: [
        privateVideoFixture({
          youtube_video_id: "meta-1",
          title: "Video modificabile",
          description: "Descrizione esistente",
          category_id: "24",
          category_title: "Intrattenimento",
          privacy_status: "public",
          actual_privacy: "public",
        }),
      ],
    });

    renderPanel();

    await waitFor(() => {
      expect(screen.getByText("Video modificabile")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: "Dettagli" }));
    await waitFor(() => {
      expect(screen.getByTestId("edit-metadata-drawer")).toBeInTheDocument();
    });

    // Prefilled from the video's canonical metadata.
    const titleInput = screen.getByTestId("edit-metadata-title-input") as HTMLInputElement;
    expect(titleInput.value).toBe("Video modificabile");
    const categorySelect = screen.getByTestId("edit-metadata-category") as HTMLSelectElement;
    expect(categorySelect.value).toBe("24");
    // Visibility is now editable: a select pre-selected on "public".
    const privacySelect = screen.getByRole("combobox", { name: /visibilità/i }) as HTMLSelectElement;
    expect(privacySelect.value).toBe("public");

    fireEvent.change(titleInput, { target: { value: "Titolo aggiornato" } });
    fireEvent.change(categorySelect, { target: { value: "20" } });
    fireEvent.click(screen.getByTestId("edit-metadata-save"));

    await waitFor(() => {
      const patchCall = authedFetchMock.mock.calls.find(([url, init]) =>
        String(url) === "/api/v1/groups/7/youtube/videos/meta-1" && (init as RequestInit).method === "PATCH",
      );
      expect(patchCall).toBeDefined();
      expect(JSON.parse(String((patchCall as unknown[])[1] && (patchCall[1] as RequestInit).body))).toEqual({
        platform_account_id: 42,
        title: "Titolo aggiornato",
        description: "Descrizione esistente",
        category_id: "20",
        privacy_status: "public",
      });
    });
    expect(toastMock.success).toHaveBeenCalledWith("Metadati video salvati.");
  });
});
