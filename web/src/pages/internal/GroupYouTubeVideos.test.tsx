import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";

const { authedFetchMock } = vi.hoisted(() => ({
  authedFetchMock: vi.fn(),
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

import { ApiError } from "../../lib/auth";
import { GroupYouTubeVideos } from "./GroupYouTubeVideos";

function renderPanel() {
  return render(
    <MemoryRouter>
      <GroupYouTubeVideos groupId={7} />
    </MemoryRouter>,
  );
}

function jsonResponse(data: unknown) {
  return { json: async () => data };
}

beforeEach(() => {
  authedFetchMock.mockReset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("GroupYouTubeVideos", () => {
  it("loads the first recent video page automatically on mount", async () => {
    authedFetchMock.mockResolvedValue(
      jsonResponse({
        videos: [],
        summary: { total_videos: 12 },
      }),
    );

    renderPanel();
    expect(authedFetchMock).toHaveBeenCalledWith(
      "/api/v1/groups/7/youtube/videos?include_subgroups=true&limit=50&offset=0&days=90",
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );

    await waitFor(() => {
      expect(screen.getByText(/nessun video recente/i)).toBeInTheDocument();
    });
    expect(screen.getByTestId("group-youtube-videos-recency")).toBeInTheDocument();
  });

  it("loads the next offset page and appends videos without refetching page one", async () => {
    authedFetchMock
      .mockResolvedValueOnce(
        jsonResponse({
          videos: [
            {
              youtube_video_id: "first-id",
              title: "First video",
              privacy_status: "private",
              platform_account_id: 42,
            },
          ],
          summary: { total_videos: 2, accounts: 1, accounts_with_videos: 1, failed_accounts: 0 },
          has_more: true,
          next_offset: 1,
        }),
      )
      .mockResolvedValueOnce(
        jsonResponse({
          videos: [
            {
              youtube_video_id: "second-id",
              title: "Second video",
              privacy_status: "private",
              platform_account_id: 42,
            },
          ],
          summary: { total_videos: 2, accounts: 1, accounts_with_videos: 1, failed_accounts: 0 },
          has_more: false,
        }),
      );

    renderPanel();

    await waitFor(() => {
      expect(screen.getByText("First video")).toBeInTheDocument();
      expect(screen.getByTestId("group-youtube-videos-load-more")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByTestId("group-youtube-videos-load-more"));

    await waitFor(() => {
      expect(screen.getByText("Second video")).toBeInTheDocument();
    });
    expect(screen.getByText("First video")).toBeInTheDocument();
    expect(authedFetchMock).toHaveBeenNthCalledWith(
      2,
      "/api/v1/groups/7/youtube/videos?include_subgroups=true&limit=50&offset=1&days=90",
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
    expect(screen.queryByTestId("group-youtube-videos-load-more")).not.toBeInTheDocument();
  });

  it("explains that an empty result does not prove the video was not published", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ videos: [] }));

    renderPanel();

    await waitFor(() => {
      expect(screen.getByText(/nessun video recente/i)).toBeInTheDocument();
    });
    expect(screen.getByText(/non ci sono video negli ultimi 90 giorni/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Cambia periodo" })).toBeInTheDocument();
  });

  it("renders published phantom videos with the canonical list", async () => {
    authedFetchMock.mockResolvedValue(
      jsonResponse({
        videos: [
          {
            youtube_video_id: "RWpq6fdRFak",
            title: "Test video",
            thumbnail_url: "https://i.ytimg.com/vi/RWpq6fdRFak/hqdefault.jpg",
            privacy_status: "public",
            actual_privacy: "public",
            editor_status: "published",
            youtube_sync_status: "confirmed",
            platform_account_id: 42,
            channel_name: "Amish",
            phantom: true,
          },
        ],
      }),
    );

    renderPanel();

    await waitFor(() => {
      expect(screen.getByText("Test video")).toBeInTheDocument();
    });
  });

  it("filters the grid by privacy status client-side", async () => {
    authedFetchMock.mockResolvedValue(
      jsonResponse({
        videos: [
          {
            youtube_video_id: "pub-id",
            title: "Video pubblico",
            privacy_status: "public",
            actual_privacy: "public",
            platform_account_id: 42,
          },
          {
            youtube_video_id: "priv-id",
            title: "Video privato",
            privacy_status: "private",
            actual_privacy: "private",
            platform_account_id: 42,
          },
        ],
        summary: { total_videos: 2 },
      }),
    );

    renderPanel();

    await waitFor(() => {
      expect(screen.getByText("Video pubblico")).toBeInTheDocument();
      expect(screen.getByText("Video privato")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByTestId("group-videos-filter-private"));
    await waitFor(() => {
      expect(screen.getByText("Video privato")).toBeInTheDocument();
    });
    expect(screen.queryByText("Video pubblico")).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId("group-videos-filter-public"));
    await waitFor(() => {
      expect(screen.getByText("Video pubblico")).toBeInTheDocument();
    });
    expect(screen.queryByText("Video privato")).not.toBeInTheDocument();
  });

  it("filters the grid by free-text search across title, channel and video id", async () => {
    authedFetchMock.mockResolvedValue(
      jsonResponse({
        videos: [
          {
            youtube_video_id: "abc-1",
            title: "Wrestling Highlights",
            channel_name: "Wrestling Insider RU",
            privacy_status: "private",
            actual_privacy: "private",
            platform_account_id: 42,
          },
          {
            youtube_video_id: "xyz-2",
            title: "Cucina veloce",
            channel_name: "Chef Mario",
            privacy_status: "private",
            actual_privacy: "private",
            platform_account_id: 43,
          },
        ],
      }),
    );

    renderPanel();

    await waitFor(() => {
      expect(screen.getByText("Wrestling Highlights")).toBeInTheDocument();
      expect(screen.getByText("Cucina veloce")).toBeInTheDocument();
    });

    // Match by channel name (case-insensitive).
    fireEvent.change(screen.getByTestId("group-videos-search"), { target: { value: "wrestling" } });
    await waitFor(() => {
      expect(screen.getByText("Wrestling Highlights")).toBeInTheDocument();
    });
    expect(screen.queryByText("Cucina veloce")).not.toBeInTheDocument();

    // Match by video id.
    fireEvent.change(screen.getByTestId("group-videos-search"), { target: { value: "xyz-2" } });
    await waitFor(() => {
      expect(screen.getByText("Cucina veloce")).toBeInTheDocument();
    });
    expect(screen.queryByText("Wrestling Highlights")).not.toBeInTheDocument();

    // Clear restores the whole list.
    fireEvent.click(screen.getByTestId("group-videos-search-clear"));
    await waitFor(() => {
      expect(screen.getByText("Wrestling Highlights")).toBeInTheDocument();
      expect(screen.getByText("Cucina veloce")).toBeInTheDocument();
    });
  });

  it("filters the grid by category, resolving labels from the canonical snapshot", async () => {
    authedFetchMock.mockResolvedValue(
      jsonResponse({
        videos: [
          {
            youtube_video_id: "cat-1",
            title: "Video sport",
            privacy_status: "public",
            actual_privacy: "public",
            platform_account_id: 42,
            category_id: "17",
            category_title: "Sport",
          },
          {
            youtube_video_id: "cat-2",
            title: "Video gaming",
            privacy_status: "public",
            actual_privacy: "public",
            platform_account_id: 42,
            category_id: "20",
          },
          {
            youtube_video_id: "cat-3",
            title: "Senza categoria",
            privacy_status: "public",
            actual_privacy: "public",
            platform_account_id: 42,
          },
        ],
      }),
    );

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
    expect(screen.queryByText("Senza categoria")).not.toBeInTheDocument();

    // category_id without category_title resolves to the canonical label.
    fireEvent.change(select, { target: { value: "20" } });
    await waitFor(() => {
      expect(screen.getByText("Video gaming")).toBeInTheDocument();
    });
    expect(screen.queryByText("Video sport")).not.toBeInTheDocument();

    // Back to all categories.
    fireEvent.change(select, { target: { value: "all" } });
    await waitFor(() => {
      expect(screen.getByText("Senza categoria")).toBeInTheDocument();
    });
  });

  it("shows a clear-filters empty state when search matches nothing", async () => {
    authedFetchMock.mockResolvedValue(
      jsonResponse({
        videos: [
          {
            youtube_video_id: "abc-1",
            title: "Wrestling Highlights",
            privacy_status: "private",
            actual_privacy: "private",
            platform_account_id: 42,
          },
        ],
      }),
    );

    renderPanel();

    await waitFor(() => {
      expect(screen.getByText("Wrestling Highlights")).toBeInTheDocument();
    });

    fireEvent.change(screen.getByTestId("group-videos-search"), { target: { value: "inesistente" } });
    await waitFor(() => {
      expect(screen.getByText(/nessun video corrisponde ai filtri/i)).toBeInTheDocument();
    });
    expect(screen.getByTestId("group-videos-clear-filters")).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("group-videos-clear-filters"));
    await waitFor(() => {
      expect(screen.getByText("Wrestling Highlights")).toBeInTheDocument();
    });
  });

  it("shows an actionable message when YouTube returns a 502", async () => {
    authedFetchMock.mockRejectedValue(new ApiError(502, "youtube list failed for every account"));

    renderPanel();

    await waitFor(() => {
      expect(screen.getByTestId("group-youtube-upstream-error")).toBeInTheDocument();
    });
    expect(screen.getByText(/youtube non risponde temporaneamente/i)).toBeInTheDocument();
  });

  it("distinguishes pending verification, drift, and unconfirmed publication", async () => {
    authedFetchMock.mockResolvedValue(
      jsonResponse({
        videos: [
          {
            youtube_video_id: "pending-id",
            title: "Pending",
            privacy_status: "private",
            actual_privacy: "private",
            desired_privacy: "public",
            editor_status: "published",
            youtube_sync_status: "pending",
            platform_account_id: 42,
          },
          {
            youtube_video_id: "drift-id",
            title: "Drift",
            privacy_status: "private",
            actual_privacy: "private",
            desired_privacy: "public",
            editor_status: "published",
            youtube_sync_status: "drift",
            platform_account_id: 42,
          },
          {
            youtube_video_id: "unconfirmed-id",
            title: "Unconfirmed",
            privacy_status: "private",
            editor_status: "published",
            youtube_sync_status: "unconfirmed",
            platform_account_id: 42,
          },
        ],
      }),
    );

    renderPanel();

    await waitFor(() => {
      expect(screen.getByText(/verifica in corso/i)).toBeInTheDocument();
      expect(screen.getByText(/privacy da verificare/i)).toBeInTheDocument();
      expect(screen.getByText(/pubblicazione registrata · verifica youtube/i)).toBeInTheDocument();
    });
    expect(screen.queryByText("Pubblicato su YouTube")).not.toBeInTheDocument();
  });
});
