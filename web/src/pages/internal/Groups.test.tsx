import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
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

beforeEach(() => {
  groupsDataMock.mockReturnValue({
    state: {
      kind: "ready",
      groups: [],
      accounts: [],
      workspaceId: 1,
      accountsByGroup: new Map(),
    },
    selectedGroupId: null,
    setSelectedGroupId: vi.fn(),
    setSelectedAccountId: vi.fn(),
    newGroupName: "",
    setNewGroupName: vi.fn(),
    creatingGroup: false,
    load: vi.fn(),
    handleCreateGroup: vi.fn(),
    tree: [],
    selectedGroup: null,
    selectedAccount: null,
  });
});

describe("GroupsPage", () => {
  it("provides a direct link back to the real InstaEditLogin Home", () => {
    render(
      <MemoryRouter initialEntries={["/app/groups"]}>
        <GroupsPage />
      </MemoryRouter>,
    );

    expect(screen.getByTestId("groups-home-link")).toHaveAttribute(
      "href",
      "/app/dashboard",
    );
    expect(screen.getByRole("link", { name: /torna alla home/i })).toBeInTheDocument();
  });
});
