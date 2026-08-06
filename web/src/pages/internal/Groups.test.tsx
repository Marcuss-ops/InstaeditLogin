import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";

const { groupsDataMock } = vi.hoisted(() => ({
  groupsDataMock: vi.fn(),
}));

vi.mock("./useGroupsData", () => ({
  useGroupsData: groupsDataMock,
}));

vi.mock("../../lib/auth", () => ({
  authedFetch: vi.fn(),
}));

import { GroupsPage } from "./Groups";
import type { PlatformAccount, TreeNode } from "./groupsTypes";

const groupedAccount: PlatformAccount = {
  id: 1,
  platform: "youtube",
  username: "channel-grouped",
  avatar_url: "https://cdn.example.test/grouped.png",
  platform_user_id: "UC-one",
  status: "active",
  created_at: "2026-01-01T00:00:00Z",
};

const secondYouTubeAccount: PlatformAccount = {
  id: 2,
  platform: "youtube",
  username: "channel-available",
  avatar_url: "https://cdn.example.test/available.png",
  platform_user_id: "UC-two",
  status: "active",
  created_at: "2026-01-01T00:00:00Z",
};

const nonYouTubeAccount: PlatformAccount = {
  id: 3,
  platform: "tiktok",
  username: "channel-tiktok",
  platform_user_id: "TT-three",
  status: "active",
  created_at: "2026-01-01T00:00:00Z",
};

const tree: TreeNode[] = [
  {
    id: 7,
    workspace_id: 9,
    name: "WWE",
    children: [],
    accounts: [groupedAccount],
  },
];

function makeReadyState() {
  return {
    kind: "ready" as const,
    groups: [{ id: 7, workspace_id: 9, name: "WWE" }],
    accounts: [groupedAccount, secondYouTubeAccount, nonYouTubeAccount],
    workspaceId: 9,
    accountsByGroup: new Map([[7, [groupedAccount]]]),
    groupAccountIDs: new Map([[7, [groupedAccount.id]]]),
  };
}

function makeMock(overrides: Record<string, unknown> = {}) {
  groupsDataMock.mockReturnValue({
    state: makeReadyState(),
    selectedGroupId: null,
    setSelectedGroupId: vi.fn(),
    setSelectedAccountId: vi.fn(),
    newGroupName: "",
    setNewGroupName: vi.fn(),
    creatingGroup: false,
    busyAccountId: null,
    load: vi.fn(),
    handleCreateGroup: vi.fn(),
    assignAccountToGroup: vi.fn(),
    setGroupAccounts: vi.fn().mockResolvedValue(undefined),
    renameGroup: vi.fn().mockResolvedValue(undefined),
    availableYouTubeAccounts: [groupedAccount, secondYouTubeAccount],
    tree,
    selectedGroup: null,
    selectedAccount: null,
    ...overrides,
  });
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/app/groups"]}>
      <GroupsPage />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  makeMock();
});

describe("GroupsPage", () => {
  it("does not render a redundant Home link", () => {
    renderPage();

    expect(screen.queryByTestId("groups-home-link")).not.toBeInTheDocument();
  });

  it("renders every available YouTube channel and excludes other platforms", () => {
    renderPage();

    expect(screen.getByTestId("youtube-channels-tray")).toBeInTheDocument();
    expect(screen.getByText("channel-grouped")).toBeInTheDocument();
    expect(screen.getByText("channel-available")).toBeInTheDocument();
    expect(screen.queryByText("channel-tiktok")).not.toBeInTheDocument();
    expect(screen.getByRole("img", { name: "channel-available avatar" })).toHaveAttribute(
      "src",
      "https://cdn.example.test/available.png",
    );
  });

  it("shows a clear empty state when no YouTube channels exist", () => {
    makeMock({ availableYouTubeAccounts: [] });

    renderPage();

    expect(screen.getByTestId("youtube-channels-tray")).toBeInTheDocument();
    expect(screen.getByTestId("youtube-channels-empty")).toHaveTextContent("Nessun canale YouTube disponibile");
    expect(screen.getByTestId("youtube-channels-empty")).toHaveTextContent("Collega un canale YouTube");
  });

  it("assigns a channel to a group when dropped onto a group chip", () => {
    const assignAccountToGroup = vi.fn();
    makeMock({ assignAccountToGroup });

    renderPage();

    const card = screen.getByText("channel-available").closest("[data-account-id]");
    expect(card).not.toBeNull();

    const dataTransfer = {
      setData: vi.fn(),
      getData: vi.fn(() => String(secondYouTubeAccount.id)),
      effectAllowed: "",
    };
    fireEvent.dragStart(card!, { dataTransfer });
    // The chip in the top bar includes the member count in its accessible
    // name, unlike the (hidden) TreeView button.
    fireEvent.drop(screen.getByRole("button", { name: /WWE1 canali/ }), { dataTransfer });

    expect(assignAccountToGroup).toHaveBeenCalledTimes(1);
    expect(assignAccountToGroup).toHaveBeenCalledWith(secondYouTubeAccount.id, 7);
  });

  it("selects multiple channels and sends them together when one selected card is dragged", () => {
    const setGroupAccounts = vi.fn().mockResolvedValue(undefined);
    makeMock({
      selectedGroupId: 7,
      selectedGroup: tree[0],
      setGroupAccounts,
    });

    renderPage();

    const tray = screen.getByTestId("youtube-channels-tray");
    fireEvent.click(within(tray).getByRole("button", { name: "Seleziona channel-grouped" }));
    fireEvent.click(within(tray).getByRole("button", { name: "Seleziona channel-available" }));
    expect(within(tray).getByText(/2 canali selezionati/)).toBeInTheDocument();

    const card = screen.getByText("channel-available").closest("[data-account-id]");
    expect(card).not.toBeNull();
    const dataTransfer = {
      setData: vi.fn(),
      getData: vi.fn((type: string) => type === "application/x-instaedit-channel-ids" ? JSON.stringify([1, 2]) : JSON.stringify([1, 2])),
      effectAllowed: "",
    };
    fireEvent.dragStart(card!, { dataTransfer });
    fireEvent.drop(screen.getByRole("button", { name: /WWE1 canali/ }), { dataTransfer });

    expect(setGroupAccounts).toHaveBeenCalledWith(7, expect.any(Function));
    const membershipUpdater = setGroupAccounts.mock.calls[0]?.[1] as (currentIDs: number[]) => number[];
    expect(membershipUpdater([groupedAccount.id])).toEqual([groupedAccount.id, secondYouTubeAccount.id]);
  });

  it("supports the explicit all, assigned, and unassigned filters", () => {
    makeMock({
      selectedGroupId: 7,
      state: {
        ...makeReadyState(),
        groupAccountIDs: new Map([[7, [groupedAccount.id]]]),
      },
      selectedGroup: tree[0],
    });

    renderPage();
    const tray = screen.getByTestId("youtube-channels-tray");
    const filter = within(tray).getByRole("combobox", { name: "Filtra canali" });

    fireEvent.change(filter, { target: { value: "all" } });
    expect(within(tray).getByText("channel-grouped")).toBeInTheDocument();
    expect(within(tray).getByText("channel-available")).toBeInTheDocument();

    fireEvent.change(filter, { target: { value: "assigned" } });
    expect(within(tray).getByText("channel-grouped")).toBeInTheDocument();
    expect(within(tray).queryByText("channel-available")).not.toBeInTheDocument();

    fireEvent.change(filter, { target: { value: "unassigned" } });
    expect(within(tray).getByText("channel-available")).toBeInTheDocument();
    expect(within(tray).queryByText("channel-grouped")).not.toBeInTheDocument();
  });

  it("toggles an individual channel selection", () => {
    renderPage();
    const tray = screen.getByTestId("youtube-channels-tray");
    const channel = within(tray).getByRole("button", { name: "Seleziona channel-available" });

    fireEvent.click(channel);
    expect(within(tray).getByText("1 canali selezionati")).toBeInTheDocument();
    expect(within(tray).getByRole("button", { name: "Deseleziona channel-available" })).toHaveAttribute("aria-pressed", "true");

    fireEvent.click(within(tray).getByRole("button", { name: "Deseleziona channel-available" }));
    expect(within(tray).queryByText(/canali selezionati/)).not.toBeInTheDocument();
  });

  it("selects and deselects all visible channels", () => {
    renderPage();
    const tray = screen.getByTestId("youtube-channels-tray");
    const selectAll = within(tray).getByRole("button", { name: "Seleziona tutti" });

    fireEvent.click(selectAll);
    expect(within(tray).getByText("2 canali selezionati")).toBeInTheDocument();
    expect(within(tray).getByRole("button", { name: "Deseleziona channel-grouped" })).toHaveAttribute("aria-pressed", "true");
    expect(within(tray).getByRole("button", { name: "Deseleziona channel-available" })).toHaveAttribute("aria-pressed", "true");

    fireEvent.click(within(tray).getByRole("button", { name: "Deseleziona tutti" }));
    expect(within(tray).queryByText("2 canali selezionati")).not.toBeInTheDocument();
    expect(within(tray).getByRole("button", { name: "Seleziona channel-grouped" })).toHaveAttribute("aria-pressed", "false");
  });

  it("clears selections that become hidden after changing the search", () => {
    renderPage();
    const tray = screen.getByTestId("youtube-channels-tray");

    fireEvent.click(within(tray).getByRole("button", { name: "Seleziona tutti" }));
    fireEvent.change(within(tray).getByRole("textbox", { name: "Cerca canali" }), { target: { value: "grouped" } });
    expect(within(tray).getByText("1 canali selezionati")).toBeInTheDocument();

    fireEvent.change(within(tray).getByRole("textbox", { name: "Cerca canali" }), { target: { value: "available" } });
    expect(within(tray).queryByText(/canali selezionati/)).not.toBeInTheDocument();
    expect(within(tray).getByRole("button", { name: "Seleziona channel-available" })).toHaveAttribute("aria-pressed", "false");
  });

  it("shows the unassigned empty state when every channel is already organized", () => {
    makeMock({
      state: {
        ...makeReadyState(),
        groupAccountIDs: new Map([[7, [groupedAccount.id, secondYouTubeAccount.id]]]),
      },
      availableYouTubeAccounts: [groupedAccount, secondYouTubeAccount],
    });

    renderPage();
    const tray = screen.getByTestId("youtube-channels-tray");
    fireEvent.change(within(tray).getByRole("combobox", { name: "Filtra canali" }), { target: { value: "unassigned" } });

    expect(within(tray).getByTestId("youtube-channels-empty")).toHaveTextContent("Tutti i canali sono già nei gruppi");
  });

  it("shows filter-specific empty states for assigned and unassigned channels", () => {
    makeMock({
      state: {
        ...makeReadyState(),
        groupAccountIDs: new Map([[7, []]]),
      },
      availableYouTubeAccounts: [groupedAccount, secondYouTubeAccount],
    });

    renderPage();
    const tray = screen.getByTestId("youtube-channels-tray");
    const filter = within(tray).getByRole("combobox", { name: "Filtra canali" });

    fireEvent.change(filter, { target: { value: "assigned" } });
    expect(within(tray).getByTestId("youtube-channels-empty")).toHaveTextContent("Nessun canale nei gruppi");

    fireEvent.change(filter, { target: { value: "unassigned" } });
    expect(within(tray).queryByTestId("youtube-channels-empty")).not.toBeInTheDocument();
    expect(within(tray).getByText("channel-available")).toBeInTheDocument();
  });

  it("shows a clear empty state when the current search has no matches", () => {
    renderPage();
    const tray = screen.getByTestId("youtube-channels-tray");

    fireEvent.change(within(tray).getByRole("textbox", { name: "Cerca canali" }), { target: { value: "missing-channel" } });

    expect(within(tray).getByTestId("youtube-channels-empty")).toHaveTextContent("Nessun canale trovato");
    expect(within(tray).getByTestId("youtube-channels-empty")).toHaveTextContent("missing-channel");
  });

  it("removes the folder selector so assignment uses drag and drop", () => {
    renderPage();

    expect(screen.queryByText("Cartella…")).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Cartella" })).not.toBeInTheDocument();
  });
});
