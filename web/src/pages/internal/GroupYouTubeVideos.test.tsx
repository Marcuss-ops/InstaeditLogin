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
  it("loads the first aggregate page and renders its summary", async () => {
    authedFetchMock.mockResolvedValue(
      jsonResponse({
        videos: [],
        summary: {
          total_videos: 12,
          truncated: true,
          accounts: 4,
          accounts_with_videos: 3,
          failed_accounts: 1,
          invalid_token_accounts: [42],
        },
      }),
    );

    renderPanel();

    await waitFor(() => {
      expect(screen.getByTestId("group-youtube-summary")).toBeInTheDocument();
    });
    expect(screen.getByText("12")).toBeInTheDocument();
    expect(screen.getByText("Limite raggiunto")).toBeInTheDocument();
    expect(screen.getByText(/nessun video trovato nel gruppo/i)).toBeInTheDocument();
    expect(authedFetchMock).toHaveBeenCalledWith(
      "/api/v1/groups/7/youtube/videos?include_subgroups=true&limit=50&offset=0",
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
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
              privacy_status: "unlisted",
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
      "/api/v1/groups/7/youtube/videos?include_subgroups=true&limit=50&offset=1",
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
    expect(screen.queryByTestId("group-youtube-videos-load-more")).not.toBeInTheDocument();
  });

  it("explains that an empty result does not prove the video was not published", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ videos: [] }));

    renderPanel();

    await waitFor(() => {
      expect(screen.getByText(/nessun video trovato nel gruppo/i)).toBeInTheDocument();
    });
    expect(screen.getByText(/non conferma né esclude una pubblicazione/i)).toBeInTheDocument();
  });

  it("shows confirmed publication, effective privacy, and both navigation links", async () => {
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
      expect(screen.getByText("Pubblicato su YouTube")).toBeInTheDocument();
    });
    expect(screen.getByText("Privacy: Pubblico")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /apri su youtube/i })).toHaveAttribute(
      "href",
      "https://www.youtube.com/watch?v=RWpq6fdRFak",
    );
    expect(screen.getByRole("link", { name: /apri nel canale/i })).toHaveAttribute(
      "href",
      "/app/dashboard-channels/42?video=RWpq6fdRFak",
    );
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
