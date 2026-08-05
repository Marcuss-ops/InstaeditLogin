import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { GroupDetailPanel } from "./GroupsDetailPanels";
import type { TreeNode } from "./groupsTypes";

const group: TreeNode = {
  id: 7,
  workspace_id: 9,
  name: "Editorial",
  children: [],
  accounts: [
    {
      id: 101,
      platform: "youtube",
      username: "channel-one",
      platform_user_id: "UC-one",
      status: "active",
      language: "en",
      created_at: "2026-01-01T00:00:00Z",
    },
    {
      id: 102,
      platform: "youtube",
      username: "channel-two",
      platform_user_id: "UC-two",
      status: "active",
      language: "fr",
      created_at: "2026-01-01T00:00:00Z",
    },
  ],
};

describe("GroupDetailPanel batch settings", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("persists a removed account immediately", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);
    const onSaved = vi.fn();

    render(
      <MemoryRouter>
        <GroupDetailPanel
          group={group}
          onPickAccount={() => {}}
          onCreateSubgroup={() => {}}
          onDeleteGroup={() => {}}
          onSaved={onSaved}
        />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: /Rimuovi channel-two dalla cartella/i }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/v1/groups/7/accounts/102"),
      expect.objectContaining({ method: "DELETE" }),
    ));
    expect(onSaved).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("button", { name: /Rimuovi channel-two dalla cartella/i })).not.toBeInTheDocument();
  });

  it("saves language and remaining membership settings", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);
    const onSaved = vi.fn();

    render(
      <MemoryRouter>
        <GroupDetailPanel
          group={group}
          onPickAccount={() => {}}
          onCreateSubgroup={() => {}}
          onDeleteGroup={() => {}}
          onSaved={onSaved}
        />
      </MemoryRouter>,
    );

    fireEvent.change(screen.getByLabelText("Language for channel-one"), { target: { value: "it" } });
    fireEvent.click(screen.getByRole("button", { name: /Save changes/i }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/v1/accounts/101"),
      expect.objectContaining({
        method: "PATCH",
        body: JSON.stringify({ metadata: { language: "it" } }),
      }),
    ));
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/v1/groups/7/settings"),
      expect.objectContaining({
        method: "PATCH",
        body: JSON.stringify({
          accounts: [
            { account_id: 101, language: "it" },
            { account_id: 102, language: "fr" },
          ],
        }),
      }),
    );
    expect(onSaved).toHaveBeenCalledTimes(1);
  });
});
