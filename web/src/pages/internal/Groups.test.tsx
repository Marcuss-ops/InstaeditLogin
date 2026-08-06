import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
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
  platform_user_id: "UC-one",
  status: "active",
  created_at: "2026-01-01T00:00:00Z",
};

const ungroupedAccount: PlatformAccount = {
  id: 2,
  platform: "tiktok",
  username: "channel-ungrouped",
  platform_user_id: "TT-two",
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
    accounts: [groupedAccount, ungroupedAccount],
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
    ungroupedAccounts: [ungroupedAccount],
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

  it("renders ungrouped channels and hides channels already assigned to a group", () => {
    renderPage();

    expect(screen.getByTestId("ungrouped-section")).toBeInTheDocument();
    expect(screen.getByText("channel-ungrouped")).toBeInTheDocument();
    expect(screen.queryByText("channel-grouped")).not.toBeInTheDocument();
  });

  it("omits the ungrouped section when every channel is assigned", () => {
    makeMock({ ungroupedAccounts: [] });

    renderPage();

    expect(screen.queryByTestId("ungrouped-section")).not.toBeInTheDocument();
  });

  it("assigns a channel to a group when dropped onto a group chip", () => {
    const assignAccountToGroup = vi.fn();
    makeMock({ assignAccountToGroup });

    renderPage();

    const card = screen.getByText("channel-ungrouped").closest("[data-account-id]");
    expect(card).not.toBeNull();

    const dataTransfer = {
      setData: vi.fn(),
      getData: vi.fn(() => String(ungroupedAccount.id)),
      effectAllowed: "",
    };
    fireEvent.dragStart(card!, { dataTransfer });
    // The chip in the top bar includes the member count in its accessible
    // name, unlike the (hidden) TreeView button.
    fireEvent.drop(screen.getByRole("button", { name: /WWE1 canali/ }), { dataTransfer });

    expect(assignAccountToGroup).toHaveBeenCalledTimes(1);
    expect(assignAccountToGroup).toHaveBeenCalledWith(ungroupedAccount.id, 7);
  });

  it("assigns a channel to a group via the select fallback", () => {
    const assignAccountToGroup = vi.fn();
    makeMock({ assignAccountToGroup });

    renderPage();

    fireEvent.change(
      screen.getByLabelText("Assegna channel-ungrouped a una cartella"),
      { target: { value: String(7) } },
    );

    expect(assignAccountToGroup).toHaveBeenCalledTimes(1);
    expect(assignAccountToGroup).toHaveBeenCalledWith(ungroupedAccount.id, 7);
  });
});
