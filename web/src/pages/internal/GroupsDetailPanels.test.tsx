import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import type { ComponentProps } from "react";
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

function renderPanel(props: Partial<ComponentProps<typeof GroupDetailPanel>> = {}) {
  return render(
    <MemoryRouter>
      <GroupDetailPanel
        group={group}
        onPickAccount={() => {}}
        onDeleteGroup={() => {}}
        onSaved={() => {}}
        onRename={() => {}}
        availableAccounts={[]}
        onAddAccounts={() => {}}
        {...props}
      />
    </MemoryRouter>,
  );
}

describe("GroupDetailPanel", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("requires confirmation and preserves the channel when cancelled", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 204, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);
    const confirmMock = vi.spyOn(window, "confirm").mockReturnValue(false);

    renderPanel();

    fireEvent.click(screen.getByRole("button", { name: /Rimuovi channel-two dalla cartella/i }));

    expect(confirmMock).toHaveBeenCalledWith(expect.stringContaining('soltanto dalla cartella "Editorial"'));
    const confirmation = String(confirmMock.mock.calls[0]?.[0] ?? "");
    expect(confirmation).toContain("rimarrà collegato a InstaEdit");
    expect(confirmation).toContain("\n\n");
    expect(screen.getByRole("button", { name: /Rimuovi channel-two dalla cartella/i })).toHaveAttribute("title", expect.stringContaining("il canale resta collegato"));
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/api/v1/groups/7/accounts/102"))).toBe(false);
    expect(screen.getByRole("button", { name: /Rimuovi channel-two dalla cartella/i })).toBeInTheDocument();
  });

  it("persists a removed account immediately", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 204, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const onSaved = vi.fn();

    renderPanel({ onSaved });

    fireEvent.click(screen.getByRole("button", { name: /Rimuovi channel-two dalla cartella/i }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/v1/groups/7/accounts/102"),
      expect.objectContaining({ method: "DELETE" }),
    ));
    expect(onSaved).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/api/v1/accounts/102/disconnect"))).toBe(false);
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/api/v1/accounts/102/data"))).toBe(false);
    expect(screen.queryByRole("button", { name: /Rimuovi channel-two dalla cartella/i })).not.toBeInTheDocument();
  });

  it("renames the group inline and supports cancel", async () => {
    const onRename = vi.fn().mockResolvedValue(undefined);

    renderPanel({ onRename });

    fireEvent.click(screen.getByRole("button", { name: "Azioni cartella" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Rinomina gruppo" }));
    const input = screen.getByRole("textbox", { name: "Nome del gruppo" });
    fireEvent.change(input, { target: { value: "YouTube WWE" } });
    fireEvent.click(screen.getByRole("button", { name: "Annulla rinomina" }));
    expect(screen.queryByRole("textbox", { name: "Nome del gruppo" })).not.toBeInTheDocument();
    expect(onRename).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Azioni cartella" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Rinomina gruppo" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Nome del gruppo" }), { target: { value: "YouTube WWE" } });
    fireEvent.click(screen.getByRole("button", { name: "Salva" }));
    await waitFor(() => expect(onRename).toHaveBeenCalledWith("YouTube WWE"));
  });

  it("validates rename input and keeps the editor open", async () => {
    const onRename = vi.fn().mockResolvedValue(undefined);

    renderPanel({ onRename });

    fireEvent.click(screen.getByRole("button", { name: "Azioni cartella" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Rinomina gruppo" }));
    const input = screen.getByRole("textbox", { name: "Nome del gruppo" });
    fireEvent.change(input, { target: { value: "   " } });
    fireEvent.click(screen.getByRole("button", { name: "Salva" }));
    expect(screen.getByRole("alert")).toHaveTextContent("obbligatorio");
    expect(onRename).not.toHaveBeenCalled();

    fireEvent.change(input, { target: { value: "x".repeat(81) } });
    fireEvent.click(screen.getByRole("button", { name: "Salva" }));
    expect(screen.getByRole("alert")).toHaveTextContent("80 caratteri");
    expect(onRename).not.toHaveBeenCalled();
  });

  it("prevents duplicate rename submits while saving", async () => {
    let resolveRename: (() => void) | undefined;
    const onRename = vi.fn(() => new Promise<void>((resolve) => { resolveRename = resolve; }));

    renderPanel({ onRename });

    fireEvent.click(screen.getByRole("button", { name: "Azioni cartella" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Rinomina gruppo" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Nome del gruppo" }), { target: { value: "YouTube WWE" } });
    fireEvent.click(screen.getByRole("button", { name: "Salva" }));
    await waitFor(() => expect(onRename).toHaveBeenCalledTimes(1));
    expect(screen.getByRole("button", { name: "Salvo…" })).toBeDisabled();
    fireEvent.submit(screen.getByRole("textbox", { name: "Nome del gruppo" }).closest("form")!);
    expect(onRename).toHaveBeenCalledTimes(1);

    resolveRename?.();
    await waitFor(() => expect(screen.queryByRole("textbox", { name: "Nome del gruppo" })).not.toBeInTheDocument());
  });

  it("shows a backend rename error without closing the editor", async () => {
    const onRename = vi.fn().mockRejectedValue(new Error("Nome già utilizzato"));

    renderPanel({ onRename });

    fireEvent.click(screen.getByRole("button", { name: "Azioni cartella" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Rinomina gruppo" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Nome del gruppo" }), { target: { value: "YouTube WWE" } });
    fireEvent.click(screen.getByRole("button", { name: "Salva" }));

    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Nome già utilizzato"));
    expect(screen.getByRole("textbox", { name: "Nome del gruppo" })).toBeInTheDocument();
  });

  it("syncs a server language into an initially empty panel after refresh", async () => {
    const staleGroup: TreeNode = {
      ...group,
      accounts: [{ ...group.accounts[0], username: "BoxeClubITA", language: "" }],
    };
    const refreshedGroup: TreeNode = {
      ...staleGroup,
      accounts: [{ ...staleGroup.accounts[0], language: "it" }],
    };

    const { rerender } = render(
      <MemoryRouter>
        <GroupDetailPanel
          group={staleGroup}
          onPickAccount={() => {}}
          onDeleteGroup={() => {}}
          onSaved={() => {}}
          onRename={() => {}}
        />
      </MemoryRouter>,
    );

    expect(screen.getByLabelText("Language for BoxeClubITA")).toHaveAttribute("title", "Lingua non impostata · clicca per cambiare");
    rerender(
      <MemoryRouter>
        <GroupDetailPanel
          group={refreshedGroup}
          onPickAccount={() => {}}
          onDeleteGroup={() => {}}
          onSaved={() => {}}
          onRename={() => {}}
        />
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByLabelText("Language for BoxeClubITA")).toHaveAttribute("title", "Italiano · clicca per cambiare"));
  });

  it("saves a language immediately when the operator picks one", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);
    const onSaved = vi.fn();

    renderPanel({ onSaved });

    fireEvent.click(screen.getByLabelText("Language for channel-one"));
    fireEvent.click(screen.getByRole("menuitem", { name: "Italiano" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/v1/accounts/101"),
      expect.objectContaining({
        method: "PATCH",
        body: JSON.stringify({ metadata: { language: "it" } }),
      }),
    ));
    expect(onSaved).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/api/v1/groups/7/settings"))).toBe(false);
  });

  it("opens the add-channels dialog and lists only channels not already in the group", () => {
    const available = [{
      id: 201,
      platform: "youtube",
      username: "channel-new",
      platform_user_id: "UC-new",
      status: "active",
      created_at: "2026-01-01T00:00:00Z",
    }];
    renderPanel({ availableAccounts: available });

    fireEvent.click(screen.getByTestId("group-add-channels"));

    const dialog = screen.getByTestId("group-add-channels-dialog");
    expect(dialog).toBeInTheDocument();
    expect(within(dialog).getByText("channel-new")).toBeInTheDocument();
    // Channels already in the group are not offered again.
    expect(within(dialog).queryByText("channel-one")).not.toBeInTheDocument();
  });

  it("shows which other groups an offered channel already belongs to in the add dialog", () => {
    const available = [{
      id: 201,
      platform: "youtube",
      username: "channel-new",
      platform_user_id: "UC-new",
      status: "active",
      created_at: "2026-01-01T00:00:00Z",
    }];
    const groupNamesByAccountId = new Map([[201, ["Editorial", "Rap"]]]);
    renderPanel({ availableAccounts: available, groupNamesByAccountId });

    fireEvent.click(screen.getByTestId("group-add-channels"));

    const dialog = screen.getByTestId("group-add-channels-dialog");
    // The channel is already in "Rap" (a different folder): the badge shows
    // it, while the current group "Editorial" is deliberately not repeated.
    expect(within(dialog).getByTitle("Rap")).toBeInTheDocument();
    expect(within(dialog).queryByTitle("Editorial")).not.toBeInTheDocument();
  });

  it("shows which other groups a member channel already belongs to in the group list", () => {
    const groupNamesByAccountId = new Map([[101, ["Editorial", "HipHop", "Rap", "Boxe"]]]);
    renderPanel({ groupNamesByAccountId });

    // channel-one is already in HipHop, Rap and Boxe (all different from the
    // current "Editorial" folder): the first two render as badges with the
    // overflow marker for the third.
    expect(screen.getByTitle("HipHop")).toBeInTheDocument();
    expect(screen.getByTitle("Rap")).toBeInTheDocument();
    expect(screen.getByText("+1")).toBeInTheDocument();
    // The current group is not shown as a badge on its own members.
    expect(screen.queryByTitle("Editorial")).not.toBeInTheDocument();
    expect(screen.getByText("già in")).toBeInTheDocument();
  });

  it("adds the selected channels when confirming the dialog", () => {
    const onAddAccounts = vi.fn();
    const available = [{
      id: 201,
      platform: "youtube",
      username: "channel-new",
      platform_user_id: "UC-new",
      status: "active",
      created_at: "2026-01-01T00:00:00Z",
    }];
    renderPanel({ availableAccounts: available, onAddAccounts });

    fireEvent.click(screen.getByTestId("group-add-channels"));
    const dialog = screen.getByTestId("group-add-channels-dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: /channel-new/ }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Aggiungi (1)" }));

    expect(onAddAccounts).toHaveBeenCalledTimes(1);
    expect(onAddAccounts).toHaveBeenCalledWith([201]);
  });

  it("shows a message when every available channel is already in the group", () => {
    renderPanel({ availableAccounts: [] });

    fireEvent.click(screen.getByTestId("group-add-channels"));

    const dialog = screen.getByTestId("group-add-channels-dialog");
    expect(within(dialog).getByText("Nessun canale da aggiungere")).toBeInTheDocument();
    expect(within(dialog).getByText(/già in questa cartella/i)).toBeInTheDocument();
  });

  it("filters the add-channels list with the search box", () => {
    const available = [
      { id: 201, platform: "youtube", username: "BoxeClubITA", platform_user_id: "UC-one", status: "active", created_at: "2026-01-01T00:00:00Z" },
      { id: 202, platform: "youtube", username: "RedGloveTR", platform_user_id: "UC-two", status: "active", created_at: "2026-01-01T00:00:00Z" },
    ];
    renderPanel({ availableAccounts: available });

    fireEvent.click(screen.getByTestId("group-add-channels"));
    const dialog = screen.getByTestId("group-add-channels-dialog");
    fireEvent.change(within(dialog).getByRole("textbox", { name: "Cerca canali da aggiungere" }), { target: { value: "Boxe" } });

    expect(within(dialog).getByText("BoxeClubITA")).toBeInTheDocument();
    expect(within(dialog).queryByText("RedGloveTR")).not.toBeInTheDocument();
  });
});
