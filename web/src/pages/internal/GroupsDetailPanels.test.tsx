import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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
});
