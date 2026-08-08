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

vi.mock("../../features/youtube/api/editorSessionsApi", () => ({
  openInstaEditorWithLaunch: vi.fn(),
}));

vi.mock("./GroupCoverCreateDialog", () => {
  const React = require("react");
  return {
    GroupCoverCreateDialog: (props: { groupId: number }) =>
      React.createElement(
        "div",
        { "data-testid": "group-cover-create-dialog", "data-group-id": String(props.groupId) },
        "Crea copertina",
      ),
  };
});

import { GroupCovers } from "./GroupCovers";

function renderPanel() {
  return render(
    <MemoryRouter>
      <GroupCovers groupId={7} />
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

beforeEach(() => {
  authedFetchMock.mockReset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("GroupCovers", () => {
  it("fetches the group covers endpoint on mount and renders the covers grid", async () => {
    authedFetchMock.mockResolvedValue(
      jsonResponse({
        covers: [
          coverFixture(),
          coverFixture({
            project_id: "ytes_cover_2",
            project_status: "archived",
            channel_name: "Wwe Insider De",
            youtube_video_id: "sY6Ce0bTuwo",
          }),
        ],
      }),
    );

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

  it("renders a short empty state with a Crea copertina button that opens the create dialog", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse({ covers: [] }));

    renderPanel();

    await waitFor(() => {
      expect(screen.getByText(/nessuna copertina in questo gruppo/i)).toBeInTheDocument();
    });
    // The long explanatory description was removed on purpose.
    expect(
      screen.queryByText(/Quando crei una copertina per un video di questo gruppo/i),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId("group-covers-create"));
    expect(screen.getByTestId("group-cover-create-dialog")).toBeInTheDocument();
  });

  it("filters by project status via the filter chips", async () => {
    authedFetchMock.mockResolvedValue(
      jsonResponse({
        covers: [
          coverFixture({ project_id: "ytes_draft", project_status: "draft", draft_title: "Draft cover" }),
          coverFixture({ project_id: "ytes_ready", project_status: "ready", draft_title: "Ready cover" }),
          coverFixture({ project_id: "ytes_arch", project_status: "archived", draft_title: "Archived cover" }),
        ],
      }),
    );

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
    authedFetchMock.mockResolvedValue(
      jsonResponse({
        covers: [coverFixture({ draft_title: "Il mio nuovo design" })],
      }),
    );

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
